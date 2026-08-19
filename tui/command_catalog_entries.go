package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

func commandCatalog() []command {
	return []command{
		{
			id: commandFocusSession, group: commandGroupSession,
			title:       staticCommandTitle("Focus Selected Session"),
			description: "Jump to the selected session's Kitty tab.",
			aliases:     []string{"jump", "tab"}, shortcut: "enter / space",
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, focusCmd(m.client, ctx.target.TabID)
			},
		},
		{
			id: commandPeekScrollback, group: commandGroupSession,
			title:       staticCommandTitle("Peek Scrollback"),
			description: "Open the active session's scrollback in an overlay pager.",
			aliases:     []string{"pager", "scrollback"}, shortcut: keyE,
			availability: peekCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, peekCmd(m.client, ctx.target.WindowID)
			},
		},
		{
			id: commandRestartSession, group: commandGroupSession,
			title:       staticCommandTitle("Restart Session"),
			description: "Confirm before closing and relaunching the selected session.",
			aliases:     []string{"relaunch"}, shortcut: keyR,
			availability: unlockedSessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.targetSession = ctx.target
				m.actionErr = nil
				m.confirmAction = "restart"
				m.confirmConsequence = restartConsequence
				m.confirmReturnMode = ModeList
				m.mode = ModeConfirm
				return m, nil
			},
		},
		{
			id: commandOverrideProfile, group: commandGroupSession,
			title:       staticCommandTitle("Override Profile"),
			description: "Restart the selected session under a different harness profile.",
			aliases:     []string{"account", "profile"}, shortcut: keyC,
			availability: unlockedSessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.targetSession = ctx.target
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = ctx.target.EffectiveProfile()
				m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, ctx.target.EffectiveProfile())
				m.configPickerPurpose = ConfigPickerRestartProfile
				m.configPickerReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
				return m, nil
			},
		},
		{
			id: commandLaunchAnother, group: commandGroupSession,
			title:       staticCommandTitle("Launch Another Session"),
			description: "Pick a profile and launch another session from the selected project.",
			aliases:     []string{"duplicate", "new"}, shortcut: keyDashboardLaunch,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.targetSession = ctx.target
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = ctx.target.EffectiveProfile()
				m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, ctx.target.EffectiveProfile())
				m.configPickerPurpose = ConfigPickerDashboardLaunch
				m.configPickerReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
				return m, nil
			},
		},
		{
			id: commandClearStatus, group: commandGroupSession,
			title:       staticCommandTitle("Clear Status Icon"),
			description: "Drop the attention, key, or stop badge from the selected tab.",
			aliases:     []string{"attention", "badge", "icon"}, shortcut: keyClearStatus,
			availability: clearStatusCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.targetSession = ctx.target
				m.actionErr = nil
				return m, m.clearStatusCmd(ctx.target)
			},
		},
		{
			id: commandCloseSession, group: commandGroupSession,
			title:       staticCommandTitle("Close Session"),
			description: "Exit the harness process while preserving its transcript.",
			aliases:     []string{"exit", "kill"}, shortcut: keyClose,
			availability: unlockedSessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.targetSession = ctx.target
				m.actionErr = nil
				if m.cfg.Defaults.ConfirmDestructive {
					m.confirmAction = "close"
					m.confirmConsequence = closeConsequence
					m.confirmReturnMode = ModeList
					m.mode = ModeConfirm
					return m, nil
				}
				return m, m.closeWithToast(ctx.target)
			},
		},
		{
			id: commandOpenDirectory, group: commandGroupSession,
			title:       staticCommandTitle("Open Directory in Shell"),
			description: "Open a plain shell tab in the selected project's directory.",
			aliases:     []string{"directory", "shell", "terminal"}, shortcut: keyO,
			availability: configuredProjectCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				cs, ok := findConfigSession(m.cfg, ctx.target.EffectiveProfile(), ctx.target.EffectiveProject())
				if !ok {
					return m, nil
				}
				m.targetSession = ctx.target
				m.actionErr = nil
				return m, openDirActionCmd(m, cs)
			},
		},
		{
			id: commandOpenEditor, group: commandGroupSession,
			title:       staticCommandTitle("Open in Editor"),
			description: "Open the selected project in the configured editor.",
			aliases:     []string{"directory", "edit"}, shortcut: keyOpenEditor,
			availability: configuredProjectCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				cs, ok := findConfigSession(m.cfg, ctx.target.EffectiveProfile(), ctx.target.EffectiveProject())
				if !ok {
					return m, nil
				}
				m.targetSession = ctx.target
				m.actionErr = nil
				return m, openInEditorActionCmd(m, cs)
			},
		},
		{
			id: commandCopyTitle, group: commandGroupSession,
			title:       staticCommandTitle("Copy Session Title"),
			description: "Copy the selected session's display title to the clipboard.",
			aliases:     []string{"clipboard", "yank"}, shortcut: keyY,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, copyTitleCmd(runningSessionDisplayTitle(ctx.target))
			},
		},
		{
			id: commandToggleLock, group: commandGroupSession,
			title: func(_ Model, ctx commandContext) string {
				if ctx.hasTarget && ctx.target.Locked {
					return "Unlock Session"
				}
				return "Lock Session"
			},
			description: "Toggle protection from close, restart, and profile override.",
			aliases:     []string{"protect", "unlock"}, shortcut: keyLock,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, toggleLockCmd(m.client, ctx.target)
			},
		},
		{
			id: commandToggleMute, group: commandGroupSession,
			title: func(_ Model, ctx commandContext) string {
				if ctx.hasTarget && ctx.target.Muted {
					return "Unmute Session"
				}
				return "Mute Session"
			},
			description: "Toggle macOS notifications for the selected session.",
			aliases:     []string{"notifications", "unmute"}, shortcut: keyM,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, toggleMuteCmd(m.client, ctx.target)
			},
		},
		{
			id: commandSearchProject, group: commandGroupSession,
			title:       staticCommandTitle("Search This Project's Past Sessions"),
			description: "Search transcript history filtered to the selected project.",
			aliases:     []string{"history", "project", "transcripts"}, shortcut: keyProjectSessionSearch,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				updated, cmd := m.enterSessionSearchForProject(ctx.target.EffectiveProject())
				return updated.(Model), cmd
			},
		},
		{
			id: commandShowDetails, group: commandGroupSession,
			title:       staticCommandTitle("Show Session Details"),
			description: "Open the selected session's hook event log.",
			aliases:     []string{"events", "hooks"}, shortcut: keyD,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				updated, cmd := m.enterDetailMode(ctx.target.WindowID, false)
				return updated.(Model), cmd
			},
		},
		{
			id: commandShowTimeline, group: commandGroupSession,
			title:       staticCommandTitle("Show Session Timeline"),
			description: "Open the selected session's events grouped into timeline phases.",
			aliases:     []string{"events", "phases"}, shortcut: keyOpenTimeline,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				updated, cmd := m.enterDetailMode(ctx.target.WindowID, true)
				return updated.(Model), cmd
			},
		},

		{
			id: commandFilterSessions, group: commandGroupExplore,
			title:       staticCommandTitle("Filter Sessions"),
			description: "Filter running sessions with the dashboard query language.",
			aliases:     []string{"find", "search"}, shortcut: keySlash,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				m.enterFilter("")
				return m, nil
			},
		},
		{
			id: commandCycleProfileFilter, group: commandGroupExplore,
			title:       staticCommandTitle("Cycle Profile Filter"),
			description: "Cycle running sessions by effective profile and back to all profiles.",
			aliases:     []string{"account", "filter", "running"}, shortcut: keyF,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				return m, m.cycleDashboardProfile()
			},
		},
		{
			id: commandClearFilters, group: commandGroupExplore,
			title:       staticCommandTitle("Clear Filters"),
			description: "Remove every active dashboard filter chip.",
			aliases:     []string{"chips", "reset"}, shortcut: keyEsc,
			visible: func(m Model, _ commandContext) bool { return !m.activeFilter.IsEmpty() },
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				m.applyFilterChange(Filter{})
				return m, nil
			},
		},
		{
			id: commandOpenLauncher, group: commandGroupExplore,
			title:       staticCommandTitle("Open Launcher"),
			description: "Choose configured projects to launch.",
			aliases:     []string{"launch", "projects"}, shortcut: keyL,
			execute: openLauncherCommand,
		},
		{
			id: commandSearchAllSessions, group: commandGroupExplore,
			title:       staticCommandTitle("Search All Past Sessions"),
			description: "Search transcript history across every project and harness.",
			aliases:     []string{"history", "transcripts"}, shortcut: keySessionSearch,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				updated, cmd := m.enterSessionSearch()
				return updated.(Model), cmd
			},
		},
		{
			id: commandSearchPlans, group: commandGroupExplore,
			title:       staticCommandTitle("Search Plans"),
			description: "Search indexed agent-session-loop plans.",
			aliases:     []string{"agent plans", "history"}, shortcut: keyPlanSearch,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				updated, cmd := m.enterPlanSearch()
				return updated.(Model), cmd
			},
		},
		{
			id: commandTogglePreview, group: commandGroupExplore,
			title: func(m Model, _ commandContext) string {
				if m.previewEnabled {
					return "Hide Preview"
				}
				return "Show Preview"
			},
			description: "Toggle the selected session's live scrollback pane.",
			aliases:     []string{"hide", "pane", "scrollback"}, shortcut: keyTogglePreview,
			availability: previewCommandAvailability,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				updated, cmd := m.togglePreview()
				return updated.(Model), cmd
			},
		},

		{
			id: commandResortTabs, group: commandGroupApp,
			title:       staticCommandTitle("Resort Tabs"),
			description: "Reorder managed Kitty tabs to match the dashboard sort order.",
			aliases:     []string{"reorder", "tabs"}, shortcut: keyResort,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				m.actionErr = nil
				cmds := []tea.Cmd{resortCmd(m.client, m.sortMode)}
				if cmd := m.pushToast(Toast{
					Severity: toastInfo,
					Text:     "Resorting tabs…",
					Expires:  time.Now().Add(progressToastTTL),
				}); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			},
		},
		{
			id: commandUndoClose, group: commandGroupApp,
			title:       staticCommandTitle("Undo Last Close"),
			description: "Relaunch the most recently closed session before undo expires.",
			aliases:     []string{"restore", "reopen"}, shortcut: keyU,
			availability: undoCommandAvailability,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				return m, m.undoLastCmd()
			},
		},
		{
			id: commandDismissDeprecation, group: commandGroupApp,
			title:       staticCommandTitle("Dismiss Deprecation Warning"),
			description: "Hide the visible configuration deprecation banner.",
			aliases:     []string{"banner", "warning"}, shortcut: keyD,
			visible: func(m Model, _ commandContext) bool {
				return !m.deprecationDismissed && len(m.deprecations) > 0
			},
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				m.deprecationDismissed = true
				return m, nil
			},
		},
		{
			id: commandShowKeyboardHelp, group: commandGroupApp,
			title:       staticCommandTitle("Show Keyboard Shortcuts"),
			description: "Open the complete dashboard keyboard reference.",
			aliases:     []string{"help", "keys"}, shortcut: keyQuestion,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				m.openHelp(ModeList)
				return m, nil
			},
		},
		{
			id: commandQuit, group: commandGroupApp,
			title:       staticCommandTitle("Quit"),
			description: "Exit the agentmux dashboard.",
			aliases:     []string{"close app", "exit"}, shortcut: keyQ + " / " + keyCtrlC,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				return m, tea.Quit
			},
		},
	}
}
