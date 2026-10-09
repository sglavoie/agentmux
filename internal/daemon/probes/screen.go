package probes

import (
	"errors"
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// Screen freshness. A screen observation is evidence about one pane of one
// backend instance generation at one instant, and nothing else. Before any
// classifier reads it, CheckScreen decides whether it is still evidence about
// the pane the probe meant to read; anything short of that is withheld, so a
// capture that failed, describes another pane, predates a server restart, was
// taken too long ago, or shows the screen a dead program left behind can never
// be read as a finished turn.
//
// This is the backend-neutral counterpart of the Kitty epochs in freshness.go.
// Those decide whether a BINDING still holds; this decides whether a SCREEN
// may be read at all, and applies to every backend alike.

// ScreenMaxAge bounds how long a screen observation stays usable when the
// expectation names no tighter limit. A probe tick reads and classifies within
// milliseconds; an observation older than this was held across a stall, and
// the pane has had time to start or finish a turn since.
const ScreenMaxAge = 10 * time.Second

// ScreenVerdict is why a screen observation may or may not be read.
type ScreenVerdict string

const (
	// ScreenFresh is the only verdict that lets a classifier read the screen.
	ScreenFresh ScreenVerdict = "fresh"
	// ScreenUnavailable means the read failed or produced no observation.
	ScreenUnavailable ScreenVerdict = "unavailable"
	// ScreenWrongPane means the observation describes a different pane.
	ScreenWrongPane ScreenVerdict = "wrong_pane"
	// ScreenWrongInstance means another backend, another server, or another
	// generation of the server answered. A restarted tmux server reuses pane
	// IDs, so the same ID under a new generation is a different pane.
	ScreenWrongInstance ScreenVerdict = "wrong_instance"
	// ScreenStale means the observation is older than the caller allows, or
	// predates the moment the caller's own evidence was taken.
	ScreenStale ScreenVerdict = "stale"
	// ScreenExited means the pane's program has ended; the retained screen
	// describes no running turn and so no finished one either.
	ScreenExited ScreenVerdict = "exited"
)

// ErrScreenWithheld matches every *ScreenError.
var ErrScreenWithheld = errors.New("probes: screen observation withheld")

// ScreenError reports a screen observation that must not be read.
type ScreenError struct {
	Verdict ScreenVerdict
	Pane    backend.Ref
	// Err is the read failure behind ScreenUnavailable, if there was one.
	Err error
}

func (e *ScreenError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("probes: screen of %s %s: %v", e.Pane, e.Verdict, e.Err)
	}
	return fmt.Sprintf("probes: screen of %s %s", e.Pane, e.Verdict)
}

func (e *ScreenError) Unwrap() error { return e.Err }

// Is matches ErrScreenWithheld.
func (e *ScreenError) Is(target error) bool { return target == ErrScreenWithheld }

// ScreenExpectation is what a probe meant to read.
type ScreenExpectation struct {
	// Instance is the backend instance the pane belongs to. Kind is always
	// compared. Address and Generation are compared when set, and a set
	// Generation is never satisfied by an observation that names none.
	Instance runtimesession.BackendInstance
	// Pane is the pane the probe is bound to.
	Pane backend.Ref
	// NotBefore, when set, is the earliest capture instant that still counts:
	// typically when the evidence that made the pane eligible was taken.
	NotBefore time.Time
	// MaxAge bounds the observation's age; zero means ScreenMaxAge.
	MaxAge time.Duration
}

// ExpectScreenOf builds the expectation for a registered runtime-session
// target: its exact instance generation and pane.
func ExpectScreenOf(target runtimesession.Target) ScreenExpectation {
	return ScreenExpectation{Instance: target.Instance, Pane: backend.PaneID(target.Ref)}
}

// CheckScreen judges one observation and the error its read returned against
// what the probe meant to read. The checks run from "nothing to read" to "too
// old", so the verdict names the most basic reason first.
func CheckScreen(screen backend.Screen, err error, want ScreenExpectation, now time.Time) ScreenVerdict {
	if err != nil || screen.CapturedAt.IsZero() || screen.Pane.IsZero() {
		return ScreenUnavailable
	}
	if screen.Pane != want.Pane {
		return ScreenWrongPane
	}
	if screen.Instance.Kind != want.Instance.Kind ||
		(want.Instance.Address != "" && screen.Instance.Address != want.Instance.Address) ||
		(want.Instance.Generation != "" && screen.Instance.Generation != want.Instance.Generation) {
		return ScreenWrongInstance
	}
	if screen.Exited {
		return ScreenExited
	}
	maxAge := want.MaxAge
	if maxAge <= 0 {
		maxAge = ScreenMaxAge
	}
	if now.Sub(screen.CapturedAt) > maxAge ||
		(!want.NotBefore.IsZero() && screen.CapturedAt.Before(want.NotBefore)) {
		return ScreenStale
	}
	return ScreenFresh
}

// FreshScreenText returns the observation's text when CheckScreen finds it
// fresh, and a *ScreenError otherwise. The text has any escape sequences
// removed, so a backend that captured with styling reads like one that did
// not.
func FreshScreenText(screen backend.Screen, err error, want ScreenExpectation, now time.Time) (string, error) {
	if verdict := CheckScreen(screen, err, want, now); verdict != ScreenFresh {
		return "", &ScreenError{Verdict: verdict, Pane: want.Pane, Err: err}
	}
	return screenstate.StripANSI(screen.Text), nil
}

// ReadScreen classifies an observation with adapter only when it is fresh.
// Every other verdict yields the zero Observation — State Unknown, no tier —
// which no caller may settle on, alongside the verdict that withheld it.
func ReadScreen(
	adapter screenstate.Adapter,
	screen backend.Screen,
	err error,
	want ScreenExpectation,
	now time.Time,
) (screenstate.Observation, ScreenVerdict) {
	text, readErr := FreshScreenText(screen, err, want, now)
	if readErr != nil {
		var withheld *ScreenError
		errors.As(readErr, &withheld)
		return screenstate.Observation{}, withheld.Verdict
	}
	return adapter.Classify(text), ScreenFresh
}
