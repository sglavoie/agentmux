package tui

// This file is the command palette's controller: what opening, typing,
// navigating, and pressing enter DO to the root model. Matching, measuring,
// wrapping, scrolling, and painting live in tui/commands and are reached
// through command_palette_view.go.

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
)

// openCommandPalette captures the current dashboard target and initializes a
// fresh result set for the dashboard ':' binding.
func (m *Model) openCommandPalette() {
	m.clearJumpChord()
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
		if m.commandCursor > 0 {
			m.commandCursor--
		}
		scrollCommandPaletteToCursor(&m)
		return m, nil

	case keyDown, keyCtrlJ, keyCtrlN:
		if m.commandCursor+1 < len(m.commandResults) {
			m.commandCursor++
		}
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
