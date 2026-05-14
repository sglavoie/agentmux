package session

import (
	"errors"
	"time"

	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
)

const (
	closePollMax   = 5
	closePollDelay = 100 * time.Millisecond
)

// ErrCloseTimeout is returned when the tab did not disappear within the poll
// window. The close command was still sent; the tab likely closed shortly after.
var ErrCloseTimeout = errors.New("tab close timed out; proceeding anyway")

var closeDebug = newFileLogger("/tmp/ccm-debug.log", "[close] ")

// Close closes the tab with the given ID and polls until it disappears.
// Returns ErrCloseTimeout (non-fatal) if the tab is still present after
// closePollMax polls; callers may ignore this error.
func Close(client kitty.KittyClient, tabID int) error {
	closeDebug.Printf("CloseTab: sending close for tabID=%d", tabID)
	if err := client.CloseTab(kitty.MatchTabByID(tabID)); err != nil {
		closeDebug.Printf("CloseTab: error=%v", err)
		return err
	}
	closeDebug.Printf("CloseTab: success, starting PollClosed")
	return PollClosed(client, tabID)
}

// PollClosed waits for the tab with tabID to disappear from kitten @ ls.
// Returns nil once the tab is gone, ErrCloseTimeout after closePollMax polls.
func PollClosed(client kitty.KittyClient, tabID int) error {
	for i := range closePollMax {
		time.Sleep(closePollDelay)
		closeDebug.Printf("PollClosed: poll %d/%d for tabID=%d", i+1, closePollMax, tabID)
		osWindows, err := client.ListWindows()
		if err != nil {
			closeDebug.Printf("PollClosed: ListWindows error=%v, assuming closed", err)
			return nil
		}
		if !tabPresent(osWindows, tabID) {
			closeDebug.Printf("PollClosed: tab %d gone", tabID)
			return nil
		}
		closeDebug.Printf("PollClosed: tab %d still present", tabID)
	}
	closeDebug.Printf("PollClosed: timeout for tabID=%d", tabID)
	return ErrCloseTimeout
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
