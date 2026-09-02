package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// These reliability-trace tests replay the concrete failure scenarios the
// reducer must survive and assert the full audit trace plus the resulting
// effective state, not just the final icon. They cover automatic compaction,
// overlapping and nested ASL runs, duplicate subagent starts, permission/error
// precedence, stale active self-heal, identified same-turn terminal suppression,
// and the legacy guard_duration grace.

func lastTrace(t *testing.T, r *Reducer) ReducerTrace {
	t.Helper()
	traces := r.Traces()
	if len(traces) == 0 {
		t.Fatal("no traces recorded")
	}
	return traces[len(traces)-1]
}

// TestReliabilityTraceAutomaticCompaction: a mid-turn automatic compaction shows
// 🌀 transiently, then ⚡ resumes when work continues. Compaction outranks active
// work while present, but a newer active from the same source overwrites it.
func TestReliabilityTraceAutomaticCompaction(t *testing.T) {
	r := NewReducer(zeroCfg)
	src := harnessSource()

	r.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 1, At: 10})
	if got := r.Reduce(100); got.Icon != "bolt" {
		t.Fatalf("after active: icon = %q; want bolt", got.Icon)
	}

	r.Apply(protocol.ReducerEvent{Source: src, Action: "compact", Seq: 2, At: 11})
	if got := r.Reduce(100); got.Icon != "compact" {
		t.Fatalf("during compaction: icon = %q; want compact", got.Icon)
	}

	r.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 3, At: 12})
	if got := r.Reduce(100); got.Icon != "bolt" {
		t.Fatalf("after compaction: icon = %q; want bolt", got.Icon)
	}

	if d := lastTrace(t, r).Decision; d != protocol.DecisionApplied {
		t.Fatalf("final decision = %q; want applied", d)
	}
}

// TestReliabilityTraceOverlappingASLRuns: two runs overlap; finishing one keeps
// the other's fact and the loop marker intact. A finished run never mutates the
// other run's fact.
func TestReliabilityTraceOverlappingASLRuns(t *testing.T) {
	r := NewReducer(zeroCfg)
	run1, run2 := aslSource("run-1"), aslSource("run-2")

	r.Apply(protocol.ReducerEvent{Source: run1, Phase: ASLPhaseTurn, Seq: 1, At: 10})
	r.Apply(protocol.ReducerEvent{Source: run2, Phase: ASLPhaseTurn, Seq: 1, At: 11})
	if got := r.Reduce(100); !got.Looping || got.Icon != "bolt" {
		t.Fatalf("both running: %+v; want bolt + looping", got)
	}

	// run-1 finishes: its lease drops, but run-2 remains live.
	r.Apply(protocol.ReducerEvent{Source: run1, Phase: ASLPhaseFinished, Seq: 2, At: 12})
	got := r.Reduce(100)
	if !got.Looping {
		t.Fatal("after run-1 finish: looping = false; want true (run-2 still live)")
	}
	if got.Icon != "bolt" || got.Source != run2 {
		t.Fatalf("after run-1 finish: icon=%q src=%+v; want bolt from run-2", got.Icon, got.Source)
	}

	// run-2 finishes: no live lease, both stopped -> 🛑 without 🔁.
	r.Apply(protocol.ReducerEvent{Source: run2, Phase: ASLPhaseFinished, Seq: 2, At: 13})
	got = r.Reduce(100)
	if got.Looping {
		t.Fatal("both finished: looping = true; want false")
	}
	if got.Icon != "stop" {
		t.Fatalf("both finished: icon = %q; want stop", got.Icon)
	}
}

// TestReliabilityTraceNestedRunCompletion: a nested (child) run completing must
// not disturb the outer run's fact or its loop lease.
func TestReliabilityTraceNestedRunCompletion(t *testing.T) {
	r := NewReducer(zeroCfg)
	outer, nested := aslSource("outer"), aslSource("nested")

	r.Apply(protocol.ReducerEvent{Source: outer, Phase: ASLPhaseTurn, Seq: 1, At: 10})
	r.Apply(protocol.ReducerEvent{Source: nested, Phase: ASLPhaseVerification, Seq: 1, At: 11})
	if got := r.Reduce(100); !got.Looping {
		t.Fatal("nested running: looping = false; want true")
	}

	r.Apply(protocol.ReducerEvent{Source: nested, Phase: ASLPhaseFinished, Seq: 2, At: 12})
	got := r.Reduce(100)
	if !got.Looping || got.Icon != "bolt" || got.Source != outer {
		t.Fatalf("after nested finish: %+v; want bolt + looping from outer run", got)
	}
}

// TestReliabilityTraceDuplicateSubagentStarts: a subagent start redelivered with
// the same or an older sequence collapses onto one fact; a genuinely distinct
// agent id is a second source.
func TestReliabilityTraceDuplicateSubagentStarts(t *testing.T) {
	r := NewReducer(zeroCfg)
	a := subagentSource("agent-a")

	r.Apply(protocol.ReducerEvent{Source: a, Action: "subagent", Seq: 1, At: 10})
	r.Apply(protocol.ReducerEvent{Source: a, Action: "subagent", Seq: 1, At: 10}) // duplicate delivery
	r.Apply(protocol.ReducerEvent{Source: a, Action: "subagent", Seq: 0, At: 9})  // reordered/older

	traces := r.Traces()
	if traces[0].Decision != protocol.DecisionApplied {
		t.Fatalf("trace[0] = %q; want applied", traces[0].Decision)
	}
	if traces[1].Decision != protocol.DecisionDuplicate {
		t.Fatalf("trace[1] = %q; want duplicate", traces[1].Decision)
	}
	if traces[2].Decision != protocol.DecisionReordered {
		t.Fatalf("trace[2] = %q; want reordered", traces[2].Decision)
	}
	if got := r.Reduce(100); got.Icon != "subagent" || got.Source != a {
		t.Fatalf("after duplicate starts: %+v; want one subagent from agent-a", got)
	}

	// A distinct agent id is a genuinely separate source (still one icon).
	r.Apply(protocol.ReducerEvent{Source: subagentSource("agent-b"), Action: "subagent", Seq: 1, At: 11})
	if got := r.Reduce(100); got.Icon != "subagent" {
		t.Fatalf("two agents: icon = %q; want subagent", got.Icon)
	}
}

// TestReliabilityTracePermissionErrorPrecedence: a terminal error outranks a
// pending permission and stays put even as unrelated active work continues on
// another source.
func TestReliabilityTracePermissionErrorPrecedence(t *testing.T) {
	r := NewReducer(zeroCfg)

	r.Apply(protocol.ReducerEvent{Source: harnessSource(), Action: "permission", Seq: 1, At: 10})
	if got := r.Reduce(100); got.Icon != "key" {
		t.Fatalf("permission pending: icon = %q; want key", got.Icon)
	}

	// A context-limit probe raises a terminal error.
	probe := protocol.SourceID{Kind: protocol.SourceProbe, Key: "context-limit"}
	r.Apply(protocol.ReducerEvent{Source: probe, Action: "stop_failure", Seq: 1, At: 11})
	if got := r.Reduce(100); got.Icon != "error" || got.Source != probe {
		t.Fatalf("error raised: icon=%q src=%+v; want error from probe", got.Icon, got.Source)
	}

	// Unrelated active work on a subagent does not dislodge the sticky error.
	r.Apply(protocol.ReducerEvent{Source: subagentSource("a"), Action: "subagent", Seq: 1, At: 12})
	if got := r.Reduce(100); got.Icon != "error" {
		t.Fatalf("after subagent: icon = %q; want error (terminal precedence)", got.Icon)
	}
}

// TestReliabilityTraceStaleActiveWork: an active fact self-heals to idle once it
// exceeds the configured active staleness, and a fresh heartbeat revives it.
func TestReliabilityTraceStaleActiveWork(t *testing.T) {
	cfg := ReducerConfig{StaleActive: 600}
	r := NewReducer(cfg)
	src := harnessSource()

	r.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 1, At: 1000})
	if got := r.Reduce(1000); got.Icon != "bolt" {
		t.Fatalf("fresh: icon = %q; want bolt", got.Icon)
	}
	if got := r.Reduce(1000 + 599); got.Icon != "bolt" {
		t.Fatalf("just under threshold: icon = %q; want bolt", got.Icon)
	}
	if got := r.Reduce(1000 + 600); got.Icon != "" {
		t.Fatalf("stale: icon = %q; want empty (self-healed)", got.Icon)
	}

	// A newer active heartbeat revives the bolt.
	r.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 2, At: 2000})
	if got := r.Reduce(2000); got.Icon != "bolt" {
		t.Fatalf("after heartbeat: icon = %q; want bolt", got.Icon)
	}
}

// TestReliabilityTraceIdentifiedTerminalSuppression: an identified source that
// stops suppresses only trailing activity from the SAME turn; a new turn breaks
// through immediately.
func TestReliabilityTraceIdentifiedTerminalSuppression(t *testing.T) {
	r := NewReducer(ReducerConfig{GuardDuration: 1})
	src := harnessSource()

	r.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 1, TurnID: "t1", At: 10})
	r.Apply(protocol.ReducerEvent{Source: src, Action: "stop", Seq: 2, TurnID: "t1", At: 11})
	// Trailing same-turn active is suppressed (a straggling PostToolUse).
	r.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 3, TurnID: "t1", At: 12})
	if got := r.Reduce(100); got.Icon != "stop" {
		t.Fatalf("same-turn trailing: icon = %q; want stop (suppressed)", got.Icon)
	}
	if d := lastTrace(t, r).Decision; d != protocol.DecisionSuppressed {
		t.Fatalf("trailing decision = %q; want suppressed", d)
	}

	// A new turn reasserts activity.
	r.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 4, TurnID: "t2", At: 13})
	if got := r.Reduce(100); got.Icon != "bolt" {
		t.Fatalf("new turn: icon = %q; want bolt", got.Icon)
	}
}

// TestReliabilityTraceLegacyGraceSelfHeal: an unidentified legacy source lacks
// turn identity, so it falls back to the guard_duration trailing grace. Activity
// inside the grace is suppressed; activity after it self-heals.
func TestReliabilityTraceLegacyGraceSelfHeal(t *testing.T) {
	r := NewReducer(ReducerConfig{GuardDuration: 5})
	src := legacySource()

	r.Apply(protocol.ReducerEvent{Source: src, Action: "stop", Seq: 1, At: 100})
	// Within the grace window -> suppressed.
	r.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 2, At: 103})
	if got := r.Reduce(200); got.Icon != "stop" {
		t.Fatalf("within grace: icon = %q; want stop (suppressed)", got.Icon)
	}
	if d := lastTrace(t, r).Decision; d != protocol.DecisionSuppressed {
		t.Fatalf("within-grace decision = %q; want suppressed", d)
	}

	// After the grace expires -> activity self-heals.
	r.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 3, At: 106})
	if got := r.Reduce(200); got.Icon != "bolt" {
		t.Fatalf("after grace: icon = %q; want bolt (self-healed)", got.Icon)
	}
}
