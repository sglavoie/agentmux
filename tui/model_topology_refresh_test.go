package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// topologyRefreshRecorder replaces the model's live-topology seam with one that
// records what it was asked for and runs nothing. Recording happens when the
// command is built, not when it runs, so a test can see every dispatch without
// a Bubble Tea runtime and without touching a daemon or Kitty.
type topologyRefreshRecorder struct {
	dispatches []topologyDispatch
}

type topologyDispatch struct {
	seq            int
	sourceWindowID int
}

func recordTopologyRefreshes(m Model) (Model, *topologyRefreshRecorder) {
	recorder := &topologyRefreshRecorder{}
	m.backgroundCommands.topologyRefresh = func(_ Model, seq, sourceWindowID int) tea.Cmd {
		recorder.dispatches = append(recorder.dispatches, topologyDispatch{seq: seq, sourceWindowID: sourceWindowID})
		return func() tea.Msg { return nil }
	}
	return m, recorder
}

func (r *topologyRefreshRecorder) count() int { return len(r.dispatches) }

func (r *topologyRefreshRecorder) last() topologyDispatch {
	return r.dispatches[len(r.dispatches)-1]
}

func send(m Model, msg tea.Msg) Model {
	updated, _ := m.Update(msg)
	return updated.(Model)
}

// armedModel returns a model whose constructor-armed startup refresh is
// outstanding, along with the recorder and that refresh's sequence. This is the
// state the dashboard is in for its whole first interval, so it is where the
// coalescing rules matter most.
func armedModel(t *testing.T) (Model, *topologyRefreshRecorder, int) {
	t.Helper()
	m, recorder := recordTopologyRefreshes(testModel())
	if !m.topologyRefreshInFlight {
		t.Fatal("constructor left no refresh in flight; the first tick would start a second one")
	}
	return m, recorder, m.topologyRefreshActiveSeq
}

// TestTicksCoalesceIntoOnePendingRefresh is the core of the serialization rule:
// however many ticks arrive during one slow refresh, they cost exactly one
// follow-up.
func TestTicksCoalesceIntoOnePendingRefresh(t *testing.T) {
	m, recorder, startupSeq := armedModel(t)

	for range 3 {
		m = send(m, commands.TickMsg{})
	}
	if recorder.count() != 0 {
		t.Fatalf("ticks during an in-flight refresh dispatched %d refreshes, want 0", recorder.count())
	}
	if !m.topologyRefreshPending {
		t.Fatal("ticks during an in-flight refresh left no pending request")
	}

	m = send(m, commands.SessionsMsg{Seq: startupSeq, Sessions: makeSessions()})
	if recorder.count() != 1 {
		t.Fatalf("three coalesced ticks produced %d catch-up refreshes, want exactly 1", recorder.count())
	}
	if m.topologyRefreshPending {
		t.Fatal("catch-up left the pending bit set; it would fire again after the next completion")
	}
	if !m.topologyRefreshInFlight || recorder.last().seq != m.topologyRefreshActiveSeq {
		t.Fatalf("catch-up dispatched seq %d, want the active %d", recorder.last().seq, m.topologyRefreshActiveSeq)
	}
}

// TestPostActionRefreshCoalescesWithTicks pins that the action follow-up enters
// through the same controller as the tick. An action taken during a refresh is
// the realistic way two refreshes would otherwise overlap.
func TestPostActionRefreshCoalescesWithTicks(t *testing.T) {
	m, recorder, startupSeq := armedModel(t)

	m = send(m, commands.TickMsg{})
	m = send(m, commands.ActionDoneMsg{})
	m = send(m, commands.ResortDoneMsg{})
	if recorder.count() != 0 {
		t.Fatalf("actions during an in-flight refresh dispatched %d refreshes, want 0", recorder.count())
	}

	send(m, commands.SessionsMsg{Seq: startupSeq, Sessions: makeSessions()})
	if recorder.count() != 1 {
		t.Fatalf("a tick plus two actions produced %d catch-up refreshes, want exactly 1", recorder.count())
	}
}

// TestActionRefreshesWhenNothingIsInFlight is the control for the test above:
// an action still refreshes promptly when it is not competing with one.
func TestActionRefreshesWhenNothingIsInFlight(t *testing.T) {
	m, recorder, startupSeq := armedModel(t)
	m = send(m, commands.SessionsMsg{Seq: startupSeq, Sessions: makeSessions()})
	if recorder.count() != 0 {
		t.Fatalf("a completion with nothing pending dispatched %d refreshes, want 0", recorder.count())
	}

	m = send(m, commands.ActionDoneMsg{})
	if recorder.count() != 1 {
		t.Fatalf("an action with nothing in flight dispatched %d refreshes, want 1", recorder.count())
	}
	if !m.topologyRefreshInFlight {
		t.Fatal("action refresh did not mark itself in flight")
	}
}

// TestFailedRefreshWaitsForTheNextTick pins the anti-storm rule. A daemon and a
// Kitty that are both down stay down for more than a moment; retrying the
// instant the failure lands would spin against them at message speed.
func TestFailedRefreshWaitsForTheNextTick(t *testing.T) {
	m, recorder, startupSeq := armedModel(t)
	m = send(m, commands.TickMsg{})
	if !m.topologyRefreshPending {
		t.Fatal("tick during the startup refresh left no pending request")
	}

	m = send(m, commands.ErrMsg{Seq: startupSeq, Err: errors.New("kitten @ ls: signal: killed")})
	if recorder.count() != 0 {
		t.Fatalf("failure dispatched %d immediate retries, want 0", recorder.count())
	}
	if m.topologyRefreshPending {
		t.Fatal("failure left the pending bit set; the next completion would fire a surprise retry")
	}
	if m.topologyRefreshInFlight {
		t.Fatal("failure left the refresh in flight; every later request would coalesce forever")
	}
	if m.err == nil {
		t.Fatal("failure was not surfaced on the model")
	}

	m = send(m, commands.TickMsg{})
	if recorder.count() != 1 {
		t.Fatalf("the tick after a failure dispatched %d refreshes, want 1", recorder.count())
	}
}

// TestFailedRefreshRetainsTheLastSessionList is the completion contract's third
// clause: a read that failed must not empty a dashboard that was working.
func TestFailedRefreshRetainsTheLastSessionList(t *testing.T) {
	m, _, startupSeq := armedModel(t)
	m = send(m, commands.SessionsMsg{Seq: startupSeq, Sessions: makeSessions()})
	before := len(m.filtered)
	if before == 0 {
		t.Fatal("setup produced no rows to lose")
	}

	// Arm the next refresh the way a tick would, then fail it.
	m = send(m, commands.TickMsg{})
	m = send(m, commands.ErrMsg{Seq: m.topologyRefreshActiveSeq, Err: errors.New("daemon unavailable")})

	if len(m.sessions) != len(makeSessions()) || len(m.filtered) != before {
		t.Fatalf("after a failed refresh: %d sessions / %d rows, want %d / %d", len(m.sessions), len(m.filtered), len(makeSessions()), before)
	}
}

// TestObsoleteCompletionsAreIgnored covers both ways a completion can be
// obsolete: it carries a superseded sequence, or it arrives when the controller
// is waiting on nothing at all.
func TestObsoleteCompletionsAreIgnored(t *testing.T) {
	m, _, startupSeq := armedModel(t)
	m = send(m, commands.SessionsMsg{Seq: startupSeq, Sessions: makeSessions()})
	m = send(m, commands.TickMsg{})
	activeSeq := m.topologyRefreshActiveSeq
	if activeSeq == startupSeq {
		t.Fatal("the second refresh reused the first refresh's sequence")
	}

	// A straggler from the settled startup refresh.
	stale := []session.RunningSession{{TabID: 99, Base: "stale", Account: "work"}}
	m = send(m, commands.SessionsMsg{Seq: startupSeq, Sessions: stale})
	if len(m.sessions) != len(makeSessions()) {
		t.Fatalf("a superseded completion replaced the list: %+v", m.sessions)
	}
	if !m.topologyRefreshInFlight {
		t.Fatal("a superseded completion settled the outstanding refresh")
	}

	// An unsequenced completion belonging to no refresh this model started.
	m = send(m, commands.SessionsMsg{Sessions: stale})
	if len(m.sessions) != len(makeSessions()) {
		t.Fatalf("an unsequenced completion replaced the list: %+v", m.sessions)
	}

	// A superseded failure must not settle the live refresh either.
	m = send(m, commands.ErrMsg{Seq: startupSeq, Err: errors.New("old failure")})
	if m.err != nil || !m.topologyRefreshInFlight {
		t.Fatalf("a superseded failure landed: err=%v inFlight=%v", m.err, m.topologyRefreshInFlight)
	}

	m = send(m, commands.SessionsMsg{Seq: activeSeq, Sessions: stale})
	if len(m.sessions) != 1 || m.sessions[0].TabID != 99 {
		t.Fatalf("the active refresh's completion was not applied: %+v", m.sessions)
	}
}

// TestStartupSelectionSurvivesAnInitialFailure pins why the source window ID is
// held until the first usable result rather than spent on the first attempt: a
// dashboard opened from a tab must still land on that tab when the first
// refresh fails.
func TestStartupSelectionSurvivesAnInitialFailure(t *testing.T) {
	base := testModel()
	base.initialSourceWindowID = 42
	m, recorder := recordTopologyRefreshes(base)
	startupSeq := m.topologyRefreshActiveSeq

	m.Init()
	if recorder.count() != 1 || recorder.last() != (topologyDispatch{seq: startupSeq, sourceWindowID: 42}) {
		t.Fatalf("startup dispatched %+v, want one refresh for window 42", recorder.dispatches)
	}

	m = send(m, commands.ErrMsg{Seq: startupSeq, Err: errors.New("daemon unavailable")})
	m = send(m, commands.TickMsg{})
	if recorder.count() != 2 || recorder.last().sourceWindowID != 42 {
		t.Fatalf("the retry after a failure dispatched %+v, want it to still carry window 42", recorder.dispatches)
	}

	// The retry succeeds and resolves the window into a tab, so the selection
	// is spent and later refreshes stop asking for it.
	m = send(m, commands.SessionsMsg{
		Seq:            m.topologyRefreshActiveSeq,
		Sessions:       makeSessions(),
		SelectionTabID: 2,
	})
	if m.initialSourceWindowID != 0 {
		t.Fatalf("startup source window = %d, want it spent after a usable result", m.initialSourceWindowID)
	}
	if got := m.filtered[m.cursor].TabID; got != 2 {
		t.Fatalf("cursor landed on tab %d, want the source tab 2", got)
	}

	m = send(m, commands.TickMsg{})
	if recorder.count() != 3 || recorder.last().sourceWindowID != 0 {
		t.Fatalf("the refresh after a usable result dispatched %+v, want no source window", recorder.dispatches)
	}
}

// TestSessionsMsgStoresTopologyFreshness pins that the freshness metadata
// reaches the model, which is what lets later work tell a daemon snapshot from
// a listing this process ran.
func TestSessionsMsgStoresTopologyFreshness(t *testing.T) {
	m, _, startupSeq := armedModel(t)
	m = send(m, commands.SessionsMsg{
		Seq:      startupSeq,
		Sessions: makeSessions(),
		Topology: commands.TopologyFreshness{
			Source: commands.TopologyFromDaemon, Stale: true, Generation: 9,
		},
	})
	if m.topology.Source != commands.TopologyFromDaemon || !m.topology.Stale || m.topology.Generation != 9 {
		t.Fatalf("stored topology = %+v, want the message's stale daemon snapshot", m.topology)
	}
}
