package tui

import (
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

func commandPaletteLayoutText(palette commands.PaletteLayout) string {
	lines := make([]string, len(palette.Rows))
	for i, row := range palette.Rows {
		lines[i] = row.Text
	}
	return strings.Join(lines, "\n")
}

func assertCommandPaletteSelectionVisible(t *testing.T, m Model) {
	t.Helper()
	palette, visible := commandPaletteScrollBounds(m)
	if m.commandCursor < 0 || m.commandCursor >= len(palette.Spans) {
		t.Fatalf("cursor %d outside %d command spans", m.commandCursor, len(palette.Spans))
	}
	span := palette.Spans[m.commandCursor]
	if span.End-span.Start > visible {
		t.Fatalf("test viewport holds %d rows but selected command needs %d", visible, span.End-span.Start)
	}
	if span.Start < m.commandOffset || span.End > m.commandOffset+visible {
		t.Fatalf("selected span [%d,%d) outside window [%d,%d)",
			span.Start, span.End, m.commandOffset, m.commandOffset+visible)
	}
}

func TestCommandPaletteFullAndFilteredLayouts(t *testing.T) {
	m := commandPaletteTestModel(100, 40)
	palette := commandPaletteResultLayout(m, commandPaletteContentWidth(m))
	text := commandPaletteLayoutText(palette)
	for _, group := range []string{"Session", "Explore", "App"} {
		count := 0
		for _, row := range palette.Rows {
			if row.GroupHeader && row.Text == group {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("full layout group %q count = %d, want 1\n%s", group, count, text)
		}
	}

	m.commandQuery = "quit"
	m.recomputeCommandPaletteResults()
	palette = commandPaletteResultLayout(m, commandPaletteContentWidth(m))
	text = commandPaletteLayoutText(palette)
	if !strings.Contains(text, "App") || !strings.Contains(text, "Quit") {
		t.Fatalf("filtered layout missing App/Quit:\n%s", text)
	}
	for _, hidden := range []string{"Session", "Explore"} {
		if strings.Contains(text, hidden) {
			t.Fatalf("filtered layout leaked empty %s header:\n%s", hidden, text)
		}
	}
}

func TestCommandPaletteRendersContextDisabledNoResultsAndNoSession(t *testing.T) {
	locked := session.RunningSession{
		TabID: 7, Base: "api", Project: "api", Profile: "work", State: "bolt", Locked: true,
	}
	m := loadSessions(testModelWithSessions(), []session.RunningSession{locked})
	m.width, m.height = 80, 30
	m.openCommandPalette()
	m.commandQuery = "restart"
	m.recomputeCommandPaletteResults()
	out := stripAnsi(renderCommandPalette(m))
	for _, want := range []string{"Command Palette", "Session: api", ":restart" + commands.PaletteCursorGlyph, "Restart Session", commands.ReasonLocked} {
		if !strings.Contains(out, want) {
			t.Fatalf("locked palette missing %q:\n%s", want, out)
		}
	}

	m.commandQuery = "definitely-no-command"
	m.recomputeCommandPaletteResults()
	out = stripAnsi(renderCommandPalette(m))
	if !strings.Contains(out, `No commands match "definitely-no-command".`) {
		t.Fatalf("no-result text missing:\n%s", out)
	}

	empty := testModelWithSessions()
	empty.width, empty.height = 80, 30
	empty.openCommandPalette()
	empty.commandQuery = "restart"
	empty.recomputeCommandPaletteResults()
	out = stripAnsi(renderCommandPalette(empty))
	for _, want := range []string{"Session: none", commands.ReasonNoSession} {
		if !strings.Contains(out, want) {
			t.Fatalf("no-session palette missing %q:\n%s", want, out)
		}
	}
}

func TestCommandPaletteShortcutAlignmentAndWrappedDetail(t *testing.T) {
	m := commandPaletteTestModel(46, 30)
	m.commandQuery = "restart"
	m.recomputeCommandPaletteResults()
	width := commandPaletteContentWidth(m)
	palette := commandPaletteResultLayout(m, width)
	span := palette.Spans[0]
	if span.End-span.Start < 3 {
		t.Fatalf("narrow command span has %d rows, want wrapped title plus detail rows", span.End-span.Start)
	}
	title := palette.Rows[span.Start].Text
	if layout.Width(title) != width {
		t.Fatalf("title row width = %d, want right-aligned width %d: %q", layout.Width(title), width, title)
	}
	if !strings.HasSuffix(title, keyR) {
		t.Fatalf("title row shortcut is not right-aligned: %q", title)
	}
	for _, row := range palette.Rows {
		if got := layout.Width(row.Text); got > width {
			t.Fatalf("display row width = %d, limit %d: %q", got, width, row.Text)
		}
	}

	locked := session.RunningSession{TabID: 9, Base: "api", Project: "api", Profile: "work", Locked: true}
	m = loadSessions(testModelWithSessions(), []session.RunningSession{locked})
	m.width, m.height = 46, 30
	m.openCommandPalette()
	m.commandQuery = "restart"
	m.recomputeCommandPaletteResults()
	palette = commandPaletteResultLayout(m, commandPaletteContentWidth(m))
	detail := commandPaletteLayoutText(palette)
	for _, want := range []string{"unavailable:", commands.ReasonLocked} {
		if !strings.Contains(detail, want) {
			t.Fatalf("wrapped disabled detail missing %q:\n%s", want, detail)
		}
	}
}

func TestCommandPaletteWrappedRowScrollingAndResizeClamping(t *testing.T) {
	m := commandPaletteTestModel(64, 20)
	for i := 0; i < 12; i++ {
		m = pressKey(m, keyDown)
	}
	if m.commandOffset == 0 {
		t.Fatal("navigating through wrapped commands did not scroll the display-row window")
	}
	assertCommandPaletteSelectionVisible(t, m)

	m.commandOffset = 10_000
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 42, Height: 20})
	m = updated.(Model)
	palette, visible := commandPaletteScrollBounds(m)
	maxOffset := max(len(palette.Rows)-visible, 0)
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
	m := commandPaletteTestModel(100, 30)
	m.commandCursor = 4
	scrollCommandPaletteToCursor(&m)
	obs := observeUITest(m, UITestSize{Width: 100, Height: 30})
	if obs.Mode != "command_palette" || obs.Cursor != 4 {
		t.Fatalf("palette observation = mode %q cursor %d", obs.Mode, obs.Cursor)
	}
}
