package daemon

import (
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func shadowMeta(t *testing.T, h *Handler, windowID int) WindowDebugShadow {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return cloneWindowDebugMetadata(h.windowMetadata[windowID]).Shadow
}

func windowEffective(t *testing.T, h *Handler, windowID int) (EffectiveState, string) {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok {
		t.Fatalf("window %d has no state", windowID)
	}
	return st.Effective, desiredIconForState(st)
}

func TestShadowReducerAgreesWithLegacyOnBasicFlow(t *testing.T) {
	h := newTestHandler()

	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 7, Action: "active", Event: "UserPromptSubmit", IsPrompt: true})
	if !resp.OK || !resp.Allowed {
		t.Fatalf("active update rejected: %+v", resp)
	}
	eff, legacyIcon := windowEffective(t, h, 7)
	if eff.Icon != "bolt" || legacyIcon != "bolt" {
		t.Fatalf("after active: shadow %q legacy %q, want bolt/bolt", eff.Icon, legacyIcon)
	}

	resp = h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 7, Action: "stop", Event: "Stop"})
	if !resp.OK || !resp.Allowed {
		t.Fatalf("stop update rejected: %+v", resp)
	}
	eff, legacyIcon = windowEffective(t, h, 7)
	if eff.Icon != "stop" || legacyIcon != "stop" {
		t.Fatalf("after stop: shadow %q legacy %q, want stop/stop", eff.Icon, legacyIcon)
	}

	meta := shadowMeta(t, h, 7)
	if meta.MismatchCount != 0 || len(meta.Mismatches) != 0 {
		t.Fatalf("agreeing flow recorded mismatches: %+v", meta)
	}
	if meta.EffectiveIcon != "stop" || meta.EffectiveGen != 2 {
		t.Fatalf("shadow meta = %+v, want stop at generation 2", meta)
	}
}

func TestReducerNewTurnRecoversAfterTerminalFailure(t *testing.T) {
	h := newTestHandler()
	src := SourceID{Kind: SourceHarness, Key: "claude:s1"}
	now := time.Now().Unix()

	failure := NormalizedEvent{Source: src, Action: "stop_failure", Seq: 1, TurnID: "t1", At: now, Event: "StopFailure"}
	h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 7, Action: "stop_failure", Event: "StopFailure", normalized: &failure})

	// The legacy machine still refuses a non-prompt active after a terminal
	// failure, but the reducer sees a genuinely new turn for the same
	// identified source: authoritative recovery. The reduced state is what
	// renders; the legacy refusal survives only as an exported mismatch.
	active := NormalizedEvent{Source: src, Action: "active", Seq: 2, TurnID: "t2", At: now + 1, Event: "PostToolBatch"}
	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 7, Action: "active", Event: "PostToolBatch", normalized: &active})
	if !resp.OK || resp.Allowed {
		t.Fatalf("expected legacy refusal alongside reducer recovery, got %+v", resp)
	}

	eff, visibleIcon := windowEffective(t, h, 7)
	if eff.Icon != "bolt" || visibleIcon != "bolt" {
		t.Fatalf("visible state = %+v/%q, want the new turn's bolt", eff, visibleIcon)
	}

	meta := shadowMeta(t, h, 7)
	if meta.MismatchCount != 1 || len(meta.Mismatches) != 1 {
		t.Fatalf("mismatch export = %+v, want exactly one entry", meta)
	}
	mm := meta.Mismatches[0]
	if mm.LegacyIcon != "error" || mm.ShadowIcon != "bolt" || mm.Event != "PostToolBatch" {
		t.Fatalf("mismatch entry = %+v", mm)
	}
	if mm.SourceKind != string(SourceHarness) || mm.SourceKey != "claude:s1" {
		t.Fatalf("mismatch attribution = %s:%s, want harness:claude:s1", mm.SourceKind, mm.SourceKey)
	}
}

// TestReducerSameTurnTrailingCompletionStaysSuppressed is the counterpart:
// an identifiable trailing completion of the terminated turn (same turn ID)
// must not repaint the bolt, no matter how much later it arrives.
func TestReducerSameTurnTrailingCompletionStaysSuppressed(t *testing.T) {
	h := newTestHandler()
	src := SourceID{Kind: SourceHarness, Key: "claude:s1"}
	now := time.Now().Unix()

	stop := NormalizedEvent{Source: src, Action: "stop", Seq: 1, TurnID: "t1", At: now, Event: "Stop"}
	h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 7, Action: "stop", Event: "Stop", normalized: &stop})

	// Same turn, hours later: still the terminated turn's trailing completion.
	trailing := NormalizedEvent{Source: src, Action: "active", Seq: 2, TurnID: "t1", At: now + 7200, Event: "PostToolBatch"}
	h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 7, Action: "active", Event: "PostToolBatch", normalized: &trailing})

	if eff, visibleIcon := windowEffective(t, h, 7); eff.Icon != "stop" || visibleIcon != "stop" {
		t.Fatalf("visible state = %+v/%q, want stop preserved", eff, visibleIcon)
	}

	// A new tool call on a fresh turn recovers immediately.
	recovery := NormalizedEvent{Source: src, Action: "active", Seq: 3, TurnID: "t2", ToolUse: "tool-9", At: now + 7201, Event: "PreToolUse"}
	h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 7, Action: "active", Event: "PreToolUse", normalized: &recovery})

	if eff, visibleIcon := windowEffective(t, h, 7); eff.Icon != "bolt" || visibleIcon != "bolt" {
		t.Fatalf("visible state = %+v/%q, want bolt after new turn", eff, visibleIcon)
	}
}

func TestShadowReducerTracksASLLoopLease(t *testing.T) {
	h := newTestHandler()

	started := NormalizedEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Event: agentSessionLoopStarted}
	h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 9, Action: "loop_marker", Event: agentSessionLoopStarted, normalized: &started})

	eff, legacyIcon := windowEffective(t, h, 9)
	if !eff.Looping || eff.Icon != "" || legacyIcon != "" {
		t.Fatalf("after loop start: effective %+v legacy %q, want live lease and no icon", eff, legacyIcon)
	}
	if meta := shadowMeta(t, h, 9); meta.MismatchCount != 0 || !meta.EffectiveLooping {
		t.Fatalf("loop start diverged from legacy marker: %+v", meta)
	}

	finished := NormalizedEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseFinished, RunID: "run-1", Event: agentSessionLoopFinished}
	h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 9, Action: "loop_marker", Event: agentSessionLoopFinished, normalized: &finished})

	eff, legacyIcon = windowEffective(t, h, 9)
	if eff.Looping {
		t.Fatalf("lease still live after run finished: %+v", eff)
	}
	// The reducer paints ⏹ for a finished run; the legacy marker path never
	// painted icons, so the divergence is exported while the reduced state is
	// what actually renders.
	if eff.Icon != "stop" || legacyIcon != "stop" {
		t.Fatalf("after finish: effective %q visible %q, want stop/stop", eff.Icon, legacyIcon)
	}
	if meta := shadowMeta(t, h, 9); meta.MismatchCount != 1 {
		t.Fatalf("finish divergence not exported: %+v", meta)
	}
}

func TestShadowReducerAssignsReceiptSequenceForSequencelessEvents(t *testing.T) {
	h := newTestHandler()

	// Two identical sequence-less legacy requests must both apply (receipt
	// sequences keep the source monotonic) rather than collapse as duplicates.
	h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 3, Action: "active", Event: "PreToolUse", IsPrompt: true})
	h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 3, Action: "stop", Event: "Stop"})

	h.stateMu.Lock()
	st := h.store[3]
	shadowSeq := st.ShadowSeq
	facts := st.Reducer.Facts()
	h.stateMu.Unlock()
	if shadowSeq != 2 {
		t.Fatalf("ShadowSeq = %d, want 2", shadowSeq)
	}
	if len(facts) != 1 || facts[0].Action != "stop" || facts[0].Seq != 2 {
		t.Fatalf("facts = %+v, want one legacy stop fact at seq 2", facts)
	}
}

func TestShadowReducerSurvivesRestartViaUserVar(t *testing.T) {
	h1 := newTestHandler()
	h1.SetReducerConfig(ReducerConfig{GuardDuration: 5})
	src := SourceID{Kind: SourceHarness, Key: "claude:s1"}
	perm := NormalizedEvent{Source: src, Action: "permission", Seq: 4, TurnID: "t1", At: 200, Event: "PermissionRequest"}
	h1.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 42, Action: "permission", Event: "PermissionRequest", normalized: &perm})

	h1.stateMu.Lock()
	encoded, err := h1.store[42].MarshalUserVar()
	wantEffective := h1.store[42].Effective
	wantGen := h1.store[42].EffectiveGen
	h1.stateMu.Unlock()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}

	h2 := newTestHandler()
	h2.SetReducerConfig(ReducerConfig{GuardDuration: 5})
	h2.restoreFromUserVars([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			Windows: []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: encoded},
			}},
		}},
	}}, time.Now())

	h2.stateMu.Lock()
	st, ok := h2.store[42]
	h2.stateMu.Unlock()
	if !ok {
		t.Fatalf("restored state missing")
	}
	if st.Effective != wantEffective || st.EffectiveGen != wantGen {
		t.Fatalf("restored effective = %+v gen %d, want %+v gen %d", st.Effective, st.EffectiveGen, wantEffective, wantGen)
	}
	if st.Reducer == nil {
		t.Fatalf("restored state has no reducer")
	}
	if st.Reducer.cfg != (ReducerConfig{GuardDuration: 5}) {
		t.Fatalf("restored reducer cfg = %+v, want daemon thresholds re-bound", st.Reducer.cfg)
	}
	if eff := st.Reducer.Reduce(201); eff != wantEffective {
		t.Fatalf("restored Reduce = %+v, want %+v", eff, wantEffective)
	}
}

// TestReduceStateUpdate_SuppressedMutationMarksActivityDirty pins the durable
// half of terminal suppression. Reducer.Apply consumes a guard-blocked trailing
// event by advancing the source cutoff while preserving the terminal fact. That
// fact-only mutation must be flushed with the next maintenance pass without
// forcing an immediate title render; otherwise a restart can restore the older
// terminal payload and accept an already-consumed replay.
func TestReduceStateUpdate_SuppressedMutationMarksActivityDirty(t *testing.T) {
	h := newTestHandler()
	src := SourceID{Kind: SourceHarness, Key: "claude:s1"}
	now := time.Now()

	terminal := NormalizedEvent{
		Source: src, Action: "stop", Seq: 5, TurnID: "t1", At: now.Unix(), Event: "Stop",
	}
	if resp := h.handleStateUpdate(Request{
		Type: TypeStateUpdate, WindowID: 42, Action: "stop", Event: "Stop", normalized: &terminal,
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
	h.renderQueue.purge(42)

	trailing := NormalizedEvent{
		Source: src, Action: "active", Seq: 7, TurnID: "t1", At: now.Add(time.Second).Unix(), Event: "PostToolBatch",
	}
	resp := h.handleStateUpdate(Request{
		Type: TypeStateUpdate, WindowID: 42, Action: "active", Event: "PostToolBatch", normalized: &trailing,
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
	if !h.renderQueue.empty() {
		t.Fatal("fact-only suppressed mutation forced an immediate render")
	}

	// The maintenance-flush payload restores the advanced cutoff. A replay
	// between the persisted terminal (seq 5) and consumed trailing event (seq 7)
	// is now rejected even though its new turn would otherwise revive the bolt.
	var restored WindowState
	if err := restored.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar() error: %v", err)
	}
	replay := ReducerEvent{
		Source: src, Action: "active", Seq: 6, TurnID: "t2", At: now.Add(2 * time.Second).Unix(),
	}
	if decision := restored.Reducer.Apply(replay); decision != DecisionReordered {
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
		event NormalizedEvent
		want  ReducerDecision
	}{
		{name: "duplicate", event: trailing, want: DecisionDuplicate},
		{name: "reordered", event: NormalizedEvent{
			Source: src, Action: "active", Seq: 6, TurnID: "t1", At: now.Add(time.Second).Unix(), Event: "PostToolBatch",
		}, want: DecisionReordered},
		{name: "quarantined", event: NormalizedEvent{
			Source: SourceID{Kind: SourceInvalid, Key: InvalidPayloadReason}, Seq: 7, Event: "Stop", Reason: InvalidPayloadReason,
		}, want: DecisionQuarantined},
		{name: "ignored", event: NormalizedEvent{
			Seq: 7, Event: "InstructionsLoaded", Reason: reasonIgnoredEvent,
		}, want: DecisionQuarantined},
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
			h.renderQueue.purge(42)

			event := tt.event
			h.reduceStateUpdate(Request{
				Type: TypeStateUpdate, WindowID: 42, Action: event.Action, Event: event.Event, normalized: &event,
			}, applyOutcome{}, Response{OK: true}, now.Add(3*time.Second))

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
			if !h.renderQueue.empty() {
				t.Fatal("mutation-free decision entered the render queue")
			}
		})
	}
}

func TestShadowReducerRetentionRetiresCompletedSubagentsAfterStragglerWindow(t *testing.T) {
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceSubagent, Key: "agent-1"}, Action: "subagent", Seq: 1, TurnID: "t1", At: 100})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceSubagent, Key: "agent-1"}, Action: "stop", Seq: 2, TurnID: "t1", At: 110})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceSubagent, Key: "agent-2"}, Action: "stop_failure", Seq: 1, TurnID: "t1", At: 120})

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
	if removed := r.Compact(110 + subagentStopRetentionSeconds); removed != 1 {
		t.Fatalf("Compact past straggler window removed %d facts, want the completed subagent only", removed)
	}
	facts := r.Facts()
	if len(facts) != 1 || facts[0].Key != "agent-2" || facts[0].Icon != "error" {
		t.Fatalf("facts after compact = %+v, want only the failed subagent", facts)
	}
}

func TestShadowReducerRetentionExpiresTerminalFactsAfterAcknowledgement(t *testing.T) {
	src := SourceID{Kind: SourceHarness, Key: "claude:s1"}
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: src, Action: "stop", Seq: 1, TurnID: "t1", At: 100})

	// Fresh, unacknowledged terminal facts persist inside the retention window.
	if removed := r.Compact(101); removed != 0 {
		t.Fatalf("fresh tombstone compacted: removed %d", removed)
	}

	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceProbe, Key: "user"}, Action: "user_clear", Seq: 2, At: 102})
	if removed := r.Compact(103); removed != 1 {
		t.Fatalf("acked tombstone survived: removed %d", removed)
	}
	if facts := r.Facts(); len(facts) != 0 {
		t.Fatalf("facts after acked compact = %+v, want none", facts)
	}
}

func TestShadowReducerRetentionExpiresTerminalFactsAfterTombstoneWindow(t *testing.T) {
	src := SourceID{Kind: SourceHarness, Key: "claude:s1"}
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: src, Action: "stop", Seq: 1, TurnID: "t1", At: 100})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseTurn, RunID: "run-1", Seq: 1, At: 100})

	if removed := r.Compact(100 + tombstoneRetentionSeconds - 1); removed != 0 {
		t.Fatalf("tombstone expired early: removed %d", removed)
	}
	if removed := r.Compact(100 + tombstoneRetentionSeconds); removed != 1 {
		t.Fatalf("tombstone not expired at retention: removed %d", removed)
	}
	// Non-terminal facts (the live ASL turn) are never compacted.
	facts := r.Facts()
	if len(facts) != 1 || facts[0].Kind != SourceASL || !facts[0].LeaseLive {
		t.Fatalf("facts after expiry = %+v, want the live ASL turn only", facts)
	}
}

func TestShadowReducerRetentionKeepsAckedNonTerminalFacts(t *testing.T) {
	src := SourceID{Kind: SourceHarness, Key: "claude:s1"}
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: src, Action: "active", Seq: 5, At: 100})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceProbe, Key: "user"}, Action: "user_clear", Seq: 6, At: 101})

	if removed := r.Compact(102); removed != 0 {
		t.Fatalf("acked non-terminal fact compacted: removed %d", removed)
	}
	// The retained fact still guards sequence monotonicity: replaying the old
	// event stays a no-op.
	if dec := r.Apply(ReducerEvent{Source: src, Action: "active", Seq: 5, At: 100}); dec != DecisionDuplicate {
		t.Fatalf("replayed event decision = %s, want duplicate", dec)
	}
}

// TestReducerNotificationDedupedBySourceEventID pins that notification
// decisions are generated from reducer transitions and deduplicated by the
// upstream source event ID. Sequence-less deliveries get fresh daemon receipt
// sequences, so the reducer applies a redelivered event as if it were new —
// the source event ID is the only identity that can recognize the retry.
func TestReducerNotificationDedupedBySourceEventID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		h := NewHandler(guard, notifier)
		src := SourceID{Kind: SourceHarness, Key: "claude:s1"}

		deliver := func(sourceEventID string) {
			ev := NormalizedEvent{Source: src, Action: "stop", Event: "Stop", SourceEventID: sourceEventID}
			h.handleStateUpdate(Request{
				Type: TypeStateUpdate, WindowID: 3, Action: "stop", Event: "Stop",
				SessionTitle: "W:api", normalized: &ev,
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
