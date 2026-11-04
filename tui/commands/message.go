package commands

import (
	"time"

	tea "charm.land/bubbletea/v2"

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

// TopologySource names where a session refresh obtained its topology.
type TopologySource string

const (
	// TopologyFromDaemon identifies a daemon-published snapshot.
	TopologyFromDaemon TopologySource = "daemon"
	// TopologyFromDirect identifies direct discovery of registered tmux sessions.
	TopologyFromDirect TopologySource = "direct"
)

// TopologyFreshness travels with every successful refresh and describes the
// session list it carried: where it came from, when it was captured, and
// whether the source marked it stale.
type TopologyFreshness struct {
	Source     TopologySource
	Stale      bool
	Generation uint64
	CapturedAt time.Time
	Age        time.Duration
}

// SessionsMsg carries the discovered tmux sessions and any daemon activity,
// watchdog, and reducer state successfully joined to those registered slots.
//
// Seq is the refresh sequence the model's controller handed the command that
// produced this message. A completion whose Seq is not the one currently
// outstanding was superseded before it landed and is discarded, so a slow
// refresh can never move the list backwards over a newer one. Sequences start
// at one, which leaves the zero value recognizable as "never issued".
// HostWindowID and HostTabID identify the session hosting this surface when
// known. SelectionTabID is a one-time requested selection.
type SessionsMsg struct {
	Seq            int
	Sessions       []session.RunningSession
	Activity       map[int]time.Time
	Watchdog       map[int]protocol.WatchdogState
	States         map[int]protocol.WindowStateSnapshot
	SelectionTabID int
	SelectionToken string
	Topology       TopologyFreshness
	HostWindowID   int
	HostTabID      int
	// PortableErr reports that the tmux rows in Sessions were not read by
	// this refresh: the backend failed or its server is gone, and the rows
	// are the last ones listed. Nil on an ordinary refresh.
	PortableErr error
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
//
// Request names the dispatch this completion answers, or zero when the action
// was started outside the shared session-action controller — an undo, a
// worktree mutation, a direct call. A consumer correlates on it rather than
// assuming the only outstanding action is the one it started, which is what
// keeps a slow close from reporting its error over a restart the operator ran
// after it.
//
// Unconfirmed is the third answer, between the error and the silence: the action
// was accepted and did not fail, but the effect it was asked for was never
// observed. A close whose tab was still listed when the polls ran out, or whose
// listing could not be read at all, reports it. Nothing that is merely slow sets
// it — it means "nobody looked, or looking said no" — so a consumer may treat it
// as "do not advertise anything that depends on this having happened" without
// treating it as a failure.
type ActionDoneMsg struct {
	Err         error
	Request     uint64
	Unconfirmed bool
}

// StampRequest marks every ActionDoneMsg a command eventually produces with the
// request identity it was dispatched under.
//
// It descends into a batch because the commands it wraps are routinely batches:
// a close is the tmux close and the undo toast together, and a stamp that only
// looked at the outer message would silently correlate nothing. Every other
// message passes through untouched, so wrapping a command that reports
// something else costs nothing but the closure.
func StampRequest(cmd tea.Cmd, request uint64) tea.Cmd {
	if cmd == nil || request == 0 {
		return cmd
	}
	return func() tea.Msg {
		switch msg := cmd().(type) {
		case ActionDoneMsg:
			msg.Request = request
			return msg
		case tea.BatchMsg:
			stamped := make(tea.BatchMsg, 0, len(msg))
			for _, inner := range msg {
				stamped = append(stamped, StampRequest(inner, request))
			}
			return stamped
		default:
			return msg
		}
	}
}

// CopyDoneMsg is sent when a viewer copy action completes. Text is what was
// handed to the clipboard, so a success can say what landed there.
type CopyDoneMsg struct {
	Err  error
	Text string
}

// PickerOpenDoneMsg is sent when one or more open-dir actions, or an
// open-in-editor action, complete from any entry point (list, picker, actions
// overlay). The model quits on success (Err == nil) and surfaces Err in the
// header otherwise.
type PickerOpenDoneMsg struct {
	Err error
}

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
