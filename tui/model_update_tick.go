package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/tui/commands"
)

// handleTick processes a commands.TickMsg: sweeps stale transition pulses, schedules
// the next session/active-loop fetches and tick, and conditionally fires a health check.
func (m Model) handleTick(msg commands.TickMsg) (tea.Model, tea.Cmd) {
	_ = msg
	m.sweepRecentTransitions()
	m.activeLoopRefreshSeq++
	cmds := []tea.Cmd{
		m.runInitialFetchCmd(0),
		m.runActiveLoopRefreshCmd(m.activeLoopRefreshSeq),
		commands.Tick(m.interval),
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
	// The Runs view polls on the shared tick rather than owning a refresh key,
	// so a snapshot arriving under the cursor cannot change modes or selection.
	if m.activeRootMode() == ModeASLRuns {
		m.captureASLSelection()
		m.aslLoading = true
		m.aslRefreshSeq++
		cmds = append(cmds, m.runASLSnapshotCmd(m.aslRefreshSeq))
	}
	return m, tea.Batch(cmds...)
}

// handleHealth processes a commands.HealthMsg: updates the daemon-alive flag and/or
// hook health summary, gated by which checks were actually run.
func (m Model) handleHealth(msg commands.HealthMsg) (tea.Model, tea.Cmd) {
	now := time.Now()
	if msg.DaemonChecked {
		m.daemonAlive = msg.DaemonAlive
		m.daemonHealthCheckedAt = now
	}
	if msg.HookChecked {
		m.hookHealth = msg.HookSummary
		m.hookHealthErr = msg.HookErr
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
