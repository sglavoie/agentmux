package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/daemon"
)

// activeRootMode resolves the root view that currently owns the model's async
// state. Help, confirmations, and the profile/handoff picker replace m.mode for
// the duration of the detour but return to the view they were raised from, so a
// load completing behind one of them still belongs to that view.
func (m Model) activeRootMode() Mode {
	switch m.mode {
	case ModeHelp:
		return m.helpReturnMode
	case ModeConfirm:
		return m.confirmReturnMode
	case ModeConfigPicker:
		return m.configPickerReturnMode
	case ModeLiveSessionPicker:
		return m.livePickerReturnMode
	case ModeASLResponse:
		return ModeASLRuns
	default:
		return m.mode
	}
}

func (m Model) handleConfigPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc, keyQ:
		if m.activeConfigPickerPurpose() == ConfigPickerHandoff {
			m.handoffConversation = nil
			m.handoffEdit = false
		}
		m.mode = m.configPickerReturnMode
		if m.activeConfigPickerPurpose() == ConfigPickerWorktreeReopen {
			m.worktreeActionTarget = nil
		}
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		m.configPickerCursor = clamp(m.configPickerCursor+1, 0, len(m.configPickerAccounts)-1)
	case keyK, keyUp:
		m.configPickerCursor = clamp(m.configPickerCursor-1, 0, len(m.configPickerAccounts)-1)
	case keyEnter:
		if m.configPickerCursor >= 0 && m.configPickerCursor < len(m.configPickerAccounts) {
			acc := m.configPickerAccounts[m.configPickerCursor]
			purpose := m.activeConfigPickerPurpose()
			m.mode = m.configPickerReturnMode
			if purpose == ConfigPickerHandoff {
				if m.handoffConversation == nil {
					return m, nil
				}
				meta := *m.handoffConversation
				edit := m.handoffEdit
				m.handoffConversation = nil
				m.handoffEdit = false
				return m, m.conversationHandoffCmd(meta, acc.Name, edit)
			}
			if purpose == ConfigPickerWorktreeReopen {
				return m.reopenWorktreeWithParameters(map[string]string{"profile": acc.Name})
			}
			if purpose == ConfigPickerLauncherOverride && m.configPickerProjectIdx >= 0 && m.configPickerProjectIdx < len(m.pickerSessions) {
				defaultProfile := m.pickerSessions[m.configPickerProjectIdx].EffectiveProfile()
				if strings.EqualFold(acc.Name, defaultProfile) {
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
			if purpose == ConfigPickerRestartProfile && strings.EqualFold(acc.Name, m.configPickerCurrent) {
				return m, nil
			}
			if purpose == ConfigPickerDashboardLaunch {
				return m, m.dashboardLaunchWithAccountCmd(acc)
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
	total, visible := actionsScrollBounds(m)
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	switch s {
	case keyEsc, keyQ:
		m.exitActionsMode()
		m.actionsOffset = 0
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		if m.actionsOffset < maxOffset {
			m.actionsOffset++
		}
		return m, nil
	case keyK, keyUp:
		if m.actionsOffset > 0 {
			m.actionsOffset--
		}
		return m, nil
	case keyPgDown, keyCtrlF:
		m.actionsOffset += visible
		if m.actionsOffset > maxOffset {
			m.actionsOffset = maxOffset
		}
		return m, nil
	case keyPgUp, keyCtrlB:
		m.actionsOffset -= visible
		if m.actionsOffset < 0 {
			m.actionsOffset = 0
		}
		return m, nil
	case keyCtrlD:
		step := visible / 2
		if step < 1 {
			step = 1
		}
		m.actionsOffset += step
		if m.actionsOffset > maxOffset {
			m.actionsOffset = maxOffset
		}
		return m, nil
	case keyCtrlU:
		step := visible / 2
		if step < 1 {
			step = 1
		}
		m.actionsOffset -= step
		if m.actionsOffset < 0 {
			m.actionsOffset = 0
		}
		return m, nil
	case keyG:
		m.actionsOffset = 0
		return m, nil
	case keyGG:
		m.actionsOffset = maxOffset
		return m, nil
	}
	for _, a := range actionsFor(m) {
		if s != a.keyMatch {
			continue
		}
		if !a.available(m) {
			return m, nil
		}
		next, cmd := a.run(m)
		next.actionsOffset = 0
		return next, cmd
	}
	return m, nil
}

// enterDetailMode switches to ModeDetail for the given window, marks the
// pane as loading, and returns a command that fetches the event ring from
// the daemon. Callers must guard against an empty session list.
func (m Model) enterDetailMode(windowID int, startInTimeline bool) (tea.Model, tea.Cmd) {
	m.clearJumpChord()
	m.mode = ModeDetail
	m.detailWindowID = windowID
	m.detailEvents = nil
	m.detailScroll = 0
	m.detailLoading = true
	m.detailErr = nil
	m.detailViewTimeline = startInTimeline
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
	m.detailViewTimeline = false
	m.detailTimelineScroll = 0
	m.detailTimelineFold = nil
	m.detailShowSuppressed = false
	m.detailShowTimestamps = false
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
	case keyToggleTimeline:
		m.detailViewTimeline = !m.detailViewTimeline
		m.detailTimelineScroll = 0
		return m, nil
	case keyToggleTimestamps:
		if m.detailViewTimeline {
			m.detailShowTimestamps = !m.detailShowTimestamps
		}
		return m, nil

	case keyPhasePrev:
		if !m.detailViewTimeline {
			return m, nil
		}
		phases := groupIntoPhases(m.detailEvents)
		positions := phaseHeaderLinePositions(phases, m.detailTimelineFold, m.detailShowSuppressed)
		target := -1
		for _, pos := range positions {
			if pos < m.detailTimelineScroll {
				target = pos
			}
		}
		if target >= 0 {
			m.detailTimelineScroll = target
		}
		return m, nil

	case keyPhaseNext:
		if !m.detailViewTimeline {
			return m, nil
		}
		phases := groupIntoPhases(m.detailEvents)
		positions := phaseHeaderLinePositions(phases, m.detailTimelineFold, m.detailShowSuppressed)
		for _, pos := range positions {
			if pos > m.detailTimelineScroll {
				m.detailTimelineScroll = pos
				break
			}
		}
		return m, nil

	case keyEnter:
		if !m.detailViewTimeline {
			return m, nil
		}
		phases := groupIntoPhases(m.detailEvents)
		positions := phaseHeaderLinePositions(phases, m.detailTimelineFold, m.detailShowSuppressed)
		phaseIdx := phaseAtScroll(positions, m.detailTimelineScroll)
		if phaseIdx < 0 {
			return m, nil
		}
		ph := phases[phaseIdx]
		if ph.kind == "gap" || ph.kind == "separator" || (ph.kind == "preamble" && len(ph.events) == 0) {
			return m, nil
		}
		if m.detailTimelineFold == nil {
			m.detailTimelineFold = make(map[int]bool)
		}
		currentlyExpanded := isPhaseExpanded(ph, phaseIdx, m.detailTimelineFold)
		m.detailTimelineFold[phaseIdx] = !currentlyExpanded
		clampTimelineScroll(&m, phases)
		return m, nil

	case "s":
		if !m.detailViewTimeline {
			return m, nil
		}
		m.detailShowSuppressed = !m.detailShowSuppressed
		clampTimelineScroll(&m, groupIntoPhases(m.detailEvents))
		return m, nil

	case "z":
		if !m.detailViewTimeline {
			return m, nil
		}
		phases := groupIntoPhases(m.detailEvents)
		m.detailTimelineFold = make(map[int]bool)
		for i, p := range phases {
			if p.kind != "gap" && p.kind != "separator" {
				m.detailTimelineFold[i] = false
			}
		}
		clampTimelineScroll(&m, phases)
		return m, nil

	case "Z":
		if !m.detailViewTimeline {
			return m, nil
		}
		phases := groupIntoPhases(m.detailEvents)
		m.detailTimelineFold = make(map[int]bool)
		for i, p := range phases {
			if p.kind != "gap" && p.kind != "separator" {
				m.detailTimelineFold[i] = true
			}
		}
		return m, nil
	}

	if m.detailViewTimeline {
		phases := groupIntoPhases(m.detailEvents)
		innerWidth := detailBoxInnerWidth(m)
		renderWidth := innerWidth - 2
		if renderWidth < 1 {
			renderWidth = 1
		}
		tlLines := renderTimelineLines(phases, renderWidth, m.detailTimelineFold, m.detailShowSuppressed, m.detailShowTimestamps)
		innerH := detailOverlayInnerHeight(m)
		tlVisible := innerH - detailInnerChrome
		if tlVisible < 0 {
			tlVisible = 0
		}
		tlMax := len(tlLines) - tlVisible
		if tlMax < 0 {
			tlMax = 0
		}
		switch msg.String() {
		case keyJ, keyDown:
			if m.detailTimelineScroll < tlMax {
				m.detailTimelineScroll++
			}
		case keyK, keyUp:
			if m.detailTimelineScroll > 0 {
				m.detailTimelineScroll--
			}
		case keyPgDown:
			m.detailTimelineScroll += tlVisible
			if m.detailTimelineScroll > tlMax {
				m.detailTimelineScroll = tlMax
			}
		case keyPgUp:
			m.detailTimelineScroll -= tlVisible
			if m.detailTimelineScroll < 0 {
				m.detailTimelineScroll = 0
			}
		case keyG:
			m.detailTimelineScroll = 0
		case keyGG:
			m.detailTimelineScroll = tlMax
		}
		return m, nil
	}

	switch msg.String() {
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
		m.mode = m.helpReturnMode
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
