// This file is the per-row actions overlay: the action set a selected row
// offers, the availability rules that gray entries out, and the root-named
// renderers that paint the overlay through the shared tui/commands widget.

package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
)

// actionsFor returns the ordered list of actions exposed in the overlay for
// the current target row (m.targetSession). The availability functions take
// the model so they can read live state (lastActivity, idle threshold, etc.).
func actionsFor(_ Model) []commands.Action[Model] {
	return []commands.Action[Model]{
		{
			KeyDisplay:  "⏎",
			KeyMatch:    keyEnter,
			Label:       "focus tab",
			Description: "jump to the session's Kitty tab",
			Available:   func(Model) bool { return true },
			Run: func(m Model) (Model, tea.Cmd) {
				cmd := commands.Focus(m.client, m.targetSession.TabID)
				m.exitActionsMode()
				return m, cmd
			},
		},
		{
			KeyDisplay:  "e",
			KeyMatch:    keyE,
			Label:       "peek scrollback",
			Description: "open the session's scrollback in an overlay pager",
			Available: func(m Model) bool {
				return m.targetSession.State == "bolt"
			},
			Run: func(m Model) (Model, tea.Cmd) {
				cmd := commands.Peek(m.client, m.targetSession.WindowID)
				m.exitActionsMode()
				return m, cmd
			},
		},
		{
			KeyDisplay:  "r",
			KeyMatch:    keyR,
			Label:       "restart",
			Description: "close and relaunch the session",
			Available:   func(m Model) bool { return !m.targetSession.Locked },
			Run: func(m Model) (Model, tea.Cmd) {
				m.actionErr = nil
				if m.cfg.Defaults.ConfirmDestructive {
					m.confirmAction = "restart"
					m.confirmConsequence = restartConsequence
					m.confirmReturnMode = m.actionsReturnMode
					m.actionsReturnMode = ModeList
					m.mode = ModeConfirm
					return m, nil
				}
				m.exitActionsMode()
				cmd := m.restartWithToast(m.targetSession)
				return m, cmd
			},
		},
		{
			KeyDisplay:  "c",
			KeyMatch:    keyC,
			Label:       "override profile",
			Description: "restart under a different harness profile",
			Available:   func(m Model) bool { return !m.targetSession.Locked },
			Run: func(m Model) (Model, tea.Cmd) {
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = m.targetSession.EffectiveProfile()
				m.configPickerCursor = launch.AccountIndex(m.cfg.Accounts, m.targetSession.EffectiveProfile())
				m.configPickerPurpose = launch.ConfigPickerRestartProfile
				m.configPickerReturnMode = m.actionsReturnMode
				m.actionsReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
				return m, nil
			},
		},
		{
			KeyDisplay:  "C",
			KeyMatch:    keyDashboardLaunch,
			Label:       "launch new",
			Description: "pick a profile and launch another session from this row",
			Available:   func(Model) bool { return true },
			Run: func(m Model) (Model, tea.Cmd) {
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = m.targetSession.EffectiveProfile()
				m.configPickerCursor = launch.AccountIndex(m.cfg.Accounts, m.targetSession.EffectiveProfile())
				m.configPickerPurpose = launch.ConfigPickerDashboardLaunch
				m.configPickerReturnMode = m.actionsReturnMode
				m.actionsReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
				return m, nil
			},
		},
		{
			KeyDisplay:  "x",
			KeyMatch:    keyClearStatus,
			Label:       "clear icon",
			Description: "drop the attention/key/stop badge on the tab title",
			Available: func(m Model) bool {
				return dashboard.StateGroup(m.targetSession, m.lastActivity, m.idleThreshold) == 0
			},
			Run: func(m Model) (Model, tea.Cmd) {
				cmd := m.clearStatusCmd(m.targetSession)
				m.actionErr = nil
				m.exitActionsMode()
				return m, cmd
			},
		},
		{
			KeyDisplay:  "X",
			KeyMatch:    keyClose,
			Label:       "close",
			Description: "exit the Claude process; transcript persists",
			Available:   func(m Model) bool { return !m.targetSession.Locked },
			Run: func(m Model) (Model, tea.Cmd) {
				m.actionErr = nil
				if m.cfg.Defaults.ConfirmDestructive {
					m.confirmAction = "close"
					m.confirmConsequence = closeConsequence
					m.confirmReturnMode = m.actionsReturnMode
					m.actionsReturnMode = ModeList
					m.mode = ModeConfirm
					return m, nil
				}
				m.exitActionsMode()
				cmd := m.closeWithToast(m.targetSession)
				return m, cmd
			},
		},
		{
			KeyDisplay:  "o",
			KeyMatch:    keyO,
			Label:       "open dir in shell",
			Description: "spawn a plain shell tab at the session's directory",
			Available: func(m Model) bool {
				_, ok := commands.FindConfigSession(m.cfg, m.targetSession.EffectiveProfile(), m.targetSession.EffectiveProject())
				return ok
			},
			Run: func(m Model) (Model, tea.Cmd) {
				cs, ok := commands.FindConfigSession(m.cfg, m.targetSession.EffectiveProfile(), m.targetSession.EffectiveProject())
				m.exitActionsMode()
				if !ok {
					return m, nil
				}
				return m, commands.OpenDirectory(m.client, m.cfg, cs)
			},
		},
		{
			KeyDisplay:  "O",
			KeyMatch:    keyOpenEditor,
			Label:       "open in editor",
			Description: "open the project directory in $VISUAL/$EDITOR (or defaults.editor)",
			Available: func(m Model) bool {
				_, ok := commands.FindConfigSession(m.cfg, m.targetSession.EffectiveProfile(), m.targetSession.EffectiveProject())
				return ok
			},
			Run: func(m Model) (Model, tea.Cmd) {
				cs, ok := commands.FindConfigSession(m.cfg, m.targetSession.EffectiveProfile(), m.targetSession.EffectiveProject())
				m.exitActionsMode()
				if !ok {
					return m, nil
				}
				return m, commands.OpenInEditor(m.client, m.cfg, cs)
			},
		},
		{
			KeyDisplay:  "y",
			KeyMatch:    keyY,
			Label:       "copy title",
			Description: "copy the session title to the system clipboard",
			Available:   func(Model) bool { return true },
			Run: func(m Model) (Model, tea.Cmd) {
				title := dashboard.SessionDisplayTitle(m.targetSession)
				m.exitActionsMode()
				return m, commands.CopyTitle(title)
			},
		},
		{
			KeyDisplay:  "L",
			KeyMatch:    keyLock,
			Label:       "lock/unlock",
			Description: "toggle session lock (prevents close/restart/override)",
			Available:   func(Model) bool { return true },
			Run: func(m Model) (Model, tea.Cmd) {
				m.exitActionsMode()
				return m, commands.ToggleLock(m.client, m.targetSession)
			},
		},
		{
			KeyDisplay:  "m",
			KeyMatch:    keyM,
			Label:       "mute/unmute",
			Description: "toggle session mute (suppresses macOS notifications)",
			Available:   func(Model) bool { return true },
			Run: func(m Model) (Model, tea.Cmd) {
				m.exitActionsMode()
				return m, commands.ToggleMute(m.client, m.targetSession)
			},
		},
	}
}

func (m *Model) exitActionsMode() {
	m.mode = ModeList
	m.actionsReturnMode = ModeList
}

// This file is the seam between the root Bubble Tea model and the actions-
// overlay half of tui/commands. It projects the model onto the leaf's input
// value and re-exposes the leaf's geometry, scrolling, and renderers under the
// names the rest of the root package already uses. Deciding what a key in the
// overlay DOES stays with actions.go and model_modes.go.

// actionsInput charges the root's own chrome against the body height before
// handing it to the leaf, which knows nothing about the header, footer, or
// status bar surrounding the modal.
func actionsInput(m Model) commands.ActionsInput[Model] {
	return commands.ActionsInput[Model]{
		Model:      m,
		Actions:    actionsFor(m),
		Title:      dashboard.SessionDisplayTitle(m.targetSession),
		Offset:     m.actionsOffset,
		Width:      m.width,
		BodyHeight: rootBodyHeight(m),
	}
}

func renderActions(m Model, descWidth int) string {
	return commands.RenderActions(actionsInput(m), descWidth)
}

func renderActionsBox(m Model) string { return commands.RenderActionsBox(actionsInput(m)) }

func actionsScrollBounds(m Model) (total, visible int) { return actionsInput(m).ScrollBounds() }

func clampActionsOffset(m *Model) {
	if m.mode != ModeActions {
		m.actionsOffset = 0
		return
	}
	m.actionsOffset = actionsInput(*m).ClampOffset()
}
