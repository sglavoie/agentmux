package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
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
	default:
		return m.handleListKey(msg)
	}
}

func (m Model) handleListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.snapshotSeenStates()
	key := msg.String()

	// Completion popup (active over the type-to-filter buffer) intercepts a
	// fixed key set before the normal dispatcher runs. Mirrors handleSearchKey.
	if m.completion.Visible {
		switch key {
		case keyEsc:
			m.closeCompletion()
			return m, nil
		case keyEnter:
			m.insertCompletionInto(&m.typeBuffer)
			m.applyTypeBuffer()
			m.recomputeCompletionFor(m.typeBuffer)
			return m, nil
		case keyTab, keyDown:
			m.cycleCompletion(1)
			return m, nil
		case keyUp:
			m.cycleCompletion(-1)
			return m, nil
		}
	}

	bufferActive := m.typeBuffer != ""

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
		// Carry an in-progress type-to-filter buffer over to ModeSearch so
		// users escalating to the full DSL don't lose what they've typed.
		prefill := ""
		if m.typeBuffer != "" {
			prefill = m.typeBuffer
			m.typeBuffer = ""
		}
		m.enterFilter(prefill)

	case keyF:
		m.typeBuffer = ""
		m.enterFilter("@")

	case keyEnter:
		if m.typeBuffer != "" {
			// Commit the type-to-filter buffer: the filter is already
			// live-applied; clearing the buffer returns row-action keys to
			// service while the chips above the list reflect the commit.
			m.typeBuffer = ""
			m.closeCompletion()
			return m, nil
		}
		if m.focusedChipIdx >= 0 {
			m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
			return m, nil
		}
		if len(m.filtered) > 0 {
			return m, focusCmd(m.client, m.filtered[m.cursor].TabID)
		}

	case keySpace:
		// Space is a row-action shortcut (focus tab) only while the
		// type-to-filter buffer is empty. Once the user is typing a filter,
		// pressing space must advance to the next DSL token — otherwise
		// multi-word filters like "foo bar" silently lose the separator.
		if bufferActive {
			m.typeBuffer += " "
			m.applyTypeBuffer()
			m.recomputeCompletionFor(m.typeBuffer)
			return m, nil
		}
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
		// Digits are chip-removal shortcuts only when no type-to-filter
		// buffer is open. Once the user is actively typing a filter,
		// the digit appends to the buffer so titles like "v2" / "h2"
		// remain matchable mid-typing.
		if bufferActive {
			if t := msg.Key().Text; t != "" {
				m.typeBuffer += t
				m.applyTypeBuffer()
				m.recomputeCompletionFor(m.typeBuffer)
			}
			return m, nil
		}
		idx := int(key[0] - '1')
		if idx < len(m.activeFilter.Components()) {
			m.applyFilterChange(m.activeFilter.RemoveComponent(idx))
		}

	case keyBackspace:
		if m.typeBuffer != "" {
			if r := []rune(m.typeBuffer); len(r) > 0 {
				m.typeBuffer = string(r[:len(r)-1])
			}
			m.applyTypeBuffer()
			m.recomputeCompletionFor(m.typeBuffer)
			return m, nil
		}
		if m.focusedChipIdx >= 0 {
			m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
		}

	case keyEsc:
		if m.typeBuffer != "" {
			m.typeBuffer = ""
			m.closeCompletion()
		}
		// Clear active filter (and any chip focus) regardless. After a
		// type-buffer was cleared above, this also clears the now-committed
		// filter chips so a single esc returns the user to a clean list.
		m.applyFilterChange(Filter{})

	case keyQuestion:
		// typeBuffer policy on mode transitions: `?` (help) and `l` (picker)
		// both *preserve* the buffer and any visible completion popup so
		// returning to ModeList resumes exactly where the user left off.
		// `/` carries the buffer forward as prefill (above); `f`/`esc`/`enter`
		// each clear it explicitly. ModeHelp and ModePicker have their own
		// render paths, so the popup state lives dormant until ModeList
		// becomes active again.
		m.helpOffset = 0
		m.mode = ModeHelp

	case keyL:
		// See typeBuffer policy comment on the `?` case above.
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
		// Resort is a global, top-level action — kept above `default:` so the
		// keystroke can't be diverted into the type-to-filter buffer.
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
		// Row-action keys: handled normally when the type buffer is empty;
		// diverted into the buffer once it has at least one character.
		if !bufferActive {
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
				return m.enterDetailMode(m.filtered[m.cursor].WindowID)
			}
		}
		// Fall through: anything else printable goes into the type buffer.
		if t := msg.Key().Text; t != "" {
			m.typeBuffer += t
			m.applyTypeBuffer()
			m.recomputeCompletionFor(m.typeBuffer)
		}
	}

	return m, nil
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
		m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
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
		m.mode = ModeList
		switch m.confirmAction {
		case "close":
			return m, closeCmd(m.client, m.targetSession.TabID)
		case "restart":
			return m, restartCmd(m.client, m.targetSession, m.cfg)
		}
	case keyN, keyEsc:
		m.mode = ModeList
	case keyCtrlC, keyQ:
		return m, tea.Quit
	}
	return m, nil
}
