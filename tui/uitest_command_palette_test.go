//go:build tuitest

package tui

// The command palette checked through the scenario harness: its modal-fit
// detector and its observation of the live model.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

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
