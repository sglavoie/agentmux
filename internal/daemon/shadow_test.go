package daemon

import (
	"reflect"
	"testing"
	"testing/synctest"
	"time"

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

// busySince reads the window's busy-stretch anchor.
func busySince(t *testing.T, h *Handler, windowID int) time.Time {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok {
		t.Fatalf("window %d has no state", windowID)
	}
	return st.BusySince
}

// publishIcon drives one reduced publish through publishEffectiveLocked, the
// single funnel the busy-stretch anchor is maintained from.
func publishIcon(h *Handler, windowID int, icon string) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	h.publishEffectiveLocked(windowID, h.ensureWindowStateLocked(windowID), EffectiveState{Icon: icon})
}

func TestBusySinceStampedOnActiveClassEntry(t *testing.T) {
	cases := []struct{ name, from, to string }{
		{"stop to bolt", "stop", "bolt"},
		{"empty to bolt", "", "bolt"},
		{"stop to subagent", "stop", "subagent"},
		{"empty to compact", "", "compact"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newTestHandler()
				publishIcon(h, 7, tc.from)
				if got := busySince(t, h, 7); !got.IsZero() {
					t.Fatalf("anchor before entry = %v; want zero", got)
				}

				time.Sleep(time.Minute)
				entered := time.Now()
				publishIcon(h, 7, tc.to)

				if got := busySince(t, h, 7); !got.Equal(entered) {
					t.Fatalf("anchor = %v; want the entry instant %v", got, entered)
				}
			})
		})
	}
}

// TestBusySinceSurvivesActiveClassChurn pins why the anchor is an idempotent
// invariant rather than transition detection: a bolt handing off to a subagent
// and back is one busy stretch, not three.
func TestBusySinceSurvivesActiveClassChurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTestHandler()
		publishIcon(h, 7, "bolt")
		anchor := busySince(t, h, 7)
		if anchor.IsZero() {
			t.Fatal("bolt did not stamp the anchor")
		}

		for _, icon := range []string{"subagent", "bolt", "compact", "bolt"} {
			time.Sleep(30 * time.Second)
			publishIcon(h, 7, icon)
			if got := busySince(t, h, 7); !got.Equal(anchor) {
				t.Fatalf("anchor after %q = %v; want the stretch's original %v", icon, got, anchor)
			}
		}
	})
}

func TestBusySinceClearedByEveryNonActiveIcon(t *testing.T) {
	for _, icon := range []string{"stop", "key", "question", "warning", "error", ""} {
		name := icon
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			h := newTestHandler()
			publishIcon(h, 7, "bolt")
			if busySince(t, h, 7).IsZero() {
				t.Fatal("bolt did not stamp the anchor")
			}

			publishIcon(h, 7, icon)

			if got := busySince(t, h, 7); !got.IsZero() {
				t.Fatalf("anchor after %q = %v; want zero", icon, got)
			}
		})
	}
}

// TestBusySinceNotRestampedByIdenticalRepublish covers publishEffectiveLocked's
// early-exit path: a republish of an unchanged effective state skips the
// generation bump but still reaches the anchor invariant, which must leave a
// live stretch alone instead of restarting it.
func TestBusySinceNotRestampedByIdenticalRepublish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTestHandler()
		publishIcon(h, 7, "bolt")
		anchor := busySince(t, h, 7)
		h.stateMu.Lock()
		genBefore := h.store[7].EffectiveGen
		h.stateMu.Unlock()

		time.Sleep(5 * time.Minute)
		publishIcon(h, 7, "bolt")

		h.stateMu.Lock()
		genAfter := h.store[7].EffectiveGen
		h.stateMu.Unlock()
		if genAfter != genBefore {
			t.Fatalf("EffectiveGen = %d; want %d so the republish exercises the early exit", genAfter, genBefore)
		}
		if got := busySince(t, h, 7); !got.Equal(anchor) {
			t.Fatalf("anchor = %v; want the unchanged %v", got, anchor)
		}
	})
}

// TestBusySinceUntouchedBySkipReduceUpdate pins the assumption that lets the
// legacy scalar DesiredIcon writer keep the anchor out of handleStateUpdate:
// a skipReduce request is icon-preserving, never reaches
// publishEffectiveLocked, and must leave a live stretch running.
func TestBusySinceUntouchedBySkipReduceUpdate(t *testing.T) {
	h := newTestHandler()
	if resp := h.handleStateUpdate(Request{
		Type: TypeStateUpdate, WindowID: 7, Action: "active", Event: "UserPromptSubmit", IsPrompt: true,
	}); !resp.OK || !resp.Allowed {
		t.Fatalf("active update rejected: %+v", resp)
	}
	anchor := busySince(t, h, 7)
	if anchor.IsZero() {
		t.Fatal("active update did not stamp the anchor")
	}

	h.stateMu.Lock()
	shadowSeqBefore := h.store[7].ShadowSeq
	factsBefore := h.store[7].Reducer.Facts()
	h.stateMu.Unlock()

	if resp := h.handleStateUpdate(Request{
		Type: TypeStateUpdate, WindowID: 7, Action: "active", Event: "PostToolUse", PreservesIcon: true,
	}); !resp.OK {
		t.Fatalf("child-complete update = %+v; want OK", resp)
	}

	h.stateMu.Lock()
	shadowSeqAfter := h.store[7].ShadowSeq
	factsAfter := h.store[7].Reducer.Facts()
	h.stateMu.Unlock()
	if shadowSeqAfter != shadowSeqBefore || !reflect.DeepEqual(factsAfter, factsBefore) {
		t.Fatalf("child-complete update reached the reducer: shadow_seq %d/%d facts %+v/%+v",
			shadowSeqAfter, shadowSeqBefore, factsAfter, factsBefore)
	}
	if got := busySince(t, h, 7); !got.Equal(anchor) {
		t.Fatalf("anchor = %v; want the untouched %v", got, anchor)
	}
}
