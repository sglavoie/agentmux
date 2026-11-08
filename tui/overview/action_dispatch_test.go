package overview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// TestALiveRefreshCannotRedirectTheCapturedTarget is the capture rule under the
// event that makes it matter. The menu captured a row; a refresh then closes it
// and the grid's own cursor reconciles onto the nearest survivor. Running the
// entry must refuse rather than act on the row that took its place.
func TestALiveRefreshCannotRedirectTheCapturedTarget(t *testing.T) {
	sessions := viewSessions(5)
	m, r := actionModel(t, sessions, 2, hostTabElsewhere)
	captured := m.selectedTabID

	m = openMenu(t, m)

	survivors := append(append([]session.RunningSession{}, sessions[:2]...), sessions[3:]...)
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, hosted(snapshot(m.refreshActiveSeq, 0, survivors), hostTabElsewhere))
	if m.selectedTabID == captured {
		t.Fatal("the refresh left the cursor on the captured row; the case proves nothing")
	}
	if m.acts.target.Session.TabID != captured {
		t.Fatalf("the refresh retargeted the menu onto tab %d, want the captured %d", m.acts.target.Session.TabID, captured)
	}

	m = press(t, m, "y") // copy title: the least consequential entry there is
	if len(r.dispatches) != 0 {
		t.Fatalf("an action ran against %v after its row was closed under it", r.actionIDs())
	}
	if got := m.feedback(); !strings.Contains(got, sessionactions.ReasonSessionGone) {
		t.Fatalf("the feedback row says %q, want the refusal naming the vanished session", got)
	}
}

// TestACapturedTargetSurvivesReorderingIsStillActedOn is the other half of the
// rule above: a refresh that moves the captured row WITHOUT removing it changes
// nothing, and the action still reaches exactly that row.
func TestACapturedTargetSurvivesReorderingIsStillActedOn(t *testing.T) {
	sessions := viewSessions(5)
	m, r := actionModel(t, sessions, 3, hostTabElsewhere)
	captured := m.selectedTabID

	m = openMenu(t, m)
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, hosted(snapshot(m.refreshActiveSeq, 0, sessions[2:]), hostTabElsewhere))

	m = press(t, m, "y")
	d, ok := r.lastDispatch()
	if !ok || d.Action != commands.CopyTitleID {
		t.Fatalf("the menu dispatched %v, want a copy of the title", r.actionIDs())
	}
	if d.Target.TabID() != captured {
		t.Fatalf("the action ran against tab %d, want the captured %d", d.Target.TabID(), captured)
	}
}

// actionCase is one menu entry as an operator meets it: the key that runs it,
// the row state that makes it available, and the overlays it raises on the way.
type actionCase struct {
	id      commands.ID
	key     string
	state   string
	profile bool
	confirm bool
	// focus marks the one entry that goes through the grid's own single-flight
	// focus path rather than the generic effect seam.
	focus bool
	// settled marks the entries whose completion messages carry no request
	// identity, so the dispatch is closed at once rather than left correlating
	// against a reply that can never arrive.
	settled bool
}

// menuCases is all thirteen, in menu order.
func menuCases() []actionCase {
	return []actionCase{
		{id: commands.FocusSession, key: "enter", state: "bolt", focus: true, settled: true},
		{id: commands.PeekScrollback, key: "e", state: "bolt", settled: true},
		{id: commands.LaunchAnother, key: "C", state: "bolt", profile: true},
		{id: commands.OpenDirectoryID, key: "o", state: "bolt", settled: true},
		{id: commands.OpenEditorID, key: "O", state: "bolt", settled: true},
		{id: commands.CopyTitleID, key: "y", state: "bolt"},
		{id: commands.ClearStatusIcon, key: "x", state: "key"},
		{id: commands.MarkActiveIcon, key: "b", state: "stop"},
		{id: commands.ToggleLockID, key: "L", state: "bolt"},
		{id: commands.ToggleMuteID, key: "m", state: "bolt"},
		{id: commands.RestartSession, key: "r", state: "bolt", confirm: true},
		{id: commands.OverrideProfile, key: "c", state: "bolt", profile: true, confirm: true},
		{id: commands.CloseSession, key: "X", state: "bolt", confirm: true},
	}
}

// TestEveryMenuActionReachesItsEffectInBothModes is the session's "all thirteen
// actions work in normal and compact F2" written as one sweep.
//
// Compact is not a cosmetic variant to check for completeness: the mode key is
// `c`, which is also a mnemonic, so running the menu over a compacted grid is
// exactly the arrangement in which a routing mistake would show up.
func TestEveryMenuActionReachesItsEffectInBothModes(t *testing.T) {
	for _, compact := range []bool{false, true} {
		mode := "normal"
		if compact {
			mode = "compact"
		}
		for _, tc := range menuCases() {
			t.Run(fmt.Sprintf("%s/%s", mode, tc.id), func(t *testing.T) {
				sessions := viewSessions(4)
				sessions[1].State = tc.state
				m, r := actionModel(t, sessions, 1, hostTabElsewhere)
				if compact {
					m = press(t, m, "c")
					if !m.compact {
						t.Fatal("`c` on the grid did not compact it")
					}
				}
				row := m.sessions[m.selected]

				m = openMenu(t, m)
				m = press(t, m, tc.key)

				if tc.profile {
					if m.acts.surface != surfaceProfile {
						t.Fatalf("%q left surface %v, want the profile picker", tc.key, m.acts.surface)
					}
					m = chooseProfile(t, m, otherProfile(t, m, row))
				}
				if tc.confirm {
					if m.acts.surface != surfaceConfirm {
						t.Fatalf("%q left surface %v, want the confirmation", tc.key, m.acts.surface)
					}
					m = press(t, m, "y")
				}

				if tc.focus {
					if len(r.focus) != 1 || r.focus[0] != row.TabID {
						t.Fatalf("focus was dispatched %v, want exactly one against tab %d", r.focus, row.TabID)
					}
				} else {
					d, ok := r.lastDispatch()
					if !ok || d.Action != tc.id {
						t.Fatalf("the seam received %v, want %q", r.actionIDs(), tc.id)
					}
					if d.Target.TabID() != row.TabID {
						t.Fatalf("%q ran against tab %d, want the captured %d", tc.id, d.Target.TabID(), row.TabID)
					}
					if tc.profile && d.Profile == "" {
						t.Fatalf("%q was dispatched with no profile", tc.id)
					}
					if !tc.profile && d.Profile != "" {
						t.Fatalf("%q was dispatched with profile %q, which it never asked for", tc.id, d.Profile)
					}
				}

				if m.acts.surface != surfaceGrid {
					t.Fatalf("a dispatched action left surface %v, want the grid", m.acts.surface)
				}
				if inflight := m.acts.controller.InFlight(); tc.settled != (len(inflight) == 0) {
					t.Fatalf("%q left %d dispatches in flight; settled-at-dispatch is %t", tc.id, len(inflight), tc.settled)
				}
				if m.compact != compact {
					t.Fatalf("running %q changed the view mode to compact=%t", tc.id, m.compact)
				}
			})
		}
	}
}

// TestAnUnavailableEntryReportsItsReasonAndRunsNothing. The menu draws such an
// entry dimmed with the reason beside it; an operator who pressed its mnemonic
// anyway is owed the same sentence rather than silence.
func TestAnUnavailableEntryReportsItsReasonAndRunsNothing(t *testing.T) {
	sessions := viewSessions(3)
	sessions[1].State = "bolt" // not a clearable badge
	m, r := actionModel(t, sessions, 1, hostTabElsewhere)

	m = openMenu(t, m)
	m = press(t, m, "x")
	if len(r.dispatches) != 0 {
		t.Fatalf("an unavailable entry dispatched %v", r.actionIDs())
	}
	if got := m.feedback(); got == "" {
		t.Fatal("an unavailable entry ran nothing and said nothing")
	}
	if m.acts.surface != surfaceMenu {
		t.Fatalf("a refused mnemonic left surface %v, want the menu still open", m.acts.surface)
	}
}

// TestChoosingTheProfileARowAlreadyRunsIsANoOp pins the picker behavior the
// shared controller has always had, on this surface too: a replacement that
// replaces nothing is not a restart.
func TestChoosingTheProfileARowAlreadyRunsIsANoOp(t *testing.T) {
	sessions := viewSessions(3)
	m, r := actionModel(t, sessions, 1, hostTabElsewhere)
	row := m.sessions[m.selected]

	m = openMenu(t, m)
	m = press(t, m, "c")
	m = chooseProfile(t, m, row.EffectiveProfile())

	if len(r.dispatches) != 0 {
		t.Fatalf("re-choosing the current profile dispatched %v", r.actionIDs())
	}
	if m.acts.surface != surfaceGrid {
		t.Fatalf("the no-op left surface %v, want the grid", m.acts.surface)
	}
	if m.acts.controller.Busy() {
		t.Fatal("the no-op left an action pending")
	}
}
