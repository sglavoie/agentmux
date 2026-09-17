// This file is the per-row actions overlay: the runner each shared action
// identity dispatches to in this model, and the root-named renderers that paint
// the overlay through the shared tui/commands widget.
//
// What each entry is CALLED, which key it answers, which band it sits in, and
// whether the captured row can run it are not stated here: they are read from
// tui/sessionactions, the one declaration the palette reads too. What is left
// here is what a choice DOES to this model, which is the half that cannot leave
// the root because it closes over the root's own fields and modes.

package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// sessionActionEnv projects the live model state every shared availability rule
// reads onto the leaf's value type. Both entry points call it — the overlay
// below and the palette's session commands in command_catalog.go — so the two
// surfaces cannot answer "can this row run this action" from different facts.
func sessionActionEnv(m Model) sessionactions.Env {
	return sessionactions.Env{
		Config:        m.cfg,
		LastActivity:  m.lastActivity,
		IdleThreshold: m.idleThreshold,
	}
}

// overlayTarget is the row the overlay acts on: the target captured when `a`
// opened it. It is read live rather than captured once per rendered frame,
// which is the behavior the availability closures below already had.
func overlayTarget(m Model) sessionactions.Target {
	return sessionactions.Capture(m.targetSession)
}

// actionsFor returns the ordered list of actions exposed in the overlay for
// the current target row (m.targetSession), in the shared vocabulary's menu
// order. Every fact but the runner comes from tui/sessionactions; the
// availability functions still take the model so they can read live state
// (lastActivity, idle threshold, configured project) at the moment they are
// asked.
//
// A definition with no runner is dropped rather than rendered as a dead row.
// That case is a programming error, not a state the dashboard can reach, and
// TestOverlayOffersEveryMenuDefinition fails on it.
func actionsFor(_ Model) []commands.Action[Model] {
	runners := rowActionRunners()
	definitions := sessionactions.MenuDefinitions()
	actions := make([]commands.Action[Model], 0, len(definitions))
	for _, definition := range definitions {
		run, ok := runners[definition.ID]
		if !ok {
			continue
		}
		actions = append(actions, commands.Action[Model]{
			ID:          definition.ID,
			KeyDisplay:  definition.Mnemonic.Display,
			KeyMatch:    definition.Mnemonic.Key,
			Label:       definition.MenuLabel,
			Description: definition.MenuDescription,
			Available: func(m Model) bool {
				return definition.AvailabilityFor(sessionActionEnv(m), overlayTarget(m)).Available
			},
			Run: run,
		})
	}
	return actions
}

// rowActionRunners is what an overlay entry DOES, keyed by the shared identity.
//
// Every entry is the same call: hand the identity and the row the overlay was
// opened against to the shared controller. What used to be here — the
// confirm_destructive branch, the picker fields, the exitActionsMode bookkeeping
// — was the half of each runner that the palette also spelled, differently. It
// lives in tui/sessionactions now, and the effects it dispatches to live in
// session_action_controller.go, which is the one table both surfaces read.
//
// The map is still keyed by identity rather than derived from the effect table
// because actionsFor drops a definition with no runner: what is offered stays a
// question this file answers.
func rowActionRunners() map[commands.ID]func(Model) (Model, tea.Cmd) {
	effects := sessionActionRunners()
	runners := make(map[commands.ID]func(Model) (Model, tea.Cmd), len(effects))
	for _, definition := range sessionactions.MenuDefinitions() {
		if _, ok := effects[definition.ID]; !ok {
			continue
		}
		action := definition.ID
		runners[action] = func(m Model) (Model, tea.Cmd) {
			m.pendingActionOrigin = menuActionOrigin(m)
			return m.runSessionAction(action, overlayTarget(m))
		}
	}
	return runners
}

func (m *Model) exitActionsMode() {
	m.mode = ModeList
	m.actionsReturnMode = ModeList
}

// This file is also the seam between the root Bubble Tea model and the actions-
// overlay half of tui/commands. It projects the model onto the leaf's input
// value and re-exposes the leaf's geometry, scrolling, and renderers under the
// names the rest of the root package already uses. Deciding what a key in the
// overlay DOES stays with the runners above and model_modes.go.

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
