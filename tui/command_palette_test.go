package tui

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

func commandResultIDs(results []commands.Command[Model]) []commands.ID {
	ids := make([]commands.ID, len(results))
	for i, result := range results {
		ids[i] = result.ID
	}
	return ids
}

func assertCommandPaletteCleared(t *testing.T, m Model) {
	t.Helper()
	if m.commandQuery != "" || m.commandResults != nil || m.commandCursor != 0 || m.commandOffset != 0 {
		t.Fatalf("palette state not cleared: query=%q results=%d cursor=%d offset=%d",
			m.commandQuery, len(m.commandResults), m.commandCursor, m.commandOffset)
	}
	if m.commandContext.HasTarget || m.commandContext.Target.TabID != 0 {
		t.Fatalf("palette context not cleared: %+v", m.commandContext)
	}
}

func TestModeCommandPaletteIsAppended(t *testing.T) {
	if ModeCommandPalette != ModePlanView+1 {
		t.Fatalf("ModeCommandPalette = %d, want %d after ModePlanView", ModeCommandPalette, ModePlanView+1)
	}
}

func TestCommandMatchesQueryAcrossEveryField(t *testing.T) {
	candidate := commands.Command[Model]{
		ID:          "test.command",
		Group:       commands.GroupSession,
		Title:       commands.StaticTitle[Model]("Alpha Action"),
		Description: "Performs a Beta operation.",
		Aliases:     []string{"Gamma helper"},
		Shortcut:    "ctrl+x",
	}

	for _, query := range []string{
		"ALPHA",
		"beta",
		"gamma",
		"CTRL+X",
		"alpha operation helper ctrl+x",
		"  alpha   BETA  ",
		"",
	} {
		t.Run(query, func(t *testing.T) {
			if !commands.MatchesQuery(candidate, Model{}, commands.Context{}, query) {
				t.Fatalf("query %q should match all of its tokens", query)
			}
		})
	}
	if commands.MatchesQuery(candidate, Model{}, commands.Context{}, "alpha missing") {
		t.Fatal("AND matching accepted a query with one missing token")
	}
}

func TestCommandPaletteResultsPreserveCatalogAndGroupOrder(t *testing.T) {
	m := testModelWithSessions()
	ctx := captureCommandContext(m)

	results := commands.PaletteResults(commandCatalog(), m, ctx, "HISTORY")
	want := []commands.ID{commands.SearchProject, commands.SearchAllSessions, commands.SearchPlans}
	if got := commandResultIDs(results); !reflect.DeepEqual(got, want) {
		t.Fatalf("history result IDs = %v, want %v", got, want)
	}
	if results[0].Group != commands.GroupSession || results[1].Group != commands.GroupExplore || results[2].Group != commands.GroupExplore {
		t.Fatalf("history result groups lost catalog order: %v, %v, %v", results[0].Group, results[1].Group, results[2].Group)
	}

	results = commands.PaletteResults(commandCatalog(), m, ctx, "no-command-can-match-this")
	if len(results) != 0 {
		t.Fatalf("impossible query returned %d results", len(results))
	}

	results = commands.PaletteResults(commandCatalog(), m, ctx, "")
	if len(results) != 28 {
		t.Fatalf("clean empty query returned %d visible commands, want 28", len(results))
	}
	for _, result := range results {
		if result.ID == commands.ClearFilters || result.ID == commands.DismissDeprecation {
			t.Fatalf("conditionally hidden command %q leaked into results", result.ID)
		}
	}
}

func TestOpenCommandPaletteResetsStateAndCapturesTarget(t *testing.T) {
	target := session.RunningSession{
		TabID: 7, WindowID: 70, Base: "api", Project: "api", Profile: "work", State: "bolt",
	}
	m := loadSessions(testModelWithSessions(), []session.RunningSession{target})
	m.pendingJumpDir = 1
	m.commandQuery = "stale"
	m.commandResults = []commands.Command[Model]{{ID: "stale"}}
	m.commandCursor = 9
	m.commandOffset = 4
	m.commandContext = commands.Context{Target: session.RunningSession{TabID: 99}, HasTarget: true}

	m.openCommandPalette()

	if m.mode != ModeCommandPalette {
		t.Fatalf("mode = %v, want ModeCommandPalette", m.mode)
	}
	if m.pendingJumpDir != 0 {
		t.Fatalf("pending jump direction = %d, want cleared", m.pendingJumpDir)
	}
	if m.commandQuery != "" || m.commandOffset != 0 {
		t.Fatalf("entry state = query %q offset %d, want reset", m.commandQuery, m.commandOffset)
	}
	if !m.commandContext.HasTarget || m.commandContext.Target.TabID != target.TabID {
		t.Fatalf("captured context = %+v, want tab %d", m.commandContext, target.TabID)
	}
	if len(m.commandResults) == 0 || m.commandResults[m.commandCursor].ID != commands.FocusSession {
		t.Fatalf("initial selection = cursor %d IDs %v, want focus command", m.commandCursor, commandResultIDs(m.commandResults))
	}
}

func TestOpenCommandPaletteSelectsFirstAvailableOrDisabledResult(t *testing.T) {
	m := testModelWithSessions()
	m.openCommandPalette()
	if got := m.commandResults[m.commandCursor].ID; got != commands.FilterSessions {
		t.Fatalf("empty-list initial selection = %q, want first available %q", got, commands.FilterSessions)
	}
	for i := 0; i < m.commandCursor; i++ {
		if m.commandResults[i].AvailabilityFor(m, m.commandContext).Available {
			t.Fatalf("result %d (%q) before cursor is unexpectedly available", i, m.commandResults[i].ID)
		}
	}

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Text: "confirm relaunching"}))
	m = updated.(Model)
	if len(m.commandResults) != 1 || m.commandResults[0].ID != commands.RestartSession {
		t.Fatalf("restart-specific results = %v, want restart only", commandResultIDs(m.commandResults))
	}
	if m.commandCursor != 0 || m.commandResults[0].AvailabilityFor(m, m.commandContext).Available {
		t.Fatalf("all-disabled results should select first disabled row: cursor=%d", m.commandCursor)
	}
}

func TestCommandPaletteInputIsRuneSafeAndPrintableKeysStayText(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.openCommandPalette()
	m.commandCursor = len(m.commandResults) - 1
	m.commandOffset = 8

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'é', Text: "é界"}))
	m = updated.(Model)
	if m.commandQuery != "é界" {
		t.Fatalf("query = %q, want %q", m.commandQuery, "é界")
	}
	if m.commandCursor != 0 || m.commandOffset != 0 {
		t.Fatalf("query edit did not reset selection/scroll: cursor=%d offset=%d", m.commandCursor, m.commandOffset)
	}

	m = pressKey(m, keyBackspace)
	if m.commandQuery != "é" {
		t.Fatalf("first Backspace query = %q, want %q", m.commandQuery, "é")
	}
	m = pressKey(m, keyBackspace)
	if m.commandQuery != "" {
		t.Fatalf("second Backspace query = %q, want empty", m.commandQuery)
	}

	for _, key := range []string{keyJ, keyK, keyQ} {
		m = pressKey(m, key)
	}
	if m.commandQuery != "jkq" || m.mode != ModeCommandPalette {
		t.Fatalf("printable navigation-like keys changed mode/state: mode=%v query=%q", m.mode, m.commandQuery)
	}
}

func TestCommandPaletteNavigationClampsAndVisitsDisabledRows(t *testing.T) {
	m := testModelWithSessions()
	m.openCommandPalette()
	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 's', Text: "search"}))
	m = updated.(Model)

	if got := commandResultIDs(m.commandResults); !reflect.DeepEqual(got, []commands.ID{
		commands.SearchProject, commands.FilterSessions, commands.SearchAllSessions, commands.SearchPlans,
	}) {
		t.Fatalf("search result IDs = %v", got)
	}
	if m.commandCursor != 1 {
		t.Fatalf("initial available search cursor = %d, want 1", m.commandCursor)
	}

	m = pressKey(m, keyUp)
	if m.commandCursor != 0 || m.commandResults[0].AvailabilityFor(m, m.commandContext).Available {
		t.Fatalf("Up did not visit disabled first row: cursor=%d", m.commandCursor)
	}
	m = pressKey(m, keyUp)
	if m.commandCursor != 0 {
		t.Fatalf("Up wrapped above first row: cursor=%d", m.commandCursor)
	}

	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Mod: tea.ModCtrl}))
	m = updated.(Model)
	if m.commandCursor != 1 {
		t.Fatalf("Ctrl+n cursor = %d, want 1", m.commandCursor)
	}
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Mod: tea.ModCtrl}))
	m = updated.(Model)
	if m.commandCursor != 2 {
		t.Fatalf("Ctrl+j cursor = %d, want 2", m.commandCursor)
	}
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'k', Mod: tea.ModCtrl}))
	m = updated.(Model)
	if m.commandCursor != 1 {
		t.Fatalf("Ctrl+k cursor = %d, want 1", m.commandCursor)
	}
	for i := 0; i < 10; i++ {
		m = pressKey(m, keyDown)
	}
	if want := len(m.commandResults) - 1; m.commandCursor != want {
		t.Fatalf("Down cursor = %d, want clamped %d", m.commandCursor, want)
	}
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModCtrl}))
	m = updated.(Model)
	if want := len(m.commandResults) - 2; m.commandCursor != want {
		t.Fatalf("Ctrl+p cursor = %d, want %d", m.commandCursor, want)
	}
}

func TestCommandPaletteEnterDisabledOrEmptyIsNoOp(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "disabled", query: "confirm relaunching"},
		{name: "no result", query: "no-command-can-match-this"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testModelWithSessions()
			m.openCommandPalette()
			updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: rune(tc.query[0]), Text: tc.query}))
			m = updated.(Model)
			beforeQuery := m.commandQuery
			beforeResults := commandResultIDs(m.commandResults)

			updated, cmd := m.Update(keyPress(keyEnter))
			got := updated.(Model)
			if cmd != nil || got.mode != ModeCommandPalette {
				t.Fatalf("Enter changed disabled/empty palette: mode=%v cmd=%v", got.mode, cmd)
			}
			if got.commandQuery != beforeQuery || !reflect.DeepEqual(commandResultIDs(got.commandResults), beforeResults) {
				t.Fatalf("Enter mutated disabled/empty palette state")
			}
		})
	}
}

func TestCommandPaletteCtrlCQuits(t *testing.T) {
	m := testModelWithSessions()
	m.openCommandPalette()
	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("Ctrl+c returned nil, want tea.Quit")
	}
	if msg := cmd(); msg != (tea.QuitMsg{}) {
		t.Fatalf("Ctrl+c produced %T, want tea.QuitMsg", msg)
	}
	if got.mode != ModeCommandPalette {
		t.Fatalf("Ctrl+c mutated palette mode to %v", got.mode)
	}
}

func TestCommandPaletteExecutionUsesCapturedTargetAndClearsBeforeTransition(t *testing.T) {
	original := session.RunningSession{
		TabID: 7, WindowID: 70, Base: "api", Project: "api", Profile: "work", State: "bolt",
	}
	m := loadSessions(testModelWithSessions(), []session.RunningSession{original})
	m.openCommandPalette()
	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Text: "confirm relaunching"}))
	m = updated.(Model)

	replacement := session.RunningSession{
		TabID: 8, WindowID: 80, Base: "backend", Project: "backend", Profile: "work", State: "bolt",
	}
	m = applySessions(m, commands.SessionsMsg{Sessions: []session.RunningSession{replacement}})
	if m.filtered[0].TabID != replacement.TabID {
		t.Fatalf("background refresh did not replace live row: %+v", m.filtered)
	}

	updated, cmd := m.Update(keyPress(keyEnter))
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("restart palette command should enter confirmation without an immediate command")
	}
	if m.mode != ModeConfirm || m.confirmAction != "restart" {
		t.Fatalf("execution transition = mode %v action %q, want restart confirmation", m.mode, m.confirmAction)
	}
	if m.targetSession.TabID != original.TabID {
		t.Fatalf("restart target = tab %d, want captured tab %d", m.targetSession.TabID, original.TabID)
	}
	assertCommandPaletteCleared(t, m)
}

func TestCommandPaletteEscClearsTransientStateAndPreservesDashboardSelection(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.cursor = 1
	m.openCommandPalette()
	m = pressKey(m, keyQ)
	m.commandOffset = 3

	m = pressKey(m, keyEsc)
	if m.mode != ModeList || m.cursor != 1 {
		t.Fatalf("Esc returned mode/cursor = %v/%d, want ModeList/1", m.mode, m.cursor)
	}
	assertCommandPaletteCleared(t, m)

	m.openCommandPalette()
	if m.commandQuery != "" || m.commandCursor != 0 || m.commandContext.Target.TabID != m.filtered[1].TabID {
		t.Fatalf("reopen did not start fresh: query=%q cursor=%d context=%+v", m.commandQuery, m.commandCursor, m.commandContext)
	}
}

func TestColonOpensCommandPaletteFromDashboard(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.cursor = 1
	wantTabID := m.filtered[m.cursor].TabID
	updated, cmd := m.Update(keyPress(":"))
	m = updated.(Model)
	if cmd != nil || m.mode != ModeCommandPalette {
		t.Fatalf("colon did not open palette: mode=%v cmd=%v", m.mode, cmd)
	}
	if !m.commandContext.HasTarget || m.commandContext.Target.TabID != wantTabID {
		t.Fatalf("captured target = %+v, want tab %d", m.commandContext, wantTabID)
	}
}
