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

// overviewPressureTiles is how many distinct tabs the correlation-pressure cases
// drive: above the controller's sixteen-entry record and inside the plan's 8–20
// session target, so the state it reaches is a busy morning rather than a load
// test.
const overviewPressureTiles = 20

// overviewPressureChurn is enough non-lifecycle work on the held tile to fill the
// correlation record on its own, several times over.
const overviewPressureChurn = 18

// selectTab walks the grid's own navigation onto one tab, paging as it goes,
// which is how an operator reaches a tile that is not on the first page.
//
// It goes Home first and then forward, because Tab clamps at the last tile
// rather than wrapping: walking forward from wherever the cursor happens to be
// would never reach a tile behind it.
func selectTab(t *testing.T, m model, tabID int) model {
	t.Helper()
	m = press(t, m, "home")
	for steps := 0; steps <= len(m.sessions); steps++ {
		if m.selectedTabID == tabID {
			return m
		}
		m = press(t, m, "tab")
	}
	t.Fatalf("tab %d is not selectable in the grid", tabID)
	return m
}

// lifecycleDispatchesFor counts the effects that reached the seam holding one
// tab's lifecycle exclusion. A refusal that actually refused leaves this
// unchanged; a released guard shows up here as a second entry.
func lifecycleDispatchesFor(r *recorder, tabID int) int {
	held := 0
	for _, d := range r.dispatches {
		if sessionactions.Destructive(d.Action) && d.Target.TabID() == tabID {
			held++
		}
	}
	return held
}

// applyOverviewPressure accumulates more dispatches than the correlation record
// holds and completes none of them: every other tile's own lifecycle action, then
// a long run of churn on the held tile that the guard does not read — Copy title,
// which touches no process, and Launch another, which is recorded like any
// dispatch but is deliberately outside the single-flight set.
//
// withCloses says whether the other tiles may be CLOSED. The recovery stack has a
// bound of its own that nineteen further closes would reach, and the cases that
// assert on the held close's recovery pass false so that the only bookkeeping
// under pressure is the controller's, which is what the finding is about.
func applyOverviewPressure(t *testing.T, m model, r *recorder, heldTabID int, withCloses bool) model {
	t.Helper()
	lifecycle := []commands.ID{commands.RestartSession, commands.OverrideProfile}
	if withCloses {
		lifecycle = append(lifecycle, commands.CloseSession)
	}
	drove := 0
	for i, s := range m.sessions {
		if s.TabID == heldTabID {
			continue
		}
		action := lifecycle[i%len(lifecycle)]
		m = selectTab(t, m, s.TabID)
		profile := ""
		if action == commands.OverrideProfile {
			profile = otherProfile(t, m, m.sessions[m.selected])
		}
		before := len(r.dispatches)
		m = beginLifecycle(t, m, action, profile)
		if len(r.dispatches) != before+1 {
			t.Fatalf("%s on tab %d reached %d effects, want exactly one", action, s.TabID, len(r.dispatches)-before)
		}
		drove++
	}
	m = selectTab(t, m, heldTabID)
	for i := 0; i < overviewPressureChurn; i++ {
		churn := commands.CopyTitleID
		profile := ""
		if i%4 == 3 {
			churn, profile = commands.LaunchAnother, otherProfile(t, m, m.sessions[m.selected])
		}
		before := len(r.dispatches)
		m = beginLifecycle(t, m, churn, profile)
		if len(r.dispatches) != before+1 {
			t.Fatalf("%s #%d on the held tile reached %d effects, want exactly one", churn, i+1, len(r.dispatches)-before)
		}
		if got := m.feedback(); strings.Contains(got, sessionactions.ReasonLifecycleInFlight) {
			t.Fatalf("%s #%d on the held tile was refused: %q", churn, i+1, got)
		}
		drove++
	}
	if drove <= 16 {
		t.Fatalf("the pressure drove %d dispatches, want more than the controller's record holds", drove)
	}
	return m
}

// pressuredGrid is the shared setup: twenty loaded tiles, one lifecycle action
// parked on the first of them, and then the pressure. It returns the grid, the
// recorder, the held tab, and the parked operation's request identity.
func pressuredGrid(t *testing.T, held commands.ID, confirms, withCloses bool) (model, *recorder, int, sessionactions.RequestID) {
	t.Helper()
	sessions := viewSessions(overviewPressureTiles)
	m, r := singleFlightModel(t, sessions, 0, confirms)
	tabID := m.selectedTabID

	profile := ""
	if held == commands.OverrideProfile {
		profile = otherProfile(t, m, m.sessions[m.selected])
	}
	m = beginLifecycle(t, m, held, profile)
	d, ok := r.lastDispatch()
	if !ok || d.Action != held || d.Target.TabID() != tabID {
		t.Fatalf("the held %s dispatched %v against tab %d, want it on tab %d", held, r.actionIDs(), d.Target.TabID(), tabID)
	}

	m = applyOverviewPressure(t, m, r, tabID, withCloses)
	return m, r, tabID, d.Request
}

// TestReviewLifecycleGuardSurvivesCorrelationPressure is the grid's half of the
// bound's contract: twenty tiles' worth of accumulated, uncompleted work must not
// release the exclusion the operator's own restart holds, and must not lose the
// correlation its completion arrives under.
//
// The correlation record used to drop its oldest entry at sixteen whatever that
// entry was. On this surface that meant a second restart REACHED the seam — two
// replacements for one tile, since a restart launches before it closes — and the
// first one's completion arrived uncorrelated, which handleActionDone discards,
// so a close that really happened offered no undo and reported nothing.
func TestReviewLifecycleGuardSurvivesCorrelationPressure(t *testing.T) {
	lifecycle := []commands.ID{commands.CloseSession, commands.RestartSession, commands.OverrideProfile}

	// Every lifecycle action held against every lifecycle retry, under both
	// confirmation settings. The retry is typed at the grid exactly as the first
	// one was: `a`, the mnemonic, and whatever the action opens.
	for _, confirms := range []bool{false, true} {
		for _, held := range lifecycle {
			for _, retry := range lifecycle {
				name := string(held) + "-then-" + string(retry)
				if confirms {
					name += "-confirming"
				}
				t.Run(name, func(t *testing.T) {
					m, r, tabID, _ := pressuredGrid(t, held, confirms, held != commands.CloseSession)

					if got := lifecycleDispatchesFor(r, tabID); got != 1 {
						t.Fatalf("tab %d reached %d lifecycle effects after the pressure, want the one %s", tabID, got, held)
					}
					for attempt := 0; attempt < 3; attempt++ {
						m = selectTab(t, m, tabID)
						profile := ""
						if retry == commands.OverrideProfile {
							profile = otherProfile(t, m, m.sessions[m.selected])
						}
						m = beginLifecycle(t, m, retry, profile)
						if got := lifecycleDispatchesFor(r, tabID); got != 1 {
							t.Fatalf("retry #%d reached a second lifecycle effect on tab %d: %d in all", attempt+1, tabID, got)
						}
						if got := m.feedback(); !strings.Contains(got, sessionactions.ReasonLifecycleInFlight) {
							t.Fatalf("retry #%d: the feedback row says %q, want it to name the in-flight operation", attempt+1, got)
						}
						if m.acts.surface != surfaceGrid {
							t.Fatalf("retry #%d left surface %v, want the grid", attempt+1, m.acts.surface)
						}
					}
				})
			}
		}
	}

	// The correlation is the other half of what eviction took, and the close's
	// recovery is where its loss is visible to an operator: handleActionDone
	// discards a completion the record cannot name, so `u` would have nothing to
	// offer for a close that really happened.
	t.Run("the held close keeps its recovery", func(t *testing.T) {
		m, r, tabID, request := pressuredGrid(t, commands.CloseSession, false, false)

		m = only(t, m, commands.ActionDoneMsg{Request: uint64(request)})
		if got := m.acts.recovery.Available(m.deps.now()); len(got) != 1 {
			t.Fatalf("recovery entries = %d, want exactly one for the one closed session", len(got))
		}
		if offer := m.feedback(); !strings.Contains(offer, "closed ") {
			t.Fatalf("the feedback row says %q, want the undo offer for the held close", offer)
		}
		// And the tile is usable again, which is the same fact from the
		// operator's side.
		m = selectTab(t, m, tabID)
		m = beginLifecycle(t, m, commands.RestartSession, "")
		if got := lifecycleDispatchesFor(r, tabID); got != 2 {
			t.Fatalf("tab %d reached %d lifecycle effects, want the close and its retry", tabID, got)
		}
	})

	// A second close refused under pressure must not reserve a second recovery:
	// the duplicate undo is what a duplicate dispatch costs, and `u` pressed
	// twice would relaunch one session twice.
	t.Run("no duplicate close recovery under pressure", func(t *testing.T) {
		m, r, tabID, _ := pressuredGrid(t, commands.CloseSession, false, false)

		m = selectTab(t, m, tabID)
		m = beginLifecycle(t, m, commands.CloseSession, "")
		for _, d := range r.dispatches {
			if d.Action != commands.CloseSession {
				continue
			}
			m = only(t, m, commands.ActionDoneMsg{Request: uint64(d.Request)})
		}
		if got := m.acts.recovery.Available(m.deps.now()); len(got) != 1 {
			t.Fatalf("recovery entries = %d, want exactly one for one closed session", len(got))
		}
	})

	// Release is by completion under pressure exactly as it is without it, for
	// every outcome. A failed restart that locked its own tile out of a retry
	// would be a worse answer than the duplicate the guard refuses.
	t.Run("a reported completion frees the tile", func(t *testing.T) {
		for name, done := range map[string]commands.ActionDoneMsg{
			"succeeded":   {},
			"failed":      {Err: errors.New("kitty refused")},
			"unconfirmed": {Unconfirmed: true},
		} {
			t.Run(name, func(t *testing.T) {
				m, r, tabID, request := pressuredGrid(t, commands.RestartSession, false, true)

				done.Request = uint64(request)
				m = only(t, m, done)

				m = selectTab(t, m, tabID)
				m = beginLifecycle(t, m, commands.CloseSession, "")
				if got := lifecycleDispatchesFor(r, tabID); got != 2 {
					t.Fatalf("after a %s restart tab %d reached %d lifecycle effects, want the restart and its retry", name, tabID, got)
				}
				if got := m.feedback(); strings.Contains(got, sessionactions.ReasonLifecycleInFlight) {
					t.Fatalf("the retry after a %s restart was refused: %q", name, got)
				}
			})
		}
	})

	// Control: the pressure is nineteen other tiles' work and it stays theirs.
	// Each holds exactly its own exclusion, and completing one frees that tile
	// alone.
	t.Run("independent tiles stay usable", func(t *testing.T) {
		m, r, heldTabID, _ := pressuredGrid(t, commands.RestartSession, false, true)

		for _, s := range m.sessions {
			if got := lifecycleDispatchesFor(r, s.TabID); got != 1 {
				t.Fatalf("tab %d reached %d lifecycle effects, want exactly its own", s.TabID, got)
			}
		}

		var other session.RunningSession
		for _, s := range m.sessions {
			if s.TabID != heldTabID {
				other = s
			}
		}
		var request sessionactions.RequestID
		for _, d := range r.dispatches {
			if sessionactions.Destructive(d.Action) && d.Target.TabID() == other.TabID {
				request = d.Request
			}
		}
		if !request.Valid() {
			t.Fatalf("tab %d holds no lifecycle dispatch to complete", other.TabID)
		}
		m = only(t, m, commands.ActionDoneMsg{Request: uint64(request)})

		m = selectTab(t, m, other.TabID)
		m = beginLifecycle(t, m, commands.RestartSession, "")
		if got := lifecycleDispatchesFor(r, other.TabID); got != 2 {
			t.Fatalf("tab %d reached %d lifecycle effects after its own completion, want two", other.TabID, got)
		}
		if got := lifecycleDispatchesFor(r, heldTabID); got != 1 {
			t.Fatalf("the held tile reached %d lifecycle effects, want the one restart it started with", got)
		}
	})
}
