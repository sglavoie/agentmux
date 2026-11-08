package overview

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/tui/commands"
)

// TestTheConstructorOwnsTheFirstSequenceAndInitOnlyDispatchesIt is the review
// session 3 asks for, written down as an assertion.
//
// The failure it guards is specific: if Init drew the sequence, its value receiver
// would discard the in-flight bit along with its copy of the model, the first
// interval tick would find an idle controller, and two startup reads would race —
// the slower of which could then apply an older tree over a newer one.
func TestTheConstructorOwnsTheFirstSequenceAndInitOnlyDispatchesIt(t *testing.T) {
	m, r := newTestModel(77)
	if m.refreshActiveSeq != firstRefreshSeq || m.refreshNextSeq != firstRefreshSeq {
		t.Fatalf("the constructor armed seq %d/%d, want %d", m.refreshActiveSeq, m.refreshNextSeq, firstRefreshSeq)
	}
	if !m.refreshInFlight {
		t.Fatal("the constructor did not mark the startup read in flight")
	}
	if len(r.topology) != 0 {
		t.Fatalf("the constructor itself dispatched %d reads; the command belongs to Init", len(r.topology))
	}

	m.Init()
	if len(r.topology) != 1 || r.topology[0] != (topologyAsk{seq: firstRefreshSeq, sourceWindowID: 77}) {
		t.Fatalf("Init dispatched %v, want exactly one read of seq %d for window 77", r.topology, firstRefreshSeq)
	}
	if len(r.ticks) != 1 || r.ticks[0] != 2*time.Second {
		t.Fatalf("Init scheduled %v, want exactly one 2s interval", r.ticks)
	}

	// The tick that arrives one interval later must not become a second startup
	// read.
	m = only(t, m, commands.TickMsg{})
	if len(r.topology) != 1 {
		t.Fatalf("a tick during the startup read dispatched %d reads, want the startup one alone", len(r.topology))
	}
	if !m.refreshPending {
		t.Fatal("the tick left no pending request, so its refresh was lost rather than coalesced")
	}
}

// TestTheConfiguredIntervalDrivesTheTicker pins that the cadence is the
// operator's, with the documented fallback.
func TestTheConfiguredIntervalDrivesTheTicker(t *testing.T) {
	for _, tc := range []struct {
		configured int
		want       time.Duration
	}{
		{configured: 0, want: 2 * time.Second},
		{configured: 5, want: 5 * time.Second},
		{configured: 30, want: 30 * time.Second},
		{configured: -1, want: 2 * time.Second},
	} {
		cfg := viewConfig()
		cfg.Defaults.RefreshInterval = tc.configured
		m := newModel(cfg, nil, 0)
		r := &recorder{now: viewNow}
		r.install(&m)
		m.Init()
		if len(r.ticks) != 1 || r.ticks[0] != tc.want {
			t.Errorf("refresh_interval=%d scheduled %v, want %v", tc.configured, r.ticks, tc.want)
		}
		// Every later tick re-arms at the same interval, so the cadence is a
		// property of the model rather than of Init.
		only(t, m, commands.TickMsg{})
		if len(r.ticks) != 2 || r.ticks[1] != tc.want {
			t.Errorf("refresh_interval=%d re-armed at %v, want %v", tc.configured, r.ticks, tc.want)
		}
	}
}

// TestTicksCoalesceIntoOneCatchUpRead is the serialization rule: however many
// intervals elapse during one slow read, they cost exactly one follow-up.
func TestTicksCoalesceIntoOneCatchUpRead(t *testing.T) {
	m, r := started(t, 0)

	for i := 0; i < 4; i++ {
		m = only(t, m, commands.TickMsg{})
	}
	if len(r.topology) != 1 {
		t.Fatalf("ticks during an in-flight read dispatched %d reads, want the startup one alone", len(r.topology))
	}
	if len(r.ticks) != 5 {
		t.Fatalf("the ticker was re-armed %d times, want one per tick plus Init's", len(r.ticks))
	}

	m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(3)))
	if len(r.topology) != 2 {
		t.Fatalf("four coalesced ticks produced %d catch-up reads, want exactly 1", len(r.topology)-1)
	}
	if m.refreshPending {
		t.Fatal("the catch-up left the pending bit set; it would fire again after the next completion")
	}
	if !m.refreshInFlight || r.topology[1].seq != m.refreshActiveSeq {
		t.Fatalf("the catch-up dispatched seq %d, want the active %d", r.topology[1].seq, m.refreshActiveSeq)
	}
	if r.topology[1].seq == r.topology[0].seq {
		t.Fatalf("the catch-up reused seq %d; the settled read's own answer would then be applied twice", r.topology[1].seq)
	}
}

// TestASuccessWithNothingPendingStartsNothing is the control for the test above.
func TestASuccessWithNothingPendingStartsNothing(t *testing.T) {
	m, r := started(t, 0)
	m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(3)))
	if len(r.topology) != 1 {
		t.Fatalf("a completion with nothing pending dispatched %d reads, want 0", len(r.topology)-1)
	}
	if m.refreshInFlight {
		t.Fatal("a settled read left the in-flight bit set; every later tick would coalesce forever")
	}
}

// TestAnObsoleteCompletionChangesNothing is the stale-response rule. A read that
// was superseded before it landed must not move the list, the freshness stamp, or
// the selection backwards.
func TestAnObsoleteCompletionChangesNothing(t *testing.T) {
	m, _ := started(t, 0)
	m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(3)))
	m = press(t, m, "tab")
	before := m

	for _, msg := range []tea.Msg{
		snapshot(firstRefreshSeq, 0, viewSessions(9)),                   // already settled
		snapshot(firstRefreshSeq+5, 0, viewSessions(9)),                 // never issued
		commands.ErrMsg{Seq: firstRefreshSeq, Err: errors.New("stale")}, // already settled
	} {
		m = only(t, m, msg)
		if got := len(m.sessions); got != 3 {
			t.Fatalf("%T with an obsolete sequence installed %d sessions, want the 3 already shown", msg, got)
		}
		if m.selected != before.selected || m.selectedTabID != before.selectedTabID {
			t.Fatalf("%T with an obsolete sequence moved the selection to %d/tab %d", msg, m.selected, m.selectedTabID)
		}
		if m.err != nil {
			t.Fatalf("%T with an obsolete sequence recorded %v", msg, m.err)
		}
		if !m.topologyObservedAt.Equal(before.topologyObservedAt) {
			t.Fatalf("%T with an obsolete sequence restamped the freshness reading", msg)
		}
	}
}

// TestAnObsoleteCompletionDoesNotSettleTheReadInFlight is the other half of the
// stale rule, and the one that would otherwise hang the surface: if a superseded
// completion cleared the in-flight bit, the read still genuinely outstanding would
// be settled by nobody and its own answer would then be dropped as obsolete.
func TestAnObsoleteCompletionDoesNotSettleTheReadInFlight(t *testing.T) {
	m, r := started(t, 0)
	m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(3)))
	m = only(t, m, commands.TickMsg{})
	live := m.refreshActiveSeq

	m = only(t, m, snapshot(live-1, 0, viewSessions(9)))
	if !m.refreshInFlight {
		t.Fatal("an obsolete completion settled the read that is still out")
	}
	m = only(t, m, snapshot(live, 0, viewSessions(9)))
	if m.refreshInFlight {
		t.Fatal("the live completion did not settle its own read")
	}
	if len(m.sessions) != 9 {
		t.Fatalf("the live completion installed %d sessions, want 9", len(m.sessions))
	}
	if len(r.topology) != 2 {
		t.Fatalf("%d reads were dispatched, want the startup one and the tick's", len(r.topology))
	}
}

// TestAFailedReadRetainsTheListAndWaitsForTheNextTick is the anti-storm rule plus
// the retention rule. A daemon and tmux that are both down stay down for more
// than a moment, and a surface that emptied itself over a timed-out subprocess
// would be worse than one showing a slightly old list.
func TestAFailedReadRetainsTheListAndWaitsForTheNextTick(t *testing.T) {
	m, r := started(t, 0)
	m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(4)))
	m = press(t, m, "tab")
	selected := m.selectedTabID
	stamped := m.topologyObservedAt

	m = only(t, m, commands.TickMsg{})
	live := m.refreshActiveSeq
	m = only(t, m, commands.TickMsg{})
	if !m.refreshPending {
		t.Fatal("a tick during the outstanding read left no pending request")
	}

	outage := errors.New("tmux list-sessions: signal: killed")
	m = only(t, m, commands.ErrMsg{Seq: live, Err: outage})
	if len(r.topology) != 2 {
		t.Fatalf("a failure dispatched %d immediate retries, want 0", len(r.topology)-2)
	}
	if m.refreshPending {
		t.Fatal("a failure left the pending bit set; the next completion would fire a surprise retry")
	}
	if len(m.sessions) != 4 || m.selectedTabID != selected {
		t.Fatalf("a failure left %d sessions selected at tab %d, want the 4 and tab %d it was showing", len(m.sessions), m.selectedTabID, selected)
	}
	if !m.topologyObservedAt.Equal(stamped) {
		t.Fatal("a failure restamped the freshness reading; the rows would stop aging out of their own last capture")
	}
	if !errors.Is(m.err, outage) {
		t.Fatalf("a failure recorded %v, want the outage", m.err)
	}

	// The next tick is the retry, and a success clears the error.
	m = only(t, m, commands.TickMsg{})
	if len(r.topology) != 3 {
		t.Fatalf("the tick after a failure dispatched %d reads, want 1", len(r.topology)-2)
	}
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, viewSessions(4)))
	if m.err != nil {
		t.Fatalf("recovery left %v behind", m.err)
	}
	if m.selectedTabID != selected {
		t.Fatalf("recovery moved the selection to tab %d, want the retained tab %d", m.selectedTabID, selected)
	}
}

// TestRepeatedFailuresCostOneReadPerInterval generalizes the anti-storm rule over
// a sustained outage, which is the shape an operator actually meets: a daemon
// restarting, or tmux unavailable while the overlay window is still up.
func TestRepeatedFailuresCostOneReadPerInterval(t *testing.T) {
	m, r := started(t, 0)
	m = only(t, m, commands.ErrMsg{Seq: m.refreshActiveSeq, Err: errors.New("down")})
	for i := 0; i < 6; i++ {
		m = only(t, m, commands.TickMsg{})
		m = only(t, m, commands.ErrMsg{Seq: m.refreshActiveSeq, Err: errors.New("still down")})
	}
	if len(r.topology) != 7 {
		t.Fatalf("six intervals of outage cost %d reads, want one per interval plus the startup one", len(r.topology))
	}
	if m.refreshInFlight || m.refreshPending {
		t.Fatalf("the controller is wedged: in-flight=%t pending=%t", m.refreshInFlight, m.refreshPending)
	}
}
