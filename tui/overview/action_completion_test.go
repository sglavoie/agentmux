package overview

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/commands"
)

// TestCompletionRoutingMapsEachOutcomeToItsDestination is the session's
// completion-routing requirement, one case per destination. The division is
// whether the operator ENDED UP somewhere else: a focus, a shell and an editor
// that opened are where they now are, and everything else leaves them here.
func TestCompletionRoutingMapsEachOutcomeToItsDestination(t *testing.T) {
	sessions := viewSessions(4)

	t.Run("a confirmed focus leaves", func(t *testing.T) {
		m, _ := actionModel(t, sessions, 1, hostTabElsewhere)
		m = openMenu(t, m)
		m = press(t, m, "enter")
		if _, cmd := send(t, m, commands.AttachDoneMsg{Switched: true}); !quits(t, cmd) {
			t.Fatal("a confirmed focus did not leave for its destination")
		}
	})

	t.Run("a failed focus stays with the reason", func(t *testing.T) {
		m, _ := actionModel(t, sessions, 1, hostTabElsewhere)
		m = openMenu(t, m)
		m = press(t, m, "enter")
		m, cmd := send(t, m, commands.AttachDoneMsg{Err: errors.New("no matching tab")})
		if quits(t, cmd) {
			t.Fatal("a failed focus left the overview")
		}
		if got := m.feedback(); !strings.Contains(got, "no matching tab") {
			t.Fatalf("the feedback row says %q, want the failure", got)
		}
	})

	for _, name := range []string{"o", "O"} {
		t.Run("an opened "+name+" returns", func(t *testing.T) {
			m, _ := actionModel(t, sessions, 1, hostTabElsewhere)
			m = openMenu(t, m)
			m = press(t, m, name)
			if _, cmd := send(t, m, commands.PickerOpenDoneMsg{}); quits(t, cmd) {
				t.Fatal("a shell or editor that returned closed the overview")
			}
		})
	}

	t.Run("a failed open stays with the reason", func(t *testing.T) {
		m, _ := actionModel(t, sessions, 1, hostTabElsewhere)
		m = openMenu(t, m)
		m = press(t, m, "o")
		m, cmd := send(t, m, commands.PickerOpenDoneMsg{Err: errors.New("no such directory")})
		if quits(t, cmd) {
			t.Fatal("a failed open left the overview")
		}
		if got := m.feedback(); !strings.Contains(got, "no such directory") {
			t.Fatalf("the feedback row says %q, want the failure", got)
		}
	})

	t.Run("a peek stays either way", func(t *testing.T) {
		m, _ := actionModel(t, sessions, 1, hostTabElsewhere)
		m = openMenu(t, m)
		m = press(t, m, "e")
		if _, cmd := send(t, m, commands.PeekDoneMsg{}); quits(t, cmd) {
			t.Fatal("a successful peek left the overview; the pager returns to this grid")
		}
		m, cmd := send(t, m, commands.PeekDoneMsg{Err: errors.New("pager failed")})
		if quits(t, cmd) {
			t.Fatal("a failed peek left the overview")
		}
		if got := m.feedback(); !strings.Contains(got, "pager failed") {
			t.Fatalf("the feedback row says %q, want the failure", got)
		}
	})

	t.Run("a restart stays and refreshes", func(t *testing.T) {
		m, r := actionModel(t, sessions, 1, hostTabElsewhere)
		m = openMenu(t, m)
		m = press(t, m, "r")
		m = press(t, m, "y")
		d, _ := r.lastDispatch()
		reads := len(r.topology)

		m, cmd := send(t, m, commands.ActionDoneMsg{Request: uint64(d.Request)})
		if quits(t, cmd) {
			t.Fatal("a completed restart left the overview")
		}
		if cmd != nil {
			cmd()
		}
		if len(r.topology) != reads+1 {
			t.Fatalf("a completed restart dispatched %d reads, want 1: it changed the tree the grid is drawn from", len(r.topology)-reads)
		}
		if len(m.acts.controller.InFlight()) != 0 {
			t.Fatal("the completion did not settle its own dispatch")
		}
	})

	t.Run("a failed action reports rather than leaving", func(t *testing.T) {
		m, r := actionModel(t, sessions, 1, hostTabElsewhere)
		m = openMenu(t, m)
		m = press(t, m, "r")
		m = press(t, m, "y")
		d, _ := r.lastDispatch()
		m, cmd := send(t, m, commands.ActionDoneMsg{Request: uint64(d.Request), Err: errors.New("tmux kill-session: no match")})
		if quits(t, cmd) {
			t.Fatal("a failed action left the overview")
		}
		if got := m.feedback(); !strings.Contains(got, "no match") {
			t.Fatalf("the feedback row says %q, want the failure and its reason", got)
		}
	})

	t.Run("an uncorrelated completion changes nothing", func(t *testing.T) {
		m, _ := actionModel(t, sessions, 1, hostTabElsewhere)
		before := m.feedback()
		m, cmd := send(t, m, commands.ActionDoneMsg{Request: 999})
		if cmd != nil {
			t.Fatal("a completion for nothing in flight started a command")
		}
		if got := m.feedback(); got != before {
			t.Fatalf("an uncorrelated completion changed the feedback row to %q", got)
		}
	})
}

// TestADestructiveActionAimedAtTheHostingTabExplainsItself. The action is still
// offered — restarting the session the overview was raised beside is an ordinary
// thing to do — but the sentence is owed before the keystroke.
func TestADestructiveActionAimedAtTheHostingTabExplainsItself(t *testing.T) {
	sessions := viewSessions(3)
	m, _ := actionModel(t, sessions, 1, sessions[1].TabID)

	m = openMenu(t, m)
	m = press(t, m, "X")
	if m.acts.surface != surfaceConfirm {
		t.Fatalf("closing the hosting tab is not offered at all (surface %v)", m.acts.surface)
	}
	if !strings.Contains(m.acts.confirmConsequence, hostTabConsequence) {
		t.Fatalf("the consequence is %q, want it to warn that this overview closes with the tab", m.acts.confirmConsequence)
	}
	if got := m.overlayView(); !strings.Contains(got, "overview") {
		t.Fatalf("the rendered prompt does not carry the warning:\n%s", got)
	}

	// And a close aimed anywhere else keeps the ordinary wording.
	other, _ := actionModel(t, sessions, 1, hostTabElsewhere)
	other = openMenu(t, other)
	other = press(t, other, "X")
	if strings.Contains(other.acts.confirmConsequence, hostTabConsequence) {
		t.Fatalf("an ordinary close warns about the host tab: %q", other.acts.confirmConsequence)
	}
}
