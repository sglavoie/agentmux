package tui

// Worktree lifecycle: opening a checkout, reopening a session against it, and
// the confirmed remove/forget mutations plus their outcomes. Every mutation
// routes through a confirmation and reports what durable steps actually ran,
// because a partial cleanup needs operator recovery rather than a silent retry.
// The list and its filter live in model_worktrees.go.

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
	parameters := map[string]string{}
	if deleteBranch {
		parameters["delete_branch"] = "true"
		if entry.Exists {
			switch {
			case entry.ReachableFromSource == nil:
				return m.worktreeDisabledToast("merge status against the recorded source branch is unavailable")
			case !*entry.ReachableFromSource:
				return m.worktreeDisabledToast("branch is not merged into the recorded source branch")
			}
		}
	}
	request := actions.Request{Worktree: &entry.Entry, Parameters: parameters}
	availability := commands.Registry.Availability(actions.Dependencies{Config: m.cfg}, actions.WorktreeRemove, request)
	if !availability.Available {
		return m.worktreeDisabledToast(availability.Reason)
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

func (m Model) beginWorktreeForget() (tea.Model, tea.Cmd) {
	entry, ok := m.selectedWorktree()
	if !ok || m.worktreeActionInFlight {
		return m, nil
	}
	request := actions.Request{Worktree: &entry.Entry}
	availability := commands.Registry.Availability(actions.Dependencies{Config: m.cfg}, actions.WorktreeForget, request)
	if !availability.Available {
		return m.worktreeDisabledToast(availability.Reason)
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
