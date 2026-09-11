// This file is the root keyboard dispatch: the top-level handler that routes a
// keystroke to the mode that owns it, the dashboard list's own key handling
// with its jump chords, and the two modeless overlays — filter search and the
// confirm prompt — that any mode can raise.

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
	case ModeProjectSwitcher:
		return m.handleProjectSwitcherKey(msg)
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

// clearJumpChord resets any in-flight bracket-key chord. Callers invoke this
// before transitioning away from ModeList so the footer hint disappears and
// the next return to ModeList starts in a clean state.
func (m *Model) clearJumpChord() {
	m.pendingJumpDir = 0
}

// focusSelectedAndPreview focuses the session under the cursor and refreshes
// the preview for it. The two ]f / [f focus jumps land on a matched item and
// then act on it, so the target is resolved through selectedSession rather
// than by indexing: a matcher only ever matches a session leaf today, but a
// selection that turns out not to be one must move the cursor without issuing
// a Kitty focus.
func (m Model) focusSelectedAndPreview() (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if s, ok := m.selectedSession(); ok {
		cmds = append(cmds, commands.Focus(m.client, s.TabID))
	}
	if cmd := m.refreshPreviewForCursor(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.snapshotSeenStates()
	key := msg.String()

	// The inspector claims the navigation keys and nothing else while it holds
	// the focus, so every command key below still reaches its binding. It is
	// asked before the chord intercept because a pending ]/[ chord is a jump
	// through the QUEUE: arming one while the keys are aimed at the output pane
	// is the operator's own doing, and the follow-up letter belongs to the chord
	// either way.
	if m.pendingJumpDir == 0 && m.inspectorFocused() {
		if updated, cmd, claimed := m.handleInspectorKey(key); claimed {
			return updated, cmd
		}
	}

	// Chord intercept. When ]/[ has set pendingJumpDir, the very next key is
	// the chord follow-up — including q, which would otherwise quit, and
	// single letters like p/e/a/c that map to existing list actions. The
	// bracket keys themselves bypass the intercept so their cases can handle
	// the double-tap (]] / [[) attention jump.
	if m.pendingJumpDir != 0 && key != keyJumpAttentionPrev && key != keyJumpAttentionNext {
		dir := m.pendingJumpDir
		m.pendingJumpDir = 0
		if key == keyEsc {
			return m, nil
		}
		if matcher := dashboard.ChordStateMatcher(key); matcher != nil {
			if next := dashboard.FindNextMatching(m.listItems(), m.cursor, dir, matcher); next >= 0 {
				m.cursor = next
				scrollListToCursor(&m)
				if cmd := m.refreshPreviewForCursor(); cmd != nil {
					return m, cmd
				}
			}
			return m, nil
		}
		// Unknown chord key: chord is canceled (pendingJumpDir already
		// reset); fall through so the keypress hits its normal handler.
	}

	// What remains in this switch is everything a dashboard key can do that is
	// not a catalog command: moving the cursor, folding a family, editing the
	// filter chips, and opening the two surfaces (the palette and the actions
	// overlay) that offer commands rather than being one. Every other key falls
	// through to the binding table, which is the same declaration the palette
	// and the footer read.
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

	case keyJumpAttentionNext:
		if m.pendingJumpDir == +1 {
			// Double-tap ]]: perform the attention-set jump.
			m.pendingJumpDir = 0
			attn := dashboard.AttentionMatcher(m.attentionStates)
			if next := dashboard.FindNextMatching(m.listItems(), m.cursor, +1, attn); next >= 0 {
				m.cursor = next
				scrollListToCursor(&m)
				if cmd := m.refreshPreviewForCursor(); cmd != nil {
					return m, cmd
				}
			}
			return m, nil
		}
		// Single-tap ]: arm the chord.
		m.pendingJumpDir = +1
		return m, nil

	case keyJumpAttentionPrev:
		if m.pendingJumpDir == -1 {
			// Double-tap [[: perform the attention-set jump backward.
			m.pendingJumpDir = 0
			attn := dashboard.AttentionMatcher(m.attentionStates)
			if prev := dashboard.FindNextMatching(m.listItems(), m.cursor, -1, attn); prev >= 0 {
				m.cursor = prev
				scrollListToCursor(&m)
				if cmd := m.refreshPreviewForCursor(); cmd != nil {
					return m, cmd
				}
			}
			return m, nil
		}
		// Single-tap [: arm the chord.
		m.pendingJumpDir = -1
		return m, nil

	case keyJumpAttentionNextFocus:
		attn := dashboard.AttentionMatcher(m.attentionStates)
		if next := dashboard.FindNextMatching(m.listItems(), m.cursor, +1, attn); next >= 0 {
			m.cursor = next
			scrollListToCursor(&m)
			return m.focusSelectedAndPreview()
		}
		return m, nil

	case keyJumpAttentionPrevFocus:
		attn := dashboard.AttentionMatcher(m.attentionStates)
		if prev := dashboard.FindNextMatching(m.listItems(), m.cursor, -1, attn); prev >= 0 {
			m.cursor = prev
			scrollListToCursor(&m)
			return m.focusSelectedAndPreview()
		}
		return m, nil

	case keyG:
		m.cursor = 0
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyGG:
		m.cursor = m.listItemCount() - 1
		if m.cursor < 0 {
			m.cursor = 0
		}
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyCtrlD:
		step := listVisibleHeight(m) / 2
		if step < 1 {
			step = 1
		}
		m.cursor = clamp(m.cursor+step, 0, m.listItemCount()-1)
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyCtrlU:
		step := listVisibleHeight(m) / 2
		if step < 1 {
			step = 1
		}
		m.cursor = clamp(m.cursor-step, 0, m.listItemCount()-1)
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

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

	// enter and space reach the binding table, but only after the two things
	// they mean on a row that is not a session. A focused chip and a family
	// summary are list structure rather than targets, and neither is expressible
	// as a command: the summary in particular must never become one, since a
	// synthetic row standing for several sessions has no single session to act
	// on. Both branches answer here and the keypress never reaches FocusSession.
	case keyEnter:
		if m.focusedChipIdx >= 0 {
			return m, m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
		}
		if m.summarySelected() {
			return m, m.toggleSelectedFamily()
		}
		return m.runDashboardBinding(key)

	case keySpace:
		if m.summarySelected() {
			return m, m.toggleSelectedFamily()
		}
		return m.runDashboardBinding(key)

	case keyA:
		// The actions overlay is a surface over the commands, not one of them,
		// so it keeps its own case. It captures its target the same way the
		// bindings do, which is what keeps a family summary out of it.
		if s, ok := m.selectedSession(); ok {
			m.targetSession = s
			m.actionErr = nil
			m.actionsOffset = 0
			m.mode = ModeActions
		}
		return m, nil

	case keyCollapse:
		// ← used to hand the keyboard to the project rail in Urgency view. The
		// rail is gone and the switcher is a modal reached by `p`, so the key has
		// nothing to fold outside the Projects view and does nothing there — the
		// same answer keyExpand already gave.
		if m.listView == dashboard.ViewUrgency {
			return m, nil
		}
		return m, m.collapseSelection()

	case keyExpand:
		if m.listView == dashboard.ViewUrgency {
			return m, nil
		}
		return m, m.expandSelection()

	// Tab means "move to the next thing that takes keys". With an inspector on
	// screen that is the other body pane, and the chip ring gives the key up:
	// chips remain reachable by 1-9 and removable by backspace, while the two
	// panes have no other way to hand the keyboard between them. With no
	// inspector there is nothing to switch to and the chips keep the key.
	case keyTab:
		if m.toggleInspectorFocus() {
			return m, nil
		}
		if n := len(m.activeFilter.Components()); n > 0 {
			if m.focusedChipIdx < 0 {
				m.focusedChipIdx = 0
			} else {
				m.focusedChipIdx = (m.focusedChipIdx + 1) % n
			}
		}

	// Two panes are a cycle of length two, so shift+tab is the same move as tab
	// rather than its inverse. It is bound anyway: an operator who reaches for
	// the reverse of a pane switch should land on the pane they meant, not on
	// nothing.
	case keyShiftTab:
		if m.toggleInspectorFocus() {
			return m, nil
		}
		if n := len(m.activeFilter.Components()); n > 0 {
			if m.focusedChipIdx <= 0 {
				m.focusedChipIdx = n - 1
			} else {
				m.focusedChipIdx--
			}
		}

	case keyInspector:
		return m.toggleInspector()

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

	case key1, key2, key3, key4, key5, key6, key7, key8, key9:
		idx := int(key[0] - '1')
		if idx < len(m.activeFilter.Components()) {
			return m, m.applyFilterChange(m.activeFilter.RemoveComponent(idx))
		}

	case keyBackspace:
		if m.focusedChipIdx >= 0 {
			return m, m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
		}

	default:
		if updated, cmd, claimed := m.dispatchDashboardBinding(key); claimed {
			return updated, cmd
		}
	}

	return m, nil
}
