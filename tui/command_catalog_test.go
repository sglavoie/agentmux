package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

func commandByID(t *testing.T, id commands.ID) commands.Command[Model] {
	t.Helper()
	for _, candidate := range commandCatalog() {
		if candidate.ID == id {
			return candidate
		}
	}
	t.Fatalf("command %q not found", id)
	return commands.Command[Model]{}
}

func commandTestModel(target session.RunningSession) (Model, commands.Context, *uitestKittyClient) {
	client := newUITestKittyClient(nil)
	m := testModelWithSessions()
	m.client = client
	m.width = 160
	m.height = 50
	m.cfg.Defaults.Preview = config.PreviewConfig{
		Direction: "horizontal",
		Ratio:     0.5,
		RefreshMs: 500,
		MinCols:   100,
		MinRows:   30,
	}
	m = loadSessions(m, []session.RunningSession{target})
	return m, captureCommandContext(m), client
}

func TestCommandCatalogStableInventory(t *testing.T) {
	type expectedCommand struct {
		id       commands.ID
		group    commands.Group
		title    string
		aliases  []string
		shortcut string
	}
	// The order is the palette's reading order, and the first five entries are
	// the destinations: a palette opened on a cold dashboard is most often a way
	// to go somewhere, and every session command below them needs a selected row
	// to mean anything at all.
	want := []expectedCommand{
		{commands.OpenLauncher, commands.GroupNavigate, "Open Launcher", []string{"launch", "projects"}, "l"},
		{commands.SearchAllSessions, commands.GroupNavigate, "Search All Past Sessions", []string{"history", "transcripts"}, keyAllSessions},
		{commands.SearchPlans, commands.GroupNavigate, "Search Plans", []string{"agent plans", "history"}, ""},
		{commands.ManageWorktrees, commands.GroupNavigate, "Manage Worktrees", []string{"git", "branches", "inventory"}, ""},
		{commands.ShowRuns, commands.GroupNavigate, "Show Runs", []string{"asl", "loop", "needs action"}, ""},
		{commands.FilterSessions, commands.GroupNavigate, "Filter Sessions", []string{"find", "search"}, "/"},
		{commands.CycleProfileFilter, commands.GroupNavigate, "Cycle Profile Filter", []string{"account", "filter", "running"}, ""},
		{commands.ClearFilters, commands.GroupNavigate, "Clear Filters", []string{"chips", "reset"}, "esc"},
		{commands.ToggleInspector, commands.GroupNavigate, "Hide Inspector", []string{"hide", "pane", "scrollback", "preview", "output"}, "i"},
		// "all projects" rather than a project name: the catalog is read against a
		// model with no project selector applied, and the title names the scope the
		// dashboard is currently showing.
		{commands.SwitchProject, commands.GroupNavigate, "Switch Project (all projects)", []string{"projects", "scope", "sidebar", "rail"}, "p"},
		{commands.ToggleListView, commands.GroupNavigate, "Show Projects View", []string{"projects", "families", "urgency", "group"}, ""},
		{commands.FocusSession, commands.GroupSession, "Focus Selected Session", []string{"jump", "tab"}, "enter"},
		{commands.PeekScrollback, commands.GroupSession, "Peek Scrollback", []string{"pager", "scrollback"}, ""},
		{commands.RestartSession, commands.GroupSession, "Restart Session", []string{"relaunch"}, ""},
		{commands.OverrideProfile, commands.GroupSession, "Override Profile", []string{"account", "profile"}, ""},
		{commands.LaunchAnother, commands.GroupSession, "Launch Another Session", []string{"duplicate", "new"}, ""},
		{commands.ClearStatusIcon, commands.GroupSession, "Clear Status Icon", []string{"attention", "badge", "icon"}, ""},
		{commands.MarkActiveIcon, commands.GroupSession, "Mark Session Active", []string{"bolt", "busy", "working"}, ""},
		{commands.CloseSession, commands.GroupSession, "Close Session", []string{"exit", "kill"}, ""},
		{commands.OpenDirectoryID, commands.GroupSession, "Open Directory in Shell", []string{"directory", "shell", "terminal"}, ""},
		{commands.OpenEditorID, commands.GroupSession, "Open in Editor", []string{"directory", "edit"}, ""},
		{commands.CopyTitleID, commands.GroupSession, "Copy Session Title", []string{"clipboard", "yank"}, ""},
		{commands.ToggleLockID, commands.GroupSession, "Lock Session", []string{"protect", "unlock"}, ""},
		{commands.ToggleMuteID, commands.GroupSession, "Mute Session", []string{"notifications", "unmute"}, ""},
		{commands.SearchProject, commands.GroupSession, "Search This Project's Past Sessions", []string{"history", "project", "transcripts"}, keyProjectSessionSearch},
		{commands.ShowDetails, commands.GroupSession, "Show Session Details", []string{"events", "hooks"}, ""},
		{commands.ShowTimeline, commands.GroupSession, "Show Session Timeline", []string{"events", "phases"}, ""},
		{commands.ResortTabs, commands.GroupApplication, "Resort Tabs", []string{"reorder", "tabs"}, ""},
		{commands.UndoClose, commands.GroupApplication, "Undo Last Close", []string{"restore", "reopen"}, keyUndoClose},
		{commands.ShowHealth, commands.GroupApplication, "Show Health Details", []string{"daemon", "hooks", "diagnostics", "status"}, ""},
		{commands.DismissDeprecation, commands.GroupApplication, "Dismiss Deprecation Warning", []string{"banner", "warning"}, ""},
		{commands.ShowKeyboardHelp, commands.GroupApplication, "Show Keyboard Shortcuts", []string{"help", "keys"}, "?"},
		{commands.Quit, commands.GroupApplication, "Quit", []string{"close app", "exit"}, "q / ctrl+c"},
	}

	m, ctx, _ := commandTestModel(session.RunningSession{
		TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work", State: "bolt",
	})
	got := commandCatalog()
	if len(got) != len(want) {
		t.Fatalf("catalog has %d commands, want %d", len(got), len(want))
	}
	seen := make(map[commands.ID]bool, len(got))
	for i, candidate := range got {
		expected := want[i]
		if candidate.ID != expected.id || candidate.Group != expected.group {
			t.Errorf("command[%d] identity = (%q, %q), want (%q, %q)", i, candidate.ID, candidate.Group, expected.id, expected.group)
		}
		if title := candidate.DisplayTitle(m, ctx); title != expected.title {
			t.Errorf("command[%d] title = %q, want %q", i, title, expected.title)
		}
		if !reflect.DeepEqual(candidate.Aliases, expected.aliases) {
			t.Errorf("command[%d] aliases = %v, want %v", i, candidate.Aliases, expected.aliases)
		}
		if candidate.Shortcut != expected.shortcut {
			t.Errorf("command[%d] shortcut = %q, want %q", i, candidate.Shortcut, expected.shortcut)
		}
		if strings.TrimSpace(candidate.Description) == "" {
			t.Errorf("command[%d] %q has no description", i, candidate.ID)
		}
		if candidate.Execute == nil {
			t.Errorf("command[%d] %q has no execution function", i, candidate.ID)
		}
		if seen[candidate.ID] {
			t.Errorf("duplicate command ID %q", candidate.ID)
		}
		seen[candidate.ID] = true
	}

	// Group contiguity is part of stable catalog order, not a renderer concern.
	wantGroups := []commands.Group{commands.GroupNavigate, commands.GroupSession, commands.GroupApplication}
	var gotGroups []commands.Group
	for _, candidate := range got {
		if len(gotGroups) == 0 || gotGroups[len(gotGroups)-1] != candidate.Group {
			gotGroups = append(gotGroups, candidate.Group)
		}
	}
	if !reflect.DeepEqual(gotGroups, wantGroups) {
		t.Fatalf("catalog group order = %v, want %v", gotGroups, wantGroups)
	}
}

// TestCommandCatalogDashboardShortcutBindings is where the small keymap is
// stated as data. Every command keeps its palette entry; ten of them
// still carry a dashboard key, and the rest render with no shortcut column at
// all rather than with a key that would do nothing if pressed.
//
// The map is exhaustive in both directions on purpose. A command missing from
// it fails, and an entry naming a command the catalog no longer has fails too,
// so "the palette advertises exactly the keys the dashboard dispatches" is
// checked rather than assumed — Shortcut is read from dashboardBindings, so a
// key retired there must show up here as an empty string.
func TestCommandCatalogDashboardShortcutBindings(t *testing.T) {
	const noDashboardKey = ""
	want := map[commands.ID]string{
		commands.FocusSession:      keyEnter,
		commands.SearchProject:     keyProjectSessionSearch,
		commands.FilterSessions:    keySlash,
		commands.ClearFilters:      keyEsc,
		commands.OpenLauncher:      keyL,
		commands.SearchAllSessions: keyAllSessions,
		commands.ToggleInspector:   keyInspector,
		commands.SwitchProject:     keyProjectSwitcher,
		commands.ShowKeyboardHelp:  keyQuestion,
		commands.Quit:              keyQ + " / " + keyCtrlC,

		// Everything below is palette-and-actions-only. None of these lost its
		// command; each lost the direct dashboard shortcut that used to duplicate
		// it, which is what the replacement keymap is.
		commands.PeekScrollback:     noDashboardKey,
		commands.RestartSession:     noDashboardKey,
		commands.OverrideProfile:    noDashboardKey,
		commands.LaunchAnother:      noDashboardKey,
		commands.ClearStatusIcon:    noDashboardKey,
		commands.MarkActiveIcon:     noDashboardKey,
		commands.CloseSession:       noDashboardKey,
		commands.OpenDirectoryID:    noDashboardKey,
		commands.OpenEditorID:       noDashboardKey,
		commands.CopyTitleID:        noDashboardKey,
		commands.ToggleLockID:       noDashboardKey,
		commands.ToggleMuteID:       noDashboardKey,
		commands.ShowDetails:        noDashboardKey,
		commands.ShowTimeline:       noDashboardKey,
		commands.CycleProfileFilter: noDashboardKey,
		commands.SearchPlans:        noDashboardKey,
		commands.ManageWorktrees:    noDashboardKey,
		commands.ShowRuns:           noDashboardKey,
		commands.ToggleListView:     noDashboardKey,
		commands.ResortTabs:         noDashboardKey,
		commands.UndoClose:          keyUndoClose,
		commands.ShowHealth:         noDashboardKey,
		commands.DismissDeprecation: noDashboardKey,
	}

	catalog := commandCatalog()
	if len(catalog) != len(want) {
		t.Fatalf("catalog has %d commands, shortcut map has %d", len(catalog), len(want))
	}

	seen := make(map[commands.ID]bool, len(catalog))
	byShortcut := make(map[string][]commands.ID, len(catalog))
	for _, candidate := range catalog {
		shortcut, ok := want[candidate.ID]
		if !ok {
			t.Errorf("command %q has no dashboard shortcut mapping", candidate.ID)
			continue
		}
		seen[candidate.ID] = true
		if candidate.Shortcut != shortcut {
			t.Errorf("command %q shortcut = %q, want dashboard binding %q", candidate.ID, candidate.Shortcut, shortcut)
		}
		byShortcut[candidate.Shortcut] = append(byShortcut[candidate.Shortcut], candidate.ID)
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("dashboard shortcut mapping contains missing command %q", id)
		}
	}

	// No dashboard key is shared any more. The one exemption that used to live
	// here — `d` for both ShowDetails and DismissDeprecation — is gone with both
	// keys, so an overload is now a plain failure rather than a listed
	// exception. The empty shortcut is skipped: "no key" is shared by every
	// palette-only command by construction and is not an overload.
	for shortcut, ids := range byShortcut {
		if shortcut == "" || len(ids) < 2 {
			continue
		}
		t.Errorf("dashboard shortcut %q is shared by %v", shortcut, ids)
	}
}

func TestCommandCatalogDynamicTitles(t *testing.T) {
	m, ctx, _ := commandTestModel(session.RunningSession{
		TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work",
	})
	cases := []struct {
		id   commands.ID
		edit func(*Model, *commands.Context)
		want string
	}{
		{commands.ToggleLockID, func(_ *Model, ctx *commands.Context) { ctx.Target.Locked = true }, "Unlock Session"},
		{commands.ToggleMuteID, func(_ *Model, ctx *commands.Context) { ctx.Target.Muted = true }, "Unmute Session"},
		// The inspector reads the other way round from the two above it: its
		// resting state is ON, so the edit that changes the title is the hide.
		{commands.ToggleInspector, func(m *Model, _ *commands.Context) { m.inspectorDisplay = inspectorHidden }, "Show Inspector"},
	}
	for _, tc := range cases {
		t.Run(string(tc.id), func(t *testing.T) {
			gotModel, gotCtx := m, ctx
			tc.edit(&gotModel, &gotCtx)
			if got := commandByID(t, tc.id).DisplayTitle(gotModel, gotCtx); got != tc.want {
				t.Fatalf("title = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCommandCatalogAvailability(t *testing.T) {
	t.Run("empty list disables every session command", func(t *testing.T) {
		m := testModelWithSessions()
		ctx := captureCommandContext(m)
		// Selected by group rather than by a leading slice: the session block no
		// longer opens the catalog, and a fixed index would have kept passing
		// against whatever moved into it.
		checked := 0
		for _, candidate := range commandCatalog() {
			if candidate.Group != commands.GroupSession {
				continue
			}
			checked++
			availability := candidate.AvailabilityFor(m, ctx)
			if availability.Available || availability.Reason != commands.ReasonNoSession {
				t.Errorf("%q availability = %+v, want disabled with %q", candidate.ID, availability, commands.ReasonNoSession)
			}
		}
		if checked == 0 {
			t.Fatal("no session-group command was checked")
		}
	})

	type availabilityCase struct {
		name   string
		id     commands.ID
		target session.RunningSession
		setup  func(*Model)
		want   commands.Availability
	}
	base := session.RunningSession{TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work"}
	cases := []availabilityCase{
		{"peek active", commands.PeekScrollback, withCommandState(base, "bolt"), nil, commands.Available()},
		{"peek idle", commands.PeekScrollback, base, nil, commands.Unavailable(commands.ReasonPeekInactive)},
		{"restart locked", commands.RestartSession, withCommandLock(base), nil, commands.Unavailable(commands.ReasonLocked)},
		{"override locked", commands.OverrideProfile, withCommandLock(base), nil, commands.Unavailable(commands.ReasonLocked)},
		{"close locked", commands.CloseSession, withCommandLock(base), nil, commands.Unavailable(commands.ReasonLocked)},
		{"clear attention", commands.ClearStatusIcon, withCommandState(base, "key"), nil, commands.Available()},
		{"clear active", commands.ClearStatusIcon, withCommandState(base, "bolt"), nil, commands.Unavailable(commands.ReasonStatusNotClear)},
		{"clear idle", commands.ClearStatusIcon, base, func(m *Model) {
			m.idleThreshold = time.Second
			m.lastActivity = map[int]time.Time{101: time.Now().Add(-time.Minute)}
		}, commands.Unavailable(commands.ReasonStatusNotClear)},
		{"configured directory", commands.OpenDirectoryID, base, nil, commands.Available()},
		{"configured editor", commands.OpenEditorID, base, nil, commands.Available()},
		{"orphan directory", commands.OpenDirectoryID, withCommandProject(base, "orphan"), nil, commands.Unavailable(commands.ReasonNoProject)},
		{"orphan editor", commands.OpenEditorID, withCommandProject(base, "orphan"), nil, commands.Unavailable(commands.ReasonNoProject)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ctx, _ := commandTestModel(tc.target)
			if tc.setup != nil {
				tc.setup(&m)
			}
			if got := commandByID(t, tc.id).AvailabilityFor(m, ctx); got != tc.want {
				t.Fatalf("availability = %+v, want %+v", got, tc.want)
			}
		})
	}

	// The inspector never refuses, at any size. Where the preview it replaced
	// reported "terminal is too small", the inspector answers a terminal too
	// narrow for a split by taking the whole body instead — so there is no
	// geometry in which the palette should draw this row as unavailable, and the
	// binding is declared GateUnchecked for the same reason.
	t.Run("the inspector is available at every width", func(t *testing.T) {
		m, ctx, _ := commandTestModel(base)
		inspector := commandByID(t, commands.ToggleInspector)
		for _, width := range []int{20, 80, inspectorMinQueueCols + inspectorDividerCols + inspectorMinCols, 160} {
			m.width = width
			if got := inspector.AvailabilityFor(m, ctx); got != commands.Available() {
				t.Fatalf("inspector availability at width %d = %+v", width, got)
			}
		}
	})

	t.Run("undo requires a live deadline", func(t *testing.T) {
		m, ctx, _ := commandTestModel(base)
		undo := commandByID(t, commands.UndoClose)
		expired := undoTestNow.Add(undoWindow + time.Second)
		m.now = func() time.Time { return expired }
		m.recovery = recoveryWith(undoTestNow, "api")
		if got := undo.AvailabilityFor(m, ctx); got != commands.Unavailable(commands.ReasonNothingToUndo) {
			t.Fatalf("expired undo availability = %+v", got)
		}
		m.now = func() time.Time { return undoTestNow }
		if got := undo.AvailabilityFor(m, ctx); got != commands.Available() {
			t.Fatalf("live undo availability = %+v", got)
		}
	})
}

func TestCommandCatalogConditionalVisibility(t *testing.T) {
	target := session.RunningSession{TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work"}
	m, ctx, _ := commandTestModel(target)
	clear := commandByID(t, commands.ClearFilters)
	dismiss := commandByID(t, commands.DismissDeprecation)

	if clear.IsVisible(m, ctx) {
		t.Fatal("clear filters should be hidden without an active filter")
	}
	m.activeFilter = dashboard.Parse("api")
	if !clear.IsVisible(m, ctx) {
		t.Fatal("clear filters should be visible with an active filter")
	}
	if dismiss.IsVisible(m, ctx) {
		t.Fatal("dismiss warning should be hidden without a banner")
	}
	m.deprecations = []config.Deprecation{{Key: "old", Replacement: "new"}}
	if !dismiss.IsVisible(m, ctx) {
		t.Fatal("dismiss warning should be visible with a banner")
	}
	m.deprecationDismissed = true
	if dismiss.IsVisible(m, ctx) {
		t.Fatal("dismiss warning should be hidden after dismissal")
	}

	visible := 0
	clean, cleanCtx, _ := commandTestModel(target)
	for _, candidate := range commandCatalog() {
		if candidate.IsVisible(clean, cleanCtx) {
			visible++
		}
	}
	if visible != 31 {
		t.Fatalf("clean catalog has %d visible commands, want 31", visible)
	}
}
