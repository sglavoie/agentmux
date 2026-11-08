package overview

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// TestEnterFocusesTheCapturedTabAndExitsOnlyOnSuccess is the one action the surface
// performs.
func TestEnterFocusesTheCapturedTabAndExitsOnlyOnSuccess(t *testing.T) {
	sessions := viewSessions(4)
	m, r := loadedAt(t, sessions, 2)
	target := m.selectedTabID

	m, cmd := send(t, m, key(t, "enter"))
	if len(r.focus) != 1 || r.focus[0] != target {
		t.Fatalf("Enter dispatched focus %v, want exactly one against tab %d", r.focus, target)
	}
	if cmd == nil {
		t.Fatal("Enter returned no command")
	}
	if quits(t, cmd) {
		t.Fatal("Enter quit before tmux confirmed the switch")
	}
	if m.focusTabID != target {
		t.Fatalf("the pending focus is against tab %d, want the captured %d", m.focusTabID, target)
	}

	m, cmd = send(t, m, commands.AttachDoneMsg{Switched: true})
	if !quits(t, cmd) {
		t.Fatal("a confirmed focus did not exit the overview")
	}
	if m.focusTabID != 0 {
		t.Fatal("a settled focus left the pending tab behind")
	}
}

// The pending focus keeps its tab identity while a new session arrives during
// the round trip, regardless of the grid's unchanged positions.
func TestEnterActsOnTheTabItCapturedNotTheOneTheIndexEndsUpOn(t *testing.T) {
	first := viewSession(4, "zzz-last", "work", "claude")
	m, r := started(t, first.WindowID)
	m = only(t, m, snapshot(firstRefreshSeq, first.TabID, []session.RunningSession{first}))

	m = press(t, m, "enter")
	if len(r.focus) != 1 || r.focus[0] != first.TabID {
		t.Fatalf("Enter dispatched %v, want tab %d", r.focus, first.TabID)
	}

	// A read lands mid-flight with another session, leaving the existing tile put.
	earlier := viewSession(5, "aaa-first", "personal", "codex")
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, []session.RunningSession{first, earlier}))
	if m.selected != 0 {
		t.Fatalf("the refresh left the selection at index %d, want 0", m.selected)
	}
	if m.focusTabID != first.TabID {
		t.Fatalf("the pending focus became tab %d, want the captured %d", m.focusTabID, first.TabID)
	}
	if len(r.focus) != 1 {
		t.Fatalf("%d focuses were dispatched, want the one Enter asked for", len(r.focus))
	}
}

// TestAFailedFocusStaysOnTheOverviewWithTheReason is the other half: the only
// state from which retrying makes sense is the one the operator was already in.
func TestAFailedFocusStaysOnTheOverviewWithTheReason(t *testing.T) {
	sessions := viewSessions(4)
	m, r := loadedAt(t, sessions, 1)
	target := m.selectedTabID

	m = press(t, m, "enter")
	m, cmd := send(t, m, commands.AttachDoneMsg{Err: errors.New("no matching tab")})
	if quits(t, cmd) {
		t.Fatal("a failed focus exited the overview")
	}
	if m.selectedTabID != target {
		t.Fatalf("a failed focus moved the selection to tab %d", m.selectedTabID)
	}
	if got := m.feedback(); !strings.HasPrefix(got, "Focus failed: ") || !strings.Contains(got, "no matching tab") {
		t.Fatalf("the feedback row says %q, want the focus failure and its reason", got)
	}

	// Retrying is possible because nothing is pending any more.
	m = press(t, m, "enter")
	if len(r.focus) != 2 {
		t.Fatalf("the retry dispatched %d focuses in total, want 2", len(r.focus))
	}
	if m.focusErr != nil {
		t.Fatalf("the retry did not clear the previous failure: %v", m.focusErr)
	}
}

// TestADuplicateEnterWhileFocusIsPendingIsIgnored keeps one keypress from becoming
// two commands, the second of which would complete after the program was already
// told to quit.
func TestADuplicateEnterWhileFocusIsPendingIsIgnored(t *testing.T) {
	sessions := viewSessions(4)
	m, r := loadedAt(t, sessions, 0)

	m = press(t, m, "enter")
	for i := 0; i < 3; i++ {
		next, cmd := send(t, m, key(t, "enter"))
		m = next
		if cmd != nil {
			t.Fatal("a duplicate Enter returned a command")
		}
	}
	if len(r.focus) != 1 {
		t.Fatalf("four Enter presses dispatched %d focuses, want 1", len(r.focus))
	}

	// Navigating during the round trip does not retarget the pending focus.
	m = press(t, m, "tab")
	if m.focusTabID == m.selectedTabID {
		t.Fatal("the cursor did not move off the pending focus target; the case proves nothing")
	}
	m, cmd := send(t, m, commands.AttachDoneMsg{Switched: true})
	if !quits(t, cmd) {
		t.Fatal("the confirmed focus did not exit")
	}
	if len(r.focus) != 1 {
		t.Fatalf("%d focuses were dispatched in total, want the single captured one", len(r.focus))
	}
}

// TestAFocusCompletionWithNothingPendingIsDropped covers the answer that arrives
// after the surface has already dealt with the attempt.
func TestAFocusCompletionWithNothingPendingIsDropped(t *testing.T) {
	sessions := viewSessions(4)
	m, _ := loadedAt(t, sessions, 0)

	m, cmd := send(t, m, commands.AttachDoneMsg{Switched: true})
	if quits(t, cmd) {
		t.Fatal("an unsolicited focus completion exited the overview")
	}
	if m.focusErr != nil {
		t.Fatalf("an unsolicited completion recorded %v", m.focusErr)
	}

	m, cmd = send(t, m, commands.AttachDoneMsg{Err: errors.New("late")})
	if quits(t, cmd) {
		t.Fatal("an unsolicited failure exited the overview")
	}
	if m.focusErr != nil {
		t.Fatalf("an unsolicited failure was reported as the operator's: %v", m.focusErr)
	}
}

// TestDismissalWorksDuringPendingWork is the escape hatch. An operator asking to
// leave an overlay is not asking to wait for a subprocess first.
func TestDismissalWorksDuringPendingWork(t *testing.T) {
	sessions := viewSessions(4)
	for _, name := range []string{"esc", "q", "ctrl+c"} {
		t.Run(name+" during the startup read", func(t *testing.T) {
			m, _ := started(t, 0)
			if !m.refreshInFlight {
				t.Fatal("the startup read is not outstanding; the case is not the one named")
			}
			if _, cmd := send(t, m, key(t, name)); !quits(t, cmd) {
				t.Fatalf("%s did not dismiss the overview during a read", name)
			}
		})
		t.Run(name+" during a pending focus", func(t *testing.T) {
			m, _ := loadedAt(t, sessions, 0)
			m = press(t, m, "enter")
			if m.focusTabID == 0 {
				t.Fatal("no focus is pending; the case is not the one named")
			}
			if _, cmd := send(t, m, key(t, name)); !quits(t, cmd) {
				t.Fatalf("%s did not dismiss the overview during a pending focus", name)
			}
		})
		t.Run(name+" on a surface that never loaded", func(t *testing.T) {
			m, _ := started(t, 0)
			m = only(t, m, commands.ErrMsg{Seq: firstRefreshSeq, Err: errors.New("down")})
			if _, cmd := send(t, m, key(t, name)); !quits(t, cmd) {
				t.Fatalf("%s did not dismiss a surface that never loaded", name)
			}
		})
	}
}

// TestNavigationAndRenderingNeverActOnASession is the safety claim the whole
// surface rests on, asserted behaviorally to complement the structural check in
// view_test.go.
//
// Every movement key, a resize, and a full render are driven over a loaded model,
// and the only thing that may have happened is the read the controller asked for.
// No focus, and — because the effect seams record every request — no terminal call of any kind,
// which would have panicked.
func TestNavigationAndRenderingNeverActOnASession(t *testing.T) {
	sessions := viewSessions(9)
	m, r := loadedAt(t, sessions, 4)
	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})

	keys := []string{
		"left", "right", "up", "down", "h", "j", "k", "l",
		"tab", "shift+tab", "pgup", "pgdown", "home", "end",
		"x", "space", "ctrl+r", "?", "r", "d", // unbound keys are inert, not errors
	}
	for _, name := range keys {
		next, cmd := send(t, m, key(t, name))
		m = next
		if cmd != nil {
			t.Fatalf("%q returned a command; movement changes one integer and nothing else", name)
		}
		if m.focusTabID != 0 {
			t.Fatalf("%q left a focus pending against tab %d", name, m.focusTabID)
		}
		if got := Render(overviewInput(m)); got == "" {
			t.Fatalf("the frame after %q is empty", name)
		}
	}
	if len(r.focus) != 0 {
		t.Fatalf("browsing dispatched %d focus actions, want 0", len(r.focus))
	}
	if len(r.topology) != 1 {
		t.Fatalf("browsing dispatched %d reads, want only the startup one", len(r.topology))
	}
	if len(m.sessions) != 9 {
		t.Fatalf("browsing changed the live set to %d sessions", len(m.sessions))
	}
}

// TestEveryMovementKeyAgreesWithTheNavigator pins the binding table against the
// navigation vocabulary it delegates to, so a key that stopped moving the cursor
// is a failure rather than a silently inert keypress.
func TestEveryMovementKeyAgreesWithTheNavigator(t *testing.T) {
	bindings := map[string]Move{
		"left": MoveLeft, "h": MoveLeft,
		"right": MoveRight, "l": MoveRight,
		"up": MoveUp, "k": MoveUp,
		"down": MoveDown, "j": MoveDown,
		"tab": MoveNext, "shift+tab": MovePrev,
		"pgup": MovePageUp, "pgdown": MovePageDown,
		"home": MoveFirst, "end": MoveLast,
	}
	sessions := viewSessions(9)
	for name, want := range bindings {
		if got, ok := moveFor(name); !ok || got != want {
			t.Errorf("moveFor(%q) = %v/%t, want %v", name, got, ok, want)
		}

		// Start from a middle tile so no direction is already against a wall.
		m, _ := loadedAt(t, sessions, 4)
		m = only(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
		expected := Navigate(Measure(100, 24), len(m.sessions), m.selected, want)

		m = press(t, m, name)
		if m.selected != expected {
			t.Errorf("%q selected index %d, want the navigator's %d", name, m.selected, expected)
		}
		if m.selectedTabID != m.sessions[expected].TabID {
			t.Errorf("%q left the identity on tab %d rather than on the selected row's %d", name, m.selectedTabID, m.sessions[expected].TabID)
		}
		if m.selectedWindowID != m.sessions[expected].WindowID {
			t.Errorf("%q left the anchor on window %d rather than on %d", name, m.selectedWindowID, m.sessions[expected].WindowID)
		}
	}
}

// TestAMoveSurvivesTheNextRefresh is why applyMove re-resolves the identity: a
// move that changed the index without updating the tab would be undone by the
// following read, which would find the old tab still present and put the cursor
// back.
func TestAMoveSurvivesTheNextRefresh(t *testing.T) {
	sessions := viewSessions(9)
	m, _ := loadedAt(t, sessions, 0)
	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = press(t, m, "end")
	moved := m.selectedTabID

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, viewSessions(9)))
	if m.selectedTabID != moved {
		t.Fatalf("the refresh moved the cursor back to tab %d from %d", m.selectedTabID, moved)
	}
	if m.selected != len(m.sessions)-1 {
		t.Fatalf("the cursor is at index %d, want the last session at %d", m.selected, len(m.sessions)-1)
	}
}
