package daemon

import "testing"

// These tests pin the pure reducer contract: the target icon precedence, replay
// determinism, duplicate idempotency, reordered-event rejection, multi-source
// collapse, and user_clear acknowledgement. They exercise reducer.go only and do
// not touch the live Handler, so production rendering is unaffected.

// zeroCfg disables staleness and the legacy grace so precedence/ordering tests
// isolate the property under test.
var zeroCfg = ReducerConfig{}

func harnessSource() SourceID          { return SourceID{Kind: SourceHarness, Key: "main"} }
func subagentSource(k string) SourceID { return SourceID{Kind: SourceSubagent, Key: k} }
func aslSource(k string) SourceID      { return SourceID{Kind: SourceASL, Key: k} }
func legacySource() SourceID           { return SourceID{Kind: SourceLegacy} }

func TestReducerActionIconMapping(t *testing.T) {
	cases := map[string]string{
		"active":       "bolt",
		"compact":      "compact",
		"subagent":     "subagent",
		"stop":         "stop",
		"elicitation":  "question",
		"question":     "question",
		"permission":   "key",
		"failure":      "warning",
		"stop_failure": "error",
		"idle":         "",
		"clear":        "",
		"user_clear":   "",
		"nonsense":     "",
	}
	for action, want := range cases {
		if got := reducerActionIcon(action); got != want {
			t.Errorf("reducerActionIcon(%q) = %q; want %q", action, got, want)
		}
	}
}

// TestReducerPrecedenceTable asserts the plan's icon precedence explicitly (it
// intentionally differs from the legacy actionPriority order): for every pair of
// coexisting facts the higher-precedence icon wins, regardless of which source
// arrived first.
func TestReducerPrecedenceTable(t *testing.T) {
	// Ascending precedence. Distinct pairs never tie.
	ladder := []struct {
		action string
		icon   string
	}{
		{"stop", "stop"},
		{"active", "bolt"},
		{"compact", "compact"},
		{"subagent", "subagent"},
		{"failure", "warning"},
		{"permission", "key"},
		{"stop_failure", "error"},
	}

	for i := range ladder {
		for j := i + 1; j < len(ladder); j++ {
			lo, hi := ladder[i], ladder[j]
			// Emit the higher-precedence fact FIRST so the win is by precedence,
			// not arrival/recency.
			r := NewReducer(zeroCfg)
			r.Apply(ReducerEvent{Source: harnessSource(), Action: hi.action, Seq: 1})
			r.Apply(ReducerEvent{Source: subagentSource("b"), Action: lo.action, Seq: 1})
			if got := r.Reduce(0); got.Icon != hi.icon {
				t.Errorf("hi=%q lo=%q: icon = %q; want %q", hi.icon, lo.icon, got.Icon, hi.icon)
			}
		}
	}
}

// TestReducerPrecedenceQuestionKeyTie confirms permission (key) and question
// share one tier and the tie breaks on recency (emitted timestamp), not map
// iteration order.
func TestReducerPrecedenceQuestionKeyTie(t *testing.T) {
	// Question is newer -> question wins.
	r := NewReducer(zeroCfg)
	r.Apply(ReducerEvent{Source: harnessSource(), Action: "permission", Seq: 1, At: 100})
	r.Apply(ReducerEvent{Source: subagentSource("b"), Action: "question", Seq: 1, At: 200})
	if got := r.Reduce(0); got.Icon != "question" {
		t.Fatalf("newer question: icon = %q; want question", got.Icon)
	}

	// Permission is newer -> key wins.
	r2 := NewReducer(zeroCfg)
	r2.Apply(ReducerEvent{Source: subagentSource("b"), Action: "question", Seq: 1, At: 100})
	r2.Apply(ReducerEvent{Source: harnessSource(), Action: "permission", Seq: 1, At: 200})
	if got := r2.Reduce(0); got.Icon != "key" {
		t.Fatalf("newer permission: icon = %q; want key", got.Icon)
	}
}

// TestReducerReplayDeterministic proves the reducer is order-independent: the
// same events applied in two arrival orders (with consistent per-source Seqs)
// reduce to the same state.
func TestReducerReplayDeterministic(t *testing.T) {
	events := []ReducerEvent{
		{Source: harnessSource(), Action: "active", Seq: 1, At: 10},
		{Source: subagentSource("a"), Action: "subagent", Seq: 1, At: 20},
		{Source: harnessSource(), Action: "permission", Seq: 2, At: 30},
		{Source: subagentSource("a"), Action: "stop", Seq: 2, At: 40},
	}
	shuffled := []ReducerEvent{events[3], events[0], events[2], events[1]}

	a := Replay(zeroCfg, 100, events...)
	b := Replay(zeroCfg, 100, shuffled...)
	if a != b {
		t.Fatalf("replay not deterministic: %+v vs %+v", a, b)
	}
	if a.Icon != "key" {
		t.Fatalf("icon = %q; want key (permission outranks a stopped subagent)", a.Icon)
	}
}

// TestReducerDuplicateIdempotent verifies a repeated Seq is a no-op and is
// traced as a duplicate.
func TestReducerDuplicateIdempotent(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(ReducerEvent{Source: harnessSource(), Action: "active", Seq: 5, At: 1})
	r.Apply(ReducerEvent{Source: harnessSource(), Action: "active", Seq: 5, At: 1})

	if got := r.Reduce(0); got.Icon != "bolt" {
		t.Fatalf("icon = %q; want bolt", got.Icon)
	}
	traces := r.Traces()
	if len(traces) != 2 {
		t.Fatalf("traces = %d; want 2", len(traces))
	}
	if traces[0].Decision != DecisionApplied {
		t.Errorf("trace[0].Decision = %q; want applied", traces[0].Decision)
	}
	if traces[1].Decision != DecisionDuplicate {
		t.Errorf("trace[1].Decision = %q; want duplicate", traces[1].Decision)
	}
}

// TestReducerReorderedEvent verifies an older Seq arriving after a newer one is
// dropped and traced as reordered.
func TestReducerReorderedEvent(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(ReducerEvent{Source: harnessSource(), Action: "active", Seq: 5, At: 50})
	r.Apply(ReducerEvent{Source: harnessSource(), Action: "stop", Seq: 3, At: 30}) // older

	if got := r.Reduce(0); got.Icon != "bolt" {
		t.Fatalf("icon = %q; want bolt (older stop ignored)", got.Icon)
	}
	traces := r.Traces()
	if traces[1].Decision != DecisionReordered {
		t.Fatalf("trace[1].Decision = %q; want reordered", traces[1].Decision)
	}

	// A genuinely newer event still applies.
	r.Apply(ReducerEvent{Source: harnessSource(), Action: "stop", Seq: 6, At: 60})
	if got := r.Reduce(0); got.Icon != "stop" {
		t.Fatalf("icon = %q; want stop after newer event", got.Icon)
	}
}

// TestReducerMultiSource collapses several coexisting sources — main harness,
// two subagents, and a live ASL run — into one icon and the loop marker.
func TestReducerMultiSource(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(ReducerEvent{Source: harnessSource(), Action: "active", Seq: 1, At: 10})
	r.Apply(ReducerEvent{Source: subagentSource("a"), Action: "subagent", Seq: 1, At: 11})
	r.Apply(ReducerEvent{Source: subagentSource("b"), Action: "subagent", Seq: 1, At: 12})
	r.Apply(ReducerEvent{Source: aslSource("run-1"), Phase: ASLPhaseTurn, Seq: 1, At: 13})

	got := r.Reduce(100)
	if got.Icon != "subagent" {
		t.Fatalf("icon = %q; want subagent (outranks active work)", got.Icon)
	}
	if !got.Looping {
		t.Fatal("looping = false; want true (live ASL run)")
	}

	// A permission on the main harness now outranks the subagents.
	r.Apply(ReducerEvent{Source: harnessSource(), Action: "permission", Seq: 2, At: 20})
	got = r.Reduce(100)
	if got.Icon != "key" || got.Source != harnessSource() {
		t.Fatalf("icon = %q src = %+v; want key from harness", got.Icon, got.Source)
	}
	if !got.Looping {
		t.Fatal("looping = false; want true (ASL run still live)")
	}
}

// TestReducerUserClearAcknowledgesButKeepsLease verifies user_clear hides all
// visible icons yet preserves a live ASL loop lease, and that a later heartbeat
// reasserts activity.
func TestReducerUserClearAcknowledgesButKeepsLease(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(ReducerEvent{Source: harnessSource(), Action: "permission", Seq: 1, At: 10})
	r.Apply(ReducerEvent{Source: aslSource("run-1"), Phase: ASLPhaseTurn, Seq: 1, At: 11})

	if got := r.Reduce(100); got.Icon != "key" || !got.Looping {
		t.Fatalf("pre-clear = %+v; want key + looping", got)
	}

	r.Apply(ReducerEvent{Source: harnessSource(), Action: "user_clear", Seq: 0, At: 12})
	got := r.Reduce(100)
	if got.Icon != "" {
		t.Fatalf("post-clear icon = %q; want empty (all facts acknowledged)", got.Icon)
	}
	if !got.Looping {
		t.Fatal("post-clear looping = false; want true (ASL lease preserved)")
	}
	if traces := r.Traces(); traces[len(traces)-1].Decision != DecisionAcknowledged {
		t.Fatalf("last trace decision = %q; want acknowledged", traces[len(traces)-1].Decision)
	}

	// A newer ASL turn heartbeat reasserts activity.
	r.Apply(ReducerEvent{Source: aslSource("run-1"), Phase: ASLPhaseTurn, Seq: 2, At: 20})
	if got := r.Reduce(100); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("post-heartbeat = %+v; want bolt + looping", got)
	}
}

// TestReducerWaitingRunHoldsBareLease pins the wait lifecycle: a worker blocked
// on a mailbox reply keeps 🔁 without claiming the human-actionable question
// icon, and the wait ending remains a bare loop until work resumes or finishes.
func TestReducerWaitingRunHoldsBareLease(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(ReducerEvent{Source: aslSource("run-1"), RunID: "run-1", Phase: ASLPhaseBetween, Seq: 1, At: 10})
	r.Apply(ReducerEvent{Source: aslSource("run-1"), RunID: "run-1", Phase: ASLPhaseWaiting, Seq: 2, At: 11})
	if got := r.Reduce(100); got.Icon != "" || !got.Looping {
		t.Fatalf("waiting = %+v; want bare loop marker", got)
	}

	r.Apply(ReducerEvent{Source: aslSource("run-1"), RunID: "run-1", Phase: ASLPhaseBetween, Seq: 3, At: 12})
	if got := r.Reduce(100); got.Icon != "" || !got.Looping {
		t.Fatalf("wait ended = %+v; want bare loop marker", got)
	}
}

// TestReducerWaitingRunOrderIndependent: a wait is a normal per-source fact, so
// a duplicate wait-started is a no-op and a wait-started that arrives after the
// wait-ended it precedes is rejected as reordered rather than reopening it.
func TestReducerWaitingRunOrderIndependent(t *testing.T) {
	waiting := ReducerEvent{Source: aslSource("run-1"), RunID: "run-1", Phase: ASLPhaseWaiting, Seq: 1, At: 10}
	ended := ReducerEvent{Source: aslSource("run-1"), RunID: "run-1", Phase: ASLPhaseBetween, Seq: 2, At: 11}

	r := NewReducer(zeroCfg)
	if d := r.Apply(waiting); d != DecisionApplied {
		t.Fatalf("first wait decision = %v, want applied", d)
	}
	if d := r.Apply(waiting); d != DecisionDuplicate {
		t.Fatalf("duplicate wait decision = %v, want duplicate", d)
	}
	r.Apply(ended)
	if d := r.Apply(waiting); d != DecisionReordered {
		t.Fatalf("late wait decision = %v, want reordered", d)
	}
	if got := r.Reduce(100); got.Icon != "" || !got.Looping {
		t.Fatalf("after late wait = %+v; want the ended wait to stand", got)
	}
	if got := Replay(zeroCfg, 100, ended, waiting); got.Icon != "" || !got.Looping {
		t.Fatalf("reverse arrival = %+v; want the same ended state", got)
	}
}

// TestReducerWaitingDoesNotCompeteWithAttention: an internal mailbox wait owns
// only 🔁, so a real permission prompt remains visible regardless of recency.
func TestReducerWaitingDoesNotCompeteWithAttention(t *testing.T) {
	waiting := ReducerEvent{Source: aslSource("run-1"), RunID: "run-1", Phase: ASLPhaseWaiting, Seq: 1, At: 100}
	permission := ReducerEvent{Source: harnessSource(), Action: "permission", Seq: 1, At: 200}
	if got := Replay(zeroCfg, 300, waiting, permission); got.Icon != "key" {
		t.Fatalf("newer permission = %q; want key", got.Icon)
	}

	newerWait := ReducerEvent{Source: aslSource("run-1"), RunID: "run-1", Phase: ASLPhaseWaiting, Seq: 1, At: 300}
	olderPermission := ReducerEvent{Source: harnessSource(), Action: "permission", Seq: 1, At: 200}
	if got := Replay(zeroCfg, 400, olderPermission, newerWait); got.Icon != "key" {
		t.Fatalf("newer internal wait = %q; want permission preserved", got.Icon)
	}
}

// TestReducerWaitingPausedFallback: if the wait never reports an end (the run
// pauses out of the wait instead) the pause supplies its own attention icon,
// and the terminal finish still releases the lease.
func TestReducerWaitingPausedFallback(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(ReducerEvent{Source: aslSource("run-1"), RunID: "run-1", Phase: ASLPhaseWaiting, Seq: 1, At: 10})
	r.Apply(ReducerEvent{Source: aslSource("run-1"), RunID: "run-1", Action: "failure", Phase: ASLPhasePaused, Seq: 2, At: 11})
	if got := r.Reduce(100); got.Icon != "warning" || !got.Looping {
		t.Fatalf("paused out of a wait = %+v; want warning + looping", got)
	}

	r.Apply(ReducerEvent{Source: aslSource("run-1"), RunID: "run-1", Phase: ASLPhaseFinished, Seq: 3, At: 12})
	if got := r.Reduce(100); got.Icon != "stop" || got.Looping {
		t.Fatalf("finished = %+v; want stop without looping", got)
	}
}
