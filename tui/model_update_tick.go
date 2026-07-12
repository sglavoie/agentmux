package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// handleTick processes a tickMsg: sweeps stale transition pulses, schedules
// the next session/active-loop fetches and tick, and conditionally fires a health check.
func (m Model) handleTick(msg tickMsg) (tea.Model, tea.Cmd) {
	_ = msg
	m.sweepRecentTransitions()
	m.activeLoopRefreshSeq++
	cmds := []tea.Cmd{
		m.runInitialFetchCmd(0),
		m.runActiveLoopRefreshCmd(m.activeLoopRefreshSeq),
		tickCmd(m.interval),
	}
	now := time.Now()
	checkDaemon := now.Sub(m.daemonHealthCheckedAt) >= daemonHealthInterval
	checkHooks := now.Sub(m.hookHealthCheckedAt) >= hookHealthInterval
	if checkDaemon || checkHooks {
		cmds = append(cmds, m.runHealthCmd(checkDaemon, checkHooks))
	}
	if m.mode == ModeDetail && m.detailWindowID != 0 {
		cmds = append(cmds, m.runSessionDetailCmd(m.detailWindowID))
	}
	if m.workspaceLoadErr != nil && !m.workspaceProjectionInFlight {
		if cmd := m.dispatchWorkspaceProjection(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

// handleHealth processes a healthMsg: updates the daemon-alive flag and/or
// hook health summary, gated by which checks were actually run.
func (m Model) handleHealth(msg healthMsg) (tea.Model, tea.Cmd) {
	now := time.Now()
	if msg.daemonChecked {
		m.daemonAlive = msg.daemonAlive
		m.daemonHealthCheckedAt = now
	}
	if msg.hookChecked {
		m.hookHealth = msg.hookSummary
		m.hookHealthErr = msg.hookErr
		m.hookHealthCheckedAt = now
	}
	return m, nil
}

// handlePreviewTick processes a previewTickMsg: if preview is active, schedules
// the next content fetch and tick.
func (m Model) handlePreviewTick(msg previewTickMsg) (tea.Model, tea.Cmd) {
	_ = msg
	if !m.activePreviewEnabled() {
		return m, nil
	}
	d := time.Duration(m.cfg.Defaults.Preview.RefreshMs) * time.Millisecond
	return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID), previewTickCmd(d))
}

// handlePreviewContent processes a previewContentMsg: updates the cached
// preview text or error when the message belongs to the current window.
func (m Model) handlePreviewContent(msg previewContentMsg) (tea.Model, tea.Cmd) {
	if !m.activePreviewEnabled() || msg.windowID != m.previewWindowID {
		return m, nil
	}
	if msg.err != nil {
		m.previewError = msg.err.Error()
	} else {
		m.previewError = ""
		m.previewText = msg.text
	}
	return m, nil
}
