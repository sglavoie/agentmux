// This file is the root keyboard dispatch: the top-level handler that routes a
// keystroke to the mode that owns it, the dashboard list's own key handling,
// and the two modeless overlays — filter search and the confirm prompt — that
// any mode can raise.

package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/tui/asl"
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
	case ModeSend:
		return m.handleSendKey(msg)
	case ModePlanRun:
		return m.handlePlanRunKey(msg)
	case ModeLiveSessionPicker:
		return m.handleLiveSessionPickerKey(msg)
	case ModeDeleteChoice:
		return m.handleDeleteChoiceKey(msg)
	case ModeChipPicker:
		return m.handleChipPickerKey(msg)
	case ModeProjectSwitcher:
		return m.handleProjectSwitcherKey(msg)
	case ModeFeedback:
		return m.handleFeedbackKey(msg)
	case ModeHealth:
		return m.handleHealthKey(msg)
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
			cmd := m.reapplyFilterLive()
			m.recomputeCompletion()
			return m, cmd
		case keyTab, keyDown:
			m.cycleCompletion(1)
			return m, nil
		case keyUp:
			m.cycleCompletion(-1)
			return m, nil
		}
	}

	// Every branch below that rebuilds the list hands back the command that
	// retargets the preview, which is why the handler carries one out rather
	// than returning nil: the pane's target is only knowable once the branch has
	// settled the cursor, and a dropped command leaves it reading the window the
	// operator has just filtered away.
	var cmd tea.Cmd
	switch key {
	case keyCtrlC:
		// ctrl+c must quit from every mode. ModeSearch's default branch
		// would otherwise swallow it (msg.Text is empty for ctrl combos)
		// so the user gets no exit affordance from inside the input.
		return m, tea.Quit

	case keyEsc:
		// Cancel: restore the snapshot taken on entry — the filter and the
		// selection it was taken beside — and recompute. The selection comes
		// back whenever it is still visible under the restored filter; when it
		// is not, applyActiveFilter's identity fallback picks the landing.
		m.activeFilter = m.savedFilter
		m.filterText = m.savedFilter.Raw
		m.closeCompletion()
		m.mode = ModeList
		cmd = m.applyActiveFilter(m.savedSelection)

	case keyEnter:
		// The rows were live-applied by the keystrokes that produced them, so
		// committing only leaves the input — but it leaves it for ModeList, which
		// draws more chrome above the list and so gives it a shorter window. The
		// offset is resized against that window before the pane is asked what it
		// should be showing.
		//
		// That last call cannot currently return a command: every keystroke in
		// ModeSearch already retargeted, so the commit finds the pane pointed at
		// the cursor it inherits. It is kept because the alternative is a mode
		// exit that trusts a claim about a different function — the day one
		// ModeSearch path stops retargeting, this is what keeps the pane honest.
		m.closeCompletion()
		m.mode = ModeList
		clampListOffset(&m)
		scrollListToCursor(&m)
		cmd = m.refreshPreviewForCursor()

	case keyBackspace:
		if r := []rune(m.filterText); len(r) > 0 {
			m.filterText = string(r[:len(r)-1])
		}
		cmd = m.reapplyFilterLive()
		m.recomputeCompletion()

	default:
		if t := msg.Key().Text; t != "" {
			m.filterText += t
			cmd = m.reapplyFilterLive()
			m.recomputeCompletion()
		}
	}

	return m, cmd
}

func (m Model) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyY:
		// A session action waiting here answers for itself. It revalidates the
		// captured row before it dispatches, correlates the completion with the
		// request, and runs the same effect an unconfirmed action runs — which is
		// what this branch used not to do: close and restart were dispatched raw
		// here, so a confirmed close recorded no undo entry and raised no toast
		// while an unconfirmed one did both.
		if m.sessionActionAwaitingConfirm() {
			return m.confirmSessionAction()
		}
		m.mode = m.confirmReturnMode
		if m.mode == ModeConfirm {
			m.mode = ModeList
		}
		m.confirmReturnMode = ModeList
		switch m.confirmAction {
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
		case "worktree land":
			return m.confirmedWorktreeLand()
		case asl.ConfirmStop:
			return m.submitASLControl(aslbridge.MethodRunStopAfterCurrent)
		case asl.ConfirmAbort:
			return m.submitASLControl(aslbridge.MethodRunAbort)
		}
	case keyN, keyEsc, keyQ:
		if m.sessionActionAwaitingConfirm() {
			next, cmd, cancelled := m.cancelSessionAction()
			if cancelled {
				return next, cmd
			}
		}
		m.isolatedForkConversation = nil
		m.worktreeActionTarget = nil
		m.aslActionTarget = nil
		m.mode = m.confirmReturnMode
		if m.mode == ModeConfirm {
			m.mode = ModeList
		}
		m.confirmReturnMode = ModeList
	case keyCtrlC:
		return m, tea.Quit
	}
	return m, nil
}

// moveToNeedsYou is `n` and `N`: walk the filtered sessions to the next one that
// needs the operator and put the cursor on it.
//
// It moves and nothing more. No Kitty tab is focused and no status icon is
// acknowledged — the `}`/`{` jump this replaced did focus a tab, and the
// keystroke that finds a waiting session is not the keystroke that decides to
// answer it. Enter is still how a terminal is opened.
//
// Pane focus is left exactly as it was found, which is why this is a plain
// cursor move rather than anything routed through the inspector: an operator
// reading output who presses `n` wants the next session's output in the pane
// they are already reading, not the keyboard handed back to the queue.
//
// Revealing is part of moving, not an exception to it. A row folded inside a
// family has no line to select, so the family is unfolded first and the row is
// then re-resolved against the projection that came out — the navigator answers
// against the OLD list, and an item index from it names a different line once a
// fold has changed how many lines there are.
func (m *Model) moveToNeedsYou(dir int) tea.Cmd {
	target, ok := dashboard.FindNextNeedsYou(m.items, m.cursor, dir, m.lastActivity, m.idleThreshold)
	if !ok {
		return nil
	}
	if target.Reveal {
		// The command setFamilyCollapsed hands back retargets the preview at the
		// cursor the reprojection restored, which is the row the operator was
		// leaving. It is dropped rather than batched: the cursor moves two lines
		// below and the preview is asked once, after it has settled.
		_ = m.setFamilyCollapsed(target.FamilyKey, false)
		resolved, found := m.items.ResolveRowTarget(target.Row)
		if !found || resolved.Reveal {
			// The unfold did not produce a line for the row — a refresh landing
			// between the walk and the reprojection can take the row away
			// entirely. Leave the cursor where it is rather than moving it
			// somewhere that was not asked for.
			return m.refreshPreviewForCursor()
		}
		target = resolved
	}
	m.cursor = clamp(target.Item, 0, m.listItemCount()-1)
	scrollListToCursor(m)
	return m.refreshPreviewForCursor()
}

func (m Model) handleListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.snapshotSeenStates()
	key := msg.String()

	// The inspector claims the movement keys and nothing else while it holds the
	// focus, so every command key below still reaches its binding — and so do
	// `n`/`N`, which move the queue cursor from either pane.
	if m.inspectorFocused() {
		if updated, cmd, claimed := m.handleInspectorKey(key); claimed {
			return updated, cmd
		}
	}

	// What remains in this switch is everything a dashboard key can do that is
	// not a catalog command: moving the cursor, folding a family, and opening the
	// two surfaces (the palette and the actions overlay) that offer commands
	// rather than being one. Every other key falls through to the binding table,
	// which is the same declaration the palette and the footer read.
	switch key {
	case keyJ, keyDown:
		m.cursor = clamp(m.cursor+1, 0, m.listItemCount()-1)
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyK, keyUp:
		m.cursor = clamp(m.cursor-1, 0, m.listItemCount()-1)
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyNeedsYouNext:
		return m, m.moveToNeedsYou(+1)

	case keyNeedsYouPrev:
		return m, m.moveToNeedsYou(-1)

	case keyHome, keyG:
		m.cursor = 0
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyEnd, keyGG:
		m.cursor = m.listItemCount() - 1
		if m.cursor < 0 {
			m.cursor = 0
		}
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyCtrlD, keyCtrlU:
		step := max(1, listVisibleHeight(m)/2)
		if key == keyCtrlU {
			step = -step
		}
		m.cursor = clamp(m.cursor+step, 0, m.listItemCount()-1)
		scrollListToCursor(&m)
		return m, m.refreshPreviewForCursor()

	case keyPgDown:
		step := listVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.cursor = clamp(m.cursor+step, 0, m.listItemCount()-1)
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyPgUp:
		step := listVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.cursor = clamp(m.cursor-step, 0, m.listItemCount()-1)
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyColon:
		m.openCommandPalette()

	// enter reaches the binding table, but only after the one thing it means on
	// a row that is not a session. A family summary is list structure rather than
	// a target, and it is not expressible as a command: a synthetic row standing
	// for several sessions has no single session to act on. That branch answers
	// here and the keypress never reaches FocusSession.
	//
	// Folding is all Enter does to a summary now. The `←`/`→` pair that used to
	// fold and unfold separately is retired, so this is the only key that changes
	// a fold and it toggles.
	case keyEnter:
		if m.summarySelected() {
			return m, m.toggleSelectedFamily()
		}
		return m.runDashboardBinding(key)

	case keyA:
		// The actions overlay is a surface over the commands, not one of them,
		// so it keeps its own case. It captures its target the same way the
		// bindings do, which is what keeps a family summary out of it.
		if s, ok := m.selectedSession(); ok {
			m.openActionsMenu(s)
		}
		return m, nil

	// Tab means "move to the next thing that takes keys", and on the dashboard
	// that is the other body pane. It used to fall through to a filter-chip ring
	// when there was no inspector; the ring is retired — a chip is removed by
	// clicking it or by editing the filter inside `/` — so with one pane on
	// screen there is nothing to switch to and the key answers nothing.
	//
	// Two panes are a cycle of length two, so shift+tab is the same move as tab
	// rather than its inverse. It is bound anyway: an operator who reaches for
	// the reverse of a pane switch should land on the pane they meant, not on
	// nothing.
	case keyTab, keyShiftTab:
		m.toggleInspectorFocus()
		return m, nil

	// The escape ladder, in the order the layers were raised. Overlays have
	// already had their turn — every one of them owns its own mode and handles
	// esc there — so what is left here is the inspector's two layers and then
	// the filter. Clearing filters is reached through the binding table like any
	// other command, which is what keeps the palette entry and the key on one
	// implementation.
	case keyEsc:
		if m.escapeInspectorLayer() {
			return m, nil
		}
		return m.runDashboardBinding(key)

	default:
		if updated, cmd, claimed := m.dispatchDashboardBinding(key); claimed {
			return updated, cmd
		}
	}

	return m, nil
}
