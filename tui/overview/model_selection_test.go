package overview

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

func TestOpeningSelectsTopLeftAndMarksTheSourcePane(t *testing.T) {
	m, r := started(t, 30)
	if r.topology[0].sourceWindowID != 30 {
		t.Fatalf("the startup read asked about window %d, want 30", r.topology[0].sourceWindowID)
	}

	m = only(t, m, snapshot(firstRefreshSeq, 3, viewSessions(5)))
	if m.selected != 0 || m.selectedTabID != m.sessions[0].TabID {
		t.Fatalf("selected index %d/tab %d, want the top-left card", m.selected, m.selectedTabID)
	}
	if got, ok := m.selectedSession(); !ok || got.TabID != m.sessions[0].TabID {
		t.Fatalf("the index addresses tab %d rather than the top-left card", got.TabID)
	}
	if m.sourceTabID != 3 || m.sourceTabID == m.selectedTabID {
		t.Fatalf("source tab = %d, want tab 3 independently of selection", m.sourceTabID)
	}
}

// TestAnUnmanagedSourceSelectsTheFirstSession covers the pane the overview was
// launched from having no managed session — a scratch shell, or the dashboard's
// own window. RefreshOverview reports a zero tab for it, and a zero tab is not a
// selection request.
func TestAnUnmanagedSourceSelectsTheFirstSession(t *testing.T) {
	for _, sourceWindowID := range []int{0, 999} {
		m, _ := started(t, sourceWindowID)
		m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(5)))
		if m.selected != 0 || m.selectedTabID != m.sessions[0].TabID {
			t.Errorf("source window %d selected index %d/tab %d, want the first session", sourceWindowID, m.selected, m.selectedTabID)
		}
		if m.sourceTabID != 0 {
			t.Fatalf("unmanaged source marked tab %d", m.sourceTabID)
		}
	}
}

// TestASourceTabMissingFromTheListSelectsTheFirstSession is the unmanaged case's
// near neighbor: the source pane IS managed, but its tab is not in the tree the
// read came back with.
func TestASourceTabMissingFromTheListSelectsTheFirstSession(t *testing.T) {
	m, _ := started(t, 4242)
	m = only(t, m, snapshot(firstRefreshSeq, 4242, viewSessions(5)))
	if m.selected != 0 || m.selectedTabID != m.sessions[0].TabID {
		t.Fatalf("selected index %d/tab %d, want the first session", m.selected, m.selectedTabID)
	}
	if m.startupSourceWindowID != 0 {
		t.Fatal("a resolved-but-absent source tab left the startup request unspent; it would reassert itself later")
	}
	if m.sourceTabID != 0 {
		t.Fatalf("absent source marked tab %d", m.sourceTabID)
	}
}

// TestTheStartupRequestSurvivesAFailedFirstLoad is why the request is consumed at
// the SUCCESS rather than at the dispatch: a first read that reached nothing
// resolved nothing, so the source marker still needs resolving.
func TestTheStartupRequestSurvivesAFailedFirstLoad(t *testing.T) {
	m, r := started(t, 30)
	m = only(t, m, commands.ErrMsg{Seq: firstRefreshSeq, Err: errors.New("daemon unreachable")})
	if m.startupSourceWindowID != 30 {
		t.Fatalf("a failed first load consumed the startup request (now %d)", m.startupSourceWindowID)
	}

	m = only(t, m, commands.TickMsg{})
	if got := r.topology[len(r.topology)-1].sourceWindowID; got != 30 {
		t.Fatalf("the retry asked about window %d, want the retained 30", got)
	}
	m = only(t, m, snapshot(m.refreshActiveSeq, 3, viewSessions(5)))
	if m.selected != 0 || m.selectedTabID != m.sessions[0].TabID || m.sourceTabID != 3 {
		t.Fatalf("retry selected index %d/tab %d and source %d, want top-left and source tab 3", m.selected, m.selectedTabID, m.sourceTabID)
	}
}

// TestTheStartupRequestIsSpentByTheFirstSuccess is the other side of the rule. The
// request is a one-shot: a later read that still reports a source tab must not drag
// the cursor back off wherever the operator has navigated to.
func TestTheStartupRequestIsSpentByTheFirstSuccess(t *testing.T) {
	m, r := started(t, 30)
	m = only(t, m, snapshot(firstRefreshSeq, 3, viewSessions(5)))
	if m.startupSourceWindowID != 0 {
		t.Fatalf("the first success left the startup request at %d, want it spent", m.startupSourceWindowID)
	}

	m = press(t, m, "tab")
	moved := m.selectedTabID
	if moved == m.sessions[0].TabID {
		t.Fatal("Tab did not move the selection off the first card; the rest of this test proves nothing")
	}

	m = only(t, m, commands.TickMsg{})
	if got := r.topology[len(r.topology)-1].sourceWindowID; got != 0 {
		t.Fatalf("a later read still asked about window %d, want the spent request to be 0", got)
	}
	m = only(t, m, snapshot(m.refreshActiveSeq, 3, viewSessions(5)))
	if m.selectedTabID != moved {
		t.Fatalf("a later read reasserted the source tab, moving the selection to %d from %d", m.selectedTabID, moved)
	}
	if m.sourceTabID != 3 {
		t.Fatalf("navigation or refresh changed the source marker to tab %d", m.sourceTabID)
	}
}

func TestSourceMarkerSurvivesNavigationAndAnAnchorChange(t *testing.T) {
	sessions := viewSessions(3)
	m, _ := started(t, sessions[2].WindowID)
	m = only(t, m, snapshot(firstRefreshSeq, sessions[2].TabID, sessions))
	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	if !strings.Contains(frameOf(m), "Opened from here") {
		t.Fatal("the opening frame does not identify the launching card")
	}
	m = press(t, m, "tab")
	selected := m.selectedTabID
	sessions[2].WindowID = 999
	m = only(t, m, commands.TickMsg{})
	// Later topology completions cannot replace the original source marker.
	m = only(t, m, snapshot(m.refreshActiveSeq, sessions[0].TabID, sessions))
	if m.sourceTabID != sessions[2].TabID || m.selectedTabID != selected {
		t.Fatalf("refresh changed source or selection: source %d, selected %d", m.sourceTabID, m.selectedTabID)
	}
	if !strings.Contains(frameOf(m), "Opened from here") {
		t.Fatal("the restarted launching card lost its marker")
	}
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, sessions[:2]))
	if strings.Contains(frameOf(m), "Opened from here") {
		t.Fatal("a closed source left a marker on another card")
	}
}

// A newly discovered tab cannot move an existing tile under the operator.
func TestTheSelectionStaysPutWhenANewTabArrives(t *testing.T) {
	m, _ := started(t, 0)
	later := viewSession(4, "zzz-later", "work", "claude")
	earlier := viewSession(5, "aaa-earlier", "personal", "codex")

	m = only(t, m, snapshot(firstRefreshSeq, 0, []session.RunningSession{later}))
	if m.selected != 0 || m.selectedTabID != 4 {
		t.Fatalf("the only session was not selected: index %d, tab %d", m.selected, m.selectedTabID)
	}

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, []session.RunningSession{later, earlier}))
	if got := tabs(m); len(got) != 2 || got[0] != 4 || got[1] != 5 {
		t.Fatalf("the ordered list is %v, want the new project after the existing tile", got)
	}
	if m.selected != 0 || m.selectedTabID != 4 {
		t.Fatalf("the selection is index %d/tab %d, want index 0 still on tab 4", m.selected, m.selectedTabID)
	}
}

func TestOpeningPutsDoneBeforeCompacting(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     string
		unseen    bool
		agentDone bool
	}{
		{name: "seen completion", state: "stop"},
		{name: "unseen completion", state: "stop", unseen: true},
		{name: "completed agent under active ASL", state: "bolt", agentDone: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compacting := viewSession(1, "alpha", "work", "codex")
			compacting.State = "compact"
			done := viewSession(2, "zeta", "work", "codex")
			done.State, done.Unseen = tc.state, tc.unseen
			if tc.agentDone {
				done.AgentTurn = &protocol.AgentTurnSnapshot{IconState: "stop"}
			}
			m, _ := started(t, 0)
			m = only(t, m, snapshot(firstRefreshSeq, 0, []session.RunningSession{compacting, done}))
			if got, want := tabs(m), []int{2, 1}; !reflect.DeepEqual(got, want) {
				t.Fatalf("opening tiles = %v, want Done top-left before Compacting: %v", got, want)
			}
		})
	}
}

func TestRefreshMovesTilesToTheirTierWhileRetainingTierOrderAndSelection(t *testing.T) {
	working := viewSession(1, "alpha", "work", "codex")
	working.State = "bolt"
	idle := viewSession(2, "zeta", "work", "codex")
	idle.State = "stop"
	attention := viewSession(3, "omega", "work", "codex")
	attention.State = "question"
	m, _ := started(t, 0)
	m = only(t, m, snapshot(firstRefreshSeq, 0, []session.RunningSession{working, idle, attention}))
	if got, want := tabs(m), []int{3, 2, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("opening tiles = %v, want %v", got, want)
	}
	if m.selectedTabID != 3 {
		t.Fatalf("opening selection = %d, want first attention tab 3", m.selectedTabID)
	}

	// A card moves to the tier its new status belongs in, but no tier is
	// sorted again when tmux returns the rows in a different order.
	working.State = "key"
	attention.State = "bolt"
	newcomer := viewSession(4, "aardvark", "work", "codex")
	newcomer.State = "error"
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, []session.RunningSession{newcomer, attention, working, idle}))
	if got, want := tabs(m), []int{1, 4, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("refreshed tiles = %v, want attention, then Done, then Working: %v", got, want)
	}
	if m.sessions[0].State != "key" || m.sessions[3].State != "bolt" {
		t.Fatal("the cards did not receive the refreshed status")
	}
	if m.selectedTabID != 3 || m.selected != 3 {
		t.Fatalf("selection = index %d/tab %d, want index 3 still on tab 3", m.selected, m.selectedTabID)
	}
	// Severity and metadata changes within a tier do not reshuffle it.
	working.State = "question"
	newcomer.Project = "aaa-renamed"
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, []session.RunningSession{idle, newcomer, attention, working}))
	if got, want := tabs(m), []int{1, 4, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("repeat refresh tiles = %v, want unchanged tier order %v", got, want)
	}

	// A new F2 program gets a fresh triage snapshot rather than the old order.
	reopened, _ := started(t, 0)
	reopened = only(t, reopened, snapshot(firstRefreshSeq, 0, []session.RunningSession{idle, attention, working, newcomer}))
	if got, want := tabs(reopened), []int{4, 1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened tiles = %v, want %v", got, want)
	}
}

// TestAMetadataUpdateLeavesTheSelectionAlone covers the ordinary case that happens
// every two seconds: the same tabs come back with new states and new busy
// timestamps.
func TestAMetadataUpdateLeavesTheSelectionAlone(t *testing.T) {
	sessions := viewSessions(6)
	m, _ := loadedAt(t, sessions, 3)
	index, tabID := m.selected, m.selectedTabID

	updated := viewSessions(6)
	for i := range updated {
		updated[i].State = "key"
		updated[i].BusySince = viewNow.Add(-9 * time.Minute)
	}
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, updated))
	if m.selected != index || m.selectedTabID != tabID {
		t.Fatalf("a metadata update moved the selection to %d/tab %d, want %d/tab %d", m.selected, m.selectedTabID, index, tabID)
	}
	if m.sessions[m.selected].State != "key" {
		t.Fatalf("the selected row still says %q; the update did not reach the list", m.sessions[m.selected].State)
	}
}

// TestOnlyTheActivityMapIsHeldFromARead pins the deliberate drop. The per-window
// state a tile paints is already overlaid onto these rows by the shared reader, and
// the watchdog is a dashboard status-bar input the grid has no slot for, so holding
// either here would be state nothing reads — and state nothing reads is state that
// goes stale silently.
func TestOnlyTheActivityMapIsHeldFromARead(t *testing.T) {
	sessions := viewSessions(3)
	msg := snapshot(firstRefreshSeq, 0, sessions)
	msg.Activity = map[int]time.Time{sessions[0].WindowID: viewNow.Add(-time.Minute)}
	msg.Watchdog = map[int]protocol.WatchdogState{sessions[0].WindowID: {}}
	msg.States = map[int]protocol.WindowStateSnapshot{sessions[0].WindowID: {}}

	m, _ := started(t, 0)
	m = only(t, m, msg)
	if len(m.lastActivity) != 1 {
		t.Fatalf("the activity map holds %d entries, want the one the read carried", len(m.lastActivity))
	}
	if got := m.lastActivity[sessions[0].WindowID]; !got.Equal(viewNow.Add(-time.Minute)) {
		t.Fatalf("the activity entry is %v, want the read's", got)
	}
}
