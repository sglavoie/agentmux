package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/tui/asl"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// handleKey dispatches key events by current mode.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeSearch:
		return m.handleSearchKey(msg)
	case ModeConfirm:
		return m.handleConfirmKey(msg)
	case ModePicker:
		return m.handlePickerKey(msg)
	case ModeConfigPicker:
		return m.handleConfigPickerKey(msg)
	case ModeHelp:
		return m.handleHelpKey(msg)
	case ModeDetail:
		return m.handleDetailKey(msg)
	case ModeActions:
		return m.handleActionsKey(msg)
	case ModeSessionSearch:
		return m.handleSessionSearchKey(msg)
	case ModePlanSearch:
		return m.handlePlanSearchKey(msg)
	case ModePlanView:
		return m.handlePlanViewKey(msg)
	case ModeCommandPalette:
		return m.handleCommandPaletteKey(msg)
	case ModeSessionView:
		return m.handleSessionViewKey(msg)
	case ModeMessageDetail:
		return m.handleMessageDetailKey(msg)
	case ModeWorktrees:
		return m.handleWorktreesKey(msg)
	case ModeASLRuns:
		return m.handleASLRunsKey(msg)
	case ModeASLResponse:
		return m.handleASLResponseKey(msg)
	case ModeLiveSessionPicker:
		return m.handleLiveSessionPickerKey(msg)
	case ModeDeleteChoice:
		return m.handleDeleteChoiceKey(msg)
	case ModeChipPicker:
		return m.handleChipPickerKey(msg)
	default:
		return m.handleListKey(msg)
	}
}

func (m Model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.completion.Visible {
		switch key {
		case keyEsc:
			m.closeCompletion()
			return m, nil
		case keyEnter:
			m.insertCompletion()
			m.reapplyFilterLive()
			m.recomputeCompletion()
			return m, nil
		case keyTab, keyDown:
			m.cycleCompletion(1)
			return m, nil
		case keyUp:
			m.cycleCompletion(-1)
			return m, nil
		}
	}

	switch key {
	case keyCtrlC:
		// ctrl+c must quit from every mode. ModeSearch's default branch
		// would otherwise swallow it (msg.Text is empty for ctrl combos)
		// so the user gets no exit affordance from inside the input.
		return m, tea.Quit

	case keyEsc:
		// Cancel: restore the snapshot taken on entry and recompute.
		m.activeFilter = m.savedFilter
		m.filterText = m.savedFilter.Raw
		m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
		m.sortFiltered()
		m.cursor = 0
		m.focusedChipIdx = -1
		m.closeCompletion()
		m.mode = ModeList

	case keyEnter:
		// Live-applied already — just exit the input.
		m.closeCompletion()
		m.mode = ModeList

	case keyBackspace:
		if r := []rune(m.filterText); len(r) > 0 {
			m.filterText = string(r[:len(r)-1])
		}
		m.reapplyFilterLive()
		m.recomputeCompletion()

	default:
		if t := msg.Key().Text; t != "" {
			m.filterText += t
			m.reapplyFilterLive()
			m.recomputeCompletion()
		}
	}

	return m, nil
}

func (m Model) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyY:
		m.mode = m.confirmReturnMode
		if m.mode == ModeConfirm {
			m.mode = ModeList
		}
		m.confirmReturnMode = ModeList
		switch m.confirmAction {
		case "close":
			return m, commands.Close(m.client, m.targetSession.TabID)
		case "restart":
			return m, commands.Restart(m.client, m.targetSession, m.cfg)
		case "fork worktree":
			if m.isolatedForkConversation == nil {
				return m, nil
			}
			meta := *m.isolatedForkConversation
			m.isolatedForkConversation = nil
			return m, m.conversationWorktreeForkCmd(meta)
		case "worktree remove":
			return m.confirmedWorktreeMutation(false)
		case "worktree delete branch":
			return m.confirmedWorktreeMutation(true)
		case "worktree forget":
			return m.confirmedWorktreeForget()
		case asl.ConfirmStop:
			return m.submitASLControl(aslbridge.MethodRunStopAfterCurrent)
		case asl.ConfirmAbort:
			return m.submitASLControl(aslbridge.MethodRunAbort)
		}
	case keyN, keyEsc:
		m.isolatedForkConversation = nil
		m.worktreeActionTarget = nil
		m.aslActionTarget = nil
		m.mode = m.confirmReturnMode
		if m.mode == ModeConfirm {
			m.mode = ModeList
		}
		m.confirmReturnMode = ModeList
	case keyCtrlC, keyQ:
		return m, tea.Quit
	}
	return m, nil
}
