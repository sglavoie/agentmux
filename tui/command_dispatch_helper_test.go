// This file is the model-level counterpart of the harness's `command` scenario
// action: it reaches a capability by name through the palette's own keys, for
// tests that hold a Model directly rather than a driver.
//
// It exists so a test whose subject is another screen says which capability
// took it there instead of which letter did. A dashboard shortcut is one
// spelling of a command and is being re-spelled; a test that hard-codes the
// letter merely to arrive somewhere breaks on the day the letter moves, and
// reports it as a failure of the screen it was actually about. Tests whose
// subject IS the binding keep pressing the literal key — that is what still
// detects an incorrect one.

package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
)

// runDashboardCommand runs the catalog command id from the dashboard the way an
// operator reaching it by name does: ':' to open the palette, the palette's own
// navigation keys to reach the row, enter to run it. It returns the model and
// the command enter produced, so a caller that asserts on the effect keeps
// asserting on the effect.
//
// Every refusal is a t.Fatalf rather than a returned model, for the same reason
// the driver's Command method diagnoses them separately: an unreachable, hidden,
// or disabled command that quietly returned an unchanged model would leave the
// caller asserting against a dashboard it never left.
func runDashboardCommand(t *testing.T, m Model, id commands.ID) (Model, tea.Cmd) {
	t.Helper()
	if m.mode != ModeList {
		t.Fatalf("command %q is unreachable from mode %q: the palette opens from the session list", id, uitestModeName(m.mode))
	}
	m = pressKey(m, keyColon)
	if m.mode != ModeCommandPalette {
		t.Fatalf("pressing %q left the model in mode %q instead of opening the command palette", keyColon, uitestModeName(m.mode))
	}
	index, err := uitestCommandRowIndex(m.commandResults, id, uitestCommandIDSet()[string(id)])
	if err != nil {
		t.Fatal(err)
	}
	for steps := 0; m.commandCursor != index; steps++ {
		if steps > len(m.commandResults) {
			t.Fatalf("palette cursor stopped at %d walking to row %d", m.commandCursor, index)
		}
		key := keyDown
		if m.commandCursor > index {
			key = keyUp
		}
		m = pressKey(m, key)
	}
	candidate := m.commandResults[index]
	if availability := candidate.AvailabilityFor(m, m.commandContext); !availability.Available {
		t.Fatalf("command %q is disabled for the current selection: %s", id, availability.Reason)
	}
	updated, cmd := m.Update(keyPress(keyEnter))
	next := updated.(Model)
	if next.mode == ModeCommandPalette {
		t.Fatalf("command %q left the palette open, so enter did not run it", id)
	}
	return next, cmd
}

// pressDashboardCommand is runDashboardCommand for a caller that only wants the
// resulting model, mirroring pressKey's relationship to Model.Update.
func pressDashboardCommand(t *testing.T, m Model, id commands.ID) Model {
	t.Helper()
	next, _ := runDashboardCommand(t, m, id)
	return next
}
