// This file is the root's transient feedback: the toast queue that reports
// what just happened, and the undo stack whose entries those toasts offer to
// reverse.

package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// toastSeverity classifies a toast for styling and ordering.
type toastSeverity int

const (
	toastInfo toastSeverity = iota
	toastSuccess
	toastWarning
	toastError
)

// maxToasts caps the number of transient status messages retained. Older
// toasts are dropped when a push would exceed this — newest-on-top means the
// sticky status bar always sees the most recent active action first.
const maxToasts = 3

// defaultToastTTL is the lifetime of a transient toast. Progress toasts
// reuse this so a stalled launch chain still clears itself eventually.
const defaultToastTTL = 4 * time.Second

// progressToastTTL is the lifetime of in-flight progress toasts — short
// enough that an abandoned launch doesn't linger, long enough that the
// per-launch tick (kitten @ launch is sub-second) won't TTL mid-flight.
const progressToastTTL = 10 * time.Second

// toastTickInterval drives the TTL sweeper. Coarse enough to avoid waking
// the runloop too often, fine enough that visible expiry is snappy.
const toastTickInterval = 250 * time.Millisecond

// Toast is a transient message shown through the sticky status bar.
type Toast struct {
	Severity toastSeverity
	Text     string
	Expires  time.Time
	// id ties a toast to a logical channel (e.g., a launch batch) so a
	// follow-up push can replace the existing toast instead of stacking
	// alongside it. Zero means "not addressable" — the toast is purely
	// transient.
	id int
}

// toastTickMsg fires on toastTickInterval while at least one toast is
// alive. Handler sweeps expired toasts and re-arms the ticker if any
// remain.
type toastTickMsg struct{}

// launchProgressMsg reports the result of a single picker-launched
// session. The picker dispatches one of these per selected session via
// tea.Batch so the toast subsystem can show 1/N, 2/N progress.
type launchProgressMsg struct {
	batchID int
	total   int
	title   string
	err     error
}

// toastTickCmd schedules the next toast TTL sweep.
func toastTickCmd() tea.Cmd {
	return tea.Tick(toastTickInterval, func(time.Time) tea.Msg {
		return toastTickMsg{}
	})
}

// pushToast appends t to m.toasts, capping at maxToasts (newest on top).
// Returns a cmd that arms the toast tick when the list transitions from
// empty to non-empty; otherwise nil (the existing tick handles TTL).
func (m *Model) pushToast(t Toast) tea.Cmd {
	if t.Expires.IsZero() {
		t.Expires = time.Now().Add(defaultToastTTL)
	}
	wasEmpty := len(m.toasts) == 0
	// Newest on top: prepend and trim.
	m.toasts = append([]Toast{t}, m.toasts...)
	if len(m.toasts) > maxToasts {
		m.toasts = m.toasts[:maxToasts]
	}
	if wasEmpty {
		return toastTickCmd()
	}
	return nil
}

// replaceToastByID replaces (or pushes) a toast carrying the given id.
// Used for progress toasts so successive "Launched 1/N · …" updates
// share one logical channel rather than stacking. Replacements move to the
// top so the sticky status bar treats the update as the latest transient.
// id == 0 means "not addressable" so the toast is appended as a fresh
// transient entry instead of merging with whatever happens to sit in the slot.
func (m *Model) replaceToastByID(id int, t Toast) tea.Cmd {
	if t.Expires.IsZero() {
		t.Expires = time.Now().Add(defaultToastTTL)
	}
	t.id = id
	if id != 0 {
		for i := range m.toasts {
			if m.toasts[i].id == id {
				if i == 0 {
					m.toasts[0] = t
					return nil
				}
				copy(m.toasts[1:i+1], m.toasts[0:i])
				m.toasts[0] = t
				return nil
			}
		}
	}
	return m.pushToast(t)
}

func (m *Model) clearToastByID(id int) {
	if id == 0 || len(m.toasts) == 0 {
		return
	}
	kept := m.toasts[:0]
	for _, t := range m.toasts {
		if t.id != id {
			kept = append(kept, t)
		}
	}
	m.toasts = kept
}

// sweepToasts drops every toast whose TTL has elapsed. Returns a cmd
// re-arming the tick when any toast is still alive; nil otherwise so the
// ticker quiesces when nothing is on screen.
func (m *Model) sweepToasts() tea.Cmd {
	now := time.Now()
	kept := m.toasts[:0]
	for _, t := range m.toasts {
		if now.Before(t.Expires) {
			kept = append(kept, t)
		}
	}
	m.toasts = kept
	if len(m.toasts) == 0 {
		return nil
	}
	return toastTickCmd()
}

// undoCloseToastID is the addressable id shared by every close-with-undo
// toast. Successive closes within the undo window collapse into a single
// "closed N sessions" message via replaceToastByID.
const undoCloseToastID = -1

// undoWindow is how long a close remains undoable after dispatch. Aligned
// with the close-toast TTL so the visible affordance and the active window
// stay in sync.
const undoWindow = 5 * time.Second

// undoEntry captures everything needed to relaunch a closed session: the
// reconstructed config.Session snapshot, the resolved account, and the
// deadline past which the entry is no longer eligible for undo.
type undoEntry struct {
	snapshot config.Session
	account  config.Account
	deadline time.Time
	label    string // short label for the toast (typically the row's project title)
}

// closeWithToast snapshots rs, dispatches the close, and pushes an
// aggregating "u to undo" toast. When the running session has no matching
// account in the loaded config, the snapshot is best-effort: the close
// still fires but the toast omits the undo affordance.
func (m *Model) closeWithToast(rs session.RunningSession) tea.Cmd {
	snapshot := session.SessionFromRunning(rs)
	acc, hasAcc := commands.FindAccountByName(m.cfg.Accounts, rs.EffectiveProfile())

	cmds := []tea.Cmd{commands.Close(m.client, rs.TabID)}

	if !hasAcc {
		text := fmt.Sprintf("closed %s", dashboard.SessionDisplayTitle(rs))
		if c := m.pushToast(Toast{Severity: toastInfo, Text: text}); c != nil {
			cmds = append(cmds, c)
		}
		return tea.Batch(cmds...)
	}

	m.pendingUndo = append(m.pendingUndo, undoEntry{
		snapshot: snapshot,
		account:  acc,
		deadline: time.Now().Add(undoWindow),
		label:    dashboard.SessionDisplayTitle(rs),
	})

	if c := m.replaceToastByID(undoCloseToastID, Toast{
		Severity: toastInfo,
		Text:     m.undoToastText(),
		Expires:  time.Now().Add(undoWindow),
	}); c != nil {
		cmds = append(cmds, c)
	}
	return tea.Batch(cmds...)
}

// restartWithToast dispatches the restart and pushes a transient
// confirmation toast. Restart is not undoable in this flow because Restart
// is internally close+launch — there is no separate state to roll back.
func (m *Model) restartWithToast(rs session.RunningSession) tea.Cmd {
	cmds := []tea.Cmd{commands.Restart(m.client, rs, m.cfg)}
	text := fmt.Sprintf("restarted %s", dashboard.SessionDisplayTitle(rs))
	if c := m.pushToast(Toast{Severity: toastInfo, Text: text}); c != nil {
		cmds = append(cmds, c)
	}
	return tea.Batch(cmds...)
}

// lockedDenyToast pushes the standard "session is locked" denial toast and
// returns the cmd that arms the toast tick. Reused by every direct keypress
// that would otherwise destroy or override a locked session so the user
// sees a uniform message regardless of which key was rejected.
func (m *Model) lockedDenyToast() tea.Cmd {
	return m.pushToast(Toast{
		Severity: toastError,
		Text:     "session is locked — press L to unlock",
	})
}

// undoToastText returns the user-facing message for the aggregating undo
// toast. A single pending entry shows its label; multiple entries collapse
// into a count with "undo last" so the affordance stays accurate.
func (m *Model) undoToastText() string {
	active := m.activeUndoEntries()
	switch len(active) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("closed %s · u to undo (5s)", active[0].label)
	default:
		return fmt.Sprintf("closed %d sessions · u to undo last (5s)", len(active))
	}
}

// activeUndoEntries returns the pending entries whose deadline is still in
// the future. Used both to compose the toast label and to gate the `u`
// handler so an expired entry is never silently revived.
func (m *Model) activeUndoEntries() []undoEntry {
	now := time.Now()
	out := make([]undoEntry, 0, len(m.pendingUndo))
	for _, e := range m.pendingUndo {
		if now.Before(e.deadline) {
			out = append(out, e)
		}
	}
	return out
}

// popUndo removes and returns the most recent pending entry that hasn't yet
// expired. Stale entries are dropped from the slice so a no-op `u` press
// also reclaims memory consumed by abandoned actions.
func (m *Model) popUndo() (undoEntry, bool) {
	m.sweepUndo()
	if len(m.pendingUndo) == 0 {
		return undoEntry{}, false
	}
	last := len(m.pendingUndo) - 1
	entry := m.pendingUndo[last]
	m.pendingUndo = m.pendingUndo[:last]
	return entry, true
}

// sweepUndo drops any entries past their deadline. Called from the
// toastTickMsg handler so the pending list cannot grow unbounded after
// repeated rapid closes that the user never undoes.
func (m *Model) sweepUndo() {
	if len(m.pendingUndo) == 0 {
		return
	}
	now := time.Now()
	kept := m.pendingUndo[:0]
	for _, e := range m.pendingUndo {
		if now.Before(e.deadline) {
			kept = append(kept, e)
		}
	}
	m.pendingUndo = kept
}

// undoLastCmd consumes the most-recent undoable entry and dispatches a
// relaunch using the captured snapshot. Updates the aggregating toast so
// the remaining undo count (or success message) stays accurate.
func (m *Model) undoLastCmd() tea.Cmd {
	entry, ok := m.popUndo()
	if !ok {
		return nil
	}

	sess := entry.snapshot
	cfg := m.cfg
	client := m.client
	launch := func() tea.Msg {
		_, err := commands.Registry.Execute(context.Background(), actions.Dependencies{Kitty: client, Config: cfg}, actions.Launch, actions.Request{Project: &sess})
		return commands.ActionDoneMsg{Err: err}
	}

	cmds := []tea.Cmd{launch}

	if remaining := len(m.activeUndoEntries()); remaining > 0 {
		if c := m.replaceToastByID(undoCloseToastID, Toast{
			Severity: toastInfo,
			Text:     m.undoToastText(),
			Expires:  time.Now().Add(undoWindow),
		}); c != nil {
			cmds = append(cmds, c)
		}
	} else {
		if c := m.replaceToastByID(undoCloseToastID, Toast{
			Severity: toastSuccess,
			Text:     fmt.Sprintf("restored %s", entry.label),
		}); c != nil {
			cmds = append(cmds, c)
		}
	}
	return tea.Batch(cmds...)
}
