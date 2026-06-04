package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
)

func (m Model) handlePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.pickerSearchMode {
		return m.handlePickerSearchKey(msg)
	}
	return m.handlePickerNavKey(msg)
}

// handlePickerSearchKey handles input while the picker is in search mode
// (activated by pressing `/`). Only esc, enter, and backspace have special
// meaning; every other printable character (including j, k, space, p, etc.)
// is appended to the filter buffer so the user can type freely.
func (m Model) handlePickerSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		// ctrl+c quits from every mode. The default branch below would
		// otherwise swallow it (msg.Text is empty for ctrl combos).
		return m, tea.Quit
	case keyEsc, keyEnter:
		m.pickerSearchMode = false
		return m, nil
	case keyBackspace:
		if r := []rune(m.pickerFilter); len(r) > 0 {
			m.pickerFilter = string(r[:len(r)-1])
			m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, m.pickerFilter)
			m.pickerCursor = 0
			scrollPickerToCursor(&m)
			if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
				return m, cmd
			}
		}
		return m, nil
	default:
		if t := msg.Key().Text; t != "" {
			m.pickerFilter += t
			m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, m.pickerFilter)
			m.pickerCursor = 0
			scrollPickerToCursor(&m)
			if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
				return m, cmd
			}
		}
		return m, nil
	}
}

// handlePickerNavKey handles input while the picker is in navigation mode
// (the default). Printable characters other than recognised shortcuts are
// silently ignored; press `/` to enter search mode and type a filter.
func (m Model) handlePickerNavKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keySlash:
		m.pickerSearchMode = true
		return m, nil
	case keyEsc:
		// Two-stage Esc: first clears a non-empty filter; second exits picker.
		if m.pickerFilter != "" {
			m.pickerFilter = ""
			m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, "")
			m.pickerCursor = 0
			scrollPickerToCursor(&m)
			if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
				return m, cmd
			}
			return m, nil
		}
		m.mode = ModeList
		m.pickerProfileOverride = nil
		var cmds []tea.Cmd
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.resumeListPreviewTickCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	case keyCtrlC, keyQ:
		return m, tea.Quit
	case keyJ, keyDown:
		m.pickerCursor = clamp(m.pickerCursor+1, 0, len(m.pickerFilteredIdx)-1)
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyK, keyUp:
		m.pickerCursor = clamp(m.pickerCursor-1, 0, len(m.pickerFilteredIdx)-1)
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyCtrlD:
		step := pickerVisibleHeight(m) / 2
		if step < 1 {
			step = 1
		}
		m.pickerCursor = clamp(m.pickerCursor+step, 0, len(m.pickerFilteredIdx)-1)
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyCtrlU:
		step := pickerVisibleHeight(m) / 2
		if step < 1 {
			step = 1
		}
		m.pickerCursor = clamp(m.pickerCursor-step, 0, len(m.pickerFilteredIdx)-1)
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyPgDown:
		step := pickerVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.pickerCursor = clamp(m.pickerCursor+step, 0, len(m.pickerFilteredIdx)-1)
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyPgUp:
		step := pickerVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.pickerCursor = clamp(m.pickerCursor-step, 0, len(m.pickerFilteredIdx)-1)
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyTogglePreview:
		return m.togglePickerPreview()
	case keySpace:
		if len(m.pickerFilteredIdx) == 0 {
			return m, nil
		}
		if m.pickerSelected == nil {
			m.pickerSelected = make(map[int]bool)
		}
		origIdx := m.pickerFilteredIdx[m.pickerCursor]
		m.pickerSelected[origIdx] = !m.pickerSelected[origIdx]
		return m, nil
	case keyProjectSessionSearch:
		if len(m.pickerFilteredIdx) == 0 || m.pickerCursor < 0 || m.pickerCursor >= len(m.pickerFilteredIdx) {
			return m, nil
		}
		origIdx := m.pickerFilteredIdx[m.pickerCursor]
		if origIdx < 0 || origIdx >= len(m.pickerSessions) {
			return m, nil
		}
		return m.enterSessionSearchForProject(m.pickerSessions[origIdx].Title)
	case keyO:
		if len(m.pickerFilteredIdx) > 0 {
			origIdx := m.pickerFilteredIdx[m.pickerCursor]
			return m, pickerOpenCmd(m, m.pickerSessionWithResolvedProfile(origIdx))
		}
		return m, nil
	case keyOpenEditor:
		if len(m.pickerFilteredIdx) > 0 {
			origIdx := m.pickerFilteredIdx[m.pickerCursor]
			sess := m.pickerSessionWithResolvedProfile(origIdx)
			accounts := m.cfg.Accounts
			client := m.client
			cfg := m.cfg
			return m, func() tea.Msg {
				acc, ok := findAccountByName(accounts, sess.EffectiveProfile())
				if !ok {
					return pickerOpenDoneMsg{err: fmt.Errorf("no matching account for profile %q", sess.EffectiveProfile())}
				}
				return pickerOpenDoneMsg{err: session.OpenInEditor(client, sess, acc, cfg)}
			}
		}
		return m, nil
	case keyDashboardLaunch:
		if len(m.pickerFilteredIdx) > 0 {
			origIdx := m.pickerFilteredIdx[m.pickerCursor]
			sess := m.pickerSessionWithResolvedProfile(origIdx)
			profile := m.pickerEffectiveProfile(origIdx)
			m.targetSession = session.RunningSession{
				Base:    configSessionDisplayTitle(sess),
				Project: m.pickerSessions[origIdx].Title,
				Profile: profile,
				Account: profile,
			}
			m.configPickerAccounts = m.cfg.Accounts
			m.configPickerCurrent = profile
			m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, profile)
			m.configPickerPurpose = ConfigPickerLauncherOverride
			m.configPickerReturnMode = ModePicker
			m.configPickerProjectIdx = origIdx
			m.mode = ModeConfigPicker
		}
		return m, nil
	case keyEnter:
		cmd := m.dispatchPickerLaunch()
		m.mode = ModeList
		m.pickerSelected = nil
		m.pickerProfileOverride = nil
		return m, tea.Batch(cmd, m.resumeListPreviewTickCmd())
	case keyBackspace:
		if r := []rune(m.pickerFilter); len(r) > 0 {
			m.pickerFilter = string(r[:len(r)-1])
			m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, m.pickerFilter)
			m.pickerCursor = 0
			scrollPickerToCursor(&m)
			if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
				return m, cmd
			}
		}
		return m, nil
	default:
		return m, nil
	}
}
