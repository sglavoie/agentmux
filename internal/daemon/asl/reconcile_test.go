package asl

// Session 8 coverage: per-run ASL phase facts, the new lifecycle events, and
// discovery-backed lease reconciliation. The scenarios pin the plan's ASL
// policy — overlapping runs, nested finishes, lost lifecycle events, and
// restart recovery must all converge to deterministic icons.

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// TestNormalizeASLLifecyclePhases pins the event → phase (and paused →
// attention action) contract for every lifecycle event.
func TestNormalizeASLLifecyclePhases(t *testing.T) {
	cases := []struct {
		event      string
		payload    string
		wantPhase  string
		wantAction string
	}{
		{"AgentSessionLoopStarted", `{"run_id":"r1"}`, state.ASLPhaseBetween, ""},
		{"AgentSessionLoopTurnStarted", `{"run_id":"r1"}`, state.ASLPhaseTurn, ""},
		{"AgentSessionLoopTurnHeartbeat", `{"run_id":"r1"}`, state.ASLPhaseTurn, ""},
		{"AgentSessionLoopTurnHangSuspected", `{"run_id":"r1","hang_reason":"zero_cpu_idle","hang_idle_seconds":"210"}`, state.ASLPhaseTurn, ""},
		{"AgentSessionLoopTurnHangCleared", `{"run_id":"r1","hang_reason":"zero_cpu_idle"}`, state.ASLPhaseTurn, ""},
		{"AgentSessionLoopTurnFinished", `{"run_id":"r1"}`, state.ASLPhaseBetween, ""},
		{"AgentSessionLoopVerificationStarted", `{"run_id":"r1"}`, state.ASLPhaseVerification, ""},
		{"AgentSessionLoopVerificationHeartbeat", `{"run_id":"r1"}`, state.ASLPhaseVerification, ""},
		{"AgentSessionLoopVerificationFinished", `{"run_id":"r1"}`, state.ASLPhaseBetween, ""},
		{"AgentSessionLoopWaitStarted", `{"run_id":"r1","question_id":"q1","question_preview":"pick one"}`, state.ASLPhaseWaiting, ""},
		{"AgentSessionLoopWaitEnded", `{"run_id":"r1","question_id":"q1","wait_reason":"timeout"}`, state.ASLPhaseBetween, ""},
		{"AgentSessionLoopPaused", `{"run_id":"r1","pause_reason":"question"}`, state.ASLPhasePaused, "elicitation"},
		{"AgentSessionLoopPaused", `{"run_id":"r1","pause_reason":"verify-failed"}`, state.ASLPhasePaused, "failure"},
		{"AgentSessionLoopPaused", `{"run_id":"r1","reason":"blocked"}`, state.ASLPhasePaused, "failure"},
		{"AgentSessionLoopFinished", `{"run_id":"r1"}`, state.ASLPhaseFinished, ""},
	}
	for _, tc := range cases {
		n := events.NormalizedFromHook(tc.event, "claude", tc.payload, 42, false)
		if n.Invalid || !n.Deliverable() {
			t.Errorf("%s: normalized = %+v; want deliverable", tc.event, n)
			continue
		}
		if n.Source != (protocol.SourceID{Kind: protocol.SourceASL, Key: "r1"}) {
			t.Errorf("%s: source = %+v; want ASL run key r1", tc.event, n.Source)
		}
		if n.Phase != tc.wantPhase || n.Action != tc.wantAction {
			t.Errorf("%s: phase/action = %q/%q, want %q/%q",
				tc.event, n.Phase, n.Action, tc.wantPhase, tc.wantAction)
		}
		if n.RunID != "r1" {
			t.Errorf("%s: run id = %q, want r1", tc.event, n.RunID)
		}
	}
}

// TestReducerHangEventsFoldAsTurnAndKeepLease pins the hang lifecycle
// vocabulary end to end by event name: a suspected worker hang and its
// retraction normalize as deliverable turn-phase facts (never unknown-event
// drops), so the run keeps ⚡🔁 through the whole suspicion episode and only the
// turn finish returns it to a bare loop marker.
func TestReducerHangEventsFoldAsTurnAndKeepLease(t *testing.T) {
	r := state.NewReducer(state.ReducerConfig{})
	episode := []struct {
		event   string
		payload string
	}{
		{"AgentSessionLoopTurnStarted", `{"run_id":"r1","source_sequence":"1","emitted_time":"100"}`},
		{"AgentSessionLoopTurnHangSuspected", `{"run_id":"r1","source_sequence":"2","emitted_time":"101","hang_reason":"result_received_process_alive"}`},
		{"AgentSessionLoopTurnHangCleared", `{"run_id":"r1","source_sequence":"3","emitted_time":"102","hang_reason":"result_received_process_alive"}`},
	}
	for _, step := range episode {
		n := events.NormalizedFromHook(step.event, "claude", step.payload, 42, false)
		if n.Invalid || !n.Deliverable() {
			t.Fatalf("%s: normalized = %+v; want deliverable", step.event, n)
		}
		if d := r.ApplyNormalized(n); d != protocol.DecisionApplied {
			t.Fatalf("%s: decision = %v; want applied", step.event, d)
		}
		if got := r.Reduce(200); got.Icon != "bolt" || !got.Looping {
			t.Fatalf("%s: reduced = %+v; want bolt + looping (lease preserved)", step.event, got)
		}
	}

	r.ApplyNormalized(events.NormalizedFromHook("AgentSessionLoopTurnFinished", "claude",
		`{"run_id":"r1","source_sequence":"4","emitted_time":"103"}`, 42, false))
	if got := r.Reduce(200); got.Icon != "" || !got.Looping {
		t.Fatalf("after finish = %+v; want bare loop marker", got)
	}
}

// TestReducerOverlappingRunsIndependentLeases: one run finishing never changes
// another run's fact, and the loop marker survives until the last lease
// releases.
func TestReducerOverlappingRunsIndependentLeases(t *testing.T) {
	r := state.NewReducer(state.ReducerConfig{})
	turnA := protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-A"}, RunID: "run-A", Phase: state.ASLPhaseTurn, Seq: 1, At: 100}
	betweenB := protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-B"}, RunID: "run-B", Phase: state.ASLPhaseBetween, Seq: 1, At: 101}
	r.Apply(turnA)
	r.Apply(betweenB)

	if got := r.Reduce(102); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("turn + between = %+v; want bolt looping", got)
	}

	// B finishes: A's executing turn is untouched.
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-B"}, RunID: "run-B", Phase: state.ASLPhaseFinished, Seq: 2, At: 103})
	if got := r.Reduce(104); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("after B finished = %+v; want A's bolt + looping preserved", got)
	}

	// A finishes too: no lease remains, the stopped fact becomes visible.
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-A"}, RunID: "run-A", Phase: state.ASLPhaseFinished, Seq: 2, At: 105})
	if got := r.Reduce(106); got.Icon != "stop" || got.Looping {
		t.Fatalf("after both finished = %+v; want stop without looping", got)
	}
}

// TestReducerBetweenTurnsMasksStopOrderIndependent: between turns the loop
// masks ⏹ (the harness stop that ended the previous turn) regardless of
// arrival order, and unmasks it once the lease releases.
func TestReducerBetweenTurnsMasksStopOrderIndependent(t *testing.T) {
	stop := protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "stop", Seq: 1, At: 100}
	between := protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-A"}, RunID: "run-A", Phase: state.ASLPhaseBetween, Seq: 1, At: 101}

	for name, events := range map[string][]protocol.ReducerEvent{
		"stop then between": {stop, between},
		"between then stop": {between, stop},
	} {
		if got := state.Replay(state.ReducerConfig{}, 102, events...); got.Icon != "" || !got.Looping {
			t.Errorf("%s = %+v; want bare loop marker (stop masked)", name, got)
		}
	}

	// Terminal errors are never masked.
	failed := protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "stop_failure", Seq: 2, At: 103}
	if got := state.Replay(state.ReducerConfig{}, 104, between, failed); got.Icon != "error" || !got.Looping {
		t.Errorf("between + stop_failure = %+v; want error + looping", got)
	}

	// Once the lease releases, the finished run's own 🛑 becomes visible.
	finished := protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-A"}, RunID: "run-A", Phase: state.ASLPhaseFinished, Seq: 2, At: 105}
	if got := state.Replay(state.ReducerConfig{}, 106, stop, between, finished); got.Icon != "stop" || got.Looping {
		t.Errorf("after finish = %+v; want stop visible without looping", got)
	}
}

// TestReducerPausedRunHoldsAttentionIconWithLease: paused → matching attention
// icon plus 🔁.
func TestReducerPausedRunHoldsAttentionIconWithLease(t *testing.T) {
	paused := protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-A"}, RunID: "run-A", Action: "elicitation", Phase: state.ASLPhasePaused, Seq: 1, At: 100}
	if got := state.Replay(state.ReducerConfig{}, 101, paused); got.Icon != "question" || !got.Looping {
		t.Fatalf("paused(question) = %+v; want question + looping", got)
	}
	warned := protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-A"}, RunID: "run-A", Action: "failure", Phase: state.ASLPhasePaused, Seq: 1, At: 100}
	if got := state.Replay(state.ReducerConfig{}, 101, warned); got.Icon != "warning" || !got.Looping {
		t.Fatalf("paused(failure) = %+v; want warning + looping", got)
	}
}

// TestReducerNestedFinishesIdempotent: duplicate and reordered finishes for the
// same run are no-ops, and a finish for one run cannot end a sibling's lease.
func TestReducerNestedFinishesIdempotent(t *testing.T) {
	r := state.NewReducer(state.ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-A"}, RunID: "run-A", Phase: state.ASLPhaseTurn, Seq: 1, At: 100})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-B"}, RunID: "run-B", Phase: state.ASLPhaseTurn, Seq: 1, At: 100})

	fin := protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-B"}, RunID: "run-B", Phase: state.ASLPhaseFinished, Seq: 2, At: 101}
	if d := r.Apply(fin); d != protocol.DecisionApplied {
		t.Fatalf("first finish decision = %v, want applied", d)
	}
	if d := r.Apply(fin); d != protocol.DecisionDuplicate {
		t.Fatalf("duplicate finish decision = %v, want duplicate", d)
	}
	late := protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-B"}, RunID: "run-B", Phase: state.ASLPhaseTurn, Seq: 1, At: 100}
	if d := r.Apply(late); d != protocol.DecisionReordered {
		t.Fatalf("reordered turn decision = %v, want reordered", d)
	}
	if got := r.Reduce(102); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("after nested finishes = %+v; want run-A's bolt + looping", got)
	}
}

// reconcileTestFixture builds a handler with one managed window (42) owning
// projectDir and a claude harness, plus a snapshot the reconciler can
// attribute candidates against.
