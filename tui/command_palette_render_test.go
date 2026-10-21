package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/layout"
)

func commandPaletteTestModel(width, height int) Model {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.width = width
	m.height = height
	m.openCommandPalette()
	return m
}

func commandPaletteLayoutText(rows []commands.PaletteRow) string {
	lines := make([]string, len(rows))
	for i, row := range rows {
		lines[i] = row.Text
	}
	return strings.Join(lines, "\n")
}

// commandPaletteRowFor is the display row the cursor is on. It is a search
// rather than an index because the group rules between the commands mean the
// two numbering systems do not line up, which is the whole reason PaletteRow
// carries a command index at all.
func commandPaletteRowFor(rows []commands.PaletteRow, cursor int) int {
	for i, row := range rows {
		if row.CommandIndex == cursor {
			return i
		}
	}
	return -1
}

func assertCommandPaletteSelectionVisible(t *testing.T, m Model) {
	t.Helper()
	rows, visible := commandPaletteScrollBounds(m)
	index := commandPaletteRowFor(rows, m.commandCursor)
	if index < 0 {
		t.Fatalf("cursor %d has no display row among %d rows", m.commandCursor, len(rows))
	}
	if index < m.commandOffset || index >= m.commandOffset+visible {
		t.Fatalf("selected row %d outside window [%d,%d)", index, m.commandOffset, m.commandOffset+visible)
	}
}

// TestCommandPaletteFullAndFilteredLayouts is the grouped-result assertion in
// its logical-command form: one rule per group that has a match, in catalog
// order, and one row per command rather than a title row plus however many rows
// its description happened to wrap into.
func TestCommandPaletteFullAndFilteredLayouts(t *testing.T) {
	t.Parallel()

	m := commandPaletteTestModel(100, 40)
	rows := commandPaletteRows(m, commandPaletteContentWidth(m))
	text := commandPaletteLayoutText(rows)

	var seen []commands.Group
	for _, row := range rows {
		if row.GroupRule {
			seen = append(seen, row.Group)
		}
	}
	want := []commands.Group{commands.GroupNavigate, commands.GroupSession, commands.GroupApplication}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("group rules = %v, want %v\n%s", seen, want, text)
	}
	if commandRows := len(rows) - len(seen); commandRows != len(m.commandResults) {
		t.Fatalf("layout has %d command rows for %d results\n%s", commandRows, len(m.commandResults), text)
	}

	m.commandQuery = "quit"
	m.recomputeCommandPaletteResults()
	rows = commandPaletteRows(m, commandPaletteContentWidth(m))
	text = commandPaletteLayoutText(rows)
	if !strings.Contains(text, string(commands.GroupApplication)) || !strings.Contains(text, "Quit") {
		t.Fatalf("filtered layout missing Application/Quit:\n%s", text)
	}
	for _, row := range rows {
		if row.GroupRule && row.Group != commands.GroupApplication {
			t.Fatalf("filtered layout leaked empty %q rule:\n%s", row.Group, text)
		}
	}
}

func TestCommandPaletteRendersContextDisabledNoResultsAndNoSession(t *testing.T) {
	t.Parallel()

	locked := session.RunningSession{
		TabID: 7, Base: "api", Project: "api", Profile: "work", State: "bolt", Locked: true,
	}
	m := loadSessions(testModelWithSessions(), []session.RunningSession{locked})
	m.width, m.height = 80, 30
	m.openCommandPalette()
	// `relaunch` rather than `restart`: the word restart also appears in the
	// descriptions of the two commands beside it, and only one command can be
	// selected, so the explanation can only be read off a query that lands the
	// cursor on the row being asked about.
	m.commandQuery = "relaunch"
	m.recomputeCommandPaletteResults()
	out := stripAnsi(renderCommandPalette(m))
	for _, want := range []string{"Command Palette", "Session: api", ":relaunch" + commands.PaletteCursorGlyph, "Restart Session", commands.ReasonLocked} {
		if !strings.Contains(out, want) {
			t.Fatalf("locked palette missing %q:\n%s", want, out)
		}
	}

	m.commandQuery = "definitely-no-command"
	m.recomputeCommandPaletteResults()
	out = stripAnsi(renderCommandPalette(m))
	if !strings.Contains(out, `No commands match "definitely-no-command" — backspace edits the query`) {
		t.Fatalf("no-result text missing:\n%s", out)
	}

	empty := testModelWithSessions()
	empty.width, empty.height = 80, 30
	empty.openCommandPalette()
	empty.commandQuery = "relaunch"
	empty.recomputeCommandPaletteResults()
	out = stripAnsi(renderCommandPalette(empty))
	for _, want := range []string{"Session: none", commands.ReasonNoSession} {
		if !strings.Contains(out, want) {
			t.Fatalf("no-session palette missing %q:\n%s", want, out)
		}
	}
}

// TestCommandPaletteShortcutAlignmentAndSelectedExplanation covers the two
// halves of a browsable row: the command is ONE line with its dashboard key
// against the right edge, and whatever the cursor is on is explained beneath the
// list rather than under the row.
func TestCommandPaletteShortcutAlignmentAndSelectedExplanation(t *testing.T) {
	t.Parallel()

	// The alignment half needs a command that still HAS a shortcut. Restart
	// Session no longer does — `r` is an actions-overlay mnemonic now, and the
	// palette reads its shortcut column from the dashboard binding table, so a
	// retired key leaves the column empty and there is nothing to right-align.
	// Toggle Inspector is one of the direct dashboard bindings.
	m := commandPaletteTestModel(46, 30)
	m.commandQuery = "inspector"
	m.recomputeCommandPaletteResults()
	width := commandPaletteContentWidth(m)
	rows := commandPaletteRows(m, width)
	index := commandPaletteRowFor(rows, 0)
	if index < 0 {
		t.Fatalf("no display row for the only result:\n%s", commandPaletteLayoutText(rows))
	}
	if commandRows := len(rows) - 1; commandRows != len(m.commandResults) {
		t.Fatalf("one command produced %d rows, want exactly one\n%s", commandRows, commandPaletteLayoutText(rows))
	}
	title := rows[index].Text
	if layout.Width(title) != width {
		t.Fatalf("title row width = %d, want right-aligned width %d: %q", layout.Width(title), width, title)
	}
	if !strings.HasSuffix(title, keyInspector) {
		t.Fatalf("title row shortcut is not right-aligned: %q", title)
	}
	for _, row := range rows {
		if got := layout.Width(row.Text); got > width {
			t.Fatalf("display row width = %d, limit %d: %q", got, width, row.Text)
		}
	}

	// A locked row refuses Close Session and permits Unlock Session, so `close`
	// gives the cursor somewhere to move BETWEEN a disabled row and an available
	// one — which is what makes the explanation's ownership observable.
	locked := session.RunningSession{TabID: 9, Base: "api", Project: "api", Profile: "work", Locked: true}
	m = loadSessions(testModelWithSessions(), []session.RunningSession{locked})
	m.width, m.height = 46, 30
	m.openCommandPalette()
	m.commandQuery = "close"
	m.recomputeCommandPaletteResults()
	if m.commandResults[m.commandCursor].ID != commands.ToggleLockID {
		t.Fatalf("close-filtered selection = %q, want the unlock command", m.commandResults[m.commandCursor].ID)
	}
	if out := stripAnsi(renderCommandPalette(m)); strings.Contains(out, commands.ReasonLocked) {
		t.Fatalf("an available selection was explained with another row's refusal:\n%s", out)
	}

	m = pressKey(m, keyUp)
	if m.commandResults[m.commandCursor].ID != commands.CloseSession {
		t.Fatalf("Up landed on %q, want the disabled close command", m.commandResults[m.commandCursor].ID)
	}
	out := stripAnsi(renderCommandPalette(m))
	for _, want := range []string{"Unavailable:", commands.ReasonLocked} {
		if !strings.Contains(out, want) {
			t.Fatalf("selected-command explanation missing %q:\n%s", want, out)
		}
	}

	// The explanation belongs to the SELECTION, so moving off the disabled row
	// has to take its reason with it — a reason that stayed put would be a claim
	// about whatever the cursor landed on.
	m = pressKey(m, keyDown)
	if out := stripAnsi(renderCommandPalette(m)); strings.Contains(out, commands.ReasonLocked) {
		t.Fatalf("reason survived a cursor move off the disabled row:\n%s", out)
	}
}

// TestCommandPaletteScrollingAndResizeClamping windows the display rows the
// group rules are interleaved into, which is why it asks where the SELECTED
// COMMAND's row is rather than treating the cursor as a row number.
func TestCommandPaletteScrollingAndResizeClamping(t *testing.T) {
	t.Parallel()

	m := commandPaletteTestModel(64, 20)
	for i := 0; i < 20; i++ {
		m = pressKey(m, keyDown)
	}
	if m.commandOffset == 0 {
		t.Fatal("navigating through the catalog did not scroll the display-row window")
	}
	assertCommandPaletteSelectionVisible(t, m)

	m.commandOffset = 10_000
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 42, Height: 20})
	m = updated.(Model)
	rows, visible := commandPaletteScrollBounds(m)
	maxOffset := max(len(rows)-visible, 0)
	if m.commandOffset < 0 || m.commandOffset > maxOffset {
		t.Fatalf("resize left offset %d outside [0,%d]", m.commandOffset, maxOffset)
	}
	assertCommandPaletteSelectionVisible(t, m)

	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "quit"}))
	m = updated.(Model)
	if m.commandOffset != 0 || len(m.commandResults) != 1 || m.commandResults[0].ID != commands.Quit {
		t.Fatalf("query change did not reset/clamp results: offset=%d ids=%v", m.commandOffset, commandResultIDs(m.commandResults))
	}
	assertCommandPaletteSelectionVisible(t, m)
}

func TestCommandPaletteModalGeometryAcrossViewports(t *testing.T) {
	t.Parallel()

	for _, size := range []struct {
		name          string
		width, height int
	}{
		{name: "wide", width: 140, height: 40},
		{name: "narrow", width: 48, height: 26},
		{name: "short", width: 100, height: 12},
		{name: "tiny", width: 32, height: 10},
	} {
		t.Run(size.name, func(t *testing.T) {
			m := loadSessions(testModelWithSessions(), makeSessions())
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m = updated.(Model)
			m = pressKey(m, keyColon)

			if issues := detectModalFitIssues(m); len(issues) != 0 {
				t.Fatalf("palette modal issues at %dx%d: %v\n%s", size.width, size.height, issues, stripAnsi(m.render()))
			}
			screen := captureUITestScreen(m, UITestSize{Width: size.width, Height: size.height})
			if len(screen.Issues) != 0 {
				t.Fatalf("palette screen issues at %dx%d: %v\n%s", size.width, size.height, screen.Issues, strings.Join(screen.Lines, "\n"))
			}
			if screen.Height != size.height {
				t.Fatalf("rendered height = %d, want %d", screen.Height, size.height)
			}
			if size.name == "wide" || size.name == "narrow" {
				t.Logf("normalized %s palette:\n%s", size.name, strings.Join(screen.Lines, "\n"))
			}
		})
	}
}

func TestColonRemainsInputOutsideDashboardAndNeverOpensFromNavigation(t *testing.T) {
	t.Parallel()

	search := loadSessions(testModelWithSessions(), makeSessions())
	search = pressKey(search, keySlash)
	search = pressKey(search, keyColon)
	if search.mode != ModeSearch || search.filterText != keyColon {
		t.Fatalf("dashboard filter colon = mode %v text %q", search.mode, search.filterText)
	}

	picker := loadSessions(testModelWithSessions(), makeSessions())
	picker = pressKey(picker, keyL)
	picker = pressKey(picker, keySlash)
	picker = pressKey(picker, keyColon)
	if picker.mode != ModePicker || picker.pickerFilter != keyColon {
		t.Fatalf("picker filter colon = mode %v text %q", picker.mode, picker.pickerFilter)
	}

	sessionInput := testModel()
	sessionInput.mode = ModeSessionSearch
	sessionInput.sessionSearchMode = true
	planInput := testModel()
	planInput.mode = ModePlanSearch
	planInput.planSearchMode = true
	chipInput := testModel()
	chipInput.mode = ModeChipPicker
	chipInput.chipPickerSearchMode = true
	inputModes := []struct {
		name  string
		model Model
		query func(Model) string
	}{
		{name: "session search", model: sessionInput, query: func(m Model) string { return m.sessionQuery }},
		{name: "plan search", model: planInput, query: func(m Model) string { return m.planQuery }},
		{name: "chip picker", model: chipInput, query: func(m Model) string { return m.chipPickerQuery }},
	}
	for _, tc := range inputModes {
		t.Run(tc.name+" input", func(t *testing.T) {
			updated, _ := tc.model.Update(keyPress(keyColon))
			got := updated.(Model)
			if got.mode == ModeCommandPalette || tc.query(got) != keyColon {
				t.Fatalf("colon input = mode %v query %q", got.mode, tc.query(got))
			}
		})
	}

	for _, mode := range []Mode{
		ModeConfirm, ModePicker, ModeConfigPicker, ModeHelp, ModeDetail, ModeActions,
		ModeSessionSearch, ModeSessionView, ModeDeleteChoice, ModeChipPicker,
		ModeMessageDetail, ModePlanSearch, ModePlanView,
		ModeWorktrees,
		ModeLiveSessionPicker,
	} {
		t.Run(uitestModeName(mode)+" navigation", func(t *testing.T) {
			m := testModelWithSessions()
			m.mode = mode
			updated, _ := m.Update(keyPress(keyColon))
			if got := updated.(Model).mode; got == ModeCommandPalette {
				t.Fatalf("colon opened palette from %s", uitestModeName(mode))
			}
		})
	}
}

func TestCommandPaletteUITestObservationReportsModeAndCursor(t *testing.T) {
	t.Parallel()

	m := commandPaletteTestModel(100, 30)
	m.commandCursor = 4
	scrollCommandPaletteToCursor(&m)
	obs := observeUITest(m, UITestSize{Width: 100, Height: 30})
	if obs.Mode != "command_palette" || obs.Cursor != 4 {
		t.Fatalf("palette observation = mode %q cursor %d", obs.Mode, obs.Cursor)
	}
}
