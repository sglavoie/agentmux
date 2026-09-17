// The single-flight half of the acting tests. They read as ordinary key
// sequences because the recorder never answers a dispatch: every effect this
// surface starts stays parked until a test hands it an ActionDoneMsg, which is
// exactly the window the repeated keystroke arrives in.
package overview

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// singleFlightModel is actionModel with confirm_destructive stated by the case
// rather than fixed. The setting changes how many keystrokes a lifecycle action
// costs and nothing else, which is the point: the guard must hold for both.
func singleFlightModel(t *testing.T, sessions []session.RunningSession, index int, confirms bool) (model, *recorder) {
	t.Helper()
	m, r := actionModel(t, sessions, index, hostTabElsewhere)
	m.cfg.Defaults.ConfirmDestructive = confirms
	return m, r
}

// beginLifecycle drives one lifecycle action from the grid's actions menu,
// answering the picker and the confirmation when they open. The mnemonic comes
// from the shared vocabulary so a re-keyed action does not quietly stop being
// covered here.
func beginLifecycle(t *testing.T, m model, action commands.ID, profile string) model {
	t.Helper()
	definition, ok := sessionactions.Lookup(action)
	if !ok {
		t.Fatalf("the vocabulary declares no action %q", action)
	}
	m = openMenu(t, m)
	m = press(t, m, definition.Mnemonic.Key)
	if m.acts.surface == surfaceProfile {
		m = chooseProfile(t, m, profile)
	}
	if m.acts.surface == surfaceConfirm {
		m = press(t, m, "y")
	}
	if m.acts.surface != surfaceGrid {
		t.Fatalf("%q left surface %v, want the grid", action, m.acts.surface)
	}
	return m
}

// TestReviewLifecycleDispatchSingleFlight is the single-flight contract on the
// overview: at most one close, restart, or profile replacement may be in flight
// against one captured tab, whichever of the three the repeat asks for.
//
// The grid is where the finding's sequence is cheapest to type — a,r,a,r is four
// keys with confirm_destructive off — and the recorder is where it is visible,
// because a second effect reaching the seam is a second entry in r.dispatches.
func TestReviewLifecycleDispatchSingleFlight(t *testing.T) {
	lifecycle := []commands.ID{commands.CloseSession, commands.RestartSession, commands.OverrideProfile}

	assertRefused := func(t *testing.T, m model, r *recorder, wantDispatches int) {
		t.Helper()
		if got := r.dispatches; len(got) != wantDispatches {
			t.Fatalf("dispatched %v, want %d effect(s)", r.actionIDs(), wantDispatches)
		}
		if got := m.feedback(); !strings.Contains(got, sessionactions.ReasonLifecycleInFlight) {
			t.Fatalf("the feedback row says %q, want it to say a lifecycle action is already running", got)
		}
		if m.acts.surface != surfaceGrid {
			t.Fatalf("a refusal left surface %v, want the grid", m.acts.surface)
		}
	}

	for _, confirms := range []bool{false, true} {
		name := "repeat restart quietly"
		if confirms {
			name = "repeat restart with confirmation"
		}
		t.Run(name, func(t *testing.T) {
			sessions := viewSessions(3)
			m, r := singleFlightModel(t, sessions, 1, confirms)
			target := m.selectedTabID

			m = beginLifecycle(t, m, commands.RestartSession, "")
			d, ok := r.lastDispatch()
			if !ok || d.Action != commands.RestartSession || d.Target.TabID() != target {
				t.Fatalf("the first restart dispatched %v against tab %d, want one restart on tab %d", r.actionIDs(), d.Target.TabID(), target)
			}

			// a, r, a, r — the menu reopens each time, and the operator's second
			// restart arrives before the first has reported anything.
			for attempt := 0; attempt < 3; attempt++ {
				m = beginLifecycle(t, m, commands.RestartSession, "")
				assertRefused(t, m, r, 1)
			}
			if m.selectedTabID != target {
				t.Fatalf("the refusals moved the selection to tab %d", m.selectedTabID)
			}
		})
	}

	// Close, restart, and profile replacement exclude each other: all three end
	// or replace the same harness process, so the second is a second replacement
	// rather than a different request.
	t.Run("conflicting lifecycle actions", func(t *testing.T) {
		for _, confirms := range []bool{false, true} {
			for _, first := range lifecycle {
				for _, second := range lifecycle {
					name := string(first) + "-then-" + string(second)
					if confirms {
						name += "-confirming"
					}
					t.Run(name, func(t *testing.T) {
						sessions := viewSessions(3)
						m, r := singleFlightModel(t, sessions, 1, confirms)
						profile := otherProfile(t, m, m.sessions[m.selected])

						m = beginLifecycle(t, m, first, profile)
						if len(r.dispatches) != 1 {
							t.Fatalf("%s dispatched %v, want one effect", first, r.actionIDs())
						}
						m = beginLifecycle(t, m, second, profile)
						assertRefused(t, m, r, 1)
					})
				}
			}
		}
	})

	// The recovery is reserved at dispatch, so a second close that reached an
	// effect would reserve a second undo for one session — and `u` would then
	// relaunch it twice. Every request the grid actually minted is completed, so
	// the count is one because one close ran, not because one was reported.
	t.Run("no duplicate close recovery", func(t *testing.T) {
		sessions := viewSessions(3)
		m, r := singleFlightModel(t, sessions, 1, false)

		m = beginLifecycle(t, m, commands.CloseSession, "")
		m = beginLifecycle(t, m, commands.CloseSession, "")

		// The recovery is asserted before the dispatch count, because the
		// duplicate undo is what the duplicate dispatch COSTS: two entries for
		// one session, and `u` pressed twice would relaunch it twice.
		for _, d := range r.dispatches {
			m = only(t, m, commands.ActionDoneMsg{Request: uint64(d.Request)})
		}
		if got := m.acts.recovery.Available(m.deps.now()); len(got) != 1 {
			t.Fatalf("recovery entries = %d, want exactly one for one closed session", len(got))
		}
		if got := r.actionIDs(); len(got) != 1 {
			t.Fatalf("dispatched %v, want one close", got)
		}
		if offer := m.feedback(); !strings.Contains(offer, "closed ") {
			t.Fatalf("the feedback row says %q, want the undo offer for the one close", offer)
		}
	})

	// Control: the guard is per captured tab, so the row beside it stays usable.
	// A grid that serialized every lifecycle action across nine tiles would be a
	// worse regression than the double dispatch.
	t.Run("distinct tiles are independent", func(t *testing.T) {
		sessions := viewSessions(3)
		m, r := singleFlightModel(t, sessions, 0, false)
		first := m.selectedTabID

		m = beginLifecycle(t, m, commands.RestartSession, "")
		m = press(t, m, "tab")
		if m.selectedTabID == first {
			t.Fatalf("the fixture has only one selectable tile (tab %d)", first)
		}
		m = beginLifecycle(t, m, commands.CloseSession, "")

		if got := r.actionIDs(); len(got) != 2 {
			t.Fatalf("dispatched %v, want both tiles' actions", got)
		}
		if got := m.feedback(); strings.Contains(got, sessionactions.ReasonLifecycleInFlight) {
			t.Fatalf("the second tile was refused: %q", got)
		}
	})

	// Control: launching another session ends nothing, so it is neither blocked
	// by the in-flight restart nor does it hold the tab against one. Two of them
	// are two deliberate launches.
	t.Run("launch another stays available", func(t *testing.T) {
		sessions := viewSessions(3)
		m, r := singleFlightModel(t, sessions, 1, true)
		profile := otherProfile(t, m, m.sessions[m.selected])

		m = beginLifecycle(t, m, commands.RestartSession, profile)
		for attempt := 0; attempt < 2; attempt++ {
			m = beginLifecycle(t, m, commands.LaunchAnother, profile)
			if got := m.feedback(); strings.Contains(got, sessionactions.ReasonLifecycleInFlight) {
				t.Fatalf("launch another #%d was refused: %q", attempt+1, got)
			}
		}
		if got := r.actionIDs(); len(got) != 3 {
			t.Fatalf("dispatched %v, want the restart and both launches", got)
		}
		// The launches are not part of the record the guard reads, so the restart
		// is still the only thing holding the tab.
		m = beginLifecycle(t, m, commands.CloseSession, "")
		assertRefused(t, m, r, 3)
	})

	// The guard is released by the completion rather than by success. A failed
	// close that locked its own tile out of a retry would be a worse answer than
	// the duplicate it prevents, and the operator has no other way back.
	t.Run("a reported completion frees the tile", func(t *testing.T) {
		for name, done := range map[string]commands.ActionDoneMsg{
			"succeeded":   {},
			"failed":      {Err: errors.New("kitty refused")},
			"unconfirmed": {Unconfirmed: true},
		} {
			t.Run(name, func(t *testing.T) {
				sessions := viewSessions(3)
				m, r := singleFlightModel(t, sessions, 1, false)

				m = beginLifecycle(t, m, commands.CloseSession, "")
				d, ok := r.lastDispatch()
				if !ok {
					t.Fatal("the close reached no effect")
				}
				done.Request = uint64(d.Request)
				m = only(t, m, done)

				m = beginLifecycle(t, m, commands.RestartSession, "")
				if got := r.actionIDs(); len(got) != 2 {
					t.Fatalf("after a %s close the retry dispatched %v, want the close and the restart", name, got)
				}
				if got := m.feedback(); strings.Contains(got, sessionactions.ReasonLifecycleInFlight) {
					t.Fatalf("the retry after a %s close was refused: %q", name, got)
				}
			})
		}
	})
}
