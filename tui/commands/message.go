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

// TopologySource names where one refresh's window tree came from. It is the
// difference between a dashboard that reads the daemon's cached listing and one
// that ran a `kitten @ ls` subprocess of its own.
type TopologySource string

const (
	// TopologyFromDaemon is the normal path: the tree came from the daemon's
	// published snapshot, so this process ran no Kitty subprocess at all.
	TopologyFromDaemon TopologySource = "daemon"
	// TopologyFromDirect means the refresh listed Kitty itself because the
	// daemon was unreachable, rejected the query, had never published a
	// snapshot, predates the snapshot block, or published one that is not
	// provably this dashboard's own Kitty instance — unattributed on either
	// side, or attributed to a different instance.
	TopologyFromDirect TopologySource = "direct"
)

// TopologyFreshness travels with every successful refresh and describes the
// window tree it carried: where it came from, how old it is, and whether the
// daemon has already ruled it too old to act on.
//
// Stale is the daemon's own verdict, shipped as a boolean rather than recomputed
// here from Age, so the dashboard and the daemon cannot disagree about the same
// snapshot. Generation, not CapturedAt, orders two daemon snapshots: concurrent
// listers stamp their own completion times, so capture times across successive
// generations are not monotonic. A direct listing carries its local completion
// time as CapturedAt, generation zero, and is never stale.
type TopologyFreshness struct {
	Source     TopologySource
	Stale      bool
	Generation uint64
	CapturedAt time.Time
	Age        time.Duration
}

// SessionsMsg carries a fresh session list plus the daemon's per-window
// last-activity timestamps and watchdog snapshot (nil if the daemon is
// unreachable).
//
// Seq is the refresh sequence the model's controller handed the command that
// produced this message. A completion whose Seq is not the one currently
// outstanding was superseded before it landed and is discarded, so a slow
// refresh can never move the list backwards over a newer one. Sequences start
// at one, which leaves the zero value recognizable as "never issued".
type SessionsMsg struct {
	Seq            int
	Sessions       []session.RunningSession
	Activity       map[int]time.Time
	Watchdog       map[int]protocol.WatchdogState
	States         map[int]protocol.WindowStateSnapshot
	SelectionTabID int
	SelectionToken string
	Topology       TopologyFreshness
}

// ActiveLoopsMsg carries the latest active agent-session-loop discovery result.
type ActiveLoopsMsg struct {
	Seq        int
	Candidates []agentloop.Candidate
	Err        error
}

// ErrMsg carries a refresh failure: neither the daemon nor a direct listing
// could produce a window tree. It deliberately carries no sessions, so the
// dashboard keeps the last list it successfully rendered.
//
// Seq follows the same contract as SessionsMsg.Seq: it settles the refresh
// controller only when it matches the refresh currently outstanding.
type ErrMsg struct {
	Seq int
	Err error
}

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
