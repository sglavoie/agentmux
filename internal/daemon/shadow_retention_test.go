package daemon

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func TestShadowReducerRetentionRetiresCompletedSubagentsAfterStragglerWindow(t *testing.T) {
	t.Parallel()

	r := state.NewReducer(state.ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-1"}, Action: "subagent", Seq: 1, TurnID: "t1", At: 100})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-1"}, Action: "stop", Seq: 2, TurnID: "t1", At: 110})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-2"}, Action: "stop_failure", Seq: 1, TurnID: "t1", At: 120})

	// Inside the straggler window the completed agent's icon-less tombstone is
	// retained (it absorbs duplicate stops and reordered starts) but claims no
	// icon; the failed sibling's sticky error still renders.
	if removed := r.Compact(121); removed != 0 {
		t.Fatalf("Compact inside straggler window removed %d facts, want 0", removed)
	}
	if got := r.Reduce(121); got.Icon != "error" {
		t.Fatalf("icon = %q, want error (completed tombstone contributes nothing)", got.Icon)
	}

	// Past the window the completed identity is forgotten; the sticky failure
	// remains until acknowledged.
	if removed := r.Compact(110 + state.SubagentStopRetentionSeconds); removed != 1 {
		t.Fatalf("Compact past straggler window removed %d facts, want the completed subagent only", removed)
	}
	facts := r.Facts()
	if len(facts) != 1 || facts[0].Key != "agent-2" || facts[0].Icon != "error" {
		t.Fatalf("facts after compact = %+v, want only the failed subagent", facts)
	}
}

func TestShadowReducerRetentionExpiresTerminalFactsAfterAcknowledgement(t *testing.T) {
	t.Parallel()

	src := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}
	r := state.NewReducer(state.ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: src, Action: "stop", Seq: 1, TurnID: "t1", At: 100})

	// Fresh, unacknowledged terminal facts persist inside the retention window.
	if removed := r.Compact(101); removed != 0 {
		t.Fatalf("fresh tombstone compacted: removed %d", removed)
	}

	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceProbe, Key: "user"}, Action: "user_clear", Seq: 2, At: 102})
	if removed := r.Compact(103); removed != 1 {
		t.Fatalf("acked tombstone survived: removed %d", removed)
	}
	if facts := r.Facts(); len(facts) != 0 {
		t.Fatalf("facts after acked compact = %+v, want none", facts)
	}
}

func TestShadowReducerRetentionExpiresTerminalFactsAfterTombstoneWindow(t *testing.T) {
	t.Parallel()

	src := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}
	r := state.NewReducer(state.ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: src, Action: "stop", Seq: 1, TurnID: "t1", At: 100})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: state.ASLPhaseTurn, RunID: "run-1", Seq: 1, At: 100})

	if removed := r.Compact(100 + state.TombstoneRetentionSeconds - 1); removed != 0 {
		t.Fatalf("tombstone expired early: removed %d", removed)
	}
	if removed := r.Compact(100 + state.TombstoneRetentionSeconds); removed != 1 {
		t.Fatalf("tombstone not expired at retention: removed %d", removed)
	}
	// Non-terminal facts (the live ASL turn) are never compacted.
	facts := r.Facts()
	if len(facts) != 1 || facts[0].Kind != protocol.SourceASL || !facts[0].LeaseLive {
		t.Fatalf("facts after expiry = %+v, want the live ASL turn only", facts)
	}
}

func TestShadowReducerRetentionExpiresAckedNonTerminalFactsAfterWindow(t *testing.T) {
	t.Parallel()

	src := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}
	r := state.NewReducer(state.ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 5, At: 100})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceProbe, Key: "user"}, Action: "user_clear", Seq: 6, At: 101})

	if facts := r.Facts(); len(facts) != 1 || facts[0].AckedAt != 101 {
		t.Fatalf("acknowledgement timestamp = %+v; want 101", facts)
	}
	if removed := r.Compact(101 + state.AcknowledgedFactRetentionSeconds - 1); removed != 0 {
		t.Fatalf("acked non-terminal fact compacted early: removed %d", removed)
	}
	// The retained fact still guards sequence monotonicity: replaying the old
	// event stays a no-op.
	if dec := r.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 5, At: 100}); dec != protocol.DecisionDuplicate {
		t.Fatalf("replayed event decision = %s, want duplicate", dec)
	}
	if removed := r.Compact(101 + state.AcknowledgedFactRetentionSeconds); removed != 1 {
		t.Fatalf("acked non-terminal fact survived retention boundary: removed %d", removed)
	}
}

func TestShadowReducerRetentionCapsNewestAcknowledgedFactsDeterministically(t *testing.T) {
	t.Parallel()

	r := state.NewReducer(state.ReducerConfig{})
	for i := 0; i < state.MaxAcknowledgedFacts+6; i++ {
		r.Apply(protocol.ReducerEvent{
			Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: fmt.Sprintf("claude:%02d", i)},
			Action: "active", Seq: 1, At: int64(100 + i),
		})
	}
	r.Apply(protocol.ReducerEvent{Source: protocol.UserActionSource(), Action: "user_clear", Seq: 1, At: 1000})

	if removed := r.Compact(1001); removed != 6 {
		t.Fatalf("Compact cap removed %d facts, want 6", removed)
	}
	facts := r.Facts()
	if len(facts) != state.MaxAcknowledgedFacts {
		t.Fatalf("retained facts = %d, want %d", len(facts), state.MaxAcknowledgedFacts)
	}
	for i, fact := range facts {
		want := fmt.Sprintf("claude:%02d", i+6)
		if fact.Key != want || fact.AckedAt != 1000 {
			t.Fatalf("facts[%d] = %+v, want newest %s acked at 1000", i, fact, want)
		}
	}
}

func TestShadowReducerRetentionUsesSourceIdentityAsFinalEvictionTieBreak(t *testing.T) {
	t.Parallel()

	facts := make([]protocol.SourceFactSnapshot, 0, state.MaxAcknowledgedFacts+2)
	for i := 0; i < state.MaxAcknowledgedFacts+1; i++ {
		facts = append(facts, protocol.SourceFactSnapshot{
			Kind: protocol.SourceProbe, Key: fmt.Sprintf("probe:%02d", i), Action: "active", Icon: "bolt",
			Seq: 1, CausalOrder: 1, At: 100, Acked: true, AckedAt: 200,
		})
	}
	facts = append(facts, protocol.SourceFactSnapshot{
		Kind: protocol.SourceHarness, Key: "claude:first", Action: "active", Icon: "bolt",
		Seq: 1, CausalOrder: 1, At: 100, Acked: true, AckedAt: 200,
	})
	r := state.NewReducer(state.ReducerConfig{})
	r.Restore(facts)

	if removed := r.Compact(201); removed != 2 {
		t.Fatalf("Compact tied cap removed %d facts, want 2", removed)
	}
	retained := r.Facts()
	if got := retained[0].Source(); got != (protocol.SourceID{Kind: protocol.SourceProbe, Key: "probe:01"}) {
		t.Fatalf("first retained source = %+v; want probe:01 after kind/key tie-break eviction", got)
	}
}

func TestShadowReducerRetentionPreservesUnackedFactsAndLiveASLLeases(t *testing.T) {
	t.Parallel()

	r := state.NewReducer(state.ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:acked"}, Action: "active", Seq: 1, At: 100})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: state.ASLPhaseBetween, RunID: "run-1", Seq: 1, At: 100})
	r.Apply(protocol.ReducerEvent{Source: protocol.UserActionSource(), Action: "user_clear", Seq: 1, At: 101})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:live"}, Action: "active", Seq: 1, At: 102})

	if removed := r.Compact(101 + state.AcknowledgedFactRetentionSeconds); removed != 1 {
		t.Fatalf("Compact removed %d facts, want only expired acknowledged harness fact", removed)
	}
	retained := r.Facts()
	if len(retained) != 2 || retained[0].Kind != protocol.SourceASL || !retained[0].Acked || !retained[0].LeaseLive ||
		retained[1].Key != "claude:live" || retained[1].Acked {
		t.Fatalf("retained exemptions = %+v", retained)
	}
}

// TestReducerNotificationDedupedBySourceEventID pins that notification
// decisions are generated from reducer transitions and deduplicated by the
// upstream source event ID. Sequence-less deliveries get fresh daemon receipt
// sequences, so the reducer applies a redelivered event as if it were new —
// the source event ID is the only identity that can recognize the retry.
func TestReducerNotificationDedupedBySourceEventID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := activity.NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		h := NewHandler(guard, notifier)
		src := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}

		deliver := func(sourceEventID string) {
			ev := protocol.NormalizedEvent{Source: src, Action: "stop", Event: "Stop", SourceEventID: sourceEventID}
			h.handleStateUpdate(protocol.Request{
				Type: protocol.TypeStateUpdate, WindowID: 3, Action: "stop", Event: "Stop",
				SessionTitle: "W:api", Normalized: &ev,
			})
			// Out-run the notifier's own per-action cooldown so the only thing
			// standing between a retry and a duplicate toast is the dedup.
			time.Sleep(11 * time.Second)
		}

		deliver("evt-1")
		deliver("evt-1")
		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("retried source event produced %d notifications, want 1: %+v", got, rec.snapshot())
		}

		// A genuinely new upstream event still notifies.
		deliver("evt-2")
		synctest.Wait()
		if got := rec.len(); got != 2 {
			t.Fatalf("new source event produced %d notifications, want 2: %+v", got, rec.snapshot())
		}
	})
}
