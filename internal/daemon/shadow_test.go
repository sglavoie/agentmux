package daemon

// The reducer as the state that actually renders, with the legacy machine kept
// alongside it only as a comparison. Where the two agree nothing is exported;
// where they diverge the reduced state wins and the disagreement is recorded as
// a mismatch. Also covered: receipt sequencing for events that carry no
// sequence of their own, and survival of the whole shadow across a restart via
// the Kitty user var. Suppression durability lives in shadow_suppression_test.go
// and the busy-stretch anchor in shadow_busy_since_test.go.

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func shadowMeta(t *testing.T, h *Handler, windowID int) protocol.WindowDebugShadow {
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

	resp := h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "active", Event: "UserPromptSubmit", IsPrompt: true})
	if !resp.OK || !resp.Allowed {
		t.Fatalf("active update rejected: %+v", resp)
	}
	eff, legacyIcon := windowEffective(t, h, 7)
	if eff.Icon != "bolt" || legacyIcon != "bolt" {
		t.Fatalf("after active: shadow %q legacy %q, want bolt/bolt", eff.Icon, legacyIcon)
	}

	resp = h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "stop", Event: "Stop"})
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
	src := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}
	now := time.Now().Unix()

	failure := protocol.NormalizedEvent{Source: src, Action: "stop_failure", Seq: 1, TurnID: "t1", At: now, Event: "StopFailure"}
	h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "stop_failure", Event: "StopFailure", Normalized: &failure})

	// The legacy machine still refuses a non-prompt active after a terminal
	// failure, but the reducer sees a genuinely new turn for the same
	// identified source: authoritative recovery. The reduced state is what
	// renders; the legacy refusal survives only as an exported mismatch.
	active := protocol.NormalizedEvent{Source: src, Action: "active", Seq: 2, TurnID: "t2", At: now + 1, Event: "PostToolBatch"}
	resp := h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "active", Event: "PostToolBatch", Normalized: &active})
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
	if mm.SourceKind != string(protocol.SourceHarness) || mm.SourceKey != "claude:s1" {
		t.Fatalf("mismatch attribution = %s:%s, want harness:claude:s1", mm.SourceKind, mm.SourceKey)
	}
}

// TestReducerSameTurnTrailingCompletionStaysSuppressed is the counterpart:
// an identifiable trailing completion of the terminated turn (same turn ID)
// must not repaint the bolt, no matter how much later it arrives.
func TestReducerSameTurnTrailingCompletionStaysSuppressed(t *testing.T) {
	h := newTestHandler()
	src := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}
	now := time.Now().Unix()

	stop := protocol.NormalizedEvent{Source: src, Action: "stop", Seq: 1, TurnID: "t1", At: now, Event: "Stop"}
	h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "stop", Event: "Stop", Normalized: &stop})

	// Same turn, hours later: still the terminated turn's trailing completion.
	trailing := protocol.NormalizedEvent{Source: src, Action: "active", Seq: 2, TurnID: "t1", At: now + 7200, Event: "PostToolBatch"}
	h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "active", Event: "PostToolBatch", Normalized: &trailing})

	if eff, visibleIcon := windowEffective(t, h, 7); eff.Icon != "stop" || visibleIcon != "stop" {
		t.Fatalf("visible state = %+v/%q, want stop preserved", eff, visibleIcon)
	}

	// A new tool call on a fresh turn recovers immediately.
	recovery := protocol.NormalizedEvent{Source: src, Action: "active", Seq: 3, TurnID: "t2", ToolUse: "tool-9", At: now + 7201, Event: "PreToolUse"}
	h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "active", Event: "PreToolUse", Normalized: &recovery})

	if eff, visibleIcon := windowEffective(t, h, 7); eff.Icon != "bolt" || visibleIcon != "bolt" {
		t.Fatalf("visible state = %+v/%q, want bolt after new turn", eff, visibleIcon)
	}
}

func TestShadowReducerTracksASLLoopLease(t *testing.T) {
	h := newTestHandler()

	started := protocol.NormalizedEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Event: agentSessionLoopStarted}
	h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 9, Action: "loop_marker", Event: agentSessionLoopStarted, Normalized: &started})

	eff, legacyIcon := windowEffective(t, h, 9)
	if !eff.Looping || eff.Icon != "" || legacyIcon != "" {
		t.Fatalf("after loop start: effective %+v legacy %q, want live lease and no icon", eff, legacyIcon)
	}
	if meta := shadowMeta(t, h, 9); meta.MismatchCount != 0 || !meta.EffectiveLooping {
		t.Fatalf("loop start diverged from legacy marker: %+v", meta)
	}

	finished := protocol.NormalizedEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: ASLPhaseFinished, RunID: "run-1", Event: agentSessionLoopFinished}
	h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 9, Action: "loop_marker", Event: agentSessionLoopFinished, Normalized: &finished})

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
	h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 3, Action: "active", Event: "PreToolUse", IsPrompt: true})
	h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 3, Action: "stop", Event: "Stop"})

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
	src := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}
	perm := protocol.NormalizedEvent{Source: src, Action: "permission", Seq: 4, TurnID: "t1", At: 200, Event: "PermissionRequest"}
	h1.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "permission", Event: "PermissionRequest", Normalized: &perm})

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
