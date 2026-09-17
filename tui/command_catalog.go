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
	"github.com/sglavoie/agentmux/tui/sessionactions"
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

// commandDeclarations is the catalog in palette order: every session action
// first, projected from the shared vocabulary in tui/sessionactions, then the
// explore and app commands, which act on the dashboard itself rather than on a
// row and so are declared here in full.
func commandDeclarations() []commands.Command[Model] {
	return append(sessionCommandDeclarations(), []commands.Command[Model]{
		{
			ID: commands.FilterSessions, Group: commands.GroupExplore,
			Title:       commands.StaticTitle[Model]("Filter Sessions"),
			Description: "Filter running sessions with the dashboard query language.",
			Aliases:     []string{"find", "search"},
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				// Prefilled with the filter already on screen, not emptied. The
				// chip ring is retired, and `/` is what replaced it: backspacing
				// one component off the end of the buffer is how a single chip is
				// removed from the keyboard now, so entering the editor had to stop
				// discarding the thing being edited. Esc still restores the saved
				// filter, so an entry-and-abort is unchanged.
				cmd := m.enterFilter(m.activeFilter.Raw)
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
			Description: "Browse all projects and harnesses by latest activity, with search filters cleared.",
			Aliases:     []string{"history", "transcripts"},
			Execute: func(m Model, _ commands.Context) (Model, tea.Cmd) {
				updated, cmd := m.enterAllSessionSearch()
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
				// Bound first: undoLastCmd claims the entry it relaunches, and
				// that claim has to be in the model this returns.
				cmd := m.undoLastCmd()
				return m, cmd
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
	}...)
}

// sessionCommandDeclarations projects the shared session-action vocabulary onto
// palette commands. Identity, group, title, description, aliases, and
// availability all come from tui/sessionactions, which is the same declaration
// the per-row actions overlay reads, so the palette cannot offer an action the
// overlay has retired or grant one the overlay refuses.
//
// A definition with no executor is dropped rather than offered as a row that
// would do nothing. That case is a programming error, not a reachable state,
// and TestPaletteOffersEverySessionDefinition fails on it.
func sessionCommandDeclarations() []commands.Command[Model] {
	executors := sessionCommandExecutors()
	definitions := sessionactions.Definitions()
	declarations := make([]commands.Command[Model], 0, len(definitions))
	for _, definition := range definitions {
		execute, ok := executors[definition.ID]
		if !ok {
			continue
		}
		declarations = append(declarations, commands.Command[Model]{
			ID:          definition.ID,
			Group:       sessionactions.PaletteGroup,
			Title:       func(_ Model, ctx commands.Context) string { return definition.Title(sessionactions.FromContext(ctx)) },
			Description: definition.PaletteDescription,
			Aliases:     definition.PaletteAliases,
			Availability: func(m Model, ctx commands.Context) commands.Availability {
				return definition.AvailabilityFor(sessionActionEnv(m), sessionactions.FromContext(ctx))
			},
			Execute: execute,
		})
	}
	return declarations
}

// sessionCommandExecutors is what a session palette entry DOES, keyed by the
// shared identity.
//
// Every entry is the same call the per-row menu makes: hand the identity and the
// context's captured target to the shared controller. Acting on the capture
// rather than on the cursor is unchanged; what has gone is this file's own
// answer to whether a restart confirms, which disagreed with the overlay's, and
// its own copy of the picker and confirmation bookkeeping.
func sessionCommandExecutors() map[commands.ID]func(Model, commands.Context) (Model, tea.Cmd) {
	effects := sessionActionRunners()
	executors := make(map[commands.ID]func(Model, commands.Context) (Model, tea.Cmd), len(effects))
	for _, definition := range sessionactions.Definitions() {
		if _, ok := effects[definition.ID]; !ok {
			continue
		}
		action := definition.ID
		executors[action] = func(m Model, ctx commands.Context) (Model, tea.Cmd) {
			return m.runSessionAction(action, sessionactions.FromContext(ctx))
		}
	}
	return executors
}
