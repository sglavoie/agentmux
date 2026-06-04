package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

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
		if matcher := chordStateMatcher(key); matcher != nil {
			if next := findNextMatching(m.filtered, m.cursor, dir, matcher); next >= 0 {
				m.cursor = next
				scrollListToCursor(&m)
				if cmd := m.refreshPreviewForCursor(); cmd != nil {
					return m, cmd
				}
			}
			return m, nil
		}
		if key == keyLoopTail {
			// `t` is a list action, not a bracket-jump follow-up. With a
			// pending chord it only cancels the chord so ]t/[t cannot launch.
			return m, nil
		}
		// Unknown chord key: chord is canceled (pendingJumpDir already
		// reset); fall through so the keypress hits its normal handler.
	}

	switch key {
	case keyCtrlC, keyQ:
		return m, tea.Quit

	case keyJ, keyDown:
		m.cursor = clamp(m.cursor+1, 0, len(m.filtered)-1)
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyK, keyUp:
		m.cursor = clamp(m.cursor-1, 0, len(m.filtered)-1)
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyJumpAttentionNext:
		if m.pendingJumpDir == +1 {
			// Double-tap ]]: perform the attention-set jump.
			m.pendingJumpDir = 0
			attn := isAttentionStateFor(m.attentionStates)
			if next := findNextMatching(m.filtered, m.cursor, +1, attn); next >= 0 {
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
			attn := isAttentionStateFor(m.attentionStates)
			if prev := findNextMatching(m.filtered, m.cursor, -1, attn); prev >= 0 {
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
		attn := isAttentionStateFor(m.attentionStates)
		if next := findNextMatching(m.filtered, m.cursor, +1, attn); next >= 0 {
			m.cursor = next
			scrollListToCursor(&m)
			s := m.filtered[m.cursor]
			cmds := []tea.Cmd{focusCmd(m.client, s.TabID)}
			if cmd := m.refreshPreviewForCursor(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case keyJumpAttentionPrevFocus:
		attn := isAttentionStateFor(m.attentionStates)
		if prev := findNextMatching(m.filtered, m.cursor, -1, attn); prev >= 0 {
			m.cursor = prev
			scrollListToCursor(&m)
			s := m.filtered[m.cursor]
			cmds := []tea.Cmd{focusCmd(m.client, s.TabID)}
			if cmd := m.refreshPreviewForCursor(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case keyG:
		m.cursor = 0
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyGG:
		m.cursor = len(m.filtered) - 1
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
		m.cursor = clamp(m.cursor+step, 0, len(m.filtered)-1)
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyCtrlU:
		step := listVisibleHeight(m) / 2
		if step < 1 {
			step = 1
		}
		m.cursor = clamp(m.cursor-step, 0, len(m.filtered)-1)
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyPgDown:
		step := listVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.cursor = clamp(m.cursor+step, 0, len(m.filtered)-1)
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyPgUp:
		step := listVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.cursor = clamp(m.cursor-step, 0, len(m.filtered)-1)
		scrollListToCursor(&m)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keySlash:
		m.enterFilter("")

	case keyEnter:
		if m.focusedChipIdx >= 0 {
			m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
			return m, nil
		}
		if len(m.filtered) > 0 {
			return m, focusCmd(m.client, m.filtered[m.cursor].TabID)
		}

	case keySpace:
		if len(m.filtered) > 0 {
			return m, focusCmd(m.client, m.filtered[m.cursor].TabID)
		}

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
			m.applyFilterChange(m.activeFilter.RemoveComponent(idx))
		}

	case keyBackspace:
		if m.focusedChipIdx >= 0 {
			m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
		}

	case keyEsc:
		// Clear active filter and any chip focus so a single esc returns the
		// user to a clean list.
		m.applyFilterChange(Filter{})

	case keyQuestion:
		m.helpOffset = 0
		m.mode = ModeHelp

	case keyL:
		m.pickerSessions = sortPickerSessions(m.cfg.Sessions)
		m.pickerRunning = buildPickerRunning(m.sessions)
		m.pickerSelected = make(map[int]bool)
		m.pickerProfileOverride = loadPickerOverrides(m.pickerSessions)
		m.pickerCursor = 0
		m.pickerFilter = ""
		m.pickerSearchMode = false
		m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, "")
		m.mode = ModePicker
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}

	case keyResort:
		// Resort is a global, top-level action.
		m.actionErr = nil
		cmds := []tea.Cmd{resortCmd(m.client, m.sortMode)}
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
		case keyLoopTail:
			m.actionErr = nil
			return m, m.loopTailSelectionCmd()
		case keyA:
			if len(m.filtered) > 0 {
				m.targetSession = m.filtered[m.cursor]
				m.actionErr = nil
				m.mode = ModeActions
			}
			return m, nil
		case keyE:
			if len(m.filtered) > 0 {
				s := m.filtered[m.cursor]
				return m, peekCmd(m.client, s.WindowID)
			}
			return m, nil
		case keyR:
			if len(m.filtered) > 0 {
				s := m.filtered[m.cursor]
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
			if len(m.filtered) > 0 {
				s := m.filtered[m.cursor]
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
			if len(m.filtered) > 0 {
				s := m.filtered[m.cursor]
				if s.Locked {
					return m, m.lockedDenyToast()
				}
				m.targetSession = s
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = m.targetSession.EffectiveProfile()
				m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, m.targetSession.EffectiveProfile())
				m.configPickerPurpose = ConfigPickerRestartProfile
				m.configPickerReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
			}
			return m, nil
		case keyDashboardLaunch:
			if len(m.filtered) > 0 {
				m.targetSession = m.filtered[m.cursor]
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = m.targetSession.EffectiveProfile()
				m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, m.targetSession.EffectiveProfile())
				m.configPickerPurpose = ConfigPickerDashboardLaunch
				m.configPickerReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
			}
			return m, nil
		case keyClearStatus:
			if len(m.filtered) > 0 {
				m.targetSession = m.filtered[m.cursor]
				if stateGroup(m.targetSession, m.lastActivity, m.idleThreshold) != 0 {
					return m, nil
				}
				m.actionErr = nil
				return m, clearStatusCmd(m.client, m.targetSession)
			}
			return m, nil
		case keyLock:
			if len(m.filtered) > 0 {
				s := m.filtered[m.cursor]
				m.actionErr = nil
				return m, toggleLockCmd(m.client, s)
			}
			return m, nil
		case keyM:
			if len(m.filtered) > 0 {
				s := m.filtered[m.cursor]
				m.actionErr = nil
				return m, toggleMuteCmd(m.client, s)
			}
			return m, nil
		case keyO:
			if len(m.filtered) > 0 {
				s := m.filtered[m.cursor]
				if cs, ok := findConfigSession(m.cfg, s.EffectiveProfile(), s.EffectiveProject()); ok {
					m.targetSession = s
					m.actionErr = nil
					return m, openDirActionCmd(m, cs)
				}
			}
			return m, nil
		case keyOpenEditor:
			if len(m.filtered) > 0 {
				s := m.filtered[m.cursor]
				if cs, ok := findConfigSession(m.cfg, s.EffectiveProfile(), s.EffectiveProject()); ok {
					m.targetSession = s
					m.actionErr = nil
					return m, openInEditorActionCmd(m, cs)
				}
			}
			return m, nil
		case keyY:
			if len(m.filtered) > 0 {
				title := runningSessionDisplayTitle(m.filtered[m.cursor])
				m.actionErr = nil
				return m, copyTitleCmd(title)
			}
			return m, nil
		case keySessionSearch:
			return m.enterSessionSearch()
		case keyProjectSessionSearch:
			if len(m.filtered) == 0 || m.cursor < 0 || m.cursor >= len(m.filtered) {
				return m, nil
			}
			return m.enterSessionSearchForProject(m.filtered[m.cursor].EffectiveProject())
		case keyPlanSearch:
			return m.enterPlanSearch()
		case keyD:
			if !m.deprecationDismissed && len(m.deprecations) > 0 {
				// Banner dismissal first when one is visible; the detail
				// pane is opened on a subsequent press once the banner
				// is gone.
				m.deprecationDismissed = true
				return m, nil
			}
			if len(m.filtered) == 0 {
				return m, nil
			}
			return m.enterDetailMode(m.filtered[m.cursor].WindowID, false)
		case keyOpenTimeline:
			if m.cursor < len(m.filtered) {
				return m.enterDetailMode(m.filtered[m.cursor].WindowID, true)
			}
		}
	}

	return m, nil
}
