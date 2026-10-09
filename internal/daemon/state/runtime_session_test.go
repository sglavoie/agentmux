package state

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

const (
	runtimeA = "rs-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	runtimeB = "rs-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func claudeSource(sessionID string) protocol.SourceID {
	return protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:" + sessionID}
}

func runtimeEvent(runtime string, source protocol.SourceID, action string, seq uint64, at int64) protocol.NormalizedEvent {
	return protocol.NormalizedEvent{
		Source: source, Action: action, Seq: seq, At: at,
		SessionID: "s1", RuntimeSessionID: runtime,
	}
}

func TestReducerRefusesAnotherRuntimeSessionsEvents(t *testing.T) {
	r := NewReducer(zeroCfg)
	if d := r.ApplyNormalized(runtimeEvent(runtimeA, claudeSource("s1"), "active", 1, 100)); d != protocol.DecisionApplied {
		t.Fatalf("first event decision = %q, want applied", d)
	}
	if r.RuntimeSession() != runtimeA {
		t.Fatalf("runtime binding = %q, want adopted %q", r.RuntimeSession(), runtimeA)
	}

	// Another runtime session's terminal, subagent, and permission events are
	// refused whatever their sequence, including one that reuses the same
	// harness session key.
	for i, ev := range []protocol.NormalizedEvent{
		runtimeEvent(runtimeB, claudeSource("s1"), "stop", 50, 101),
		runtimeEvent(runtimeB, subagentSource("agent-1"), "subagent", 51, 102),
		runtimeEvent(runtimeB, claudeSource("other"), "permission", 52, 103),
	} {
		if d := r.ApplyNormalized(ev); d != protocol.DecisionQuarantined {
			t.Fatalf("foreign event %d decision = %q, want quarantined", i, d)
		}
		trace, _ := r.LatestTrace()
		if trace.Reason != protocol.DecisionReasonRuntimeSessionMismatch {
			t.Fatalf("foreign event %d reason = %q", i, trace.Reason)
		}
	}
	if got := r.Reduce(104); got.Icon != "bolt" {
		t.Fatalf("icon = %q, want the bound session's bolt", got.Icon)
	}

	// Duplicate and reordered deliveries of the bound session keep their
	// ordinary per-source semantics.
	if d := r.ApplyNormalized(runtimeEvent(runtimeA, claudeSource("s1"), "active", 1, 100)); d != protocol.DecisionDuplicate {
		t.Fatalf("duplicate decision = %q, want duplicate", d)
	}
	if d := r.ApplyNormalized(runtimeEvent(runtimeA, claudeSource("s1"), "stop", 3, 105)); d != protocol.DecisionApplied {
		t.Fatalf("stop decision = %q, want applied", d)
	}
	if d := r.ApplyNormalized(runtimeEvent(runtimeA, claudeSource("s1"), "active", 2, 104)); d != protocol.DecisionReordered {
		t.Fatalf("reordered decision = %q, want reordered", d)
	}
	if got := r.Reduce(106); got.Icon != "stop" {
		t.Fatalf("icon = %q, want stop", got.Icon)
	}
}

func TestBindingANewRuntimeSessionDiscardsTheOldOnesFacts(t *testing.T) {
	r := NewReducer(zeroCfg)
	process := protocol.HarnessProcessIdentity{Harness: "claude", PID: 4242, StartToken: "Thu Oct  9 12:00:00 2026"}
	active := runtimeEvent(runtimeA, claudeSource("s1"), "active", 1, 100)
	active.HarnessProcess = process
	r.ApplyNormalized(active)
	r.ApplyNormalized(runtimeEvent(runtimeA, subagentSource("agent-1"), "subagent", 2, 101))
	if got := r.Reduce(102); got.Icon != "subagent" {
		t.Fatalf("icon before replacement = %q, want subagent", got.Icon)
	}

	if !r.BindRuntimeSession(runtimeB) {
		t.Fatal("binding a different session reported no replacement")
	}
	if got := r.Reduce(103); got.Icon != "" {
		t.Fatalf("icon after replacement = %q, want the old session's facts gone", got.Icon)
	}
	if len(r.Facts()) != 0 {
		t.Fatalf("facts after replacement = %+v", r.Facts())
	}

	// The old session's late subagent stop and process exit cannot touch the
	// new session.
	if d := r.ApplyNormalized(runtimeEvent(runtimeA, subagentSource("agent-1"), "stop", 3, 104)); d != protocol.DecisionQuarantined {
		t.Fatalf("late subagent stop decision = %q, want quarantined", d)
	}
	r.ApplyNormalized(runtimeEvent(runtimeB, claudeSource("s2"), "active", 1, 105))
	if exit := r.SettleHarnessProcessExit(process, 106, HarnessProcessExitOptions{}); exit.Changed {
		t.Fatalf("old process exit changed the new session: %+v", exit)
	}
	if got := r.Reduce(107); got.Icon != "bolt" {
		t.Fatalf("icon = %q, want the new session's bolt", got.Icon)
	}

	if r.BindRuntimeSession(runtimeB) {
		t.Fatal("rebinding the same session reported a replacement")
	}
}

func TestUnboundReducerAdoptsItsFirstRuntimeSession(t *testing.T) {
	r := NewReducer(zeroCfg)
	// A legacy fact reduced from the pane before it was claimed survives the
	// adoption: it is the same pane, not another session.
	r.ApplyNormalized(protocol.NormalizedEvent{Source: legacySource(), Action: "permission", Seq: 1, At: 100})
	if r.BindRuntimeSession(runtimeA) {
		t.Fatal("adopting a first session reported a replacement")
	}
	if got := r.Reduce(101); got.Icon != "key" {
		t.Fatalf("icon after adoption = %q, want the pane's key", got.Icon)
	}
	// Unstamped legacy events still apply to a bound reducer.
	if d := r.ApplyNormalized(protocol.NormalizedEvent{Source: legacySource(), Action: "active", Seq: 2, At: 102}); d != protocol.DecisionApplied {
		t.Fatalf("legacy event on a bound reducer = %q, want applied", d)
	}
}
