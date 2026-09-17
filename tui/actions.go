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
	"github.com/sglavoie/agentmux/internal/session"
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
			Band:        definition.MenuGroup.String(),
			KeyDisplay:  definition.Mnemonic.Display,
			KeyMatch:    definition.Mnemonic.Key,
			Label:       definition.MenuLabel,
			Description: definition.MenuDescription,
			Availability: func(m Model) commands.Availability {
				return definition.AvailabilityFor(sessionActionEnv(m), overlayTarget(m))
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

// exitActionsMode leaves the menu for the list. There is one destination rather
// than a remembered one: the menu is only ever opened from the list, and the
// field that used to carry a return mode was written by every exit and read by
// nothing.
func (m *Model) exitActionsMode() {
	m.mode = ModeList
}

// openActionsMenu captures the row the menu will act on and puts the cursor on
// the entry sessionactions says a fresh menu opens with. Both entry points into
// ModeActions go through it, so "which entry is selected when the menu opens" is
// answered once rather than wherever a mode assignment happens to be written.
func (m *Model) openActionsMenu(target session.RunningSession) {
	m.targetSession = target
	m.actionErr = nil
	m.actionsOffset = 0
	m.actionsCursor = sessionactions.MenuIndexOf(sessionactions.InitialSelection())
	m.mode = ModeActions
}

// This file is also the seam between the root Bubble Tea model and the actions-
// overlay half of tui/commands. It projects the model onto the leaf's input
// value and re-exposes the leaf's geometry, scrolling, and renderers under the
// names the rest of the root package already uses. Deciding what a key in the
// overlay DOES stays with the runners above and model_modes.go.

// actionsInput charges the root's own chrome against the body height before
// handing it to the leaf, which knows nothing about the header, footer, or
// status bar surrounding the modal.
//
// The qualifier is measured here rather than in the leaf because a name
// collision is a property of the LIVE SET and the leaf can see one session. Two
// rows called `api` offer the same twelve actions, and the menu's header is the
// only place it says which of them is about to be closed.
func actionsInput(m Model) commands.ActionsInput[Model] {
	return commands.ActionsInput[Model]{
		Model:      m,
		Actions:    actionsFor(m),
		Title:      dashboard.SessionDisplayTitle(m.targetSession),
		Qualifier:  dashboard.NewNameDisambiguator(m.sessions).Qualifier(m.targetSession),
		Context:    dashboard.SessionContextLine(m.targetSession),
		Cursor:     m.actionsCursor,
		Offset:     m.actionsOffset,
		Width:      m.width,
		BodyHeight: rootBodyHeight(m),
	}
}

func renderActions(m Model) string { return commands.RenderActions(actionsInput(m)) }

func renderActionsBox(m Model) string { return commands.RenderActionsBox(actionsInput(m)) }

// clampActionsSelection confines the cursor to the entries that exist and then
// scrolls the window to hold it. The two are done together, in that order,
// because a cursor the window has not been told about is the disappearing
// selection this menu is required not to have: every path that moves the cursor
// or resizes the terminal ends here.
func clampActionsSelection(m *Model) {
	if m.mode != ModeActions {
		m.actionsCursor = 0
		m.actionsOffset = 0
		return
	}
	in := actionsInput(*m)
	m.actionsCursor = in.ClampCursor()
	in.Cursor = m.actionsCursor
	m.actionsOffset = in.ScrollToCursor()
}
