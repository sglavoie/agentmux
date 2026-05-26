package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/daemon"
)

func (m Model) handleConfigPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc, keyQ:
		m.mode = m.configPickerReturnMode
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		m.configPickerCursor = clamp(m.configPickerCursor+1, 0, len(m.configPickerAccounts)-1)
	case keyK, keyUp:
		m.configPickerCursor = clamp(m.configPickerCursor-1, 0, len(m.configPickerAccounts)-1)
	case keyEnter:
		if m.configPickerCursor < len(m.configPickerAccounts) {
			acc := m.configPickerAccounts[m.configPickerCursor]
			m.mode = m.configPickerReturnMode
			if m.configPickerReturnMode == ModePicker && m.configPickerProjectIdx >= 0 && m.configPickerProjectIdx < len(m.pickerSessions) {
				defaultProfile := m.pickerSessions[m.configPickerProjectIdx].EffectiveProfile()
				if acc.Name == defaultProfile {
					delete(m.pickerProfileOverride, m.configPickerProjectIdx)
				} else {
					if m.pickerProfileOverride == nil {
						m.pickerProfileOverride = make(map[int]string)
					}
					m.pickerProfileOverride[m.configPickerProjectIdx] = acc.Name
				}
				m.savePickerOverrides()
				if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
					return m, cmd
				}
				return m, nil
			}
			if acc.Name == m.configPickerCurrent {
				return m, nil
			}
			return m, m.restartWithAccountCmd(acc)
		}
	}
	return m, nil
}

// handleActionsKey dispatches the per-row actions overlay. Esc/q close the
// overlay without side effects; any other key that matches an available
// action's keyMatch runs it. Unavailable or unknown keys are ignored.
func (m Model) handleActionsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	switch s {
	case keyEsc, keyQ:
		m.mode = ModeList
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	}
	for _, a := range actionsFor(m) {
		if s != a.keyMatch {
			continue
		}
		if !a.available(m) {
			return m, nil
		}
		return a.run(m)
	}
	return m, nil
}

// enterDetailMode switches to ModeDetail for the given window, marks the
// pane as loading, and returns a command that fetches the event ring from
// the daemon. Callers must guard against an empty session list.
func (m Model) enterDetailMode(windowID int) (tea.Model, tea.Cmd) {
	m.mode = ModeDetail
	m.detailWindowID = windowID
	m.detailEvents = nil
	m.detailScroll = 0
	m.detailLoading = true
	m.detailErr = nil
	return m, sessionDetailCmd(windowID)
}

// exitDetailMode returns to ModeList and clears every detail-pane field so
// the next entry starts from a clean state. No-op when the model is not in
// ModeDetail.
func (m *Model) exitDetailMode() {
	m.mode = ModeList
	m.detailWindowID = 0
	m.detailEvents = nil
	m.detailScroll = 0
	m.detailLoading = false
	m.detailErr = nil
}

func (m Model) handleDetailKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	total, visible := detailScrollBounds(m, detailVisibleHeight(m))
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	switch msg.String() {
	case keyEsc, keyQ, keyD:
		m.exitDetailMode()
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		if m.detailScroll < maxOffset {
			m.detailScroll++
		}
	case keyK, keyUp:
		if m.detailScroll > 0 {
			m.detailScroll--
		}
	case keyPgDown:
		m.detailScroll += visible
		if m.detailScroll > maxOffset {
			m.detailScroll = maxOffset
		}
	case keyPgUp:
		m.detailScroll -= visible
		if m.detailScroll < 0 {
			m.detailScroll = 0
		}
	case keyG:
		m.detailScroll = 0
	case keyGG:
		m.detailScroll = maxOffset
	}
	return m, nil
}

// sessionDetailCmd queries the daemon for a window's hook event ring
// buffer. Errors from a missing or unreachable daemon are captured on the
// returned msg so the detail pane can surface them rather than blocking
// indefinitely.
func sessionDetailCmd(windowID int) tea.Cmd {
	return func() tea.Msg {
		resp, err := daemon.NewClient().Send(daemon.Request{
			Type:     daemon.TypeSessionDetail,
			WindowID: windowID,
		})
		if err != nil {
			return detailEventsMsg{windowID: windowID, err: err}
		}
		return detailEventsMsg{windowID: windowID, events: resp.Events}
	}
}

func (m Model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	total, visible := helpScrollBounds(m, helpVisibleHeight(m))
	switch msg.String() {
	case keyEsc, keyQuestion, keyQ:
		m.mode = ModeList
		m.helpOffset = 0
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		if m.helpOffset+visible < total {
			m.helpOffset++
		}
	case keyK, keyUp:
		if m.helpOffset > 0 {
			m.helpOffset--
		}
	case keyG:
		m.helpOffset = 0
	case keyGG:
		m.helpOffset = total - visible
		if m.helpOffset < 0 {
			m.helpOffset = 0
		}
	}
	return m, nil
}
