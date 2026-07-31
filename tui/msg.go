package tui

import (
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/session"
)

// tickMsg is sent by the refresh ticker on each interval.
type tickMsg struct{}

// sessionsMsg carries a fresh session list from Kitty plus the daemon's
// per-window last-activity timestamps and watchdog snapshot (nil if the
// daemon is unreachable).
type sessionsMsg struct {
	sessions       []session.RunningSession
	activity       map[int]time.Time
	watchdog       map[int]daemon.WatchdogState
	states         map[int]daemon.WindowStateSnapshot
	selectionTabID int
	selectionToken string
}

// activeLoopsMsg carries the latest active agent-session-loop discovery result.
type activeLoopsMsg struct {
	seq        int
	candidates []agentloop.Candidate
	err        error
}

// errMsg carries an error from a Kitty query.
type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

// actionDoneMsg is sent when a close or restart action completes.
type actionDoneMsg struct{ err error }

// copyDoneMsg is sent when a viewer copy action completes.
type copyDoneMsg struct{ err error }

// resortDoneMsg is sent when a session.Resort call completes.
type resortDoneMsg struct{ err error }

// pickerOpenDoneMsg is sent when one or more open-dir actions, or an
// open-in-editor action, complete from any entry point (list, picker, actions
// overlay). The model quits on success (err == nil) and surfaces err in the
// header otherwise.
type pickerOpenDoneMsg struct{ err error }

// focusDoneMsg is sent when a focus-tab action completes.
type focusDoneMsg struct{ err error }

// peekDoneMsg is sent when the overlay pager launch completes.
type peekDoneMsg struct{ err error }

// detailEventsMsg carries the per-window hook event ring buffer fetched
// from the daemon. windowID identifies which window the query was for so
// the Update path can drop stale responses (e.g. the user closed the
// detail pane before the round-trip completed).
type detailEventsMsg struct {
	windowID int
	events   []daemon.SessionEvent
	err      error
}

// healthMsg carries the result of a daemon/hooks health refresh. Each
// section is only populated when the corresponding flag was set on the
// dispatching command, so partial refreshes (daemon-only or hooks-only)
// don't clobber the cached value of the other section.
type healthMsg struct {
	daemonChecked bool
	daemonAlive   bool

	hookChecked bool
	hookSummary HookHealth
	hookErr     error
}

// HookHealth is the TUI-local mirror of hooks.HookHealthSummary, kept in
// this package so model.go and list.go can refer to it without importing
// internal/hooks transitively in every test file.
type HookHealth struct {
	InSync  []string
	Drifted []string
	Missing []string
}

// Healthy reports whether every profile is in sync.
func (h HookHealth) Healthy() bool {
	return len(h.Drifted) == 0 && len(h.Missing) == 0
}
