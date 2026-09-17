package session

import (
	"errors"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
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

// Close closes the tab with the given ID and polls until it disappears.
// Returns ErrCloseTimeout (non-fatal) if the tab is still present after
// closePollMax polls; callers may ignore this error.
func Close(client kitty.KittyClient, tabID int) error {
	_, err := CloseObserved(client, tabID)
	return err
}

// CloseObserved is Close with the poll's observation reported beside its error.
// It returns exactly the error Close returns; the observation is the extra fact,
// not a change to the existing contract.
func CloseObserved(client kitty.KittyClient, tabID int) (CloseObservation, error) {
	closeDebug.Printf("CloseTab: sending close for tabID=%d", tabID)
	if err := client.CloseTab(kitty.MatchTabByID(tabID)); err != nil {
		closeDebug.Printf("CloseTab: error=%v", err)
		return CloseUnobserved, err
	}
	closeDebug.Printf("CloseTab: success, starting PollClosed")
	return PollClosedObserved(client, tabID)
}

// PollClosed waits for the tab with tabID to disappear from kitten @ ls.
// Returns nil once the tab is gone, ErrCloseTimeout after closePollMax polls.
func PollClosed(client kitty.KittyClient, tabID int) error {
	_, err := PollClosedObserved(client, tabID)
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
func PollClosedObserved(client kitty.KittyClient, tabID int) (CloseObservation, error) {
	for i := range closePollMax {
		if i > 0 {
			time.Sleep(closePollDelay)
		}
		closeDebug.Printf("PollClosed: poll %d/%d for tabID=%d", i+1, closePollMax, tabID)
		osWindows, err := client.ListWindows()
		if err != nil {
			closeDebug.Printf("PollClosed: ListWindows error=%v, assuming closed", err)
			return CloseUnobserved, nil
		}
		if !tabPresent(osWindows, tabID) {
			closeDebug.Printf("PollClosed: tab %d gone", tabID)
			return CloseObservedGone, nil
		}
		closeDebug.Printf("PollClosed: tab %d still present", tabID)
	}
	closeDebug.Printf("PollClosed: timeout for tabID=%d", tabID)
	return CloseUnobserved, ErrCloseTimeout
}

func tabPresent(osWindows []kitty.OSWindow, tabID int) bool {
	for _, osw := range osWindows {
		for _, tab := range osw.Tabs {
			if tab.ID == tabID {
				return true
			}
		}
	}
	return false
}
