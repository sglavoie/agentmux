package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

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
	acc, hasAcc := findAccountByName(m.cfg.Accounts, rs.EffectiveProfile())

	cmds := []tea.Cmd{closeCmd(m.client, rs.TabID)}

	if !hasAcc {
		text := fmt.Sprintf("closed %s", runningSessionDisplayTitle(rs))
		if c := m.pushToast(Toast{Severity: toastInfo, Text: text}); c != nil {
			cmds = append(cmds, c)
		}
		return tea.Batch(cmds...)
	}

	m.pendingUndo = append(m.pendingUndo, undoEntry{
		snapshot: snapshot,
		account:  acc,
		deadline: time.Now().Add(undoWindow),
		label:    runningSessionDisplayTitle(rs),
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
	cmds := []tea.Cmd{restartCmd(m.client, rs, m.cfg)}
	text := fmt.Sprintf("restarted %s", runningSessionDisplayTitle(rs))
	if c := m.pushToast(Toast{Severity: toastInfo, Text: text}); c != nil {
		cmds = append(cmds, c)
	}
	return tea.Batch(cmds...)
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
	acc := entry.account
	cfg := m.cfg
	client := m.client
	launch := func() tea.Msg {
		err := session.Launch(client, sess, acc, cfg, session.LaunchOpt{SkipDedup: true})
		return actionDoneMsg{err: err}
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
