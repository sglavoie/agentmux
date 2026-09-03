package state

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func requireFactAcknowledgement(t *testing.T, r *Reducer, source protocol.SourceID, acked bool, ackedAt int64) *sourceFact {
	t.Helper()
	f := r.facts[source]
	if f == nil || f.acked != acked || f.ackedAt != ackedAt {
		t.Fatalf("fact %v = %+v; want acked=%v acked_at=%d", source, f, acked, ackedAt)
	}
	return f
}

func TestReducerAcknowledgementPathsStampAndClearAckedAt(t *testing.T) {
	t.Run("prompt supersession and same-source reactivation", func(t *testing.T) {
		old := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:old"}
		r := NewReducer(ReducerConfig{})
		r.Apply(protocol.ReducerEvent{Source: old, Action: "stop", Seq: 1, At: 100})
		r.Apply(protocol.ReducerEvent{
			Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:new"},
			Action: "active", Prompt: true, Seq: 1, At: 200,
		})
		requireFactAcknowledgement(t, r, old, true, 200)

		r.Apply(protocol.ReducerEvent{Source: old, Action: "active", Prompt: true, Seq: 2, At: 201})
		requireFactAcknowledgement(t, r, old, false, 0)
	})

	t.Run("interactive retirement", func(t *testing.T) {
		old := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:old"}
		r := NewReducer(ReducerConfig{})
		r.Apply(protocol.ReducerEvent{Source: old, Action: "active", Seq: 1, At: 100})
		r.Apply(protocol.ReducerEvent{
			Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:new"},
			Action: "stop", Seq: 1, At: 210,
		})
		requireFactAcknowledgement(t, r, old, true, 210)
	})

	t.Run("user clear", func(t *testing.T) {
		source := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}
		r := NewReducer(ReducerConfig{})
		r.Apply(protocol.ReducerEvent{Source: source, Action: "active", Seq: 1, At: 100})
		r.Apply(protocol.ReducerEvent{Source: protocol.UserActionSource(), Action: "user_clear", Seq: 1, At: 300})
		requireFactAcknowledgement(t, r, source, true, 300)
	})

	t.Run("single-fact expiry", func(t *testing.T) {
		source := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:expiry"}
		r := NewReducer(ReducerConfig{})
		r.Apply(protocol.ReducerEvent{Source: source, Action: "active", Seq: 7, At: 100})
		if !r.ExpireFact(source, 7, 500) {
			t.Fatal("ExpireFact returned false")
		}
		requireFactAcknowledgement(t, r, source, true, 500)
		// The next real event from the source clears the acknowledgement, so an
		// expired active-class fact never suppresses the work that follows it.
		r.Apply(protocol.ReducerEvent{Source: source, Action: "active", Seq: 8, At: 501})
		requireFactAcknowledgement(t, r, source, false, 0)
	})
}

func TestReducerMergeCutoffsStampAckedAt(t *testing.T) {
	t.Run("user clear cutoff", func(t *testing.T) {
		r := NewReducer(ReducerConfig{})
		r.Apply(protocol.ReducerEvent{Source: protocol.UserActionSource(), Action: "user_clear", Seq: 1, At: 600})
		source := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:old"}
		r.MergeDisjoint([]protocol.SourceFactSnapshot{{
			Kind: source.Kind, Key: source.Key, Action: "active", Icon: "bolt", Seq: 1, At: 100,
		}})
		requireFactAcknowledgement(t, r, source, true, 600)
	})

	t.Run("prompt cutoff", func(t *testing.T) {
		r := NewReducer(ReducerConfig{})
		r.Apply(protocol.ReducerEvent{
			Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:new"},
			Action: "active", Prompt: true, Seq: 1, At: 610,
		})
		source := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:stop"}
		r.MergeDisjoint([]protocol.SourceFactSnapshot{{
			Kind: source.Kind, Key: source.Key, Action: "stop", Icon: "stop", Seq: 1, At: 100,
			Terminated: true, TerminalAt: 100,
		}})
		requireFactAcknowledgement(t, r, source, true, 610)
	})

	t.Run("interactive cutoff", func(t *testing.T) {
		r := NewReducer(ReducerConfig{})
		r.Apply(protocol.ReducerEvent{
			Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:new"},
			Action: "stop", Seq: 1, At: 620,
		})
		source := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:old"}
		r.MergeDisjoint([]protocol.SourceFactSnapshot{{
			Kind: source.Kind, Key: source.Key, Action: "active", Icon: "bolt", Seq: 1, At: 100,
		}})
		requireFactAcknowledgement(t, r, source, true, 620)
	})
}

func TestReducerProbeSettlementRecoveryStampsAndClearsAckedAt(t *testing.T) {
	interactive := protocol.SourceID{Kind: protocol.SourceHarness, Key: "copilot:s1"}
	probe := protocol.SourceID{Kind: protocol.SourceProbe, Key: "copilot-transcript"}
	r := NewReducer(ReducerConfig{})
	r.Apply(protocol.ReducerEvent{
		Source: interactive, Action: "active", SessionID: "s1", Seq: 1, At: 700,
		OriginWindowID: 91,
	})
	if decision := r.SettleAuthoritativeIdle(probe, 2, 710, "s1", 91); decision != protocol.DecisionApplied {
		t.Fatalf("SettleAuthoritativeIdle decision = %s", decision)
	}
	requireFactAcknowledgement(t, r, interactive, true, 710)
	requireFactAcknowledgement(t, r, probe, false, 0)

	if !r.RecoverInteractiveFromProbeStop(probe, "s1", 720) {
		t.Fatal("RecoverInteractiveFromProbeStop returned false")
	}
	reinstated := requireFactAcknowledgement(t, r, interactive, false, 0)
	if reinstated.at != 720 {
		t.Fatalf("reinstated fact At = %d; want 720", reinstated.at)
	}
	requireFactAcknowledgement(t, r, probe, true, 720)
}
