// The acting half's tests: the menu, the two overlays it raises, the twelve
// actions behind them, and the undo the grid offers after a close.
//
// They are written against the same recorder the lifecycle tests use, for the
// same reason: the Kitty client is nil, so anything that reached Kitty outside a
// seam would panic rather than pass, and every seam RECORDS and answers nothing,
// so the only messages this package consumes are the ones a test chose to send.
// That is what lets "a refresh landed while the menu was open" and "a completion
// arrived after the operator cancelled" be ordinary sequences.
//
// Two fixtures carry the difference from the lifecycle tests. actionConfig adds
// the two things the acting half reads that the rendering half never did — the
// configured projects the open-somewhere actions need and the Accounts mirror
// every profile picker in the TUI tree reads — and actionModel records a host
// tab, which is the fact the destructive warning and the recovery refusal are
// decided from.
package overview

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// hostTabElsewhere is the ordinary arrangement: the overview runs in a tab of
// its own, beside the sessions rather than in one of them. It is a named
// constant so the tests that DO put the overview in a session's tab say so.
const hostTabElsewhere = 4242

// actionConfig is viewConfig plus what the acting half reads.
//
// Two additions, and both are things config.Load produces that a struct literal
// has to state. The Accounts mirror is what every profile picker in the tree
// reads, including this one, so a configuration that set only Profiles would
// give the picker nothing to show and would make every close unrecoverable for
// want of a profile to relaunch under. The projects are what the two
// open-somewhere actions resolve a row against; without them those entries are
// legitimately unavailable and the sweep below would be asserting a refusal.
func actionConfig() *config.Config {
	cfg := viewConfig()
	cfg.Accounts = cfg.Profiles
	cfg.Defaults.ConfirmDestructive = true
	for i := 1; i <= 16; i++ {
		title := fmt.Sprintf("proj%02d", i)
		cfg.Sessions = append(cfg.Sessions, config.Session{
			Title:     title,
			Directory: "/home/user/projects/" + title,
			Profile:   cfg.Profiles[(i-1)%len(cfg.Profiles)].Name,
		})
	}
	cfg.Projects = cfg.Sessions
	return cfg
}

// actionModel is the loaded grid these tests act on: a real terminal size, the
// acting half's configuration, the cursor on one row, and the host tab a refresh
// reported.
func actionModel(t *testing.T, sessions []session.RunningSession, index, hostTabID int) (model, *recorder) {
	t.Helper()
	r := &recorder{now: viewNow}
	m := newModel(actionConfig(), nil, sessions[index].WindowID)
	r.install(&m)
	m.Init()
	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = only(t, m, hosted(snapshot(firstRefreshSeq, sessions[index].TabID, sessions), hostTabID))
	for steps := 0; m.selectedTabID != sessions[index].TabID && steps < len(sessions); steps++ {
		m = press(t, m, "tab")
	}
	if m.selectedTabID != sessions[index].TabID {
		t.Fatalf("the fixture selected tab %d, want %d", m.selectedTabID, sessions[index].TabID)
	}
	return m, r
}

// hosted stamps a read with the tab the overview is running in, which the shared
// reader resolves afresh on every refresh.
func hosted(msg commands.SessionsMsg, hostTabID int) commands.SessionsMsg {
	msg.HostTabID, msg.HostWindowID = hostTabID, hostTabID*10
	return msg
}

// openMenu presses the grid's own actions key and checks the menu came up, so
// every test below starts from a state it has asserted rather than assumed.
func openMenu(t *testing.T, m model) model {
	t.Helper()
	m = press(t, m, "a")
	if m.acts.surface != surfaceMenu {
		t.Fatalf("`a` left surface %v, want the menu", m.acts.surface)
	}
	return m
}

// chooseProfile moves the picker onto a named profile and takes it.
func chooseProfile(t *testing.T, m model, name string) model {
	t.Helper()
	if m.acts.surface != surfaceProfile {
		t.Fatalf("the profile picker is not open (surface %v)", m.acts.surface)
	}
	want := -1
	for i, account := range m.profileAccounts() {
		if account.Name == name {
			want = i
			break
		}
	}
	if want < 0 {
		t.Fatalf("the configuration has no profile %q", name)
	}
	for m.acts.profileCursor > want {
		m = press(t, m, "k")
	}
	for m.acts.profileCursor < want {
		m = press(t, m, "j")
	}
	return press(t, m, "enter")
}

// otherProfile names a configured profile the row is not already running under,
// which is what a profile REPLACEMENT needs: choosing the current one is
// deliberately a no-op.
func otherProfile(t *testing.T, m model, target session.RunningSession) string {
	t.Helper()
	for _, account := range m.profileAccounts() {
		if !strings.EqualFold(account.Name, target.EffectiveProfile()) {
			return account.Name
		}
	}
	t.Fatalf("the configuration offers no profile other than %q", target.EffectiveProfile())
	return ""
}

// TestOverviewMenuOffersEveryDefinition is the assertion actions.go names: the
// runner table is derived from the shared vocabulary, so an action declared
// there and given no effect here would be silently dropped from the menu rather
// than drawn.
func TestOverviewMenuOffersEveryDefinition(t *testing.T) {
	m, _ := actionModel(t, viewSessions(3), 0, hostTabElsewhere)
	m = openMenu(t, m)

	want := sessionactions.MenuDefinitions()
	if len(want) != 12 {
		t.Fatalf("the shared vocabulary offers %d menu actions, want the 12 this surface promises", len(want))
	}
	got := menuActions(m)
	if len(got) != len(want) {
		t.Fatalf("the overview menu offers %d entries, want all %d; an action with no runner is dropped", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID {
			t.Errorf("entry %d is %q, want the vocabulary's %q", i, got[i].ID, want[i].ID)
		}
		if got[i].Label != want[i].MenuLabel || got[i].Description != want[i].MenuDescription {
			t.Errorf("%q is worded %q/%q, want the vocabulary's %q/%q",
				got[i].ID, got[i].Label, got[i].Description, want[i].MenuLabel, want[i].MenuDescription)
		}
		if got[i].KeyMatch != want[i].Mnemonic.Key || got[i].KeyDisplay != want[i].Mnemonic.Display {
			t.Errorf("%q is keyed %q/%q, want %q/%q",
				got[i].ID, got[i].KeyMatch, got[i].KeyDisplay, want[i].Mnemonic.Key, want[i].Mnemonic.Display)
		}
		if got[i].Band != want[i].MenuGroup.String() {
			t.Errorf("%q sits in band %q, want %q", got[i].ID, got[i].Band, want[i].MenuGroup.String())
		}
	}
}

// TestAFreshMenuOpensOnTheEntryEnterAlreadyRan is the "superset, not a change"
// rule: before the menu existed, Enter on a row focused it, and opening the menu
// and pressing Enter must still do exactly that.
func TestAFreshMenuOpensOnTheEntryEnterAlreadyRan(t *testing.T) {
	sessions := viewSessions(4)
	m, r := actionModel(t, sessions, 2, hostTabElsewhere)
	target := m.selectedTabID

	m = openMenu(t, m)
	if m.acts.menuCursor != sessionactions.MenuIndexOf(sessionactions.InitialSelection()) {
		t.Fatalf("a fresh menu opened on entry %d, want the vocabulary's initial selection", m.acts.menuCursor)
	}
	if m.acts.target.Session.TabID != target {
		t.Fatalf("the menu captured tab %d, want the selected %d", m.acts.target.Session.TabID, target)
	}

	m = press(t, m, "enter")
	if len(r.focus) != 1 || r.focus[0] != target {
		t.Fatalf("enter in a fresh menu dispatched focus %v, want exactly one against tab %d", r.focus, target)
	}
	if len(r.dispatches) != 0 {
		t.Fatalf("focus reached the generic effect seam as %v; it belongs on the grid's own single-flight path", r.actionIDs())
	}
	if m.acts.surface != surfaceGrid {
		t.Fatalf("running an action left surface %v, want the grid", m.acts.surface)
	}
}

// TestAnOverlaySwallowsTheGridsOwnKeys is the modal contract. `c` is the grid's
// compact toggle AND the menu's mnemonic for a profile replacement, and the
// digits that focus a tile are ordinary characters an overlay may want; a router
// that consulted the grid first would act on a surface the operator cannot see.
func TestAnOverlaySwallowsTheGridsOwnKeys(t *testing.T) {
	sessions := viewSessions(6)

	t.Run("the menu", func(t *testing.T) {
		m, r := actionModel(t, sessions, 1, hostTabElsewhere)
		m = openMenu(t, m)
		selected, cursor := m.selectedTabID, m.acts.menuCursor

		for _, name := range []string{"1", "2", "3", "0", "n", "N"} {
			m = press(t, m, name)
			if m.compact {
				t.Fatalf("%q toggled compact mode from inside the menu", name)
			}
			if m.focusTabID != 0 || len(r.focus) != 0 {
				t.Fatalf("%q focused a tile from under the menu", name)
			}
			if m.selectedTabID != selected {
				t.Fatalf("%q moved the grid's selection to tab %d", name, m.selectedTabID)
			}
			if m.acts.surface != surfaceMenu || m.acts.menuCursor != cursor {
				t.Fatalf("%q disturbed the menu (surface %v, cursor %d)", name, m.acts.surface, m.acts.menuCursor)
			}
		}

		// `c` IS claimed here — as the mnemonic it is in a menu — so it raises the
		// picker rather than doing nothing. What it must not do is reach the grid.
		m = press(t, m, "c")
		if m.compact {
			t.Fatal("`c` in the menu toggled compact mode as well as choosing its entry")
		}
		if m.acts.surface != surfaceProfile {
			t.Fatalf("`c` in the menu left surface %v, want the profile picker", m.acts.surface)
		}
	})

	t.Run("the profile picker", func(t *testing.T) {
		m, r := actionModel(t, sessions, 1, hostTabElsewhere)
		m = openMenu(t, m)
		m = press(t, m, "C") // launch another: a profile with no confirmation behind it
		if m.acts.surface != surfaceProfile {
			t.Fatalf("`C` left surface %v, want the profile picker", m.acts.surface)
		}
		selected := m.selectedTabID

		for _, name := range []string{"c", "1", "5", "n", "N", "a", "u"} {
			m = press(t, m, name)
			if m.compact || m.focusTabID != 0 || len(r.focus) != 0 || m.selectedTabID != selected {
				t.Fatalf("%q reached the grid from under the profile picker", name)
			}
			if m.acts.surface != surfaceProfile {
				t.Fatalf("%q left the profile picker for surface %v", name, m.acts.surface)
			}
			if len(r.dispatches) != 0 {
				t.Fatalf("%q dispatched %v from under the picker", name, r.actionIDs())
			}
		}
	})

	t.Run("the confirmation", func(t *testing.T) {
		m, r := actionModel(t, sessions, 1, hostTabElsewhere)
		m = openMenu(t, m)
		m = press(t, m, "X")
		if m.acts.surface != surfaceConfirm {
			t.Fatalf("`X` left surface %v, want the confirmation", m.acts.surface)
		}
		selected := m.selectedTabID

		// `n` is deliberately absent: it answers the prompt. Everything else the
		// grid binds must be inert.
		for _, name := range []string{"c", "1", "5", "N", "a", "u", "enter"} {
			m = press(t, m, name)
			if m.compact || m.focusTabID != 0 || len(r.focus) != 0 || m.selectedTabID != selected {
				t.Fatalf("%q reached the grid from under the confirmation", name)
			}
			if m.acts.surface != surfaceConfirm {
				t.Fatalf("%q left the confirmation for surface %v", name, m.acts.surface)
			}
			if len(r.dispatches) != 0 {
				t.Fatalf("%q dispatched %v without a yes", name, r.actionIDs())
			}
		}
	})
}

// TestCtrlCLeavesTheProgramFromEveryOverlay. The overlay's own dismissal key is
// esc; ctrl+c is the operator asking for the program, and a modal that answered
// it by closing itself would need a second press for every layer.
func TestCtrlCLeavesTheProgramFromEveryOverlay(t *testing.T) {
	sessions := viewSessions(4)
	for _, tc := range []struct {
		name string
		open []string
	}{
		{name: "the menu", open: []string{"a"}},
		{name: "the profile picker", open: []string{"a", "C"}},
		{name: "the confirmation", open: []string{"a", "X"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := actionModel(t, sessions, 0, hostTabElsewhere)
			for _, name := range tc.open {
				m = press(t, m, name)
			}
			if m.acts.surface == surfaceGrid {
				t.Fatal("the overlay is not open; the case is not the one named")
			}
			if _, cmd := send(t, m, key(t, "ctrl+c")); !quits(t, cmd) {
				t.Fatal("ctrl+c did not leave the program")
			}
		})
	}
}

// TestTheSelectionAndPageSurviveMenuVisitsAndCancellation. The menu is a visit,
// not a navigation: an operator who opens it on a tile three pages in and backs
// out is owed that tile, on that page.
func TestTheSelectionAndPageSurviveMenuVisitsAndCancellation(t *testing.T) {
	sessions := viewSessions(11)
	m, r := actionModel(t, sessions, 8, hostTabElsewhere)
	geometry := MeasureMode(m.width, m.height, m.compact)
	selected, page := m.selectedTabID, geometry.PageOf(m.selected)
	if page == 0 {
		t.Fatal("the fixture selected a tile on the first page; the page claim would prove nothing")
	}

	assertHeld := func(what string) {
		t.Helper()
		if m.selectedTabID != selected {
			t.Fatalf("%s moved the selection to tab %d, want the retained %d", what, m.selectedTabID, selected)
		}
		if got := MeasureMode(m.width, m.height, m.compact).PageOf(m.selected); got != page {
			t.Fatalf("%s moved the grid to page %d, want %d", what, got, page)
		}
	}

	// Browsing the menu and leaving it.
	m = openMenu(t, m)
	for _, name := range []string{"j", "j", "G", "k", "g"} {
		m = press(t, m, name)
	}
	m = press(t, m, "esc")
	if m.acts.surface != surfaceGrid {
		t.Fatalf("esc left surface %v, want the grid", m.acts.surface)
	}
	assertHeld("browsing the menu")

	// A second visit opens fresh, on the same row.
	m = openMenu(t, m)
	if m.acts.menuCursor != sessionactions.MenuIndexOf(sessionactions.InitialSelection()) {
		t.Fatalf("the second visit opened on entry %d, want a fresh menu", m.acts.menuCursor)
	}
	if m.acts.target.Session.TabID != selected {
		t.Fatalf("the second visit captured tab %d, want %d", m.acts.target.Session.TabID, selected)
	}

	// Cancelling lands back in the menu where the operator LEFT it, which is
	// where their cursor was rather than on the entry a mnemonic ran: the
	// mnemonic is a shortcut past the cursor, so restoring the entry it named
	// would move a cursor the operator never moved.
	for i := 0; i < 3; i++ {
		m = press(t, m, "j")
	}
	parked := m.acts.menuCursor
	if parked == 0 {
		t.Fatal("the menu cursor did not move; the restoration claim would prove nothing")
	}

	m = press(t, m, "X")
	m = press(t, m, "n")
	if m.acts.surface != surfaceMenu {
		t.Fatalf("declining the confirmation left surface %v, want the menu it was chosen from", m.acts.surface)
	}
	if m.acts.menuCursor != parked {
		t.Fatalf("declining put the cursor on entry %d, want the parked %d", m.acts.menuCursor, parked)
	}
	assertHeld("declining a confirmation")

	// And cancelling a profile picker does the same.
	m = press(t, m, "c")
	m = press(t, m, "esc")
	if m.acts.surface != surfaceMenu {
		t.Fatalf("cancelling the picker left surface %v, want the menu", m.acts.surface)
	}
	if m.acts.menuCursor != parked {
		t.Fatalf("cancelling the picker put the cursor on entry %d, want the parked %d", m.acts.menuCursor, parked)
	}
	assertHeld("cancelling a profile picker")

	m = press(t, m, "esc")
	if len(r.dispatches) != 0 || len(r.focus) != 0 || len(r.relaunches) != 0 {
		t.Fatalf("browsing and cancelling asked for %v, %v focus and %d relaunches; it must ask for nothing",
			r.actionIDs(), r.focus, len(r.relaunches))
	}
}

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

// menuCases is all twelve, in menu order.
func menuCases() []actionCase {
	return []actionCase{
		{id: commands.FocusSession, key: "enter", state: "bolt", focus: true, settled: true},
		{id: commands.PeekScrollback, key: "e", state: "bolt", settled: true},
		{id: commands.LaunchAnother, key: "C", state: "bolt", profile: true},
		{id: commands.OpenDirectoryID, key: "o", state: "bolt", settled: true},
		{id: commands.OpenEditorID, key: "O", state: "bolt", settled: true},
		{id: commands.CopyTitleID, key: "y", state: "bolt"},
		{id: commands.ClearStatusIcon, key: "x", state: "key"},
		{id: commands.ToggleLockID, key: "L", state: "bolt"},
		{id: commands.ToggleMuteID, key: "m", state: "bolt"},
		{id: commands.RestartSession, key: "r", state: "bolt", confirm: true},
		{id: commands.OverrideProfile, key: "c", state: "bolt", profile: true, confirm: true},
		{id: commands.CloseSession, key: "X", state: "bolt", confirm: true},
	}
}

// TestEveryMenuActionReachesItsEffectInBothModes is the session's "all twelve
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
		if _, cmd := send(t, m, commands.FocusDoneMsg{}); !quits(t, cmd) {
			t.Fatal("a confirmed focus did not leave for its destination")
		}
	})

	t.Run("a failed focus stays with the reason", func(t *testing.T) {
		m, _ := actionModel(t, sessions, 1, hostTabElsewhere)
		m = openMenu(t, m)
		m = press(t, m, "enter")
		m, cmd := send(t, m, commands.FocusDoneMsg{Err: errors.New("no matching tab")})
		if quits(t, cmd) {
			t.Fatal("a failed focus left the overview")
		}
		if got := m.feedback(); !strings.Contains(got, "no matching tab") {
			t.Fatalf("the feedback row says %q, want the failure", got)
		}
	})

	for _, name := range []string{"o", "O"} {
		t.Run("an opened "+name+" leaves", func(t *testing.T) {
			m, _ := actionModel(t, sessions, 1, hostTabElsewhere)
			m = openMenu(t, m)
			m = press(t, m, name)
			if _, cmd := send(t, m, commands.PickerOpenDoneMsg{}); !quits(t, cmd) {
				t.Fatal("a shell or editor that opened did not leave for its destination")
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
		m, cmd := send(t, m, commands.ActionDoneMsg{Request: uint64(d.Request), Err: errors.New("kitten @ close-tab: no match")})
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

// closeSelected drives the whole close interaction — menu, mnemonic,
// confirmation, and the completion reporting that the tab was observed to go.
func closeSelected(t *testing.T, m model, r *recorder) model {
	t.Helper()
	m = openMenu(t, m)
	m = press(t, m, "X")
	if m.acts.surface != surfaceConfirm {
		t.Fatalf("`X` left surface %v, want the confirmation", m.acts.surface)
	}
	m = press(t, m, "y")
	d, ok := r.lastDispatch()
	if !ok || d.Action != commands.CloseSession {
		t.Fatalf("the confirmed close dispatched %v", r.actionIDs())
	}
	return only(t, m, commands.ActionDoneMsg{Request: uint64(d.Request)})
}

// TestAConfirmedCloseIsOfferedAnUndoAndRelaunches is the recovery lifecycle end
// to end: reserve at dispatch, promote on a confirmed close, claim on `u`.
func TestAConfirmedCloseIsOfferedAnUndoAndRelaunches(t *testing.T) {
	sessions := viewSessions(4)
	m, r := actionModel(t, sessions, 1, hostTabElsewhere)
	closed := m.sessions[m.selected]

	m = closeSelected(t, m, r)
	if !m.recoverable() {
		t.Fatal("a confirmed close left nothing to undo")
	}
	offer := m.feedback()
	if !strings.HasPrefix(offer, "closed ") || !strings.Contains(offer, "u undo (5s)") {
		t.Fatalf("the feedback row says %q, want the undo offer", offer)
	}
	// The offer is on the FEEDBACK row and nowhere else. A close is undoable for
	// five seconds; a permanent footer entry would go on naming `u` for the rest
	// of the session, which is the advertised-but-dead key this surface is not
	// allowed to have.
	if !strings.Contains(frameOf(m), "u undo (5s)") {
		t.Fatalf("the frame does not carry the undo offer:\n%s", frameOf(m))
	}
	if hints := hintRowOf(m); strings.Contains(hints, "undo") {
		t.Fatalf("the footer advertises the undo: %q", hints)
	}

	// The grid reconciles onto the nearest survivor when the close lands.
	m = only(t, m, commands.TickMsg{})
	survivors := append(append([]session.RunningSession{}, sessions[:1]...), sessions[2:]...)
	m = only(t, m, hosted(snapshot(m.refreshActiveSeq, 0, survivors), hostTabElsewhere))
	if m.selectedTabID != sessions[2].TabID {
		t.Fatalf("after the close the cursor is on tab %d, want the survivor that took its place (%d)", m.selectedTabID, sessions[2].TabID)
	}
	if !m.recoverable() {
		t.Fatal("the refresh that applied the close discarded its recovery")
	}

	m = press(t, m, "u")
	if len(r.relaunches) != 1 {
		t.Fatalf("`u` asked for %d relaunches, want 1", len(r.relaunches))
	}
	if got := r.relaunches[0].Session.Title; got != closed.EffectiveProject() {
		t.Fatalf("the relaunch names project %q, want the closed row's %q", got, closed.EffectiveProject())
	}
	if got := r.relaunches[0].Account.Name; got != closed.EffectiveProfile() {
		t.Fatalf("the relaunch names profile %q, want %q", got, closed.EffectiveProfile())
	}
	if m.recoverable() {
		t.Fatal("a claimed close is still offered; a second `u` would launch it twice")
	}

	// A second press takes nothing, and the outcome is reported.
	m = press(t, m, "u")
	if len(r.relaunches) != 1 {
		t.Fatalf("a second `u` asked for %d relaunches in total, want 1", len(r.relaunches))
	}
	m = only(t, m, recoveryDoneMsg{request: r.relaunchRequests[0], label: r.relaunches[0].Label})
	if got := m.feedback(); !strings.HasPrefix(got, "restored ") {
		t.Fatalf("the feedback row says %q, want the restoration", got)
	}
}

// TestAFailedRelaunchIsOfferedAgainForWhatWasLeftOfItsWindow. The deadline is the
// close's, not the retry's: an operator may retry for exactly as long as the
// close was going to stay undoable and no longer.
func TestAFailedRelaunchIsOfferedAgainForWhatWasLeftOfItsWindow(t *testing.T) {
	sessions := viewSessions(3)
	m, r := actionModel(t, sessions, 1, hostTabElsewhere)
	m = closeSelected(t, m, r)
	m = press(t, m, "u")

	m = only(t, m, recoveryDoneMsg{
		request: r.relaunchRequests[0],
		label:   r.relaunches[0].Label,
		err:     errors.New("launch failed"),
	})
	if !m.recoverable() {
		t.Fatal("a failed relaunch consumed the recovery; there is nothing left to retry")
	}
	// The released entry is back on the offer, so the failure has to displace the
	// offer rather than sit under it: redrawing "closed X · u undo" here would be
	// exactly what a dropped keystroke looks like.
	got := m.feedback()
	if !strings.Contains(got, "launch failed") {
		t.Fatalf("the feedback row says %q, want the failure", got)
	}
	if !strings.Contains(got, "u retry") {
		t.Fatalf("the feedback row says %q, want it to name the retry it is still granting", got)
	}

	// It expires with the window the close started, not with a fresh one.
	r.now = r.now.Add(sessionactions.RecoveryWindow + time.Second)
	if m.recoverable() {
		t.Fatal("the retry was granted a fresh five seconds")
	}
}

// TestReviewRecoveryRetryFeedbackDeadline is the retry hint against the close's
// own deadline rather than the report's.
//
// A released entry keeps the deadline its CLOSE was confirmed with, so the retry
// beside a failure has to be withdrawn at that deadline and not when the
// four-second report expires. The clock is injected, so each arrangement RENDERS
// the row at a chosen instant rather than asking the stack and trusting the two
// to agree: a failure inside the window read on both sides of the deadline, a
// failure that arrives after it, and a lapsed failure beside a close that is
// still recoverable — where the row has to name the entry `u` would actually
// take.
func TestReviewRecoveryRetryFeedbackDeadline(t *testing.T) {
	t.Run("a failure inside the window loses its retry at the deadline", func(t *testing.T) {
		sessions := viewSessions(3)
		m, r := actionModel(t, sessions, 1, hostTabElsewhere)
		m = closeSelected(t, m, r)
		confirmedAt := r.now

		// `u` late in the window, and the launch fails with half a second of it
		// left.
		r.now = confirmedAt.Add(4 * time.Second)
		m = press(t, m, "u")
		r.now = confirmedAt.Add(4500 * time.Millisecond)
		m = only(t, m, recoveryDoneMsg{
			request: r.relaunchRequests[0],
			label:   r.relaunches[0].Label,
			err:     errors.New("launch failed"),
		})

		if !m.recoverable() {
			t.Fatal("the failed relaunch left nothing claimable half a second before the deadline")
		}
		got := m.feedback()
		if !strings.Contains(got, "launch failed") {
			t.Fatalf("the feedback row says %q, want the failure", got)
		}
		if !strings.Contains(got, "u retry (1s)") {
			t.Fatalf("the feedback row says %q, want the retry named for what is LEFT of the window", got)
		}

		// Past the close's own deadline. The failure is still worth reading — its
		// report runs four seconds from the failure — but `u` refuses from here on,
		// so the row must stop naming it.
		r.now = confirmedAt.Add(sessionactions.RecoveryWindow + 100*time.Millisecond)
		if m.recoverable() {
			t.Fatal("the retry was granted time past the close's deadline")
		}
		got = m.feedback()
		if !strings.Contains(got, "launch failed") {
			t.Fatalf("the feedback row says %q, want the failure to stay visible", got)
		}
		if strings.Contains(got, "retry") {
			t.Fatalf("the feedback row says %q, want the retry withdrawn with the window", got)
		}
		frame := frameOf(m)
		if !strings.Contains(frame, "launch failed") || strings.Contains(frame, "retry") {
			t.Fatalf("the frame disagrees with the feedback row:\n%s", frame)
		}
	})

	t.Run("a failure after the window names no retry at all", func(t *testing.T) {
		sessions := viewSessions(3)
		m, r := actionModel(t, sessions, 1, hostTabElsewhere)
		m = closeSelected(t, m, r)
		confirmedAt := r.now

		r.now = confirmedAt.Add(4 * time.Second)
		m = press(t, m, "u")
		// The launch takes two seconds to fail, which is one second past the
		// deadline the claim was made under. The entry is released onto an offer
		// that has already expired.
		r.now = confirmedAt.Add(6 * time.Second)
		m = only(t, m, recoveryDoneMsg{
			request: r.relaunchRequests[0],
			label:   r.relaunches[0].Label,
			err:     errors.New("launch failed"),
		})

		if m.recoverable() {
			t.Fatal("releasing an expired entry made it claimable again")
		}
		got := m.feedback()
		if !strings.Contains(got, "launch failed") {
			t.Fatalf("the feedback row says %q, want the failure", got)
		}
		if strings.Contains(got, "retry") {
			t.Fatalf("the feedback row says %q, want no retry offered for an entry Claim refuses", got)
		}
		// And the key agrees with the row it is not advertised on: it takes
		// nothing, and refusing does not put a retry back.
		m = press(t, m, "u")
		if len(r.relaunches) != 1 {
			t.Fatalf("`u` past the window asked for %d relaunches in total, want the 1 already made", len(r.relaunches))
		}
		if got := m.feedback(); strings.Contains(got, "retry") {
			t.Fatalf("the refused keypress put a retry back on the row: %q", got)
		}
	})

	t.Run("a lapsed failure does not displace a close that is still recoverable", func(t *testing.T) {
		sessions := viewSessions(4)
		m, r := actionModel(t, sessions, 1, hostTabElsewhere)
		m = closeSelected(t, m, r)
		confirmedAt := r.now

		// Claim the first close a second in, then close a second row a second
		// after that, so the two entries expire two seconds apart.
		r.now = confirmedAt.Add(time.Second)
		m = press(t, m, "u")
		if len(r.relaunches) != 1 {
			t.Fatalf("`u` asked for %d relaunches, want 1", len(r.relaunches))
		}
		r.now = confirmedAt.Add(2 * time.Second)
		m = press(t, m, "tab")
		m = closeSelected(t, m, r)

		// The first relaunch fails a second past ITS deadline, while the second
		// close stays recoverable for another second.
		r.now = confirmedAt.Add(6 * time.Second)
		m = only(t, m, recoveryDoneMsg{
			request: r.relaunchRequests[0],
			label:   r.relaunches[0].Label,
			err:     errors.New("launch failed"),
		})

		restorable, ok := m.acts.recovery.Latest(r.now)
		if !ok {
			t.Fatal("the second close stopped being recoverable when the first relaunch failed")
		}
		if restorable.Label == r.relaunches[0].Label {
			t.Fatalf("the fixture claimed the wrong entry: %q is the one that failed", restorable.Label)
		}
		got := m.feedback()
		if strings.Contains(got, "retry") {
			t.Fatalf("the feedback row says %q, want no retry for the lapsed entry", got)
		}
		if !strings.Contains(got, "u undo") || !strings.Contains(got, restorable.Label) {
			t.Fatalf("the feedback row says %q, want the offer naming %q — the close `u` would actually take", got, restorable.Label)
		}
		// And it takes exactly that one.
		m = press(t, m, "u")
		if len(r.relaunches) != 2 {
			t.Fatalf("`u` asked for %d relaunches in total, want 2", len(r.relaunches))
		}
		if got := r.relaunches[1].Label; got != restorable.Label {
			t.Fatalf("`u` restored %q, want the %q the row named", got, restorable.Label)
		}
		if got := m.feedback(); !strings.Contains(got, "restoring "+restorable.Label) {
			t.Fatalf("the feedback row says %q, want the restoration the keystroke just started", got)
		}
	})
}

// TestOnlyAConfirmedCloseIsRecoverable. Relaunching a session that is still
// running produces a second copy of it rather than an undo, so a close that
// failed and one nobody could confirm both leave nothing behind.
func TestOnlyAConfirmedCloseIsRecoverable(t *testing.T) {
	sessions := viewSessions(3)
	for _, tc := range []struct {
		name       string
		completion commands.ActionDoneMsg
	}{
		{name: "a failed close", completion: commands.ActionDoneMsg{Err: errors.New("no matching tab")}},
		{name: "an unconfirmed close", completion: commands.ActionDoneMsg{Unconfirmed: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, r := actionModel(t, sessions, 1, hostTabElsewhere)
			m = openMenu(t, m)
			m = press(t, m, "X")
			m = press(t, m, "y")
			d, _ := r.lastDispatch()

			tc.completion.Request = uint64(d.Request)
			m = only(t, m, tc.completion)
			if m.recoverable() {
				t.Fatalf("%s was offered an undo", tc.name)
			}
			m = press(t, m, "u")
			if len(r.relaunches) != 0 {
				t.Fatalf("`u` after %s relaunched %d sessions", tc.name, len(r.relaunches))
			}
			if got := m.feedback(); got == "" {
				t.Fatalf("%s reported nothing", tc.name)
			}
		})
	}
}

// TestClosingTheHostingTabIsNeverRecoverable. Closing it takes the grid, the
// feedback row, and the five seconds the offer would have lasted with it, so an
// offer made there is an affordance that dies before it can be used.
func TestClosingTheHostingTabIsNeverRecoverable(t *testing.T) {
	sessions := viewSessions(3)
	m, r := actionModel(t, sessions, 1, sessions[1].TabID)

	m = closeSelected(t, m, r)
	if m.recoverable() {
		t.Fatal("closing the hosting tab was offered an undo")
	}
	if strings.Contains(m.feedback(), "u undo") {
		t.Fatalf("the feedback row offers an undo: %q", m.feedback())
	}
}

// TestAnExpiredOfferIsWithdrawn. The offer names a window, and the window is the
// stack's rather than this file's; a surface that went on drawing it would be
// promising something the stack has stopped granting.
func TestAnExpiredOfferIsWithdrawn(t *testing.T) {
	sessions := viewSessions(3)
	m, r := actionModel(t, sessions, 1, hostTabElsewhere)
	m = closeSelected(t, m, r)

	r.now = r.now.Add(sessionactions.RecoveryWindow + time.Millisecond)
	if m.recoverable() {
		t.Fatal("the offer outlived its window")
	}
	if strings.Contains(m.feedback(), "u undo") {
		t.Fatalf("the feedback row still offers the expired undo: %q", m.feedback())
	}
	m = press(t, m, "u")
	if len(r.relaunches) != 0 {
		t.Fatalf("`u` after expiry relaunched %d sessions", len(r.relaunches))
	}
	if got := m.feedback(); strings.Contains(got, "restoring") {
		t.Fatalf("the expired `u` says %q, want it to have claimed nothing", got)
	}
}

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
	for _, hint := range []string{"Enter focus", "a actions", "? help", "q exit"} {
		if !strings.Contains(frame, hint) {
			t.Fatalf("the empty grid lost the %q hint:\n%s", hint, frame)
		}
	}
	if got := m.feedback(); !strings.Contains(got, "u undo") {
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
