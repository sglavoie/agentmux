package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// These tests pin the ID-based subagent aggregation contract: per-agent facts
// keyed by agent id and grouped by parent session, a balanced multiset bucket
// for identity-less harnesses, straggler tolerance (duplicates, reorderings,
// stop-before-start), and the precedence invariant that any active agent
// produces 🤖 unless a permission, question, warning, or terminal error
// outranks it.

// TestReducerSubagentStopBeforeStart: a SubagentStop reordered before its own
// start must not resurrect 🤖 — the terminated identity suppresses the
// straggler start until Compact retires it, and neither event claims an icon.
func TestReducerSubagentStopBeforeStart(t *testing.T) {
	r := NewReducer(zeroCfg)
	a := subagentSource("agent-a")

	r.Apply(protocol.ReducerEvent{Source: a, Action: "stop", SessionID: "s1", Seq: 1, At: 100})
	if got := r.Reduce(100); got.Icon != "" {
		t.Fatalf("icon after early stop = %q, want empty (clean completion claims no icon)", got.Icon)
	}

	// The reordered start arrives with a later daemon receipt sequence: an
	// agent id never restarts, so the start is absorbed by the tombstone.
	if d := r.Apply(protocol.ReducerEvent{Source: a, Action: "subagent", SessionID: "s1", Seq: 2, At: 101}); d != protocol.DecisionSuppressed {
		t.Fatalf("straggler start decision = %q, want suppressed", d)
	}
	if got := r.Reduce(101); got.Icon != "" {
		t.Fatalf("icon after straggler start = %q, want empty (agent already stopped)", got.Icon)
	}

	// A duplicate stop replay re-asserts the terminal fact and stays icon-less.
	if d := r.Apply(protocol.ReducerEvent{Source: a, Action: "stop", SessionID: "s1", Seq: 3, At: 102}); d != protocol.DecisionApplied {
		t.Fatalf("duplicate stop decision = %q, want applied", d)
	}
	if got := r.Reduce(102); got.Icon != "" {
		t.Fatalf("icon after duplicate stop = %q, want empty", got.Icon)
	}
}

// TestReducerSubagentSessionScopedClear: agents from two sessions coexist in
// one window; each session's completion clears only its own agents.
func TestReducerSubagentSessionScopedClear(t *testing.T) {
	r := NewReducer(zeroCfg)
	s1 := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}
	s2 := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s2"}

	r.Apply(protocol.ReducerEvent{Source: subagentSource("a1"), Action: "subagent", SessionID: "s1", Seq: 1, At: 10})
	r.Apply(protocol.ReducerEvent{Source: subagentSource("b1"), Action: "subagent", SessionID: "s2", Seq: 1, At: 11})

	// Session s1 completes: only its agent ends; s2's agent keeps 🤖 over the
	// finished session's ⏹.
	r.Apply(protocol.ReducerEvent{Source: s1, Action: "stop", SessionID: "s1", EndsSession: true, Seq: 1, At: 20})
	if got := r.Reduce(20); got.Icon != "subagent" || got.Source != subagentSource("b1") {
		t.Fatalf("after s1 end: %+v, want subagent from b1", got)
	}

	// A straggler start for the cleared agent cannot resurrect it.
	if d := r.Apply(protocol.ReducerEvent{Source: subagentSource("a1"), Action: "subagent", SessionID: "s1", Seq: 2, At: 21}); d != protocol.DecisionSuppressed {
		t.Fatalf("straggler start after session end = %q, want suppressed", d)
	}

	// Session s2 completes: no agents remain, the stop outcome renders.
	r.Apply(protocol.ReducerEvent{Source: s2, Action: "stop", SessionID: "s2", EndsSession: true, Seq: 1, At: 30})
	if got := r.Reduce(30); got.Icon != "stop" {
		t.Fatalf("after s2 end: %+v, want stop", got)
	}
}

// TestReducerSubagentSessionEndWithoutIDClearsAllAgents: identity-less clients
// carry no session boundary, so a session end without a session id ends every
// agent contribution in the window (legacy terminal self-heal).
func TestReducerSubagentSessionEndWithoutIDClearsAllAgents(t *testing.T) {
	r := NewReducer(zeroCfg)
	bucket := protocol.SourceID{Kind: protocol.SourceLegacy, Key: legacySubagentBucketKey("win:1")}

	r.Apply(protocol.ReducerEvent{Source: subagentSource("a1"), Action: "subagent", SessionID: "s1", Seq: 1, At: 10})
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "subagent", SubagentDelta: 1, Seq: 1, At: 11})

	r.Apply(protocol.ReducerEvent{Source: legacySource(), Action: "stop", EndsSession: true, Seq: 1, At: 20})
	if got := r.Reduce(20); got.Icon != "stop" {
		t.Fatalf("after window-wide session end: %+v, want stop (all agents cleared)", got)
	}
}

// TestReducerLegacySubagentMultiset: the identity-less bucket balances starts
// against stops, never goes negative, and surfaces a failure-carrying stop
// even while a sibling is still running.
func TestReducerLegacySubagentMultiset(t *testing.T) {
	r := NewReducer(zeroCfg)
	bucket := protocol.SourceID{Kind: protocol.SourceLegacy, Key: legacySubagentBucketKey("s1")}

	// Balanced: two starts need two stops before 🤖 clears.
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "subagent", SubagentDelta: 1, Seq: 1, At: 10})
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "subagent", SubagentDelta: 1, Seq: 2, At: 11})
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "idle", SubagentDelta: -1, Seq: 3, At: 12})
	if got := r.Reduce(12); got.Icon != "subagent" {
		t.Fatalf("after first stop: icon = %q, want subagent (one agent still running)", got.Icon)
	}
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "idle", SubagentDelta: -1, Seq: 4, At: 13})
	if got := r.Reduce(13); got.Icon != "" {
		t.Fatalf("after last stop: icon = %q, want empty", got.Icon)
	}

	// Clamped: a stop past zero stays zero, so the next start-stop pair is
	// still balanced (a stop-before-start reordering carries no debt).
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "idle", SubagentDelta: -1, Seq: 5, At: 14})
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "subagent", SubagentDelta: 1, Seq: 6, At: 15})
	if got := r.Reduce(15); got.Icon != "subagent" {
		t.Fatalf("start after clamped stop: icon = %q, want subagent", got.Icon)
	}
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "idle", SubagentDelta: -1, Seq: 7, At: 16})
	if got := r.Reduce(16); got.Icon != "" {
		t.Fatalf("after clamped cycle: icon = %q, want empty (multiset stays balanced)", got.Icon)
	}

	// A failure-carrying stop surfaces its sticky error even with a sibling
	// still running, and the sibling's later clean stop cannot wipe it.
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "subagent", SubagentDelta: 1, Seq: 8, At: 20})
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "subagent", SubagentDelta: 1, Seq: 9, At: 21})
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "stop_failure", SubagentDelta: -1, Seq: 10, At: 22})
	if got := r.Reduce(22); got.Icon != "error" {
		t.Fatalf("failing stop with sibling running: icon = %q, want error", got.Icon)
	}
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "idle", SubagentDelta: -1, Seq: 11, At: 23})
	if got := r.Reduce(23); got.Icon != "error" {
		t.Fatalf("sibling's clean stop after failure: icon = %q, want error preserved", got.Icon)
	}
}

// TestReducerSubagentAggregationSurvivesSnapshotRestore: the parent-session
// grouping and the balanced multiset count ride the schema-v2 fact snapshots,
// so a daemon restart preserves both scoped session-end clearing and the
// remaining identity-less agent count.
func TestReducerSubagentAggregationSurvivesSnapshotRestore(t *testing.T) {
	r := NewReducer(zeroCfg)
	bucket := protocol.SourceID{Kind: protocol.SourceLegacy, Key: legacySubagentBucketKey("s2")}
	r.Apply(protocol.ReducerEvent{Source: subagentSource("a1"), Action: "subagent", SessionID: "s1", Seq: 1, At: 10})
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "subagent", SubagentDelta: 1, Seq: 1, At: 11})
	r.Apply(protocol.ReducerEvent{Source: bucket, Action: "subagent", SubagentDelta: 1, Seq: 2, At: 12})

	restored := NewReducer(zeroCfg)
	restored.Restore(r.Facts())

	// The restored s1 agent still clears on s1's completion; the s2 bucket is
	// untouched and its count still needs two stops to drain.
	restored.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"},
		Action: "stop", SessionID: "s1", EndsSession: true, Seq: 1, At: 20})
	if got := restored.Reduce(20); got.Icon != "subagent" || got.Source != bucket {
		t.Fatalf("after restore + s1 end: %+v, want subagent from the s2 bucket", got)
	}
	restored.Apply(protocol.ReducerEvent{Source: bucket, Action: "idle", SubagentDelta: -1, Seq: 3, At: 21})
	if got := restored.Reduce(21); got.Icon != "subagent" {
		t.Fatalf("restored multiset lost its count: %+v, want subagent (one agent left)", got)
	}
	restored.Apply(protocol.ReducerEvent{Source: bucket, Action: "idle", SubagentDelta: -1, Seq: 4, At: 22})
	if got := restored.Reduce(22); got.Icon != "stop" {
		t.Fatalf("after draining restored multiset: %+v, want stop (s1's outcome)", got)
	}
}

// TestReducerMultiSourceSubagentInvariant asserts the plan invariant directly:
// any active agent produces 🤖 over parent activity, compaction, or a parent
// turn stop, and yields only to permission, question, warning, or terminal
// error.
func TestReducerMultiSourceSubagentInvariant(t *testing.T) {
	lower := []string{"active", "compact", "stop"}
	for _, action := range lower {
		r := NewReducer(zeroCfg)
		r.Apply(protocol.ReducerEvent{Source: subagentSource("a"), Action: "subagent", SessionID: "s1", Seq: 1, At: 10})
		r.Apply(protocol.ReducerEvent{Source: harnessSource(), Action: action, Seq: 1, At: 20})
		if got := r.Reduce(20); got.Icon != "subagent" {
			t.Errorf("parent %q over active agent: icon = %q, want subagent", action, got.Icon)
		}
	}

	higher := []struct{ action, icon string }{
		{"permission", "key"},
		{"elicitation", "question"},
		{"failure", "warning"},
		{"stop_failure", "error"},
	}
	for _, tc := range higher {
		r := NewReducer(zeroCfg)
		r.Apply(protocol.ReducerEvent{Source: subagentSource("a"), Action: "subagent", SessionID: "s1", Seq: 1, At: 10})
		r.Apply(protocol.ReducerEvent{Source: harnessSource(), Action: tc.action, Seq: 1, At: 20})
		if got := r.Reduce(20); got.Icon != tc.icon {
			t.Errorf("parent %q over active agent: icon = %q, want %q", tc.action, got.Icon, tc.icon)
		}
	}
}
