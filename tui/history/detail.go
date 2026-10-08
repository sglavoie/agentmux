package history

import (
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
)

// DetailInput carries everything the per-session event history overlay needs
// to render and to answer geometry questions. The root package owns the Bubble
// Tea model; it projects the fields below onto this value at the seam so the
// history domain never sees the model itself.
//
// Height is the raw terminal height and is 0 before the first WindowSizeMsg;
// BodyHeight is the root's already-computed body area (terminal height minus
// the root footer and status bar), because charging root chrome is app
// orchestration and stays with the root.
type DetailInput struct {
	// Events is the daemon's event slice, oldest first.
	Events   []protocol.SessionEvent
	WindowID int

	// Sessions and Cfg back the header: the overlay only keeps a window ID
	// across mode changes, so the session row is looked up on each render.
	Sessions []session.RunningSession
	Cfg      *config.Config
	Watchdog map[int]protocol.WatchdogState

	Err     error
	Loading bool
	Scroll  int

	Width      int
	Height     int
	BodyHeight int

	// Timeline sub-view state.
	ViewTimeline   bool
	TimelineScroll int
	TimelineFold   map[int]bool
	ShowSuppressed bool
	ShowTimestamps bool

	// Now is the reference instant for relative ages. The seam supplies the
	// wall clock; tests supply a fixed instant.
	Now time.Time
}

// now returns the reference instant, falling back to the wall clock for an
// input built as a bare literal.
func (in DetailInput) now() time.Time {
	if in.Now.IsZero() {
		return time.Now()
	}
	return in.Now
}

// detailSession looks up the currently-targeted session. The detail-pane
// header relies on this to surface a title/account/state for the user even
// though only the WindowID survives across enterDetailMode.
func (in DetailInput) detailSession() (session.RunningSession, bool) {
	for _, s := range in.Sessions {
		if s.WindowID == in.WindowID {
			return s, true
		}
	}
	return session.RunningSession{}, false
}

// isTerminalAction reports whether an event action marks the end of a session
// run. Recoverable tool failures (`failure`) are intentionally excluded: they
// should stay visible in the log without drawing a false "session ended"
// separator.
func isTerminalAction(action string) bool {
	switch action {
	case "stop", "stop_failure":
		return true
	}
	return false
}
