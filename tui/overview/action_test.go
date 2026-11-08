// The acting half's tests: the menu, the two overlays it raises, the thirteen
// actions behind them, and the undo the grid offers after a close.
//
// They are written against the same recorder the lifecycle tests use, for the
// same reason: every effect runs through a seam that RECORDS and answers nothing,
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
	"fmt"
	"strings"
	"testing"

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
	if len(want) != 13 {
		t.Fatalf("the shared vocabulary offers %d menu actions, want the 13 this surface promises", len(want))
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
