package tui

// This file is the seam between the root Bubble Tea model and the palette half
// of tui/commands. It projects the model onto the leaf's input value and
// re-exposes the leaf's geometry, scrolling, and renderers under the names the
// rest of the root package already uses. Everything here is projection;
// deciding what a keypress DOES stays in command_palette.go.

import "github.com/sglavoie/agentmux/tui/commands"

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

func commandPaletteResultLayout(m Model, width int) commands.PaletteLayout {
	return commandPaletteInput(m).ResultLayout(width)
}

func commandPaletteScrollBounds(m Model) (commands.PaletteLayout, int) {
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
