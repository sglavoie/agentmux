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
	want := []expectedCommand{
		{commands.FocusSession, commands.GroupSession, "Focus Selected Session", []string{"jump", "tab"}, "enter / space"},
		{commands.PeekScrollback, commands.GroupSession, "Peek Scrollback", []string{"pager", "scrollback"}, "e"},
		{commands.RestartSession, commands.GroupSession, "Restart Session", []string{"relaunch"}, "r"},
		{commands.OverrideProfile, commands.GroupSession, "Override Profile", []string{"account", "profile"}, "c"},
		{commands.LaunchAnother, commands.GroupSession, "Launch Another Session", []string{"duplicate", "new"}, "C"},
		{commands.ClearStatusIcon, commands.GroupSession, "Clear Status Icon", []string{"attention", "badge", "icon"}, "x"},
		{commands.CloseSession, commands.GroupSession, "Close Session", []string{"exit", "kill"}, "X"},
		{commands.OpenDirectoryID, commands.GroupSession, "Open Directory in Shell", []string{"directory", "shell", "terminal"}, "o"},
		{commands.OpenEditorID, commands.GroupSession, "Open in Editor", []string{"directory", "edit"}, "O"},
		{commands.CopyTitleID, commands.GroupSession, "Copy Session Title", []string{"clipboard", "yank"}, "y"},
		{commands.ToggleLockID, commands.GroupSession, "Lock Session", []string{"protect", "unlock"}, "L"},
		{commands.ToggleMuteID, commands.GroupSession, "Mute Session", []string{"notifications", "unmute"}, "m"},
		{commands.SearchProject, commands.GroupSession, "Search This Project's Past Sessions", []string{"history", "project", "transcripts"}, "s"},
		{commands.ShowDetails, commands.GroupSession, "Show Session Details", []string{"events", "hooks"}, "d"},
		{commands.ShowTimeline, commands.GroupSession, "Show Session Timeline", []string{"events", "phases"}, "T"},
		{commands.FilterSessions, commands.GroupExplore, "Filter Sessions", []string{"find", "search"}, "/"},
		{commands.CycleProfileFilter, commands.GroupExplore, "Cycle Profile Filter", []string{"account", "filter", "running"}, "f"},
		{commands.ClearFilters, commands.GroupExplore, "Clear Filters", []string{"chips", "reset"}, "esc"},
		{commands.OpenLauncher, commands.GroupExplore, "Open Launcher", []string{"launch", "projects"}, "l"},
		{commands.SearchAllSessions, commands.GroupExplore, "Search All Past Sessions", []string{"history", "transcripts"}, "S"},
		{commands.SearchPlans, commands.GroupExplore, "Search Plans", []string{"agent plans", "history"}, "P"},
		{commands.ManageWorktrees, commands.GroupExplore, "Manage Worktrees", []string{"git", "branches", "inventory"}, "W"},
		{commands.ShowRuns, commands.GroupExplore, "Show Runs", []string{"asl", "loop", "needs action"}, "A"},
		{commands.TogglePreview, commands.GroupExplore, "Show Preview", []string{"hide", "pane", "scrollback"}, "p"},
		{commands.ToggleListView, commands.GroupExplore, "Group by Project", []string{"projects", "families", "urgency", "group"}, "v"},
		{commands.ResortTabs, commands.GroupApp, "Resort Tabs", []string{"reorder", "tabs"}, "R"},
		{commands.UndoClose, commands.GroupApp, "Undo Last Close", []string{"restore", "reopen"}, "u"},
		{commands.DismissDeprecation, commands.GroupApp, "Dismiss Deprecation Warning", []string{"banner", "warning"}, "d"},
		{commands.ShowKeyboardHelp, commands.GroupApp, "Show Keyboard Shortcuts", []string{"help", "keys"}, "?"},
		{commands.Quit, commands.GroupApp, "Quit", []string{"close app", "exit"}, "q / ctrl+c"},
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
	wantGroups := []commands.Group{commands.GroupSession, commands.GroupExplore, commands.GroupApp}
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

func TestCommandCatalogDashboardShortcutBindings(t *testing.T) {
	want := map[commands.ID]string{
		commands.FocusSession:       keyEnter + " / " + keySpace,
		commands.PeekScrollback:     keyE,
		commands.RestartSession:     keyR,
		commands.OverrideProfile:    keyC,
		commands.LaunchAnother:      keyDashboardLaunch,
		commands.ClearStatusIcon:    keyClearStatus,
		commands.CloseSession:       keyClose,
		commands.OpenDirectoryID:    keyO,
		commands.OpenEditorID:       keyOpenEditor,
		commands.CopyTitleID:        keyY,
		commands.ToggleLockID:       keyLock,
		commands.ToggleMuteID:       keyM,
		commands.SearchProject:      keyProjectSessionSearch,
		commands.ShowDetails:        keyD,
		commands.ShowTimeline:       keyOpenTimeline,
		commands.FilterSessions:     keySlash,
		commands.CycleProfileFilter: keyF,
		commands.ClearFilters:       keyEsc,
		commands.OpenLauncher:       keyL,
		commands.SearchAllSessions:  keySessionSearch,
		commands.SearchPlans:        keyPlanSearch,
		commands.ManageWorktrees:    keyWorktrees,
		commands.ShowRuns:           keyASLRuns,
		commands.TogglePreview:      keyTogglePreview,
		commands.ToggleListView:     keyToggleView,
		commands.ResortTabs:         keyResort,
		commands.UndoClose:          keyU,
		commands.DismissDeprecation: keyD,
		commands.ShowKeyboardHelp:   keyQuestion,
		commands.Quit:               keyQ + " / " + keyCtrlC,
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

	allowedShared := map[string][]commands.ID{
		keyD: {commands.ShowDetails, commands.DismissDeprecation},
	}
	for shortcut, ids := range byShortcut {
		if len(ids) < 2 {
			continue
		}
		if !reflect.DeepEqual(ids, allowedShared[shortcut]) {
			t.Errorf("shortcut %q is unexpectedly shared by %v", shortcut, ids)
		}
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
		{commands.TogglePreview, func(m *Model, _ *commands.Context) { m.previewEnabled = true }, "Hide Preview"},
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
		for _, candidate := range commandCatalog()[:15] {
			availability := candidate.AvailabilityFor(m, ctx)
			if availability.Available || availability.Reason != commands.ReasonNoSession {
				t.Errorf("%q availability = %+v, want disabled with %q", candidate.ID, availability, commands.ReasonNoSession)
			}
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

	t.Run("narrow preview can still be hidden", func(t *testing.T) {
		m, ctx, _ := commandTestModel(base)
		m.width = 80
		preview := commandByID(t, commands.TogglePreview)
		if got := preview.AvailabilityFor(m, ctx); got != commands.Unavailable(commands.ReasonPreviewTooSmall) {
			t.Fatalf("narrow preview availability = %+v", got)
		}
		m.previewEnabled = true
		if got := preview.AvailabilityFor(m, ctx); got != commands.Available() {
			t.Fatalf("enabled narrow preview availability = %+v", got)
		}
	})

	t.Run("undo requires a live deadline", func(t *testing.T) {
		m, ctx, _ := commandTestModel(base)
		undo := commandByID(t, commands.UndoClose)
		m.pendingUndo = []undoEntry{{deadline: time.Now().Add(-time.Second)}}
		if got := undo.AvailabilityFor(m, ctx); got != commands.Unavailable(commands.ReasonNothingToUndo) {
			t.Fatalf("expired undo availability = %+v", got)
		}
		m.pendingUndo = []undoEntry{{deadline: time.Now().Add(time.Minute)}}
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
	if visible != 28 {
		t.Fatalf("clean catalog has %d visible commands, want 28", visible)
	}
}
