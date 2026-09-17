// This file is the root's seam onto the command palette: the model projection
// the overlay renders from, and the root-named renderers the rest of the
// package and its tests call. Matching, layout, and painting live in
// tui/commands; deciding what the palette does to the model stays here.

package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/tui/commands"
)

// This file is the command palette's controller: what opening, typing,
// navigating, and pressing enter DO to the root model. Matching, measuring,
// wrapping, scrolling, and painting live in tui/commands and are reached
// through command_palette_view.go.

// openCommandPalette captures the current dashboard target and initializes a
// fresh result set for the dashboard ':' binding.
func (m *Model) openCommandPalette() {
	m.commandQuery = ""
	m.commandCursor = 0
	m.commandOffset = 0
	m.commandContext = captureCommandContext(*m)
	m.mode = ModeCommandPalette
	m.recomputeCommandPaletteResults()
}

// closeCommandPalette returns to the dashboard and zeros every transient
// palette field. Callers that execute a command must copy its context first.
func (m *Model) closeCommandPalette() {
	m.mode = ModeList
	m.commandQuery = ""
	m.commandResults = nil
	m.commandCursor = 0
	m.commandOffset = 0
	m.commandContext = commands.Context{}
}

// recomputeCommandPaletteResults refreshes matching rows after a query edit
// and restores deterministic selection and scroll state.
func (m *Model) recomputeCommandPaletteResults() {
	m.commandResults = commands.PaletteResults(commandCatalog(), *m, m.commandContext, m.commandQuery)
	m.commandCursor = commands.FirstAvailableIndex(m.commandResults, *m, m.commandContext)
	m.commandOffset = 0
	scrollCommandPaletteToCursor(m)
}

// handleCommandPaletteKey controls the palette state machine. Printable
// dashboard shortcuts are query text here; only the explicit control keys
// below navigate, close, execute, or quit.
func (m Model) handleCommandPaletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit

	case keyEsc:
		m.closeCommandPalette()
		return m, nil

	case keyEnter:
		if m.commandCursor < 0 || m.commandCursor >= len(m.commandResults) {
			return m, nil
		}
		candidate := m.commandResults[m.commandCursor]
		ctx := m.commandContext
		if !candidate.IsVisible(m, ctx) || !candidate.AvailabilityFor(m, ctx).Available {
			return m, nil
		}
		// The query and the row it was chosen on are captured before the palette
		// tears them down. A session command that opens a picker or a
		// confirmation can be cancelled, and by then there is nothing left in the
		// model to return to; the capture is what "cancel puts me back where I
		// was" is restored from. A command that dispatches at once never reads it,
		// and runSessionAction clears it either way.
		m.pendingActionOrigin = paletteActionOrigin(m)
		m.closeCommandPalette()
		return candidate.Run(m, ctx)

	case keyBackspace:
		runes := []rune(m.commandQuery)
		if len(runes) == 0 {
			return m, nil
		}
		m.commandQuery = string(runes[:len(runes)-1])
		m.recomputeCommandPaletteResults()
		return m, nil

	case keyUp, keyCtrlK, keyCtrlP:
		m.commandCursor = commandPaletteInput(m).MoveCursor(-1)
		scrollCommandPaletteToCursor(&m)
		return m, nil

	case keyDown, keyCtrlJ, keyCtrlN:
		m.commandCursor = commandPaletteInput(m).MoveCursor(1)
		scrollCommandPaletteToCursor(&m)
		return m, nil

	default:
		if text := msg.Key().Text; text != "" {
			m.commandQuery += text
			m.recomputeCommandPaletteResults()
		}
		return m, nil
	}
}

// This file is the seam between the root Bubble Tea model and the palette half
// of tui/commands. It projects the model onto the leaf's input value and
// re-exposes the leaf's geometry, scrolling, and renderers under the names the
// rest of the root package already uses. Everything here is projection;
// deciding what a keypress DOES stays in command_palette.go.

// commandPaletteInput charges the root's own chrome against the body height
// before handing it to the leaf, which knows nothing about the header, footer,
// or status bar surrounding the modal.
func commandPaletteInput(m Model) commands.PaletteInput[Model] {
	return commands.PaletteInput[Model]{
		Model:      m,
		Query:      m.commandQuery,
		Results:    m.commandResults,
		Cursor:     m.commandCursor,
		Offset:     m.commandOffset,
		Context:    m.commandContext,
		Width:      m.width,
		Height:     m.height,
		BodyHeight: rootBodyHeight(m),
	}
}

func commandPaletteContentWidth(m Model) int { return commandPaletteInput(m).ContentWidth() }

func commandPaletteRows(m Model, width int) []commands.PaletteRow {
	return commandPaletteInput(m).Rows(width)
}

func commandPaletteScrollBounds(m Model) ([]commands.PaletteRow, int) {
	return commandPaletteInput(m).ScrollBounds()
}

func renderCommandPalette(m Model) string { return commands.RenderPalette(commandPaletteInput(m)) }

func renderCommandPaletteBox(m Model) string {
	return commands.RenderPaletteBox(commandPaletteInput(m))
}

func clampCommandPaletteOffset(m *Model) {
	if m.mode != ModeCommandPalette {
		return
	}
	m.commandOffset = commandPaletteInput(*m).ClampOffset()
}

func scrollCommandPaletteToCursor(m *Model) {
	if m.mode != ModeCommandPalette {
		return
	}
	m.commandOffset = commandPaletteInput(*m).ScrollToCursor()
}
