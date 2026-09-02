package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/clipboard"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

var copyToClipboard = clipboard.Copy

// action describes one entry in the per-row actions overlay. keyDisplay is
// what the user sees in the overlay (e.g. "⏎"); keyMatch is the literal
// msg.String() value the handler compares against (e.g. "enter").
type action struct {
	keyDisplay  string
	keyMatch    string
	label       string
	description string
	available   func(m Model) bool
	run         func(m Model) (Model, tea.Cmd)
}

// actionsFor returns the ordered list of actions exposed in the overlay for
// the current target row (m.targetSession). The availability functions take
// the model so they can read live state (lastActivity, idle threshold, etc.).
func actionsFor(_ Model) []action {
	return []action{
		{
			keyDisplay:  "⏎",
			keyMatch:    keyEnter,
			label:       "focus tab",
			description: "jump to the session's Kitty tab",
			available:   func(Model) bool { return true },
			run: func(m Model) (Model, tea.Cmd) {
				cmd := focusCmd(m.client, m.targetSession.TabID)
				m.exitActionsMode()
				return m, cmd
			},
		},
		{
			keyDisplay:  "e",
			keyMatch:    keyE,
			label:       "peek scrollback",
			description: "open the session's scrollback in an overlay pager",
			available: func(m Model) bool {
				return m.targetSession.State == "bolt"
			},
			run: func(m Model) (Model, tea.Cmd) {
				cmd := peekCmd(m.client, m.targetSession.WindowID)
				m.exitActionsMode()
				return m, cmd
			},
		},
		{
			keyDisplay:  "r",
			keyMatch:    keyR,
			label:       "restart",
			description: "close and relaunch the session",
			available:   func(m Model) bool { return !m.targetSession.Locked },
			run: func(m Model) (Model, tea.Cmd) {
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
			keyDisplay:  "c",
			keyMatch:    keyC,
			label:       "override profile",
			description: "restart under a different harness profile",
			available:   func(m Model) bool { return !m.targetSession.Locked },
			run: func(m Model) (Model, tea.Cmd) {
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = m.targetSession.EffectiveProfile()
				m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, m.targetSession.EffectiveProfile())
				m.configPickerPurpose = ConfigPickerRestartProfile
				m.configPickerReturnMode = m.actionsReturnMode
				m.actionsReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
				return m, nil
			},
		},
		{
			keyDisplay:  "C",
			keyMatch:    keyDashboardLaunch,
			label:       "launch new",
			description: "pick a profile and launch another session from this row",
			available:   func(Model) bool { return true },
			run: func(m Model) (Model, tea.Cmd) {
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = m.targetSession.EffectiveProfile()
				m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, m.targetSession.EffectiveProfile())
				m.configPickerPurpose = ConfigPickerDashboardLaunch
				m.configPickerReturnMode = m.actionsReturnMode
				m.actionsReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
				return m, nil
			},
		},
		{
			keyDisplay:  "x",
			keyMatch:    keyClearStatus,
			label:       "clear icon",
			description: "drop the attention/key/stop badge on the tab title",
			available: func(m Model) bool {
				return dashboard.StateGroup(m.targetSession, m.lastActivity, m.idleThreshold) == 0
			},
			run: func(m Model) (Model, tea.Cmd) {
				cmd := m.clearStatusCmd(m.targetSession)
				m.actionErr = nil
				m.exitActionsMode()
				return m, cmd
			},
		},
		{
			keyDisplay:  "X",
			keyMatch:    keyClose,
			label:       "close",
			description: "exit the Claude process; transcript persists",
			available:   func(m Model) bool { return !m.targetSession.Locked },
			run: func(m Model) (Model, tea.Cmd) {
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
			keyDisplay:  "o",
			keyMatch:    keyO,
			label:       "open dir in shell",
			description: "spawn a plain shell tab at the session's directory",
			available: func(m Model) bool {
				_, ok := findConfigSession(m.cfg, m.targetSession.EffectiveProfile(), m.targetSession.EffectiveProject())
				return ok
			},
			run: func(m Model) (Model, tea.Cmd) {
				cs, ok := findConfigSession(m.cfg, m.targetSession.EffectiveProfile(), m.targetSession.EffectiveProject())
				m.exitActionsMode()
				if !ok {
					return m, nil
				}
				return m, openDirActionCmd(m, cs)
			},
		},
		{
			keyDisplay:  "O",
			keyMatch:    keyOpenEditor,
			label:       "open in editor",
			description: "open the project directory in $VISUAL/$EDITOR (or defaults.editor)",
			available: func(m Model) bool {
				_, ok := findConfigSession(m.cfg, m.targetSession.EffectiveProfile(), m.targetSession.EffectiveProject())
				return ok
			},
			run: func(m Model) (Model, tea.Cmd) {
				cs, ok := findConfigSession(m.cfg, m.targetSession.EffectiveProfile(), m.targetSession.EffectiveProject())
				m.exitActionsMode()
				if !ok {
					return m, nil
				}
				return m, openInEditorActionCmd(m, cs)
			},
		},
		{
			keyDisplay:  "y",
			keyMatch:    keyY,
			label:       "copy title",
			description: "copy the session title to the system clipboard",
			available:   func(Model) bool { return true },
			run: func(m Model) (Model, tea.Cmd) {
				title := dashboard.SessionDisplayTitle(m.targetSession)
				m.exitActionsMode()
				return m, copyTitleCmd(title)
			},
		},
		{
			keyDisplay:  "L",
			keyMatch:    keyLock,
			label:       "lock/unlock",
			description: "toggle session lock (prevents close/restart/override)",
			available:   func(Model) bool { return true },
			run: func(m Model) (Model, tea.Cmd) {
				m.exitActionsMode()
				return m, toggleLockCmd(m.client, m.targetSession)
			},
		},
		{
			keyDisplay:  "m",
			keyMatch:    keyM,
			label:       "mute/unmute",
			description: "toggle session mute (suppresses macOS notifications)",
			available:   func(Model) bool { return true },
			run: func(m Model) (Model, tea.Cmd) {
				m.exitActionsMode()
				return m, toggleMuteCmd(m.client, m.targetSession)
			},
		},
	}
}

func (m *Model) exitActionsMode() {
	m.mode = ModeList
	m.actionsReturnMode = ModeList
}
