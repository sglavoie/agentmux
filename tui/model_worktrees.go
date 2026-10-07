// This file is what the root model does about managed worktrees: loading and
// filtering the list, moving the cursor through it, and driving the lifecycle
// actions a row can trigger, including the confirmations they require.

package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/tui/commands"
	tuiworktrees "github.com/sglavoie/agentmux/tui/worktrees"
)

// Worktrees view: loading the managed-worktree list, sorting and filtering it,
// and moving the cursor through it. The lifecycle actions a row can trigger
// live in model_worktrees_lifecycle.go, and the ambiguity picker a reopen can
// raise lives in model_live_session_picker.go.

type worktreesLoadedMsg struct {
	seq      int
	entries  []actions.WorktreeView
	warnings []string
	err      error
}

type worktreeActionDoneMsg struct {
	action actions.ID
	result actions.Result
	err    error
}

func worktreeListCmd(m Model, seq int) tea.Cmd {
	return func() tea.Msg {
		result, err := commands.Registry.Execute(context.Background(), actions.Dependencies{
			Kitty:  m.client,
			Config: m.cfg,
		}, actions.WorktreeList, actions.Request{})
		if err != nil {
			return worktreesLoadedMsg{seq: seq, err: err}
		}
		entries, ok := result.Data["worktrees"].([]actions.WorktreeView)
		if !ok {
			return worktreesLoadedMsg{seq: seq, err: fmt.Errorf("worktree list returned an invalid worktrees payload")}
		}
		warnings, _ := result.Data["warnings"].([]string)
		return worktreesLoadedMsg{
			seq:      seq,
			entries:  append([]actions.WorktreeView(nil), entries...),
			warnings: append([]string(nil), warnings...),
		}
	}
}

func (m Model) enterWorktrees() (tea.Model, tea.Cmd) {
	m.mode = ModeWorktrees
	m.worktreeFilterMode = false
	m.worktreeErr = nil
	if len(m.worktreeFiltered) == 0 {
		m.worktreeCursor = 0
		m.worktreeOffset = 0
	}
	return m.startWorktreeRefresh()
}

func (m Model) startWorktreeRefresh() (tea.Model, tea.Cmd) {
	m.captureWorktreeSelection()
	m.worktreeLoading = true
	m.worktreeErr = nil
	m.worktreeRefreshSeq++
	return m, m.runWorktreeListCmd(m.worktreeRefreshSeq)
}

func (m *Model) captureWorktreeSelection() {
	if m.worktreeCursor >= 0 && m.worktreeCursor < len(m.worktreeFiltered) {
		m.worktreeSelectedID = m.worktreeFiltered[m.worktreeCursor].ID
	}
}

func (m Model) handleWorktreesLoaded(msg worktreesLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.worktreeRefreshSeq || m.activeRootMode() != ModeWorktrees {
		return m, nil
	}
	m.worktreeLoading = false
	if msg.err != nil {
		m.worktreeErr = msg.err
		return m, nil
	}
	m.worktreeErr = nil
	m.worktreeWarnings = append([]string(nil), msg.warnings...)
	m.worktreeEntries = tuiworktrees.SortViews(msg.entries)
	m.reapplyWorktreeFilter()
	return m, nil
}

func (m *Model) reapplyWorktreeFilter() {
	filtered := make([]actions.WorktreeView, 0, len(m.worktreeEntries))
	for _, entry := range m.worktreeEntries {
		if tuiworktrees.MatchesFilter(entry, m.worktreeFilter) {
			filtered = append(filtered, entry)
		}
	}
	m.worktreeFiltered = filtered
	m.restoreWorktreeSelection()
}

func (m *Model) restoreWorktreeSelection() {
	previous := m.worktreeCursor
	m.worktreeCursor = clamp(previous, 0, len(m.worktreeFiltered)-1)
	if len(m.worktreeFiltered) == 0 {
		m.worktreeCursor = 0
	}
	if m.worktreeSelectedID != "" {
		for i := range m.worktreeFiltered {
			if m.worktreeFiltered[i].ID == m.worktreeSelectedID {
				m.worktreeCursor = i
				break
			}
		}
	}
	if len(m.worktreeFiltered) == 0 {
		m.worktreeOffset = 0
	}
	scrollWorktreesToCursor(m)
}

func (m Model) handleWorktreesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.worktreeFilterMode {
		return m.handleWorktreeFilterKey(msg)
	}
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, keyQ:
		if m.worktreeFilter != "" {
			m.worktreeFilter = ""
			m.captureWorktreeSelection()
			m.reapplyWorktreeFilter()
			return m, nil
		}
		m.mode = ModeList
		m.worktreeFilterMode = false
		return m, nil
	case keySlash:
		m.worktreeFilterMode = true
		return m, nil
	case keyQuestion:
		m.openHelp(ModeWorktrees)
		return m, nil
	case keyR:
		return m.startWorktreeRefresh()
	case keyEnter:
		return m.reopenSelectedWorktree()
	case keyJ, keyDown:
		m.worktreeCursor = clamp(m.worktreeCursor+1, 0, len(m.worktreeFiltered)-1)
	case keyK, keyUp:
		m.worktreeCursor = clamp(m.worktreeCursor-1, 0, len(m.worktreeFiltered)-1)
	case keyG, keyHome:
		m.worktreeCursor = 0
	case keyGG, keyEnd:
		m.worktreeCursor = len(m.worktreeFiltered) - 1
		if m.worktreeCursor < 0 {
			m.worktreeCursor = 0
		}
	case keyCtrlD:
		m.worktreeCursor = clamp(m.worktreeCursor+halfPageStep(worktreeVisibleHeight(m)), 0, len(m.worktreeFiltered)-1)
	case keyCtrlU:
		m.worktreeCursor = clamp(m.worktreeCursor-halfPageStep(worktreeVisibleHeight(m)), 0, len(m.worktreeFiltered)-1)
	case keyPgDown, keyCtrlF:
		m.worktreeCursor = clamp(m.worktreeCursor+fullPageStep(worktreeVisibleHeight(m)), 0, len(m.worktreeFiltered)-1)
	case keyPgUp, keyCtrlB:
		m.worktreeCursor = clamp(m.worktreeCursor-fullPageStep(worktreeVisibleHeight(m)), 0, len(m.worktreeFiltered)-1)
	case keyO:
		return m.openSelectedWorktree(actions.WorktreeShell)
	case keyOpenEditor:
		return m.openSelectedWorktree(actions.WorktreeEditor)
	case keyCopyPath:
		if path := selectedWorktreePath(m); path != "" {
			return m, commands.CopyText(path)
		}
		return m, nil
	case keyD:
		return m.beginWorktreeRemove(false)
	case keyDeleteWorktreeBranch:
		return m.beginWorktreeRemove(true)
	case keyForgetWorktree:
		return m.beginWorktreeForget()
	default:
		return m, nil
	}
	m.captureWorktreeSelection()
	scrollWorktreesToCursor(&m)
	return m, nil
}

func (m Model) handleWorktreeFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc:
		m.worktreeFilterMode = false
		return m, nil
	case keyEnter:
		m.worktreeFilterMode = false
		return m, nil
	case keyBackspace:
		if runes := []rune(m.worktreeFilter); len(runes) > 0 {
			m.worktreeFilter = string(runes[:len(runes)-1])
			m.captureWorktreeSelection()
			m.reapplyWorktreeFilter()
		}
		return m, nil
	default:
		if text := msg.Key().Text; text != "" {
			m.worktreeFilter += text
			m.captureWorktreeSelection()
			m.reapplyWorktreeFilter()
		}
		return m, nil
	}
}

func (m Model) selectedWorktree() (actions.WorktreeView, bool) {
	if m.worktreeCursor < 0 || m.worktreeCursor >= len(m.worktreeFiltered) {
		return actions.WorktreeView{}, false
	}
	return m.worktreeFiltered[m.worktreeCursor], true
}

func scrollWorktreesToCursor(m *Model) {
	total := len(m.worktreeFiltered)
	height := worktreeVisibleHeight(*m)
	if height <= 0 || total == 0 {
		m.worktreeOffset = 0
		return
	}
	m.worktreeCursor = clamp(m.worktreeCursor, 0, total-1)
	if m.worktreeCursor < m.worktreeOffset {
		m.worktreeOffset = m.worktreeCursor
	}
	for m.worktreeOffset < m.worktreeCursor {
		budget := height
		if m.worktreeOffset > 0 {
			budget--
		}
		if m.worktreeCursor < total-1 {
			budget--
		}
		if budget < 1 {
			budget = 1
		}
		if m.worktreeCursor-m.worktreeOffset+1 <= budget {
			break
		}
		m.worktreeOffset++
	}
	maxOffset := total - 1
	if m.worktreeOffset > maxOffset {
		m.worktreeOffset = maxOffset
	}
	if m.worktreeOffset < 0 {
		m.worktreeOffset = 0
	}
}

// Worktree lifecycle: opening a checkout, reopening a session against it, and
// the confirmed remove/forget mutations plus their outcomes. Every mutation
// routes through a confirmation and reports what durable steps actually ran,
// because a partial cleanup needs operator recovery rather than a silent retry.
// The list and its filter live in model_worktrees.go.

func (m Model) openSelectedWorktree(action actions.ID) (tea.Model, tea.Cmd) {
	if m.worktreeCursor < 0 || m.worktreeCursor >= len(m.worktreeFiltered) {
		return m, nil
	}
	entry := m.worktreeFiltered[m.worktreeCursor]
	if !entry.Exists {
		return m, nil
	}
	m.actionErr = nil
	return m, m.runWorktreeActionCmd(action, actions.Request{Worktree: &entry.Entry})
}

func worktreeLifecycleActionCmd(m Model, action actions.ID, request actions.Request) tea.Cmd {
	readers := m.transcriptReaders
	if readers == nil {
		readers = buildTranscriptReaders()
	}
	return func() tea.Msg {
		result, err := commands.Registry.Execute(context.Background(), actions.Dependencies{
			Kitty: m.client, Config: m.cfg, Readers: readers,
		}, action, request)
		return worktreeActionDoneMsg{action: action, result: result, err: err}
	}
}

func (m Model) reopenSelectedWorktree() (tea.Model, tea.Cmd) {
	entry, ok := m.selectedWorktree()
	if !ok {
		return m, nil
	}
	m.worktreeActionTarget = &entry
	return m.reopenWorktreeWithParameters(nil)
}

func (m Model) reopenWorktreeWithParameters(parameters map[string]string) (tea.Model, tea.Cmd) {
	if m.worktreeActionTarget == nil || m.worktreeActionInFlight {
		return m, nil
	}
	request := actions.Request{Worktree: &m.worktreeActionTarget.Entry, Parameters: parameters}
	availability := commands.Registry.Availability(actions.Dependencies{Config: m.cfg}, actions.WorktreeReopen, request)
	if !availability.Available {
		m.actionErr = &actions.Error{Code: "unavailable", Action: actions.WorktreeReopen, Message: availability.Reason}
		m.worktreeActionTarget = nil
		return m, nil
	}
	m.mode = ModeWorktrees
	m.actionErr = nil
	m.worktreeActionInFlight = true
	return m, m.runWorktreeActionCmd(actions.WorktreeReopen, request)
}

func (m Model) beginWorktreeRemove(deleteBranch bool) (tea.Model, tea.Cmd) {
	entry, ok := m.selectedWorktree()
	if !ok || m.worktreeActionInFlight {
		return m, nil
	}
	if reason := m.worktreeRemoveRefusal(entry, deleteBranch); reason != "" {
		return m.worktreeDisabledToast(reason)
	}
	m.worktreeActionTarget = &entry
	m.confirmReturnMode = ModeWorktrees
	if deleteBranch {
		m.confirmAction = "worktree delete branch"
		m.confirmConsequence = fmt.Sprintf("Deletes branch %q only after Git proves it is merged into recorded target %q.", entry.Branch, tuiworktrees.SourceLabel(entry))
	} else {
		m.confirmAction = "worktree remove"
		m.confirmConsequence = fmt.Sprintf("Removes the checkout and metadata; branch %q is retained.", entry.Branch)
	}
	m.mode = ModeConfirm
	m.actionErr = nil
	return m, nil
}

// worktreeRemoveRefusal is why d (or D, with deleteBranch) would refuse entry,
// or "" when it would ask for confirmation. The key handler and the footer
// both read it, so the footer never offers a removal the handler refuses.
func (m Model) worktreeRemoveRefusal(entry actions.WorktreeView, deleteBranch bool) string {
	parameters := map[string]string{}
	if deleteBranch {
		parameters["delete_branch"] = "true"
		if entry.Exists {
			switch {
			case entry.ReachableFromSource == nil:
				return "merge status against the recorded source branch is unavailable"
			case !*entry.ReachableFromSource:
				return "branch is not merged into the recorded source branch"
			}
		}
	}
	request := actions.Request{Worktree: &entry.Entry, Parameters: parameters}
	if availability := commands.Registry.Availability(actions.Dependencies{Config: m.cfg}, actions.WorktreeRemove, request); !availability.Available {
		return availability.Reason
	}
	return ""
}

// worktreeForgetRefusal is the same answer for F.
func (m Model) worktreeForgetRefusal(entry actions.WorktreeView) string {
	request := actions.Request{Worktree: &entry.Entry}
	if availability := commands.Registry.Availability(actions.Dependencies{Config: m.cfg}, actions.WorktreeForget, request); !availability.Available {
		return availability.Reason
	}
	return ""
}

func (m Model) beginWorktreeForget() (tea.Model, tea.Cmd) {
	entry, ok := m.selectedWorktree()
	if !ok || m.worktreeActionInFlight {
		return m, nil
	}
	if reason := m.worktreeForgetRefusal(entry); reason != "" {
		return m.worktreeDisabledToast(reason)
	}
	m.worktreeActionTarget = &entry
	m.confirmReturnMode = ModeWorktrees
	m.confirmAction = "worktree forget"
	m.confirmConsequence = "Deletes only Agent Mux sidecar metadata; the checkout and Git branches are unchanged."
	m.mode = ModeConfirm
	m.actionErr = nil
	return m, nil
}

func (m Model) worktreeDisabledToast(reason string) (tea.Model, tea.Cmd) {
	m.actionErr = nil
	cmd := m.pushToast(Toast{Severity: toastWarning, Text: "Unavailable: " + reason})
	return m, cmd
}

func (m Model) confirmedWorktreeMutation(deleteBranch bool) (tea.Model, tea.Cmd) {
	if m.worktreeActionTarget == nil {
		return m, nil
	}
	parameters := map[string]string{}
	if deleteBranch {
		parameters["delete_branch"] = "true"
	}
	request := actions.Request{Worktree: &m.worktreeActionTarget.Entry, Parameters: parameters, Confirmed: true}
	m.actionErr = nil
	m.worktreeActionInFlight = true
	return m, m.runWorktreeActionCmd(actions.WorktreeRemove, request)
}

func (m Model) confirmedWorktreeForget() (tea.Model, tea.Cmd) {
	if m.worktreeActionTarget == nil {
		return m, nil
	}
	request := actions.Request{Worktree: &m.worktreeActionTarget.Entry, Confirmed: true}
	m.actionErr = nil
	m.worktreeActionInFlight = true
	return m, m.runWorktreeActionCmd(actions.WorktreeForget, request)
}

func (m Model) handleWorktreeActionDone(msg worktreeActionDoneMsg) (tea.Model, tea.Cmd) {
	m.worktreeActionInFlight = false
	if msg.action == actions.WorktreeReopen {
		return m.handleWorktreeReopenDone(msg)
	}
	if msg.action == actions.WorktreeRemove || msg.action == actions.WorktreeForget {
		return m.handleWorktreeMutationDone(msg)
	}
	if msg.err != nil {
		m.actionErr = msg.err
		return m, nil
	}
	m.actionErr = nil
	text := msg.result.Message
	if text == "" {
		text = "Worktree action completed"
	}
	return m, m.pushToast(Toast{Severity: toastSuccess, Text: text, Expires: time.Now().Add(progressToastTTL)})
}

func (m Model) handleWorktreeReopenDone(msg worktreeActionDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil {
		m.actionErr = nil
		m.worktreeActionTarget = nil
		text := msg.result.Message
		if text == "" {
			text = "Worktree reopened"
		}
		return m, m.pushToast(Toast{Severity: toastSuccess, Text: text})
	}
	var actionErr *actions.Error
	if errors.As(msg.err, &actionErr) {
		switch actionErr.Code {
		case "ambiguous_live_sessions":
			m.openWorktreeSessionPicker(actionErr.Candidates)
			return m, nil
		case "profile_required":
			m.openWorktreeProfilePicker()
			return m, nil
		}
	}
	m.mode = ModeWorktrees
	m.actionErr = msg.err
	m.worktreeActionTarget = nil
	return m, nil
}

func (m Model) handleWorktreeMutationDone(msg worktreeActionDoneMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeWorktrees
	m.worktreeActionTarget = nil
	if msg.err != nil {
		var partial *actions.PartialResultError
		if errors.As(msg.err, &partial) {
			m.actionErr = nil
			text := "Partial cleanup: " + tuiworktrees.MutationSummary(msg.result) + "; recovery required: " + msg.err.Error()
			return m.refreshWorktreesWithToast(Toast{Severity: toastWarning, Text: text})
		}
		m.actionErr = msg.err
		updated, refresh := m.startWorktreeRefresh()
		return updated, refresh
	}
	m.actionErr = nil
	text := msg.result.Message
	if text == "" {
		text = "Worktree lifecycle action completed"
	}
	return m.refreshWorktreesWithToast(Toast{Severity: toastSuccess, Text: text})
}

func (m Model) refreshWorktreesWithToast(toast Toast) (tea.Model, tea.Cmd) {
	updated, refresh := m.startWorktreeRefresh()
	m = updated.(Model)
	toastCmd := m.pushToast(toast)
	return m, tea.Batch(refresh, toastCmd)
}
