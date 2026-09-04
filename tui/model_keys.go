// This file is the root keyboard dispatch: the top-level handler that routes a
// keystroke to the mode that owns it, the dashboard list's own key handling
// with its jump chords, and the two modeless overlays — filter search and the
// confirm prompt — that any mode can raise.

package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/tui/asl"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
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

	switch key {
	case keyCtrlC, keyQ:
		return m, tea.Quit

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

	case keySlash:
		return m, m.enterFilter("")

	case keyF:
		return m, m.cycleDashboardProfile()

	case keyColon:
		m.openCommandPalette()

	case keyEnter:
		if m.focusedChipIdx >= 0 {
			return m, m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
		}
		if m.summarySelected() {
			return m, m.toggleSelectedFamily()
		}
		if s, ok := m.selectedSession(); ok {
			return m, commands.Focus(m.client, s.TabID)
		}

	case keySpace:
		if m.summarySelected() {
			return m, m.toggleSelectedFamily()
		}
		if s, ok := m.selectedSession(); ok {
			return m, commands.Focus(m.client, s.TabID)
		}

	case keyCollapse:
		return m, m.collapseSelection()

	case keyExpand:
		return m, m.expandSelection()

	case keyTab:
		if n := len(m.activeFilter.Components()); n > 0 {
			if m.focusedChipIdx < 0 {
				m.focusedChipIdx = 0
			} else {
				m.focusedChipIdx = (m.focusedChipIdx + 1) % n
			}
		}

	case key1, key2, key3, key4, key5, key6, key7, key8, key9:
		idx := int(key[0] - '1')
		if idx < len(m.activeFilter.Components()) {
			return m, m.applyFilterChange(m.activeFilter.RemoveComponent(idx))
		}

	case keyBackspace:
		if m.focusedChipIdx >= 0 {
			return m, m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
		}

	case keyEsc:
		// Clear active filter and any chip focus so a single esc returns the
		// user to a clean list.
		return m, m.applyFilterChange(dashboard.Filter{})

	case keyQuestion:
		m.openHelp(ModeList)

	case keyL:
		m.pickerSessions = launch.SortSessions(m.cfg.Sessions)
		m.pickerRunning = launch.BuildRunning(m.sessions)
		m.pickerSelected = make(map[int]bool)
		m.pickerProfileOverride, m.pickerPinned = launch.LoadState(m.pickerSessions)
		m.pickerCursor = 0
		m.pickerFilter = ""
		m.pickerProfileFilter = ""
		m.pickerSearchMode = false
		m.pickerFilteredIdx = m.pickerFilteredIndices("")
		m.mode = ModePicker
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}

	case keyResort:
		// Resort is a global, top-level action.
		m.actionErr = nil
		cmds := []tea.Cmd{commands.Resort(m.client, m.sortMode)}
		if c := m.pushToast(Toast{
			Severity: toastInfo,
			Text:     "Resorting tabs…",
			Expires:  time.Now().Add(progressToastTTL),
		}); c != nil {
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)

	default:
		switch key {
		case keyTogglePreview:
			return m.togglePreview()
		case keyToggleView:
			return m, m.toggleListView()
		case keyA:
			if s, ok := m.selectedSession(); ok {
				m.targetSession = s
				m.actionErr = nil
				m.actionsOffset = 0
				m.mode = ModeActions
			}
			return m, nil
		case keyE:
			if s, ok := m.selectedSession(); ok {
				return m, commands.Peek(m.client, s.WindowID)
			}
			return m, nil
		case keyR:
			if s, ok := m.selectedSession(); ok {
				if s.Locked {
					return m, m.lockedDenyToast()
				}
				m.targetSession = s
				m.actionErr = nil
				m.confirmAction = "restart"
				m.confirmConsequence = restartConsequence
				m.mode = ModeConfirm
				return m, nil
			}
			return m, nil
		case keyClose:
			if s, ok := m.selectedSession(); ok {
				if s.Locked {
					return m, m.lockedDenyToast()
				}
				m.targetSession = s
				m.actionErr = nil
				if m.cfg.Defaults.ConfirmDestructive {
					m.confirmAction = "close"
					m.confirmConsequence = closeConsequence
					m.mode = ModeConfirm
					return m, nil
				}
				return m, m.closeWithToast(m.targetSession)
			}
			return m, nil
		case keyU:
			return m, m.undoLastCmd()
		case keyC:
			if s, ok := m.selectedSession(); ok {
				if s.Locked {
					return m, m.lockedDenyToast()
				}
				m.targetSession = s
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = m.targetSession.EffectiveProfile()
				m.configPickerCursor = launch.AccountIndex(m.cfg.Accounts, m.targetSession.EffectiveProfile())
				m.configPickerPurpose = launch.ConfigPickerRestartProfile
				m.configPickerReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
			}
			return m, nil
		case keyDashboardLaunch:
			if s, ok := m.selectedSession(); ok {
				m.targetSession = s
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = m.targetSession.EffectiveProfile()
				m.configPickerCursor = launch.AccountIndex(m.cfg.Accounts, m.targetSession.EffectiveProfile())
				m.configPickerPurpose = launch.ConfigPickerDashboardLaunch
				m.configPickerReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
			}
			return m, nil
		case keyClearStatus:
			if s, ok := m.selectedSession(); ok {
				m.targetSession = s
				if dashboard.StateGroup(m.targetSession, m.lastActivity, m.idleThreshold) != 0 {
					return m, nil
				}
				m.actionErr = nil
				return m, m.clearStatusCmd(m.targetSession)
			}
			return m, nil
		case keyLock:
			if s, ok := m.selectedSession(); ok {
				m.actionErr = nil
				return m, commands.ToggleLock(m.client, s)
			}
			return m, nil
		case keyM:
			if s, ok := m.selectedSession(); ok {
				m.actionErr = nil
				return m, commands.ToggleMute(m.client, s)
			}
			return m, nil
		case keyO:
			if s, ok := m.selectedSession(); ok {
				if cs, found := commands.FindConfigSession(m.cfg, s.EffectiveProfile(), s.EffectiveProject()); found {
					m.targetSession = s
					m.actionErr = nil
					return m, commands.OpenDirectory(m.client, m.cfg, cs)
				}
			}
			return m, nil
		case keyOpenEditor:
			if s, ok := m.selectedSession(); ok {
				if cs, found := commands.FindConfigSession(m.cfg, s.EffectiveProfile(), s.EffectiveProject()); found {
					m.targetSession = s
					m.actionErr = nil
					return m, commands.OpenInEditor(m.client, m.cfg, cs)
				}
			}
			return m, nil
		case keyY:
			if s, ok := m.selectedSession(); ok {
				title := dashboard.SessionDisplayTitle(s)
				m.actionErr = nil
				return m, commands.CopyTitle(title)
			}
			return m, nil
		case keySessionSearch:
			return m.enterSessionSearch()
		case keyProjectSessionSearch:
			s, ok := m.selectedSession()
			if !ok {
				return m, nil
			}
			return m.enterSessionSearchForProject(s.EffectiveProject())
		case keyPlanSearch:
			return m.enterPlanSearch()
		case keyWorktrees:
			return m.enterWorktrees()
		case keyASLRuns:
			return m.enterASLRuns()
		case keyD:
			if !m.deprecationDismissed && len(m.deprecations) > 0 {
				// Banner dismissal first when one is visible; the detail
				// pane is opened on a subsequent press once the banner
				// is gone.
				m.deprecationDismissed = true
				return m, nil
			}
			s, ok := m.selectedSession()
			if !ok {
				return m, nil
			}
			return m.enterDetailMode(s.WindowID, false)
		case keyOpenTimeline:
			if s, ok := m.selectedSession(); ok {
				return m.enterDetailMode(s.WindowID, true)
			}
		}
	}

	return m, nil
}
