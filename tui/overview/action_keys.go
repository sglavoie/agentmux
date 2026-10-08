// What a key does while a modal is over the grid.
//
// handleKeyPress in update.go picks one of these three before it ever consults
// the grid's own table, so every key an overlay does not claim is SWALLOWED
// rather than falling through. That is deliberate and it is the modal contract:
// an overlay that let unclaimed keys reach the surface underneath would toggle
// the layout, move the selection, or focus a tile the operator cannot see, and
// then draw itself over the result.
//
// Each handler binds ctrl+c to leaving the whole surface rather than to leaving
// the overlay. The other two dismissal keys are the overlay's own — esc backs
// out of the menu, the picker, and the prompt — but ctrl+c is the operator
// asking for the program, and a modal that answered it by closing itself would
// need a second press for every layer they happened to be under.

package overview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// handleMenuKey drives the per-row actions menu.
//
// The two layers are ordered the way the shared vocabulary describes them: the
// selection layer takes the execute key first, so moving the cursor and pressing
// enter runs the entry under it, and only then is the key offered to the
// mnemonic scan, which runs an entry wherever the cursor happens to be.
func (m model) handleMenuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	in := menuInput(m)
	page := in.PageSize()
	move := func(delta int) (tea.Model, tea.Cmd) {
		in.Cursor = in.MoveCursor(delta)
		m.acts.menuCursor = in.Cursor
		m.acts.menuOffset = in.ScrollToCursor()
		return m, nil
	}

	switch key {
	case "ctrl+c":
		return m.dismiss()
	case "esc", "q":
		m.closeActionsMenu()
		return m, nil
	case "j", "down":
		return move(1)
	case "k", "up":
		return move(-1)
	case "pgdown", "ctrl+f":
		return move(page)
	case "pgup", "ctrl+b":
		return move(-page)
	case "ctrl+d":
		return move(page / 2)
	case "ctrl+u":
		return move(-page / 2)
	case "g":
		return move(-len(in.Actions))
	case "G":
		return move(len(in.Actions))
	case commands.MenuExecuteKey:
		if action, ok := in.SelectedAction(); ok {
			return m.runMenuAction(action)
		}
		return m, nil
	}

	for _, action := range in.Actions {
		if action.ImmediateKey() == key {
			return m.runMenuAction(action)
		}
	}
	return m, nil
}

// runMenuAction refuses an entry the live row cannot run and dispatches the rest.
//
// The refusal is answered HERE as well as by the controller because the two
// refusals are about different things: the controller re-checks the row at
// dispatch, and this reports the reason the menu is already drawing beside a
// dimmed entry, which is what an operator who pressed its mnemonic anyway is
// owed.
func (m model) runMenuAction(action commands.Action[model]) (tea.Model, tea.Cmd) {
	availability := action.AvailabilityFor(m)
	if !availability.Available {
		if availability.Reason != "" {
			m = m.withReport(availability.Reason, reportWindow)
		}
		return m, nil
	}
	next, cmd := action.Run(m)
	return next, cmd
}

// handleProfileKey drives the profile picker the two profile-taking actions
// raise. Choosing answers the controller; esc cancels back to the menu the
// action was chosen from.
func (m model) handleProfileKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	accounts := m.profileAccounts()
	switch msg.String() {
	case "ctrl+c":
		return m.dismiss()
	case "esc", "q":
		return m.cancelAction()
	case "j", "down":
		m.acts.profileCursor = clamp(m.acts.profileCursor+1, 0, max(len(accounts)-1, 0))
		return m, nil
	case "k", "up":
		m.acts.profileCursor = clamp(m.acts.profileCursor-1, 0, max(len(accounts)-1, 0))
		return m, nil
	case "enter":
		if m.acts.profileCursor < 0 || m.acts.profileCursor >= len(accounts) {
			return m, nil
		}
		updated, step, cmd := actionController(m).ChooseProfile(m, accounts[m.acts.profileCursor].Name)
		next, cmd := updated.applyActionStep(step, cmd)
		return next, cmd
	}
	return m, nil
}

// handleConfirmKey answers the destructive confirmation. Only y proceeds; n and
// esc back out to wherever the action was chosen from.
func (m model) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m.dismiss()
	case "y":
		updated, step, cmd := actionController(m).Confirm(m)
		next, cmd := updated.applyActionStep(step, cmd)
		return next, cmd
	case "n", "esc", "q":
		return m.cancelAction()
	}
	return m, nil
}

// cancelAction backs out of the nested interaction the controller is waiting on.
//
// A cancellation the controller does not recognize still leaves the overlay,
// because the overlay is on screen: a surface that refused to close because the
// state behind it had already been settled would be a modal with no way out.
func (m model) cancelAction() (tea.Model, tea.Cmd) {
	updated, step := actionController(m).Cancel(m)
	if step.Kind != sessionactions.StepCancelled {
		m.acts.surface = surfaceGrid
		return m, nil
	}
	next, cmd := updated.applyActionStep(step, nil)
	return next, cmd
}
