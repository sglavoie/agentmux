package tui

import (
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// tickMsg is sent by the refresh ticker on each interval.
type tickMsg struct{}

// sessionsMsg carries a fresh session list from Kitty plus the daemon's
// per-window last-activity timestamps (nil if the daemon is unreachable).
type sessionsMsg struct {
	sessions []session.RunningSession
	activity map[int]time.Time
}

// errMsg carries an error from a Kitty query.
type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

// actionDoneMsg is sent when a close or restart action completes.
type actionDoneMsg struct{ err error }

// pickerOpenDoneMsg is sent when a picker open-directory action completes.
type pickerOpenDoneMsg struct{ err error }

// focusDoneMsg is sent when a focus-tab action completes.
type focusDoneMsg struct{ err error }

// peekDoneMsg is sent when the overlay pager launch completes.
type peekDoneMsg struct{ err error }
