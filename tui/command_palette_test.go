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
	t.Parallel()

	if ModeCommandPalette != ModePlanView+1 {
		t.Fatalf("ModeCommandPalette = %d, want %d after ModePlanView", ModeCommandPalette, ModePlanView+1)
	}
}

func TestCommandMatchesQueryAcrossEveryField(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	m := testModelWithSessions()
	ctx := captureCommandContext(m)

	results := commands.PaletteResults(commandCatalog(), m, ctx, "HISTORY")
	want := []commands.ID{commands.SearchAllSessions, commands.SearchPlans, commands.SearchProject}
	if got := commandResultIDs(results); !reflect.DeepEqual(got, want) {
		t.Fatalf("history result IDs = %v, want %v", got, want)
	}
	wantGroups := []commands.Group{commands.GroupNavigate, commands.GroupNavigate, commands.GroupSession}
	for i, group := range wantGroups {
		if results[i].Group != group {
			t.Fatalf("history result groups lost catalog order: %v, want %v",
				[]commands.Group{results[0].Group, results[1].Group, results[2].Group}, wantGroups)
		}
	}

	results = commands.PaletteResults(commandCatalog(), m, ctx, "no-command-can-match-this")
	if len(results) != 0 {
		t.Fatalf("impossible query returned %d results", len(results))
	}

	results = commands.PaletteResults(commandCatalog(), m, ctx, "")
	if len(results) != 33 {
		t.Fatalf("clean empty query returned %d visible commands, want 33", len(results))
	}
	for _, result := range results {
		if result.ID == commands.ClearFilters || result.ID == commands.DismissDeprecation {
			t.Fatalf("conditionally hidden command %q leaked into results", result.ID)
		}
	}
}

func TestOpenCommandPaletteResetsStateAndCapturesTarget(t *testing.T) {
	t.Parallel()

	target := session.RunningSession{
		TabID: 7, WindowID: 70, Base: "api", Project: "api", Profile: "work", State: "bolt",
	}
	m := loadSessions(testModelWithSessions(), []session.RunningSession{target})
	m.commandQuery = "stale"
	m.commandResults = []commands.Command[Model]{{ID: "stale"}}
	m.commandCursor = 9
	m.commandOffset = 4
	m.commandContext = commands.Context{Target: session.RunningSession{TabID: 99}, HasTarget: true}

	m.openCommandPalette()

	if m.mode != ModeCommandPalette {
		t.Fatalf("mode = %v, want ModeCommandPalette", m.mode)
	}
	if m.commandQuery != "" || m.commandOffset != 0 {
		t.Fatalf("entry state = query %q offset %d, want reset", m.commandQuery, m.commandOffset)
	}
	if !m.commandContext.HasTarget || m.commandContext.Target.TabID != target.TabID {
		t.Fatalf("captured context = %+v, want tab %d", m.commandContext, target.TabID)
	}
	// The destinations lead the catalog, so an opened palette selects the
	// launcher rather than the focus command it used to. That is the point of
	// putting them first: the five places an operator can go are on screen and
	// the first of them is under the cursor, with no scrolling past a block of
	// session actions a cold list disables anyway.
	if len(m.commandResults) == 0 || m.commandResults[m.commandCursor].ID != commands.OpenLauncher {
		t.Fatalf("initial selection = cursor %d IDs %v, want the launcher", m.commandCursor, commandResultIDs(m.commandResults))
	}
}

func TestOpenCommandPaletteSelectsFirstAvailableOrDisabledResult(t *testing.T) {
	t.Parallel()

	m := testModelWithSessions()
	m.openCommandPalette()
	if m.commandCursor != 0 || m.commandResults[0].ID != commands.OpenLauncher {
		t.Fatalf("empty-list initial selection = cursor %d %q, want the launcher at 0", m.commandCursor, m.commandResults[m.commandCursor].ID)
	}

	// A filtered set whose leading rows are disabled is where "first available"
	// still has work to do: `close` matches the session close (no row to close)
	// and the undo (nothing to undo) before it reaches Quit.
	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Text: "close"}))
	m = updated.(Model)
	if got := m.commandResults[m.commandCursor].ID; got != commands.Quit {
		t.Fatalf("close-filtered selection = %q, want first available %q", got, commands.Quit)
	}
	for i := 0; i < m.commandCursor; i++ {
		if m.commandResults[i].AvailabilityFor(m, m.commandContext).Available {
			t.Fatalf("result %d (%q) before cursor is unexpectedly available", i, m.commandResults[i].ID)
		}
	}
	if m.commandCursor == 0 {
		t.Fatal("the close query skipped no disabled row; the first-available rule is vacuous here")
	}

	m = testModelWithSessions()
	m.openCommandPalette()
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Text: "relaunch selected preference"}))
	m = updated.(Model)
	if len(m.commandResults) != 1 || m.commandResults[0].ID != commands.RestartSession {
		t.Fatalf("restart-specific results = %v, want restart only", commandResultIDs(m.commandResults))
	}
	if m.commandCursor != 0 || m.commandResults[0].AvailabilityFor(m, m.commandContext).Available {
		t.Fatalf("all-disabled results should select first disabled row: cursor=%d", m.commandCursor)
	}
}

func TestCommandPaletteInputIsRuneSafeAndPrintableKeysStayText(t *testing.T) {
	t.Parallel()

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

	// Every one of these is a dashboard binding one keystroke outside the
	// palette: two cursor moves, a quit, and an undo that reopens a closed
	// session. Inside it they are letters and nothing else, which is what makes
	// the query typeable — a palette that quit on `q` could never be asked
	// about "Quit".
	for _, key := range []string{keyJ, keyK, keyQ, keyUndoClose} {
		m = pressKey(m, key)
	}
	if m.commandQuery != "jkqu" || m.mode != ModeCommandPalette {
		t.Fatalf("printable navigation-like keys changed mode/state: mode=%v query=%q", m.mode, m.commandQuery)
	}
}

func TestCommandPaletteNavigationClampsAndVisitsDisabledRows(t *testing.T) {
	t.Parallel()

	m := testModelWithSessions()
	m.openCommandPalette()
	// `close` is the query with disabled rows AHEAD of the available one now that
	// the destinations lead the catalog: the session close and the undo both
	// refuse on a cold list, and Quit never does.
	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Text: "close"}))
	m = updated.(Model)

	if got := commandResultIDs(m.commandResults); !reflect.DeepEqual(got, []commands.ID{
		commands.CloseSession, commands.ToggleLockID, commands.UndoClose, commands.Quit,
	}) {
		t.Fatalf("close result IDs = %v", got)
	}
	if want := len(m.commandResults) - 1; m.commandCursor != want {
		t.Fatalf("initial available close cursor = %d, want %d", m.commandCursor, want)
	}

	// Every row above the selection refuses, and Up walks onto each of them
	// rather than skipping to the top: a disabled row is worth selecting because
	// selecting it is how its reason is read.
	for m.commandCursor > 0 {
		before := m.commandCursor
		m = pressKey(m, keyUp)
		if m.commandCursor != before-1 {
			t.Fatalf("Up from %d landed on %d", before, m.commandCursor)
		}
		if m.commandResults[m.commandCursor].AvailabilityFor(m, m.commandContext).Available {
			t.Fatalf("result %d (%q) above the first available row is available", m.commandCursor, m.commandResults[m.commandCursor].ID)
		}
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
	t.Parallel()

	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "disabled", query: "relaunch selected preference"},
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
	t.Parallel()

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
	t.Parallel()

	original := session.RunningSession{
		TabID: 7, WindowID: 70, Base: "api", Project: "api", Profile: "work", State: "bolt",
	}
	m := loadSessions(testModelWithSessions(), []session.RunningSession{original})
	// The confirmation is what this test observes the capture through, so it asks
	// for one. Restart honors confirm_destructive from the palette now, the same
	// way it always did from the per-row menu.
	m.cfg.Defaults.ConfirmDestructive = true
	m.openCommandPalette()
	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Text: "relaunch selected preference"}))
	m = updated.(Model)

	// The refresh adds a row and lands the cursor on it. The captured row is
	// still listed, which is what makes this a test of the capture rather than of
	// the staleness refusal: a captured row that had GONE would be refused
	// outright, and refusing is not the same claim as not retargeting.
	arrival := session.RunningSession{
		TabID: 8, WindowID: 80, Base: "backend", Project: "backend", Profile: "work", State: "bolt",
	}
	m = applySessions(m, commands.SessionsMsg{Sessions: []session.RunningSession{arrival, original}})
	m.cursor = 0
	if m.filtered[m.cursor].TabID != arrival.TabID {
		t.Fatalf("background refresh did not move the cursor off the captured row: %+v", m.filtered)
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
	t.Parallel()

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
	t.Parallel()

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
