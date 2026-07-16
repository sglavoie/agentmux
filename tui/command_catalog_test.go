package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/overrides"
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
		{commandClearFilters, commandGroupExplore, "Clear Filters", []string{"chips", "reset"}, "esc"},
		{commandOpenLauncher, commandGroupExplore, "Open Launcher", []string{"launch", "projects"}, "l"},
		{commandSearchAllSessions, commandGroupExplore, "Search All Past Sessions", []string{"history", "transcripts"}, "S"},
		{commandSearchPlans, commandGroupExplore, "Search Plans", []string{"agent plans", "history"}, "P"},
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
		commandClearFilters:       keyEsc,
		commandOpenLauncher:       keyL,
		commandSearchAllSessions:  keySessionSearch,
		commandSearchPlans:        keyPlanSearch,
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
	if visible != 24 {
		t.Fatalf("clean catalog has %d visible commands, want 24", visible)
	}
}

func TestCommandRunRejectsDisabledAndHiddenCommands(t *testing.T) {
	m, _, _ := commandTestModel(session.RunningSession{
		TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work", Locked: true,
	})
	ctx := captureCommandContext(m)
	before := m
	after, cmd := commandByID(t, commandRestartSession).run(m, ctx)
	if cmd != nil || after.mode != before.mode || after.confirmAction != "" {
		t.Fatalf("disabled restart mutated model: mode=%v action=%q cmd=%v", after.mode, after.confirmAction, cmd != nil)
	}
	after, cmd = commandByID(t, commandClearFilters).run(m, ctx)
	if cmd != nil || !after.activeFilter.IsEmpty() {
		t.Fatal("hidden clear-filters command should be a no-op")
	}
}

func TestCommandExecutionUsesCapturedTarget(t *testing.T) {
	client := NewUITestKittyClient(nil)
	m := testModelWithSessions()
	m.client = client
	m = loadSessions(m, []session.RunningSession{
		{TabID: 11, WindowID: 101, Base: "api", Project: "api", Profile: "work"},
		{TabID: 22, WindowID: 202, Base: "backend", Project: "backend", Profile: "work"},
	})
	m.cursor = 1
	ctx := captureCommandContext(m)
	m.cursor = 0 // A refresh/controller move after capture must not retarget.
	_, cmd := commandByID(t, commandFocusSession).run(m, ctx)
	if cmd == nil {
		t.Fatal("focus returned nil command")
	}
	_ = cmd()
	calls := client.CallsFor("FocusTab")
	if len(calls) != 1 || calls[0].Match != kitty.MatchTabByID(22) {
		t.Fatalf("focus calls = %+v, want captured tab 22", calls)
	}
}

func TestCommandRowSideEffects(t *testing.T) {
	target := session.RunningSession{
		TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work", State: "bolt",
	}
	cases := []struct {
		name      string
		id        commandID
		wantOp    string
		wantMatch string
		wantVar   string
	}{
		{"focus", commandFocusSession, "FocusTab", kitty.MatchTabByID(22), ""},
		{"peek", commandPeekScrollback, "LaunchOverlayPager", kitty.MatchWindowByID(202), ""},
		{"lock", commandToggleLock, "SetUserVars", kitty.MatchWindowByID(202), "amx_locked"},
		{"mute", commandToggleMute, "SetUserVars", kitty.MatchWindowByID(202), "cc_mute"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ctx, client := commandTestModel(target)
			_, cmd := commandByID(t, tc.id).run(m, ctx)
			if cmd == nil {
				t.Fatal("execution returned nil command")
			}
			_ = cmd()
			calls := client.CallsFor(tc.wantOp)
			if len(calls) != 1 || calls[0].Match != tc.wantMatch {
				t.Fatalf("%s calls = %+v, want one call matching %q", tc.wantOp, calls, tc.wantMatch)
			}
			if tc.wantVar != "" {
				if _, ok := calls[0].Vars[tc.wantVar]; !ok {
					t.Fatalf("%s vars = %v, want %q", tc.wantOp, calls[0].Vars, tc.wantVar)
				}
			}
		})
	}
}

func TestCommandCopyAndClearStatusEffects(t *testing.T) {
	target := session.RunningSession{
		TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work", State: "key",
	}
	m, ctx, _ := commandTestModel(target)

	originalCopy := copyToClipboard
	defer func() { copyToClipboard = originalCopy }()
	var copied string
	copyToClipboard = func(text string) error {
		copied = text
		return nil
	}
	_, copyCmd := commandByID(t, commandCopyTitle).run(m, ctx)
	if copyCmd == nil {
		t.Fatal("copy title returned nil command")
	}
	_ = copyCmd()
	if copied != "api" {
		t.Fatalf("copied title = %q, want api", copied)
	}

	var request daemon.Request
	m.daemonCommand = func(req daemon.Request) (daemon.Response, error) {
		request = req
		return daemon.Response{OK: true}, nil
	}
	_, clearCmd := commandByID(t, commandClearStatus).run(m, ctx)
	if clearCmd == nil {
		t.Fatal("clear status returned nil command")
	}
	_ = clearCmd()
	if request.Action != "user_clear" || request.WindowID != 202 {
		t.Fatalf("clear-status request = %+v, want user_clear for window 202", request)
	}
}

func TestCommandConfiguredProjectExecutions(t *testing.T) {
	target := session.RunningSession{
		TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work",
	}
	for _, id := range []commandID{commandOpenDirectory, commandOpenEditor} {
		t.Run(string(id), func(t *testing.T) {
			m, ctx, _ := commandTestModel(target)
			got, cmd := commandByID(t, id).run(m, ctx)
			if cmd == nil {
				t.Fatal("configured project execution returned nil command")
			}
			if got.targetSession.TabID != target.TabID {
				t.Fatalf("target tab = %d, want captured tab %d", got.targetSession.TabID, target.TabID)
			}
		})
	}
}

func TestCommandRowTransitions(t *testing.T) {
	target := session.RunningSession{
		TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work", State: "bolt",
	}
	type transitionCase struct {
		name  string
		id    commandID
		setup func(*Model)
		check func(*testing.T, Model, tea.Cmd)
	}
	cases := []transitionCase{
		{
			name: "restart always confirms", id: commandRestartSession,
			setup: func(m *Model) { m.cfg.Defaults.ConfirmDestructive = false },
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeConfirm || got.confirmAction != "restart" || got.confirmConsequence != restartConsequence || got.confirmReturnMode != ModeList || cmd != nil {
					t.Fatalf("restart result: mode=%v action=%q consequence=%q return=%v cmd=%v", got.mode, got.confirmAction, got.confirmConsequence, got.confirmReturnMode, cmd != nil)
				}
			},
		},
		{
			name: "override profile picker", id: commandOverrideProfile,
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeConfigPicker || got.configPickerPurpose != ConfigPickerRestartProfile || got.configPickerReturnMode != ModeList || got.configPickerCurrent != "work" || got.configPickerProjectIdx != -1 || cmd != nil {
					t.Fatalf("override result: mode=%v purpose=%v return=%v current=%q project=%d cmd=%v", got.mode, got.configPickerPurpose, got.configPickerReturnMode, got.configPickerCurrent, got.configPickerProjectIdx, cmd != nil)
				}
			},
		},
		{
			name: "launch another picker", id: commandLaunchAnother,
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeConfigPicker || got.configPickerPurpose != ConfigPickerDashboardLaunch || got.configPickerReturnMode != ModeList || got.configPickerCurrent != "work" || cmd != nil {
					t.Fatalf("launch result: mode=%v purpose=%v return=%v current=%q cmd=%v", got.mode, got.configPickerPurpose, got.configPickerReturnMode, got.configPickerCurrent, cmd != nil)
				}
			},
		},
		{
			name: "close without confirmation", id: commandCloseSession,
			setup: func(m *Model) { m.cfg.Defaults.ConfirmDestructive = false },
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeList || cmd == nil || len(got.pendingUndo) != 1 || len(got.toasts) != 1 || !strings.Contains(got.toasts[0].Text, "undo") {
					t.Fatalf("close result: mode=%v cmd=%v undo=%d toasts=%v", got.mode, cmd != nil, len(got.pendingUndo), got.toasts)
				}
			},
		},
		{
			name: "close with confirmation", id: commandCloseSession,
			setup: func(m *Model) { m.cfg.Defaults.ConfirmDestructive = true },
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeConfirm || got.confirmAction != "close" || got.confirmConsequence != closeConsequence || got.confirmReturnMode != ModeList || cmd != nil || len(got.pendingUndo) != 0 {
					t.Fatalf("confirmed close result: mode=%v action=%q return=%v cmd=%v undo=%d", got.mode, got.confirmAction, got.confirmReturnMode, cmd != nil, len(got.pendingUndo))
				}
			},
		},
		{
			name: "project history", id: commandSearchProject,
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeSessionSearch || !reflect.DeepEqual(got.filterProject, []string{"api"}) || cmd == nil {
					t.Fatalf("project history result: mode=%v filter=%v cmd=%v", got.mode, got.filterProject, cmd != nil)
				}
			},
		},
		{
			name: "details ignores banner", id: commandShowDetails,
			setup: func(m *Model) {
				m.deprecations = []config.Deprecation{{Key: "old", Replacement: "new"}}
				m.deprecationDismissed = false
			},
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeDetail || got.detailWindowID != 202 || got.detailViewTimeline || !got.detailLoading || got.deprecationDismissed || cmd == nil {
					t.Fatalf("details result: mode=%v window=%d timeline=%v loading=%v dismissed=%v cmd=%v", got.mode, got.detailWindowID, got.detailViewTimeline, got.detailLoading, got.deprecationDismissed, cmd != nil)
				}
			},
		},
		{
			name: "timeline", id: commandShowTimeline,
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeDetail || got.detailWindowID != 202 || !got.detailViewTimeline || !got.detailLoading || cmd == nil {
					t.Fatalf("timeline result: mode=%v window=%d timeline=%v loading=%v cmd=%v", got.mode, got.detailWindowID, got.detailViewTimeline, got.detailLoading, cmd != nil)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ctx, _ := commandTestModel(target)
			if tc.setup != nil {
				tc.setup(&m)
			}
			got, cmd := commandByID(t, tc.id).run(m, ctx)
			if got.targetSession.TabID != 0 && got.targetSession.TabID != target.TabID {
				t.Fatalf("target tab = %d, want captured tab %d", got.targetSession.TabID, target.TabID)
			}
			tc.check(t, got, cmd)
		})
	}
}

func TestCommandExploreAndAppExecution(t *testing.T) {
	target := session.RunningSession{TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work"}

	t.Run("filter and clear filters", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commandFilterSessions).run(m, ctx)
		if got.mode != ModeSearch || got.filterText != "" || cmd != nil {
			t.Fatalf("filter result: mode=%v text=%q cmd=%v", got.mode, got.filterText, cmd != nil)
		}
		got.mode = ModeList
		got.activeFilter = Parse("api")
		got.filtered = got.sessions
		got, cmd = commandByID(t, commandClearFilters).run(got, ctx)
		if !got.activeFilter.IsEmpty() || got.cursor != 0 || cmd != nil {
			t.Fatalf("clear result: filter=%+v cursor=%d cmd=%v", got.activeFilter, got.cursor, cmd != nil)
		}
	})

	t.Run("launcher", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		t.Setenv(overrides.PathEnvVar, t.TempDir()+"/overrides.json")
		got, cmd := commandByID(t, commandOpenLauncher).run(m, ctx)
		if got.mode != ModePicker || len(got.pickerSessions) != 3 || len(got.pickerFilteredIdx) != 3 || got.pickerSearchMode || cmd != nil {
			t.Fatalf("launcher result: mode=%v sessions=%d filtered=%d search=%v cmd=%v", got.mode, len(got.pickerSessions), len(got.pickerFilteredIdx), got.pickerSearchMode, cmd != nil)
		}
	})

	t.Run("search surfaces", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commandSearchAllSessions).run(m, ctx)
		if got.mode != ModeSessionSearch || cmd == nil {
			t.Fatalf("session search result: mode=%v cmd=%v", got.mode, cmd != nil)
		}
		m, ctx, _ = commandTestModel(target)
		got, cmd = commandByID(t, commandSearchPlans).run(m, ctx)
		if got.mode != ModePlanSearch || cmd == nil {
			t.Fatalf("plan search result: mode=%v cmd=%v", got.mode, cmd != nil)
		}
	})

	t.Run("preview", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commandTogglePreview).run(m, ctx)
		if !got.previewEnabled || got.previewWindowID != 202 || cmd == nil {
			t.Fatalf("show preview result: enabled=%v window=%d cmd=%v", got.previewEnabled, got.previewWindowID, cmd != nil)
		}
		got, cmd = commandByID(t, commandTogglePreview).run(got, ctx)
		if got.previewEnabled || got.previewWindowID != 0 || cmd != nil {
			t.Fatalf("hide preview result: enabled=%v window=%d cmd=%v", got.previewEnabled, got.previewWindowID, cmd != nil)
		}
	})

	t.Run("resort", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commandResortTabs).run(m, ctx)
		if cmd == nil || len(got.toasts) != 1 || got.toasts[0].Text != "Resorting tabs…" {
			t.Fatalf("resort result: cmd=%v toasts=%v", cmd != nil, got.toasts)
		}
	})

	t.Run("undo", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		m.pendingUndo = []undoEntry{{
			snapshot: config.Session{Title: "api", Directory: "/home/user/api", Profile: "work"},
			account:  config.Account{Name: "work", Color: "blue"},
			deadline: time.Now().Add(time.Minute), label: "api",
		}}
		got, cmd := commandByID(t, commandUndoClose).run(m, ctx)
		if cmd == nil || len(got.pendingUndo) != 0 || len(got.toasts) != 1 || !strings.Contains(got.toasts[0].Text, "restored") {
			t.Fatalf("undo result: cmd=%v pending=%d toasts=%v", cmd != nil, len(got.pendingUndo), got.toasts)
		}
	})

	t.Run("dismiss warning and help", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		m.deprecations = []config.Deprecation{{Key: "old", Replacement: "new"}}
		got, cmd := commandByID(t, commandDismissDeprecation).run(m, ctx)
		if !got.deprecationDismissed || got.mode != ModeList || cmd != nil {
			t.Fatalf("dismiss result: dismissed=%v mode=%v cmd=%v", got.deprecationDismissed, got.mode, cmd != nil)
		}
		got.helpOffset = 7
		got, cmd = commandByID(t, commandShowKeyboardHelp).run(got, ctx)
		if got.mode != ModeHelp || got.helpOffset != 0 || cmd != nil {
			t.Fatalf("help result: mode=%v offset=%d cmd=%v", got.mode, got.helpOffset, cmd != nil)
		}
	})

	t.Run("quit", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commandQuit).run(m, ctx)
		if got.mode != ModeList || cmd == nil {
			t.Fatalf("quit result: mode=%v cmd=%v", got.mode, cmd != nil)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("quit command returned %T, want tea.QuitMsg", cmd())
		}
	})
}

func TestPaletteRestartDiffersFromActionsOverlay(t *testing.T) {
	target := session.RunningSession{TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work"}
	m, ctx, _ := commandTestModel(target)
	m.cfg.Defaults.ConfirmDestructive = false

	paletteResult, paletteCmd := commandByID(t, commandRestartSession).run(m, ctx)
	if paletteResult.mode != ModeConfirm || paletteCmd != nil {
		t.Fatalf("palette restart = mode %v cmd %v; want confirmation", paletteResult.mode, paletteCmd != nil)
	}

	m.targetSession = target
	m.mode = ModeActions
	m.actionsReturnMode = ModeList
	action := actionsFor(m)[2]
	actionResult, actionCmd := action.run(m)
	if actionResult.mode != ModeList || actionCmd == nil || len(actionResult.toasts) != 1 {
		t.Fatalf("actions restart = mode %v cmd %v toasts %d; want immediate toast path", actionResult.mode, actionCmd != nil, len(actionResult.toasts))
	}
}

func withCommandState(s session.RunningSession, state string) session.RunningSession {
	s.State = state
	return s
}

func withCommandLock(s session.RunningSession) session.RunningSession {
	s.Locked = true
	return s
}

func withCommandProject(s session.RunningSession, project string) session.RunningSession {
	s.Project = project
	s.Base = project
	return s
}
