package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

func (m Model) handleListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.snapshotSeenStates()
	key := msg.String()

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
				if m.cfg.Defaults.ConfirmDestructive {
					m.confirmAction = "restart"
					m.confirmConsequence = restartConsequence
					m.mode = ModeConfirm
					return m, nil
				}
				return m, m.restartWithToast(m.targetSession)
			}
			return m, nil
		case keyX:
			if m.focusedChipIdx >= 0 {
				m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
				return m, nil
			}
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
				m.configPickerReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
			}
			return m, nil
		case keyClearStatus:
			if len(m.filtered) > 0 {
				s := m.filtered[m.cursor]
				m.actionErr = nil
				return m, clearStatusCmd(m.client, s)
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
