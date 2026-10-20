package daemon

// Durability of a guard-blocked trailing event. Suppressing one still mutates
// the reducer — the source cutoff advances while the terminal fact stays — so
// the window has to be marked dirty even though nothing visible changed, or a
// restart restores the older payload and accepts a replay that was already
// consumed. The mirror-image rule matters just as much: a decision that mutates
// no facts must leave the durable state and the render queue completely alone.

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// TestReduceStateUpdate_SuppressedMutationMarksActivityDirty pins the durable
// half of terminal suppression. Reducer.Apply consumes a guard-blocked trailing
// event by advancing the source cutoff while preserving the terminal fact. That
// fact-only mutation must be flushed with the next maintenance pass without
// forcing an immediate title render; otherwise a restart can restore the older
// terminal payload and accept an already-consumed replay.
func TestReduceStateUpdate_SuppressedMutationMarksActivityDirty(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	src := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}
	now := time.Now()

	terminal := protocol.NormalizedEvent{
		Source: src, Action: "stop", Seq: 5, TurnID: "t1", At: now.Unix(), Event: "Stop",
	}
	if resp := h.handleStateUpdate(protocol.Request{
		Type: protocol.TypeStateUpdate, WindowID: 42, Action: "stop", Event: "Stop", Normalized: &terminal,
	}); !resp.OK || !resp.Allowed {
		t.Fatalf("terminal update rejected: %+v", resp)
	}

	// Model the terminal generation having reached Kitty before the trailing
	// delivery. The next mutation must independently re-dirty the window.
	h.stateMu.Lock()
	st := h.store[42]
	st.Dirty = false
	st.DirtySince = time.Time{}
	seqBeforeSuppression := st.Seq
	h.stateMu.Unlock()
	h.renderQueue.Purge(42)

	trailing := protocol.NormalizedEvent{
		Source: src, Action: "active", Seq: 7, TurnID: "t1", At: now.Add(time.Second).Unix(), Event: "PostToolBatch",
	}
	resp := h.handleStateUpdate(protocol.Request{
		Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", Event: "PostToolBatch", Normalized: &trailing,
	})
	if !resp.OK || resp.Allowed {
		t.Fatalf("trailing update = %+v, want guard-blocked success", resp)
	}

	h.stateMu.Lock()
	st = h.store[42]
	factsAfterSuppression := st.Reducer.Facts()
	dirtyAfterSuppression := st.Dirty
	seqAfterSuppression := st.Seq
	encoded, err := st.MarshalUserVar()
	h.stateMu.Unlock()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	if len(factsAfterSuppression) != 1 || factsAfterSuppression[0].Seq != 7 || factsAfterSuppression[0].Icon != "stop" {
		t.Fatalf("facts after suppression = %+v, want terminal cutoff advanced to seq 7", factsAfterSuppression)
	}
	if !dirtyAfterSuppression || seqAfterSuppression != seqBeforeSuppression+1 {
		t.Fatalf("suppressed mutation durability = dirty %v seq %d, want dirty true seq %d",
			dirtyAfterSuppression, seqAfterSuppression, seqBeforeSuppression+1)
	}
	if !h.renderQueue.Empty() {
		t.Fatal("fact-only suppressed mutation forced an immediate render")
	}

	// The maintenance-flush payload restores the advanced cutoff. A replay
	// between the persisted terminal (seq 5) and consumed trailing event (seq 7)
	// is now rejected even though its new turn would otherwise revive the bolt.
	var restored state.WindowState
	if err := restored.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar() error: %v", err)
	}
	replay := protocol.ReducerEvent{
		Source: src, Action: "active", Seq: 6, TurnID: "t2", At: now.Add(2 * time.Second).Unix(),
	}
	if decision := restored.Reducer.Apply(replay); decision != protocol.DecisionReordered {
		t.Fatalf("post-restart replay decision = %s, want reordered", decision)
	}
	if effective := restored.Reducer.Reduce(now.Add(2 * time.Second).Unix()); effective.Icon != "stop" {
		t.Fatalf("post-restart replay surfaced %+v, want terminal stop preserved", effective)
	}

	// Decisions that do not mutate reducer facts remain clean and never enter
	// the immediate render queue. Keep their explicit sequence at the current
	// receipt so the assertion isolates reduction from receipt allocation.
	tests := []struct {
		name  string
		event protocol.NormalizedEvent
		want  protocol.ReducerDecision
	}{
		{name: "duplicate", event: trailing, want: protocol.DecisionDuplicate},
		{name: "reordered", event: protocol.NormalizedEvent{
			Source: src, Action: "active", Seq: 6, TurnID: "t1", At: now.Add(time.Second).Unix(), Event: "PostToolBatch",
		}, want: protocol.DecisionReordered},
		{name: "quarantined", event: protocol.NormalizedEvent{
			Source: protocol.SourceID{Kind: protocol.SourceInvalid, Key: events.InvalidPayloadReason}, Seq: 7, Event: "Stop", Reason: events.InvalidPayloadReason,
		}, want: protocol.DecisionQuarantined},
		{name: "ignored", event: protocol.NormalizedEvent{
			Seq: 7, Event: "InstructionsLoaded", Reason: events.ReasonIgnoredEvent,
		}, want: protocol.DecisionQuarantined},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h.stateMu.Lock()
			st := h.store[42]
			st.Dirty = false
			st.DirtySince = time.Time{}
			seqBefore := st.Seq
			shadowSeqBefore := st.ShadowSeq
			factsBefore := st.Reducer.Facts()
			h.stateMu.Unlock()
			h.renderQueue.Purge(42)

			event := tt.event
			h.reduceStateUpdate(protocol.Request{
				Type: protocol.TypeStateUpdate, WindowID: 42, Action: event.Action, Event: event.Event, Normalized: &event,
			}, applyOutcome{}, protocol.Response{OK: true}, now.Add(3*time.Second))

			h.stateMu.Lock()
			st = h.store[42]
			decision := lastTrace(t, st.Reducer).Decision
			dirty := st.Dirty
			seqAfter := st.Seq
			shadowSeqAfter := st.ShadowSeq
			factsAfter := st.Reducer.Facts()
			h.stateMu.Unlock()

			if decision != tt.want {
				t.Fatalf("decision = %s, want %s", decision, tt.want)
			}
			if dirty || seqAfter != seqBefore || shadowSeqAfter != shadowSeqBefore || !reflect.DeepEqual(factsAfter, factsBefore) {
				t.Fatalf("mutation-free decision changed durable state: dirty=%v seq=%d/%d shadow_seq=%d/%d facts=%+v/%+v",
					dirty, seqAfter, seqBefore, shadowSeqAfter, shadowSeqBefore, factsAfter, factsBefore)
			}
			if !h.renderQueue.Empty() {
				t.Fatal("mutation-free decision entered the render queue")
			}
		})
	}
}
