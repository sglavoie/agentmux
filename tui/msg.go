package tui

import "github.com/sglavoie/cc-manager-kitty/internal/session"

// tickMsg is sent by the refresh ticker on each interval.
type tickMsg struct{}

// sessionsMsg carries a fresh session list from Kitty.
type sessionsMsg struct {
	sessions []session.RunningSession
}

// errMsg carries an error from a Kitty query.
type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

// actionDoneMsg is sent when a close or restart action completes.
type actionDoneMsg struct{ err error }

// pickerLaunchDoneMsg is sent when picker launch completes (one or more sessions).
type pickerLaunchDoneMsg struct{ errs []error }

// pickerOpenDoneMsg is sent when a picker open-directory action completes.
type pickerOpenDoneMsg struct{ err error }

// focusDoneMsg is sent when a focus-tab action completes.
type focusDoneMsg struct{ err error }

// peekDoneMsg is sent when the overlay pager launch completes.
type peekDoneMsg struct{ err error }
