package session

import (
	"errors"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
)

const (
	closePollMax   = 5
	closePollDelay = 100 * time.Millisecond
)

// ErrCloseTimeout is returned when the tab did not disappear within the poll
// window. The close command was still sent; the tab likely closed shortly after.
var ErrCloseTimeout = errors.New("tab close timed out; proceeding anyway")

var closeDebug = newFileLogger("/tmp/amx-debug.log", "[close] ")

// CloseObservation is what the poll SAW, which is a different question from
// whether the close request was accepted. Kitty accepts a close-tab and returns;
// the tab going away is a second event, observed by listing the window tree
// again. Two of the three ways that poll can end — a listing that failed, and a
// tab still present when the polls run out — report no error to the caller, so
// an error value alone cannot tell "the tab is gone" from "we never managed to
// look".
//
// Callers that only need the tab closed can keep ignoring this; callers that
// offer the operator something on the strength of the close having happened —
// close recovery, a "closed X" sentence — must not treat an unconfirmed close as
// a confirmed one.
type CloseObservation string

const (
	// CloseObservedGone means a successful listing no longer contained the tab.
	CloseObservedGone CloseObservation = "gone"
	// CloseUnobserved means the tab was never seen to disappear: either the
	// listing failed, or it was still present when the polls ran out.
	CloseUnobserved CloseObservation = "unobserved"
)

// Confirmed reports whether the tab was seen to disappear.
func (o CloseObservation) Confirmed() bool { return o == CloseObservedGone }

// CloseBackend is what closing a session needs from a terminal backend: the
// close itself and a discovery read to observe it.
type CloseBackend interface {
	backend.Terminator
	backend.Discoverer
}

// Close closes the group (a Kitty tab) and polls until it disappears.
// Returns ErrCloseTimeout (non-fatal) if the group is still present after
// closePollMax polls; callers may ignore this error.
func Close(term CloseBackend, group backend.Ref) error {
	_, err := CloseObserved(term, group)
	return err
}

// CloseObserved is Close with the poll's observation reported beside its error.
// It returns exactly the error Close returns; the observation is the extra fact,
// not a change to the existing contract.
func CloseObserved(term CloseBackend, group backend.Ref) (CloseObservation, error) {
	closeDebug.Printf("CloseTab: sending close for group=%s", group)
	if err := term.Close(group); err != nil {
		closeDebug.Printf("CloseTab: error=%v", err)
		return CloseUnobserved, err
	}
	closeDebug.Printf("CloseTab: success, starting PollClosed")
	return PollClosedObserved(term, group)
}

// PollClosed waits for group to disappear from backend discovery.
// Returns nil once the group is gone, ErrCloseTimeout after closePollMax polls.
func PollClosed(term backend.Discoverer, group backend.Ref) error {
	_, err := PollClosedObserved(term, group)
	return err
}

// PollClosedObserved is PollClosed with the observation reported beside the
// error, and is where both spellings are implemented.
//
// The first poll runs immediately so callers do not pay closePollDelay of
// wall-time when the tab has already disappeared by the time we ask. Subsequent
// polls sleep first so Kitty has a chance to actually process the close-tab.
//
// A failed listing returns nil — "assume it closed". That branch has no recorded
// rationale: `git log -S` and `git blame` both place its origin in df9908f, the
// original Go port that replaced the Bash/Python toolchain, and neither the
// commit nor any comment says why. It is preserved rather than corrected because
// callers depend on it: Restart closes after it has already launched the
// replacement, and turning a transient `kitten @ ls` failure into a close error
// there would report a restart that did happen as one that did not. What changes
// here is only what such a poll is allowed to CLAIM — it reports CloseUnobserved,
// so a caller that needs the tab to actually be gone can tell that case apart
// from a tab seen to disappear.
func PollClosedObserved(term backend.Discoverer, group backend.Ref) (CloseObservation, error) {
	for i := range closePollMax {
		if i > 0 {
			time.Sleep(closePollDelay)
		}
		closeDebug.Printf("PollClosed: poll %d/%d for group=%s", i+1, closePollMax, group)
		panes, err := term.Discover()
		if err != nil {
			closeDebug.Printf("PollClosed: Discover error=%v, assuming closed", err)
			return CloseUnobserved, nil
		}
		if !backend.GroupPresent(panes, group) {
			closeDebug.Printf("PollClosed: group %s gone", group)
			return CloseObservedGone, nil
		}
		closeDebug.Printf("PollClosed: group %s still present", group)
	}
	closeDebug.Printf("PollClosed: timeout for group=%s", group)
	return CloseUnobserved, ErrCloseTimeout
}
