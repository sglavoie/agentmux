package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
)

func commandCatalog() []commands.Command[Model] {
	return []commands.Command[Model]{
		{
			ID: commands.FocusSession, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Focus Selected Session"),
			Description: "Jump to the selected session's Kitty tab.",
			Aliases:     []string{"jump", "tab"}, Shortcut: "enter / space",
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, commands.Focus(m.client, ctx.Target.TabID)
			},
		},
		{
			ID: commands.PeekScrollback, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Peek Scrollback"),
			Description: "Open the active session's scrollback in an overlay pager.",
			Aliases:     []string{"pager", "scrollback"}, Shortcut: keyE,
			Availability: peekCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, commands.Peek(m.client, ctx.Target.WindowID)
			},
		},
		{
			ID: commands.RestartSession, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Restart Session"),
			Description: "Confirm before closing and relaunching the selected session.",
			Aliases:     []string{"relaunch"}, Shortcut: keyR,
			Availability: unlockedSessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.targetSession = ctx.Target
				m.actionErr = nil
				m.confirmAction = "restart"
				m.confirmConsequence = restartConsequence
				m.confirmReturnMode = ModeList
				m.mode = ModeConfirm
				return m, nil
			},
		},
		{
			ID: commands.OverrideProfile, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Override Profile"),
			Description: "Restart the selected session under a different harness profile.",
			Aliases:     []string{"account", "profile"}, Shortcut: keyC,
			Availability: unlockedSessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.targetSession = ctx.Target
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = ctx.Target.EffectiveProfile()
				m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, ctx.Target.EffectiveProfile())
				m.configPickerPurpose = launch.ConfigPickerRestartProfile
				m.configPickerReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
				return m, nil
			},
		},
		{
			ID: commands.LaunchAnother, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Launch Another Session"),
			Description: "Pick a profile and launch another session from the selected project.",
			Aliases:     []string{"duplicate", "new"}, Shortcut: keyDashboardLaunch,
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.targetSession = ctx.Target
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = ctx.Target.EffectiveProfile()
				m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, ctx.Target.EffectiveProfile())
				m.configPickerPurpose = launch.ConfigPickerDashboardLaunch
				m.configPickerReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
				return m, nil
			},
		},
		{
			ID: commands.ClearStatusIcon, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Clear Status Icon"),
			Description: "Drop the attention, key, or stop badge from the selected tab.",
			Aliases:     []string{"attention", "badge", "icon"}, Shortcut: keyClearStatus,
			Availability: clearStatusCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.targetSession = ctx.Target
				m.actionErr = nil
				return m, m.clearStatusCmd(ctx.Target)
			},
		},
		{
			ID: commands.CloseSession, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Close Session"),
			Description: "Exit the harness process while preserving its transcript.",
			Aliases:     []string{"exit", "kill"}, Shortcut: keyClose,
			Availability: unlockedSessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.targetSession = ctx.Target
				m.actionErr = nil
				if m.cfg.Defaults.ConfirmDestructive {
					m.confirmAction = "close"
					m.confirmConsequence = closeConsequence
					m.confirmReturnMode = ModeList
					m.mode = ModeConfirm
					return m, nil
				}
				return m, m.closeWithToast(ctx.Target)
			},
		},
		{
			ID: commands.OpenDirectoryID, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Open Directory in Shell"),
			Description: "Open a plain shell tab in the selected project's directory.",
			Aliases:     []string{"directory", "shell", "terminal"}, Shortcut: keyO,
			Availability: configuredProjectCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				cs, ok := commands.FindConfigSession(m.cfg, ctx.Target.EffectiveProfile(), ctx.Target.EffectiveProject())
				if !ok {
					return m, nil
				}
				m.targetSession = ctx.Target
				m.actionErr = nil
				return m, commands.OpenDirectory(m.client, m.cfg, cs)
			},
		},
		{
			ID: commands.OpenEditorID, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Open in Editor"),
			Description: "Open the selected project in the configured editor.",
			Aliases:     []string{"directory", "edit"}, Shortcut: keyOpenEditor,
			Availability: configuredProjectCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				cs, ok := commands.FindConfigSession(m.cfg, ctx.Target.EffectiveProfile(), ctx.Target.EffectiveProject())
				if !ok {
					return m, nil
				}
				m.targetSession = ctx.Target
				m.actionErr = nil
				return m, commands.OpenInEditor(m.client, m.cfg, cs)
			},
		},
		{
			ID: commands.CopyTitleID, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Copy Session Title"),
			Description: "Copy the selected session's display title to the clipboard.",
			Aliases:     []string{"clipboard", "yank"}, Shortcut: keyY,
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, commands.CopyTitle(dashboard.SessionDisplayTitle(ctx.Target))
			},
		},
		{
			ID: commands.ToggleLockID, Group: commands.GroupSession,
			Title: func(_ Model, ctx commands.Context) string {
				if ctx.HasTarget && ctx.Target.Locked {
					return "Unlock Session"
				}
				return "Lock Session"
			},
			Description: "Toggle protection from close, restart, and profile override.",
			Aliases:     []string{"protect", "unlock"}, Shortcut: keyLock,
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, commands.ToggleLock(m.client, ctx.Target)
			},
		},
		{
			ID: commands.ToggleMuteID, Group: commands.GroupSession,
			Title: func(_ Model, ctx commands.Context) string {
				if ctx.HasTarget && ctx.Target.Muted {
					return "Unmute Session"
				}
				return "Mute Session"
			},
			Description: "Toggle macOS notifications for the selected session.",
			Aliases:     []string{"notifications", "unmute"}, Shortcut: keyM,
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, commands.ToggleMute(m.client, ctx.Target)
			},
		},
		{
			ID: commands.SearchProject, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Search This Project's Past Sessions"),
			Description: "Search transcript history filtered to the selected project.",
			Aliases:     []string{"history", "project", "transcripts"}, Shortcut: keyProjectSessionSearch,
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterSessionSearchForProject(ctx.Target.EffectiveProject())
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.ShowDetails, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Show Session Details"),
			Description: "Open the selected session's hook event log.",
			Aliases:     []string{"events", "hooks"}, Shortcut: keyD,
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterDetailMode(ctx.Target.WindowID, false)
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.ShowTimeline, Group: commands.GroupSession,
			Title:       commands.StaticTitle[Model]("Show Session Timeline"),
			Description: "Open the selected session's events grouped into timeline phases.",
			Aliases:     []string{"events", "phases"}, Shortcut: keyOpenTimeline,
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterDetailMode(ctx.Target.WindowID, true)
				return updated.(Model), cmd
			},
		},

		{
			ID: commands.FilterSessions, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Filter Sessions"),
			Description: "Filter running sessions with the dashboard query language.",
			Aliases:     []string{"find", "search"}, Shortcut: keySlash,
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				m.enterFilter("")
				return m, nil
			},
		},
		{
			ID: commands.CycleProfileFilter, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Cycle Profile Filter"),
			Description: "Cycle running sessions by effective profile and back to all profiles.",
			Aliases:     []string{"account", "filter", "running"}, Shortcut: keyF,
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				return m, m.cycleDashboardProfile()
			},
		},
		{
			ID: commands.ClearFilters, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Clear Filters"),
			Description: "Remove every active dashboard filter chip.",
			Aliases:     []string{"chips", "reset"}, Shortcut: keyEsc,
			Visible: func(m Model, _ commands.Context) bool { return !m.activeFilter.IsEmpty() },
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				m.applyFilterChange(dashboard.Filter{})
				return m, nil
			},
		},
		{
			ID: commands.OpenLauncher, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Open Launcher"),
			Description: "Choose configured projects to launch.",
			Aliases:     []string{"launch", "projects"}, Shortcut: keyL,
			Execute: openLauncherCommand,
		},
		{
			ID: commands.SearchAllSessions, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Search All Past Sessions"),
			Description: "Search transcript history across every project and harness.",
			Aliases:     []string{"history", "transcripts"}, Shortcut: keySessionSearch,
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterSessionSearch()
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.SearchPlans, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Search Plans"),
			Description: "Search indexed agent-session-loop plans.",
			Aliases:     []string{"agent plans", "history"}, Shortcut: keyPlanSearch,
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterPlanSearch()
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.ManageWorktrees, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Manage Worktrees"),
			Description: "Browse managed Git worktrees and open their paths.",
			Aliases:     []string{"git", "branches", "inventory"}, Shortcut: keyWorktrees,
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterWorktrees()
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.ShowRuns, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Show Runs"),
			Description: "Review active and actionable agent-session-loop runs.",
			Aliases:     []string{"asl", "loop", "needs action"}, Shortcut: keyASLRuns,
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterASLRuns()
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.TogglePreview, Group: commands.GroupExplore,
			Title: func(m Model, _ commands.Context) string {
				if m.previewEnabled {
					return "Hide Preview"
				}
				return "Show Preview"
			},
			Description: "Toggle the selected session's live scrollback pane.",
			Aliases:     []string{"hide", "pane", "scrollback"}, Shortcut: keyTogglePreview,
			Availability: previewCommandAvailability,
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.togglePreview()
				return updated.(Model), cmd
			},
		},

		{
			ID: commands.ResortTabs, Group: commands.GroupApp,
			Title:       commands.StaticTitle[Model]("Resort Tabs"),
			Description: "Reorder managed Kitty tabs to match the dashboard sort order.",
			Aliases:     []string{"reorder", "tabs"}, Shortcut: keyResort,
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				m.actionErr = nil
				cmds := []tea.Cmd{commands.Resort(m.client, m.sortMode)}
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
			ID: commands.UndoClose, Group: commands.GroupApp,
			Title:       commands.StaticTitle[Model]("Undo Last Close"),
			Description: "Relaunch the most recently closed session before undo expires.",
			Aliases:     []string{"restore", "reopen"}, Shortcut: keyU,
			Availability: undoCommandAvailability,
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				return m, m.undoLastCmd()
			},
		},
		{
			ID: commands.DismissDeprecation, Group: commands.GroupApp,
			Title:       commands.StaticTitle[Model]("Dismiss Deprecation Warning"),
			Description: "Hide the visible configuration deprecation banner.",
			Aliases:     []string{"banner", "warning"}, Shortcut: keyD,
			Visible: func(m Model, _ commands.Context) bool {
				return !m.deprecationDismissed && len(m.deprecations) > 0
			},
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				m.deprecationDismissed = true
				return m, nil
			},
		},
		{
			ID: commands.ShowKeyboardHelp, Group: commands.GroupApp,
			Title:       commands.StaticTitle[Model]("Show Keyboard Shortcuts"),
			Description: "Open the complete dashboard keyboard reference.",
			Aliases:     []string{"help", "keys"}, Shortcut: keyQuestion,
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				m.openHelp(ModeList)
				return m, nil
			},
		},
		{
			ID: commands.Quit, Group: commands.GroupApp,
			Title:       commands.StaticTitle[Model]("Quit"),
			Description: "Exit the agentmux dashboard.",
			Aliases:     []string{"close app", "exit"}, Shortcut: keyQ + " / " + keyCtrlC,
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				return m, tea.Quit
			},
		},
	}
}
