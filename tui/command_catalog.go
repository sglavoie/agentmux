// This file is the command catalog: every command the palette can offer, in
// the order it offers them, together with the grouping and availability rules
// that decide which of them a given model state can actually run.

package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
)

// This file is the seam between the root Bubble Tea model and the command
// vocabulary in tui/commands. The command type, its identities, its groups, its
// availability verdicts, and the model-free gates all live in the leaf; what
// remains here is what a verdict rests on in *this* model — the selection
// snapshot, the configured-project lookup, the notification guard, the preview
// fit, and the undo stack — plus the one entry whose execution is pure model
// mutation.

// captureCommandContext snapshots the current dashboard selection. It is safe
// for an empty list, for transiently invalid cursor values, and for a selected
// item that is not a session leaf — all three yield the zero Context, whose
// HasTarget is false, which is what every session command's availability gate
// reads. That is the whole fail-closed story for the palette: a synthetic row
// cannot become a command target because it never produces one.
func captureCommandContext(m Model) commands.Context {
	s, ok := m.selectedSession()
	if !ok {
		return commands.Context{}
	}
	return commands.Context{Target: s, HasTarget: true}
}

func sessionCommandAvailability(_ Model, ctx commands.Context) commands.Availability {
	return commands.SessionAvailability(ctx)
}

func unlockedSessionCommandAvailability(_ Model, ctx commands.Context) commands.Availability {
	return commands.UnlockedSessionAvailability(ctx)
}

func peekCommandAvailability(_ Model, ctx commands.Context) commands.Availability {
	return commands.PeekAvailability(ctx)
}

func configuredProjectCommandAvailability(m Model, ctx commands.Context) commands.Availability {
	return commands.ConfiguredProjectAvailability(m.cfg, ctx)
}

func clearStatusCommandAvailability(m Model, ctx commands.Context) commands.Availability {
	if availability := commands.SessionAvailability(ctx); !availability.Available {
		return availability
	}
	if dashboard.StateGroup(ctx.Target, m.lastActivity, m.idleThreshold) != 0 {
		return commands.Unavailable(commands.ReasonStatusNotClear)
	}
	return commands.Available()
}

func undoCommandAvailability(m Model, _ commands.Context) commands.Availability {
	if len(m.activeUndoEntries()) == 0 {
		return commands.Unavailable(commands.ReasonNothingToUndo)
	}
	return commands.Available()
}

// openLauncherCommand is pure model mutation, so it stays with the root rather
// than moving into the leaf alongside the vocabulary it is registered under.
func openLauncherCommand(m Model, _ commands.Context) (Model, tea.Cmd) {
	var sessions []config.Session
	if m.cfg != nil {
		sessions = m.cfg.Sessions
	}
	m.pickerSessions = launch.SortSessions(sessions)
	m.pickerRunning = launch.BuildRunning(m.sessions)
	m.pickerSelected = make(map[int]bool)
	m.pickerProfileOverride, m.pickerPinned = launch.LoadState(m.pickerSessions)
	m.pickerCursor = 0
	m.pickerFilter = ""
	m.pickerProfileFilter = ""
	m.pickerSearchMode = false
	m.pickerFilteredIdx = m.pickerFilteredIndices("")
	m.mode = ModePicker
	return m, m.refreshPreviewForPickerCursor()
}

// commandCatalog is the catalog every palette, footer, and dashboard key reads.
//
// The shortcut a palette row advertises is not written beside the command: it is
// read back out of the dashboard binding table, which is the thing that actually
// dispatches the key. A command nothing is bound to advertises no shortcut, and
// a rebinding moves the palette label with it, because there is no second
// spelling for either to drift from.
func commandCatalog() []commands.Command[Model] {
	bindings := dashboardBindings()
	catalog := commandDeclarations()
	for i := range catalog {
		catalog[i].Shortcut = bindings.Display(catalog[i].ID)
	}
	return catalog
}

func commandDeclarations() []commands.Command[Model] {
	return []commands.Command[Model]{
		{
			ID: commands.FocusSession, Group: commands.GroupSession,
			Title:        commands.StaticTitle[Model]("Focus Selected Session"),
			Description:  "Jump to the selected session's Kitty tab.",
			Aliases:      []string{"jump", "tab"},
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, commands.Focus(m.client, ctx.Target.TabID)
			},
		},
		{
			ID: commands.PeekScrollback, Group: commands.GroupSession,
			Title:        commands.StaticTitle[Model]("Peek Scrollback"),
			Description:  "Open the active session's scrollback in an overlay pager.",
			Aliases:      []string{"pager", "scrollback"},
			Availability: peekCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, commands.Peek(m.client, ctx.Target.WindowID)
			},
		},
		{
			ID: commands.RestartSession, Group: commands.GroupSession,
			Title:        commands.StaticTitle[Model]("Restart Session"),
			Description:  "Confirm before closing and relaunching the selected session.",
			Aliases:      []string{"relaunch"},
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
			Title:        commands.StaticTitle[Model]("Override Profile"),
			Description:  "Restart the selected session under a different harness profile.",
			Aliases:      []string{"account", "profile"},
			Availability: unlockedSessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.targetSession = ctx.Target
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = ctx.Target.EffectiveProfile()
				m.configPickerCursor = launch.AccountIndex(m.cfg.Accounts, ctx.Target.EffectiveProfile())
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
			Title:        commands.StaticTitle[Model]("Launch Another Session"),
			Description:  "Pick a profile and launch another session from the selected project.",
			Aliases:      []string{"duplicate", "new"},
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.targetSession = ctx.Target
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = ctx.Target.EffectiveProfile()
				m.configPickerCursor = launch.AccountIndex(m.cfg.Accounts, ctx.Target.EffectiveProfile())
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
			Title:        commands.StaticTitle[Model]("Clear Status Icon"),
			Description:  "Drop the attention, key, or stop badge from the selected tab.",
			Aliases:      []string{"attention", "badge", "icon"},
			Availability: clearStatusCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.targetSession = ctx.Target
				m.actionErr = nil
				return m, m.clearStatusCmd(ctx.Target)
			},
		},
		{
			ID: commands.CloseSession, Group: commands.GroupSession,
			Title:        commands.StaticTitle[Model]("Close Session"),
			Description:  "Exit the harness process while preserving its transcript.",
			Aliases:      []string{"exit", "kill"},
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
			Title:        commands.StaticTitle[Model]("Open Directory in Shell"),
			Description:  "Open a plain shell tab in the selected project's directory.",
			Aliases:      []string{"directory", "shell", "terminal"},
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
			Title:        commands.StaticTitle[Model]("Open in Editor"),
			Description:  "Open the selected project in the configured editor.",
			Aliases:      []string{"directory", "edit"},
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
			Title:        commands.StaticTitle[Model]("Copy Session Title"),
			Description:  "Copy the selected session's display title to the clipboard.",
			Aliases:      []string{"clipboard", "yank"},
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
			Description:  "Toggle protection from close, restart, and profile override.",
			Aliases:      []string{"protect", "unlock"},
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
			Description:  "Toggle macOS notifications for the selected session.",
			Aliases:      []string{"notifications", "unmute"},
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, commands.ToggleMute(m.client, ctx.Target)
			},
		},
		{
			ID: commands.SearchProject, Group: commands.GroupSession,
			Title:        commands.StaticTitle[Model]("Search This Project's Past Sessions"),
			Description:  "Search transcript history filtered to the selected project.",
			Aliases:      []string{"history", "project", "transcripts"},
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterSessionSearchForProject(ctx.Target.EffectiveProject())
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.ShowDetails, Group: commands.GroupSession,
			Title:        commands.StaticTitle[Model]("Show Session Details"),
			Description:  "Open the selected session's hook event log.",
			Aliases:      []string{"events", "hooks"},
			Availability: sessionCommandAvailability,
			Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterDetailMode(ctx.Target.WindowID, false)
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.ShowTimeline, Group: commands.GroupSession,
			Title:        commands.StaticTitle[Model]("Show Session Timeline"),
			Description:  "Open the selected session's events grouped into timeline phases.",
			Aliases:      []string{"events", "phases"},
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
			Aliases:     []string{"find", "search"},
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				cmd := m.enterFilter("")
				return m, cmd
			},
		},
		{
			ID: commands.CycleProfileFilter, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Cycle Profile Filter"),
			Description: "Cycle running sessions by effective profile and back to all profiles.",
			Aliases:     []string{"account", "filter", "running"},
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				return m, m.cycleDashboardProfile()
			},
		},
		{
			ID: commands.ClearFilters, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Clear Filters"),
			Description: "Remove every active dashboard filter chip.",
			Aliases:     []string{"chips", "reset"},
			Visible:     func(m Model, _ commands.Context) bool { return !m.activeFilter.IsEmpty() },
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				return m, m.applyFilterChange(dashboard.Filter{})
			},
		},
		{
			ID: commands.OpenLauncher, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Open Launcher"),
			Description: "Choose configured projects to launch.",
			Aliases:     []string{"launch", "projects"},
			Execute:     openLauncherCommand,
		},
		{
			ID: commands.SearchAllSessions, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Search All Past Sessions"),
			Description: "Search transcript history across every project and harness.",
			Aliases:     []string{"history", "transcripts"},
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterSessionSearch()
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.SearchPlans, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Search Plans"),
			Description: "Search indexed agent-session-loop plans.",
			Aliases:     []string{"agent plans", "history"},
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterPlanSearch()
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.ManageWorktrees, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Manage Worktrees"),
			Description: "Browse managed Git worktrees and open their paths.",
			Aliases:     []string{"git", "branches", "inventory"},
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterWorktrees()
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.ShowRuns, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Show Runs"),
			Description: "Review active and actionable agent-session-loop runs.",
			Aliases:     []string{"asl", "loop", "needs action"},
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterASLRuns()
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.ToggleInspector, Group: commands.GroupExplore,
			Title: func(m Model, _ commands.Context) string {
				if m.inspectorActive() {
					return "Hide Inspector"
				}
				return "Show Inspector"
			},
			Description: "Toggle the selected session's inspector pane.",
			Aliases:     []string{"hide", "pane", "scrollback", "preview", "output"},
			// No availability rule. The inspector has no size at which it must
			// refuse: too narrow for a split means full-screen rather than
			// nothing, so a dimmed palette row would be stating a limit that does
			// not exist.
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.toggleInspector()
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.SwitchProject, Group: commands.GroupExplore,
			// The title names the scope the list is in, not the one the command
			// would switch to, because the command switches to whichever scope the
			// operator picks — there is no single destination to name. The
			// no-selector case spells out "all projects" rather than "all" so the
			// palette row, the header selector, the switcher's own first row, and
			// the status bar all name that scope with the same words.
			Title: func(m Model, _ commands.Context) string {
				if applied := m.activeFilter.Project; applied != "" {
					return "Switch Project (" + applied + ")"
				}
				return "Switch Project (all projects)"
			},
			Description: "Narrow the dashboard to one project's sessions, or back to all of them.",
			Aliases:     []string{"projects", "scope", "sidebar", "rail"},
			// Available in both arrangements and at every width. The project
			// selector is a filter component, and the Projects view groups by
			// family rather than replacing the filter, so narrowing to one project
			// means the same thing on either side of `v`.
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.openProjectSwitcher()
				return updated.(Model), cmd
			},
		},
		{
			ID: commands.ToggleListView, Group: commands.GroupExplore,
			// The title names the arrangement the command switches TO, not the
			// one the list is in, so the palette entry reads as the action it
			// performs rather than as a status line.
			Title: func(m Model, _ commands.Context) string {
				if m.listView == dashboard.ViewProjects {
					return "Show Urgency View"
				}
				return "Show Projects View"
			},
			Description: "Switch the list arrangement between Urgency and Projects; this does not change the project filter.",
			Aliases:     []string{"projects", "families", "urgency", "group"},
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				cmd := m.toggleListView()
				return m, cmd
			},
		},

		{
			ID: commands.ResortTabs, Group: commands.GroupApp,
			Title:       commands.StaticTitle[Model]("Resort Tabs"),
			Description: "Reorder managed Kitty tabs to match the dashboard sort order.",
			Aliases:     []string{"reorder", "tabs"},
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
			Title:        commands.StaticTitle[Model]("Undo Last Close"),
			Description:  "Relaunch the most recently closed session before undo expires.",
			Aliases:      []string{"restore", "reopen"},
			Availability: undoCommandAvailability,
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				return m, m.undoLastCmd()
			},
		},
		{
			ID: commands.ShowHealth, Group: commands.GroupApp,
			Title:       commands.StaticTitle[Model]("Show Health Details"),
			Description: "Open the full daemon, hooks, session-freshness, and configuration reading.",
			Aliases:     []string{"daemon", "hooks", "diagnostics", "status"},
			// Always visible and always available. The reading it opens includes
			// the checks that have not run yet and the ones that are fine, so an
			// entry that hid itself when nothing was wrong would be missing in
			// exactly the two cases an operator opens it to distinguish.
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				// The palette has already returned the model to ModeList, so this
				// is the view esc lands on — the same return the keyboard help
				// sheet takes from here.
				m.openHealth(ModeList)
				return m, nil
			},
		},
		{
			ID: commands.DismissDeprecation, Group: commands.GroupApp,
			Title:       commands.StaticTitle[Model]("Dismiss Deprecation Warning"),
			Description: "Silence the configuration notice for this session.",
			Aliases:     []string{"banner", "warning"},
			// The gate is the issue model's, not the raw fields', so this entry
			// and the overlay's own `d` can only ever offer the same dismissal.
			// The banner it used to hide is gone: the notice now reads as the
			// header's health indicator and as one issue in the details overlay,
			// and both of those recompute from the issue this predicate asks
			// about.
			Visible: func(m Model, _ commands.Context) bool {
				issue, ok := dashboard.FindHealthIssue(healthIssues(m), dashboard.HealthIssueConfig)
				return ok && issue.Dismissible && issue.WantsAttention()
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
			Aliases:     []string{"help", "keys"},
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				m.openHelp(ModeList)
				return m, nil
			},
		},
		{
			ID: commands.Quit, Group: commands.GroupApp,
			Title:       commands.StaticTitle[Model]("Quit"),
			Description: "Exit the agentmux dashboard.",
			Aliases:     []string{"close app", "exit"},
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				return m, tea.Quit
			},
		},
	}
}
