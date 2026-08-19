package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

func commandByID(t *testing.T, id commandID) command {
	t.Helper()
	for _, candidate := range commandCatalog() {
		if candidate.id == id {
			return candidate
		}
	}
	t.Fatalf("command %q not found", id)
	return command{}
}

func commandTestModel(target session.RunningSession) (Model, commandContext, *UITestKittyClient) {
	client := NewUITestKittyClient(nil)
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
		id       commandID
		group    commandGroup
		title    string
		aliases  []string
		shortcut string
	}
	want := []expectedCommand{
		{commandFocusSession, commandGroupSession, "Focus Selected Session", []string{"jump", "tab"}, "enter / space"},
		{commandPeekScrollback, commandGroupSession, "Peek Scrollback", []string{"pager", "scrollback"}, "e"},
		{commandRestartSession, commandGroupSession, "Restart Session", []string{"relaunch"}, "r"},
		{commandOverrideProfile, commandGroupSession, "Override Profile", []string{"account", "profile"}, "c"},
		{commandLaunchAnother, commandGroupSession, "Launch Another Session", []string{"duplicate", "new"}, "C"},
		{commandClearStatus, commandGroupSession, "Clear Status Icon", []string{"attention", "badge", "icon"}, "x"},
		{commandCloseSession, commandGroupSession, "Close Session", []string{"exit", "kill"}, "X"},
		{commandOpenDirectory, commandGroupSession, "Open Directory in Shell", []string{"directory", "shell", "terminal"}, "o"},
		{commandOpenEditor, commandGroupSession, "Open in Editor", []string{"directory", "edit"}, "O"},
		{commandCopyTitle, commandGroupSession, "Copy Session Title", []string{"clipboard", "yank"}, "y"},
		{commandToggleLock, commandGroupSession, "Lock Session", []string{"protect", "unlock"}, "L"},
		{commandToggleMute, commandGroupSession, "Mute Session", []string{"notifications", "unmute"}, "m"},
		{commandSearchProject, commandGroupSession, "Search This Project's Past Sessions", []string{"history", "project", "transcripts"}, "s"},
		{commandShowDetails, commandGroupSession, "Show Session Details", []string{"events", "hooks"}, "d"},
		{commandShowTimeline, commandGroupSession, "Show Session Timeline", []string{"events", "phases"}, "T"},
		{commandFilterSessions, commandGroupExplore, "Filter Sessions", []string{"find", "search"}, "/"},
		{commandCycleProfileFilter, commandGroupExplore, "Cycle Profile Filter", []string{"account", "filter", "running"}, "f"},
		{commandClearFilters, commandGroupExplore, "Clear Filters", []string{"chips", "reset"}, "esc"},
		{commandOpenLauncher, commandGroupExplore, "Open Launcher", []string{"launch", "projects"}, "l"},
		{commandSearchAllSessions, commandGroupExplore, "Search All Past Sessions", []string{"history", "transcripts"}, "S"},
		{commandSearchPlans, commandGroupExplore, "Search Plans", []string{"agent plans", "history"}, "P"},
		{commandManageWorktrees, commandGroupExplore, "Manage Worktrees", []string{"git", "branches", "inventory"}, "W"},
		{commandTogglePreview, commandGroupExplore, "Show Preview", []string{"hide", "pane", "scrollback"}, "p"},
		{commandResortTabs, commandGroupApp, "Resort Tabs", []string{"reorder", "tabs"}, "R"},
		{commandUndoClose, commandGroupApp, "Undo Last Close", []string{"restore", "reopen"}, "u"},
		{commandDismissDeprecation, commandGroupApp, "Dismiss Deprecation Warning", []string{"banner", "warning"}, "d"},
		{commandShowKeyboardHelp, commandGroupApp, "Show Keyboard Shortcuts", []string{"help", "keys"}, "?"},
		{commandQuit, commandGroupApp, "Quit", []string{"close app", "exit"}, "q / ctrl+c"},
	}

	m, ctx, _ := commandTestModel(session.RunningSession{
		TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work", State: "bolt",
	})
	got := commandCatalog()
	if len(got) != len(want) {
		t.Fatalf("catalog has %d commands, want %d", len(got), len(want))
	}
	seen := make(map[commandID]bool, len(got))
	for i, candidate := range got {
		expected := want[i]
		if candidate.id != expected.id || candidate.group != expected.group {
			t.Errorf("command[%d] identity = (%q, %q), want (%q, %q)", i, candidate.id, candidate.group, expected.id, expected.group)
		}
		if title := candidate.displayTitle(m, ctx); title != expected.title {
			t.Errorf("command[%d] title = %q, want %q", i, title, expected.title)
		}
		if !reflect.DeepEqual(candidate.aliases, expected.aliases) {
			t.Errorf("command[%d] aliases = %v, want %v", i, candidate.aliases, expected.aliases)
		}
		if candidate.shortcut != expected.shortcut {
			t.Errorf("command[%d] shortcut = %q, want %q", i, candidate.shortcut, expected.shortcut)
		}
		if strings.TrimSpace(candidate.description) == "" {
			t.Errorf("command[%d] %q has no description", i, candidate.id)
		}
		if candidate.execute == nil {
			t.Errorf("command[%d] %q has no execution function", i, candidate.id)
		}
		if seen[candidate.id] {
			t.Errorf("duplicate command ID %q", candidate.id)
		}
		seen[candidate.id] = true
	}

	// Group contiguity is part of stable catalog order, not a renderer concern.
	wantGroups := []commandGroup{commandGroupSession, commandGroupExplore, commandGroupApp}
	var gotGroups []commandGroup
	for _, candidate := range got {
		if len(gotGroups) == 0 || gotGroups[len(gotGroups)-1] != candidate.group {
			gotGroups = append(gotGroups, candidate.group)
		}
	}
	if !reflect.DeepEqual(gotGroups, wantGroups) {
		t.Fatalf("catalog group order = %v, want %v", gotGroups, wantGroups)
	}
}

func TestCommandCatalogDashboardShortcutBindings(t *testing.T) {
	want := map[commandID]string{
		commandFocusSession:       keyEnter + " / " + keySpace,
		commandPeekScrollback:     keyE,
		commandRestartSession:     keyR,
		commandOverrideProfile:    keyC,
		commandLaunchAnother:      keyDashboardLaunch,
		commandClearStatus:        keyClearStatus,
		commandCloseSession:       keyClose,
		commandOpenDirectory:      keyO,
		commandOpenEditor:         keyOpenEditor,
		commandCopyTitle:          keyY,
		commandToggleLock:         keyLock,
		commandToggleMute:         keyM,
		commandSearchProject:      keyProjectSessionSearch,
		commandShowDetails:        keyD,
		commandShowTimeline:       keyOpenTimeline,
		commandFilterSessions:     keySlash,
		commandCycleProfileFilter: keyF,
		commandClearFilters:       keyEsc,
		commandOpenLauncher:       keyL,
		commandSearchAllSessions:  keySessionSearch,
		commandSearchPlans:        keyPlanSearch,
		commandManageWorktrees:    keyWorktrees,
		commandTogglePreview:      keyTogglePreview,
		commandResortTabs:         keyResort,
		commandUndoClose:          keyU,
		commandDismissDeprecation: keyD,
		commandShowKeyboardHelp:   keyQuestion,
		commandQuit:               keyQ + " / " + keyCtrlC,
	}

	catalog := commandCatalog()
	if len(catalog) != len(want) {
		t.Fatalf("catalog has %d commands, shortcut map has %d", len(catalog), len(want))
	}

	seen := make(map[commandID]bool, len(catalog))
	byShortcut := make(map[string][]commandID, len(catalog))
	for _, candidate := range catalog {
		shortcut, ok := want[candidate.id]
		if !ok {
			t.Errorf("command %q has no dashboard shortcut mapping", candidate.id)
			continue
		}
		seen[candidate.id] = true
		if candidate.shortcut != shortcut {
			t.Errorf("command %q shortcut = %q, want dashboard binding %q", candidate.id, candidate.shortcut, shortcut)
		}
		byShortcut[candidate.shortcut] = append(byShortcut[candidate.shortcut], candidate.id)
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("dashboard shortcut mapping contains missing command %q", id)
		}
	}

	allowedShared := map[string][]commandID{
		keyD: {commandShowDetails, commandDismissDeprecation},
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
		id   commandID
		edit func(*Model, *commandContext)
		want string
	}{
		{commandToggleLock, func(_ *Model, ctx *commandContext) { ctx.target.Locked = true }, "Unlock Session"},
		{commandToggleMute, func(_ *Model, ctx *commandContext) { ctx.target.Muted = true }, "Unmute Session"},
		{commandTogglePreview, func(m *Model, _ *commandContext) { m.previewEnabled = true }, "Hide Preview"},
	}
	for _, tc := range cases {
		t.Run(string(tc.id), func(t *testing.T) {
			gotModel, gotCtx := m, ctx
			tc.edit(&gotModel, &gotCtx)
			if got := commandByID(t, tc.id).displayTitle(gotModel, gotCtx); got != tc.want {
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
			availability := candidate.availabilityFor(m, ctx)
			if availability.available || availability.reason != commandReasonNoSession {
				t.Errorf("%q availability = %+v, want disabled with %q", candidate.id, availability, commandReasonNoSession)
			}
		}
	})

	type availabilityCase struct {
		name   string
		id     commandID
		target session.RunningSession
		setup  func(*Model)
		want   commandAvailability
	}
	base := session.RunningSession{TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work"}
	cases := []availabilityCase{
		{"peek active", commandPeekScrollback, withCommandState(base, "bolt"), nil, availableCommand()},
		{"peek idle", commandPeekScrollback, base, nil, unavailableCommand(commandReasonPeekInactive)},
		{"restart locked", commandRestartSession, withCommandLock(base), nil, unavailableCommand(commandReasonLocked)},
		{"override locked", commandOverrideProfile, withCommandLock(base), nil, unavailableCommand(commandReasonLocked)},
		{"close locked", commandCloseSession, withCommandLock(base), nil, unavailableCommand(commandReasonLocked)},
		{"clear attention", commandClearStatus, withCommandState(base, "key"), nil, availableCommand()},
		{"clear active", commandClearStatus, withCommandState(base, "bolt"), nil, unavailableCommand(commandReasonStatusNotClear)},
		{"clear idle", commandClearStatus, base, func(m *Model) {
			m.idleThreshold = time.Second
			m.lastActivity = map[int]time.Time{101: time.Now().Add(-time.Minute)}
		}, unavailableCommand(commandReasonStatusNotClear)},
		{"configured directory", commandOpenDirectory, base, nil, availableCommand()},
		{"configured editor", commandOpenEditor, base, nil, availableCommand()},
		{"orphan directory", commandOpenDirectory, withCommandProject(base, "orphan"), nil, unavailableCommand(commandReasonNoProject)},
		{"orphan editor", commandOpenEditor, withCommandProject(base, "orphan"), nil, unavailableCommand(commandReasonNoProject)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ctx, _ := commandTestModel(tc.target)
			if tc.setup != nil {
				tc.setup(&m)
			}
			if got := commandByID(t, tc.id).availabilityFor(m, ctx); got != tc.want {
				t.Fatalf("availability = %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("narrow preview can still be hidden", func(t *testing.T) {
		m, ctx, _ := commandTestModel(base)
		m.width = 80
		preview := commandByID(t, commandTogglePreview)
		if got := preview.availabilityFor(m, ctx); got != unavailableCommand(commandReasonPreviewTooSmall) {
			t.Fatalf("narrow preview availability = %+v", got)
		}
		m.previewEnabled = true
		if got := preview.availabilityFor(m, ctx); got != availableCommand() {
			t.Fatalf("enabled narrow preview availability = %+v", got)
		}
	})

	t.Run("undo requires a live deadline", func(t *testing.T) {
		m, ctx, _ := commandTestModel(base)
		undo := commandByID(t, commandUndoClose)
		m.pendingUndo = []undoEntry{{deadline: time.Now().Add(-time.Second)}}
		if got := undo.availabilityFor(m, ctx); got != unavailableCommand(commandReasonNothingToUndo) {
			t.Fatalf("expired undo availability = %+v", got)
		}
		m.pendingUndo = []undoEntry{{deadline: time.Now().Add(time.Minute)}}
		if got := undo.availabilityFor(m, ctx); got != availableCommand() {
			t.Fatalf("live undo availability = %+v", got)
		}
	})
}

func TestCommandCatalogConditionalVisibility(t *testing.T) {
	target := session.RunningSession{TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work"}
	m, ctx, _ := commandTestModel(target)
	clear := commandByID(t, commandClearFilters)
	dismiss := commandByID(t, commandDismissDeprecation)

	if clear.isVisible(m, ctx) {
		t.Fatal("clear filters should be hidden without an active filter")
	}
	m.activeFilter = Parse("api")
	if !clear.isVisible(m, ctx) {
		t.Fatal("clear filters should be visible with an active filter")
	}
	if dismiss.isVisible(m, ctx) {
		t.Fatal("dismiss warning should be hidden without a banner")
	}
	m.deprecations = []config.Deprecation{{Key: "old", Replacement: "new"}}
	if !dismiss.isVisible(m, ctx) {
		t.Fatal("dismiss warning should be visible with a banner")
	}
	m.deprecationDismissed = true
	if dismiss.isVisible(m, ctx) {
		t.Fatal("dismiss warning should be hidden after dismissal")
	}

	visible := 0
	clean, cleanCtx, _ := commandTestModel(target)
	for _, candidate := range commandCatalog() {
		if candidate.isVisible(clean, cleanCtx) {
			visible++
		}
	}
	if visible != 26 {
		t.Fatalf("clean catalog has %d visible commands, want 26", visible)
	}
}
