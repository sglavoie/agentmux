// Package commands owns the TUI's command surface: what the dashboard can be
// asked to do, whether it may be done right now, and how the doing is carried
// out away from the Bubble Tea update loop.
//
// Three things live here. First, the command and action vocabulary — stable
// command IDs and groups, the captured target context, the availability
// verdict with its inline reasons, and the single guarded entry point that
// refuses a hidden or unavailable command before it can execute. Second, the
// command palette and the per-row actions overlay: query matching, result
// ordering, display-row layout, scroll geometry, and the rendering of both.
// Third, the effect layer — every tea.Cmd the dashboard starts (Kitty
// enumeration, daemon queries, harness lifecycle, clipboard, editor and shell
// launches, health probes, agent-session-loop discovery, URL opening) and every
// tea.Msg those effects report back with.
//
// The package is parameterized over the root program model rather than
// importing it: Command[M] and Action[M] carry the root's own closures, so the
// gates and the palette evaluate against live model state without this package
// ever knowing what a model is. Deciding what a command DOES — mutating model
// fields, switching modes, pushing toasts, wiring the update loop — stays in
// the root package, which owns the model and every catalog entry's closure.
package commands

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
)

// TickMsg is sent by the refresh ticker on each interval.
type TickMsg struct{}

// Tick schedules a single TickMsg after d.
func Tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return TickMsg{}
	})
}

// SessionsMsg carries a fresh session list from Kitty plus the daemon's
// per-window last-activity timestamps and watchdog snapshot (nil if the
// daemon is unreachable).
type SessionsMsg struct {
	Sessions       []session.RunningSession
	Activity       map[int]time.Time
	Watchdog       map[int]protocol.WatchdogState
	States         map[int]protocol.WindowStateSnapshot
	SelectionTabID int
	SelectionToken string
}

// ActiveLoopsMsg carries the latest active agent-session-loop discovery result.
type ActiveLoopsMsg struct {
	Seq        int
	Candidates []agentloop.Candidate
	Err        error
}

// ErrMsg carries an error from a Kitty query.
type ErrMsg struct{ Err error }

func (e ErrMsg) Error() string { return e.Err.Error() }

// ActionDoneMsg is sent when a close or restart action completes.
type ActionDoneMsg struct{ Err error }

// CopyDoneMsg is sent when a viewer copy action completes.
type CopyDoneMsg struct{ Err error }

// ResortDoneMsg is sent when a session.Resort call completes.
type ResortDoneMsg struct{ Err error }

// PickerOpenDoneMsg is sent when one or more open-dir actions, or an
// open-in-editor action, complete from any entry point (list, picker, actions
// overlay). The model quits on success (Err == nil) and surfaces Err in the
// header otherwise.
type PickerOpenDoneMsg struct{ Err error }

// FocusDoneMsg is sent when a focus-tab action completes.
type FocusDoneMsg struct{ Err error }

// PeekDoneMsg is sent when the overlay pager launch completes.
type PeekDoneMsg struct{ Err error }

// DetailEventsMsg carries the per-window hook event ring buffer fetched
// from the daemon. WindowID identifies which window the query was for so
// the Update path can drop stale responses (e.g. the user closed the
// detail pane before the round-trip completed).
type DetailEventsMsg struct {
	WindowID int
	Events   []protocol.SessionEvent
	Err      error
}

// HealthMsg carries the result of a daemon/hooks health refresh. Each
// section is only populated when the corresponding flag was set on the
// dispatching command, so partial refreshes (daemon-only or hooks-only)
// don't clobber the cached value of the other section.
type HealthMsg struct {
	DaemonChecked bool
	DaemonAlive   bool

	HookChecked bool
	HookSummary HookHealth
	HookErr     error
}

// HookHealth is the TUI-local mirror of hooks.HookHealthSummary, kept in
// this package so the root model and list renderer can refer to it without
// importing internal/hooks transitively in every test file.
type HookHealth struct {
	InSync  []string
	Drifted []string
	Missing []string
}

// Healthy reports whether every profile is in sync.
func (h HookHealth) Healthy() bool {
	return len(h.Drifted) == 0 && len(h.Missing) == 0
}
