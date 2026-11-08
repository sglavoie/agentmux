package overview

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/commands"
)

// TestBackgroundWorkContinuesWhileAMenuIsOpen is the "bounded topology and
// preview work continues" requirement. A modal is over the grid, not instead of
// it: the rows behind it must go on refreshing, the tiles behind it must go on
// being captured, and the ticker must go on being re-armed.
func TestBackgroundWorkContinuesWhileAMenuIsOpen(t *testing.T) {
	sessions := viewSessions(6)
	m, r := actionModel(t, sessions, 1, hostTabElsewhere)
	m = openMenu(t, m)
	captured := m.acts.target.Session.TabID
	reads, ticks, captures := len(r.topology), len(r.ticks), len(r.captures)

	m = only(t, m, commands.TickMsg{})
	if len(r.ticks) != ticks+1 {
		t.Fatalf("the ticker was re-armed %d times behind the menu, want 1", len(r.ticks)-ticks)
	}
	if len(r.topology) != reads+1 {
		t.Fatalf("%d reads were dispatched behind the menu, want 1", len(r.topology)-reads)
	}

	updated := viewSessions(6)
	for i := range updated {
		updated[i].State = "key"
	}
	m = only(t, m, hosted(snapshot(m.refreshActiveSeq, 0, updated), hostTabElsewhere))
	if m.sessions[m.selected].State != "key" {
		t.Fatal("a read that landed behind the menu did not reach the list")
	}
	if m.acts.surface != surfaceMenu || m.acts.target.Session.TabID != captured {
		t.Fatalf("a read behind the menu disturbed it (surface %v, target tab %d)", m.acts.surface, m.acts.target.Session.TabID)
	}

	m = only(t, m, commands.CaptureTickMsg{})
	if len(r.captures) < captures {
		t.Fatal("the capture scheduler stopped behind the menu")
	}

	// The preference read that startup asked for still applies behind the menu,
	// and it does so without the menu noticing.
	m = only(t, m, commands.OverviewPreferencesMsg{Read: true, Explicit: true, Compact: true})
	if !m.compact {
		t.Fatal("the stored view preference was dropped because a menu was open")
	}
	if m.acts.surface != surfaceMenu {
		t.Fatalf("applying the preference left surface %v, want the menu", m.acts.surface)
	}
	if len(r.dispatches) != 0 || len(r.focus) != 0 {
		t.Fatalf("background work asked for %v and %v focus; it must ask for neither", r.actionIDs(), r.focus)
	}
}

// TestDismissalStopsTheSchedulerFromEveryOverlay. Leaving with reads outstanding
// is the ordinary case, and the overlays must not be a state the shutdown misses.
func TestDismissalStopsTheSchedulerFromEveryOverlay(t *testing.T) {
	sessions := viewSessions(4)
	for _, tc := range []struct {
		name string
		open []string
	}{
		{name: "menu", open: []string{"a"}},
		{name: "profile picker", open: []string{"a", "C"}},
		{name: "confirmation", open: []string{"a", "X"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := actionModel(t, sessions, 0, hostTabElsewhere)
			for _, name := range tc.open {
				m = press(t, m, name)
			}
			m, cmd := send(t, m, key(t, "ctrl+c"))
			if !quits(t, cmd) {
				t.Fatal("ctrl+c did not quit")
			}
			if cmd := m.pumpCaptures(); cmd != nil {
				t.Fatal("the capture scheduler is still live after a dismissal from an overlay")
			}
		})
	}
}

// TestAnEmptyGridKeepsItsHelpItsExitAndItsRecovery is the last-session case. The
// grid has nothing to act on, but everything that is not about a row survives:
// the hints, the way out, the feedback row, and an undo that is still eligible.
func TestAnEmptyGridKeepsItsHelpItsExitAndItsRecovery(t *testing.T) {
	sessions := viewSessions(1)
	m, r := actionModel(t, sessions, 0, hostTabElsewhere)
	m = closeSelected(t, m, r)

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, hosted(snapshot(m.refreshActiveSeq, 0, nil), hostTabElsewhere))
	if len(m.sessions) != 0 {
		t.Fatalf("%d sessions survived the close", len(m.sessions))
	}

	frame := frameOf(m)
	for _, hint := range []string{"enter focus", "a actions", "? help", "q quit"} {
		if !strings.Contains(frame, hint) {
			t.Fatalf("the empty grid lost the %q hint:\n%s", hint, frame)
		}
	}
	if got := m.feedback(); !strings.Contains(got, "u relaunch") {
		t.Fatalf("the empty grid says %q, want the recovery offer it still holds", got)
	}

	// Opening a menu over nothing does nothing, rather than capturing a row that
	// is not there.
	m = press(t, m, "a")
	if m.acts.surface != surfaceGrid {
		t.Fatalf("`a` over an empty grid opened surface %v", m.acts.surface)
	}

	// The undo is still eligible, and the way out still works.
	m = press(t, m, "u")
	if len(r.relaunches) != 1 {
		t.Fatalf("`u` over an empty grid relaunched %d sessions, want the one it still holds", len(r.relaunches))
	}
	if _, cmd := send(t, m, key(t, "q")); !quits(t, cmd) {
		t.Fatal("an empty grid holding a recovery could not be left")
	}
}

// TestBrowsingTheMenuMutatesNothing is the session's "menu browsing causes no
// session mutations" requirement, stated over the two mutating seams rather than
// over a count some other seam also moves.
func TestBrowsingTheMenuMutatesNothing(t *testing.T) {
	sessions := viewSessions(9)
	m, r := actionModel(t, sessions, 4, hostTabElsewhere)
	reads := len(r.topology)

	m = openMenu(t, m)
	for _, name := range []string{"j", "k", "G", "g", "pgdown", "pgup", "ctrl+d", "ctrl+u", "down", "up"} {
		next, cmd := send(t, m, key(t, name))
		m = next
		if cmd != nil {
			t.Fatalf("browsing the menu with %q returned a command", name)
		}
		if got := m.overlayView(); got == "" {
			t.Fatalf("the menu vanished while browsing it with %q", name)
		}
	}
	m = press(t, m, "esc")

	if len(r.dispatches) != 0 {
		t.Fatalf("browsing the menu dispatched %v", r.actionIDs())
	}
	if len(r.focus) != 0 || len(r.relaunches) != 0 {
		t.Fatalf("browsing the menu focused %v and relaunched %d sessions", r.focus, len(r.relaunches))
	}
	if len(r.topology) != reads {
		t.Fatalf("browsing the menu dispatched %d reads", len(r.topology)-reads)
	}
	if m.acts.controller.Busy() || len(m.acts.controller.InFlight()) != 0 {
		t.Fatal("browsing the menu left the controller holding an action")
	}
}
