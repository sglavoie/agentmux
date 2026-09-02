package tui

// This file is the seam between the root Bubble Tea model and the actions-
// overlay half of tui/commands. It projects the model onto the leaf's input
// value and re-exposes the leaf's geometry, scrolling, and renderers under the
// names the rest of the root package already uses. Deciding what a key in the
// overlay DOES stays with actions.go and model_modes.go.

import (
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// actionsInput charges the root's own chrome against the body height before
// handing it to the leaf, which knows nothing about the header, footer, or
// status bar surrounding the modal.
func actionsInput(m Model) commands.ActionsInput[Model] {
	return commands.ActionsInput[Model]{
		Model:      m,
		Actions:    actionsFor(m),
		Title:      dashboard.SessionDisplayTitle(m.targetSession),
		Offset:     m.actionsOffset,
		Width:      m.width,
		BodyHeight: rootBodyHeight(m),
	}
}

func renderActions(m Model, descWidth int) string {
	return commands.RenderActions(actionsInput(m), descWidth)
}

func renderActionsBox(m Model) string { return commands.RenderActionsBox(actionsInput(m)) }

func actionsScrollBounds(m Model) (total, visible int) { return actionsInput(m).ScrollBounds() }

func clampActionsOffset(m *Model) {
	if m.mode != ModeActions {
		m.actionsOffset = 0
		return
	}
	m.actionsOffset = actionsInput(*m).ClampOffset()
}
