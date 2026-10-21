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
		t.Fatalf("command %q is unreachable from mode %q: the palette opens from the session list", id, m.mode.String())
	}
	m = pressKey(m, keyColon)
	if m.mode != ModeCommandPalette {
		t.Fatalf("pressing %q left the model in mode %q instead of opening the command palette", keyColon, m.mode.String())
	}
	index := paletteRowIndex(t, m.commandResults, id)
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

// paletteRowIndex finds the one palette row carrying id, and tells an id the
// catalog declares but hides for this selection apart from one that does not
// exist, the same three refusals the harness's command action reports.
func paletteRowIndex(t *testing.T, rows []commands.Command[Model], id commands.ID) int {
	t.Helper()
	found, matches := -1, 0
	for i, row := range rows {
		if row.ID != id {
			continue
		}
		matches++
		if found < 0 {
			found = i
		}
	}
	switch {
	case matches > 1:
		t.Fatalf("command %q is ambiguous: %d palette rows carry that id", id, matches)
	case matches == 1:
		return found
	}
	for _, candidate := range commandCatalog() {
		if candidate.ID == id {
			t.Fatalf("command %q is hidden for the current selection, so the palette offers no row for it", id)
		}
	}
	t.Fatalf("no command with id %q exists", id)
	return -1
}

// pressDashboardCommand is runDashboardCommand for a caller that only wants the
// resulting model, mirroring pressKey's relationship to Model.Update.
func pressDashboardCommand(t *testing.T, m Model, id commands.ID) Model {
	t.Helper()
	next, _ := runDashboardCommand(t, m, id)
	return next
}

// runRowAction reaches a per-row capability the way an operator does now: `a` to
// open the actions overlay on the cursor row, then the entry's mnemonic. It is
// the overlay's counterpart to runDashboardCommand, and it exists for the same
// reason — the mnemonics used to be dashboard keys, and a test that still
// pressed one of them directly would be asserting against a dashboard that
// dropped the keystroke rather than against the capability it meant to reach.
//
// The overlay is where the availability rule lives, so a blocked entry fails
// here rather than silently returning an unchanged model.
func runRowAction(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	if m.mode != ModeList {
		t.Fatalf("the actions overlay is unreachable from mode %q: it opens from the session list", m.mode.String())
	}
	m = pressKey(m, keyA)
	if m.mode != ModeActions {
		t.Fatalf("pressing %q left the model in mode %q instead of opening the actions overlay", keyA, m.mode.String())
	}
	for _, a := range actionsFor(m) {
		if a.KeyMatch != key {
			continue
		}
		if !a.Available(m) {
			t.Fatalf("action %q (%s) is blocked for the current row", key, a.Label)
		}
		updated, cmd := m.Update(keyPress(key))
		return updated.(Model), cmd
	}
	t.Fatalf("no actions-overlay entry is bound to %q", key)
	return m, nil
}

// drainMsgs runs cmd and everything a tea.Batch it produced holds, returning
// the messages in dispatch order.
//
// It exists because a lifecycle action's command is the effect batched with the
// toast that reports the effect is in flight. A test that ran the command and
// read its single message used to be reading the effect; it would now be reading
// whichever of the two Bubble Tea happened to put first.
func drainMsgs(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, inner := range msg {
			out = append(out, drainMsgs(t, inner)...)
		}
		return out
	case nil:
		return nil
	default:
		return []tea.Msg{msg}
	}
}

// actionDoneFrom is drainMsgs for the one message a caller usually wants.
func actionDoneFrom(t *testing.T, cmd tea.Cmd) commands.ActionDoneMsg {
	t.Helper()
	for _, msg := range drainMsgs(t, cmd) {
		if done, ok := msg.(commands.ActionDoneMsg); ok {
			return done
		}
	}
	t.Fatalf("no commands.ActionDoneMsg among the messages the command produced")
	return commands.ActionDoneMsg{}
}

// pressRowAction is runRowAction for a caller that only wants the model.
func pressRowAction(t *testing.T, m Model, key string) Model {
	t.Helper()
	next, _ := runRowAction(t, m, key)
	return next
}
