package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// TestAuthoritativeIdleSettlesStaleInteractiveBolt is the core done-when: an
// idle observation retires an older interactive session's stranded ⚡ and the
// tab converges to 🛑.
func TestAuthoritativeIdleSettlesStaleInteractiveBolt(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "old", "active", base)

	obs := AuthoritativeIdle{
		WindowID:        7,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     idleShadowSeq(t, h, 7),
		At:              base + 5,
		RequiredSamples: 1,
	}
	if !h.applyAuthoritativeIdle(obs) {
		t.Fatalf("authoritative idle was not applied")
	}
	if eff, visible := windowEffective(t, h, 7); eff.Icon != "stop" || visible != "stop" {
		t.Fatalf("effective = %+v visible %q, want stop/stop", eff, visible)
	}
	h.stateMu.Lock()
	old := factOf(h.store[7].Reducer, protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:old"})
	h.stateMu.Unlock()
	if old == nil || !old.Acked {
		t.Fatalf("older interactive bolt was not retired: %+v", old)
	}
}

// TestAuthoritativeIdleBlockedByResetFlow proves a /clear, /reset, or /new flow
// keeps its cleared-state semantics: no synthetic stop surfaces during one.
func TestAuthoritativeIdleBlockedByResetFlow(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "cur", "active", base)

	h.stateMu.Lock()
	h.store[7].PendingReset = state.ResetFlowRequested
	h.stateMu.Unlock()

	obs := AuthoritativeIdle{
		WindowID:        7,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     idleShadowSeq(t, h, 7),
		At:              base + 5,
		RequiredSamples: 1,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("observation applied during a reset flow")
	}
	if eff, _ := windowEffective(t, h, 7); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want the bolt preserved during reset", eff)
	}
}

// TestAuthoritativeIdleRequiresConsecutiveSamples proves the stabilization
// gate: an adapter that requires N consecutive idle frames only settles once
// the streak reaches the threshold.
func TestAuthoritativeIdleRequiresConsecutiveSamples(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "old", "active", base)

	captured := idleShadowSeq(t, h, 7)
	obs := AuthoritativeIdle{
		WindowID:        7,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     captured,
		At:              base + 5,
		RequiredSamples: 3,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("first sample settled before reaching the threshold")
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("second sample settled before reaching the threshold")
	}
	if eff, _ := windowEffective(t, h, 7); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want the bolt to still hold mid-stabilization", eff)
	}
	if !h.applyAuthoritativeIdle(obs) {
		t.Fatalf("third consecutive sample did not settle")
	}
	if eff, _ := windowEffective(t, h, 7); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop after the threshold", eff)
	}
}

// TestAuthoritativeIdleStreakClearedByActivity proves fresh activity resets a
// partial stabilization streak so the adapter cannot settle stale work over the
// new state, and the captured sequence check backs it up.
func TestAuthoritativeIdleStreakClearedByActivity(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "cur", "active", base)

	captured := idleShadowSeq(t, h, 7)
	obs := AuthoritativeIdle{
		WindowID:        7,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     captured,
		At:              base + 5,
		RequiredSamples: 2,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("first sample settled prematurely")
	}
	// Real activity between samples must reset the streak.
	idleSeedHarness(t, h, 7, "cur", "active", base+1)
	h.stateMu.Lock()
	_, streaking := h.idleSampleStreak[idleSampleRef{windowID: 7, key: obs.Source.Key}]
	h.stateMu.Unlock()
	if streaking {
		t.Fatalf("activity did not clear the idle-sample streak")
	}
}

// TestAuthoritativeIdlePreservesProtectedAndBackgroundState proves the
// supersession is narrow: a live ASL lease masks the stop, and an interactive
// attention icon, a pipe-mode child, and a subagent all survive.
func TestAuthoritativeIdlePreservesProtectedAndBackgroundState(t *testing.T) {
	t.Run("live ASL lease masks the stop", func(t *testing.T) {
		h := newTestHandler()
		base := time.Now().Unix()
		idleSeedHarness(t, h, 7, "old", "active", base)
		lease := protocol.NormalizedEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: state.ASLPhaseBetween, RunID: "run-1", At: base, Event: agentSessionLoopStarted}
		h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "loop_marker", Event: agentSessionLoopStarted, Normalized: &lease})

		obs := AuthoritativeIdle{WindowID: 7, Source: AuthoritativeIdleSource("claude"), Harness: "claude", CapturedSeq: idleShadowSeq(t, h, 7), At: base + 5, RequiredSamples: 1}
		if !h.applyAuthoritativeIdle(obs) {
			t.Fatalf("observation not applied")
		}
		eff, _ := windowEffective(t, h, 7)
		if eff.Icon != "" || !eff.Looping {
			t.Fatalf("effective = %+v, want the live lease to mask the stop (loop only)", eff)
		}
	})

	t.Run("interactive attention icon outranks the stop", func(t *testing.T) {
		h := newTestHandler()
		base := time.Now().Unix()
		idleSeedHarness(t, h, 7, "old", "active", base)
		idleSeedHarness(t, h, 7, "perm", "permission", base+1)

		obs := AuthoritativeIdle{WindowID: 7, Source: AuthoritativeIdleSource("claude"), Harness: "claude", CapturedSeq: idleShadowSeq(t, h, 7), At: base + 5, RequiredSamples: 1}
		if !h.applyAuthoritativeIdle(obs) {
			t.Fatalf("observation not applied")
		}
		eff, _ := windowEffective(t, h, 7)
		if eff.Icon != "key" {
			t.Fatalf("effective = %+v, want the permission prompt preserved", eff)
		}
		h.stateMu.Lock()
		perm := factOf(h.store[7].Reducer, protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:perm"})
		h.stateMu.Unlock()
		if perm == nil || perm.Acked {
			t.Fatalf("permission fact was retired: %+v", perm)
		}
	})

	t.Run("pipe-mode child work is preserved", func(t *testing.T) {
		h := newTestHandler()
		base := time.Now().Unix()
		pipe := protocol.NormalizedEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:pipe"}, Action: "active", SessionID: "pipe", PipeMode: true, At: base, Event: "Evt"}
		h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "active", Event: "Evt", Normalized: &pipe})

		obs := AuthoritativeIdle{WindowID: 7, Source: AuthoritativeIdleSource("claude"), Harness: "claude", CapturedSeq: idleShadowSeq(t, h, 7), At: base + 5, RequiredSamples: 1}
		if !h.applyAuthoritativeIdle(obs) {
			t.Fatalf("observation not applied")
		}
		if eff, _ := windowEffective(t, h, 7); eff.Icon != "bolt" {
			t.Fatalf("effective = %+v, want the pipe-mode bolt preserved", eff)
		}
		h.stateMu.Lock()
		pf := factOf(h.store[7].Reducer, protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:pipe"})
		h.stateMu.Unlock()
		if pf == nil || pf.Acked {
			t.Fatalf("pipe-mode fact was retired: %+v", pf)
		}
	})

	t.Run("running subagent is preserved", func(t *testing.T) {
		h := newTestHandler()
		base := time.Now().Unix()
		idleSeedHarness(t, h, 7, "old", "active", base)
		sa := protocol.NormalizedEvent{Source: protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-1"}, Action: "subagent", At: base + 1, Event: "SubagentStart"}
		h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "subagent", Event: "SubagentStart", Normalized: &sa})

		obs := AuthoritativeIdle{WindowID: 7, Source: AuthoritativeIdleSource("claude"), Harness: "claude", CapturedSeq: idleShadowSeq(t, h, 7), At: base + 5, RequiredSamples: 1}
		if !h.applyAuthoritativeIdle(obs) {
			t.Fatalf("observation not applied")
		}
		if eff, _ := windowEffective(t, h, 7); eff.Icon != "subagent" {
			t.Fatalf("effective = %+v, want the running subagent preserved", eff)
		}
	})
}

// TestAuthoritativeIdleRecordsRenderGeneration proves an applied observation
// advances the effective generation and stamps the shadow export metadata with
// the actual reduced result.
func TestAuthoritativeIdleRecordsRenderGeneration(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "old", "active", base)

	h.stateMu.Lock()
	genBefore := h.store[7].EffectiveGen
	h.stateMu.Unlock()

	obs := AuthoritativeIdle{WindowID: 7, Source: AuthoritativeIdleSource("claude"), Harness: "claude", CapturedSeq: idleShadowSeq(t, h, 7), At: base + 5, RequiredSamples: 1}
	if !h.applyAuthoritativeIdle(obs) {
		t.Fatalf("observation not applied")
	}

	h.stateMu.Lock()
	genAfter := h.store[7].EffectiveGen
	h.stateMu.Unlock()
	if genAfter <= genBefore {
		t.Fatalf("effective generation did not advance: %d -> %d", genBefore, genAfter)
	}

	meta := shadowMeta(t, h, 7)
	if meta.EffectiveIcon != "stop" || meta.EffectiveGen != genAfter {
		t.Fatalf("shadow meta = %+v, want stop at generation %d", meta, genAfter)
	}
}

// TestAuthoritativeIdleNotifiesOnTransitionOnlyAndDedupes proves synthetic stop
// notifications fire only when the reduced icon actually becomes stop and are
// deduplicated by upstream source event ID.
func TestAuthoritativeIdleNotifiesOnTransitionOnlyAndDedupes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		h := NewHandler(guard, notifier)
		base := time.Now().Unix()
		tick := int64(0)

		settle := func(sourceEventID string) {
			tick += 10
			idleSeedHarness(t, h, 7, "cur", "active", base+tick)
			tick += 10
			obs := AuthoritativeIdle{
				WindowID:      7,
				Source:        AuthoritativeIdleSource("claude"),
				Harness:       "claude",
				CapturedSeq:   idleShadowSeq(t, h, 7),
				At:            base + tick,
				SessionTitle:  "W:api",
				SourceEventID: sourceEventID,
			}
			if !h.applyAuthoritativeIdle(obs) {
				t.Fatalf("observation %q not applied", sourceEventID)
			}
			if eff, _ := windowEffective(t, h, 7); eff.Icon != "stop" {
				t.Fatalf("observation %q did not settle to stop: %+v", sourceEventID, eff)
			}
			// Out-run the notifier's per-action cooldown so only the source-event
			// dedup stands between a retry and a duplicate toast.
			time.Sleep(11 * time.Second)
		}

		settle("evt-1")
		settle("evt-1")
		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("retried source event produced %d notifications, want 1: %+v", got, rec.snapshot())
		}

		settle("evt-2")
		synctest.Wait()
		if got := rec.len(); got != 2 {
			t.Fatalf("new source event produced %d notifications, want 2: %+v", got, rec.snapshot())
		}
	})
}

// TestAuthoritativeIdleNoWindowIsNoop proves an observation for an unknown
// window (no reducer facts) is dropped without panicking.
func TestAuthoritativeIdleNoWindowIsNoop(t *testing.T) {
	h := newTestHandler()
	obs := AuthoritativeIdle{WindowID: 99, Source: AuthoritativeIdleSource("claude"), CapturedSeq: 0, At: time.Now().Unix(), RequiredSamples: 1}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("observation for an unknown window was applied")
	}
}

// TestIdleSampleExportedTrackingAndWindowClose covers the reusable
// consecutive-sample API and the window-closure cleanup.
func TestIdleSampleExportedTrackingAndWindowClose(t *testing.T) {
	h := newTestHandler()
	src := AuthoritativeIdleSource("claude")
	// Give the window live state so window-closure GC recognizes it as closed.
	idleSeedHarness(t, h, 7, "cur", "active", time.Now().Unix())

	if n := h.RecordIdleSample(7, src); n != 1 {
		t.Fatalf("first RecordIdleSample = %d, want 1", n)
	}
	if n := h.RecordIdleSample(7, src); n != 2 {
		t.Fatalf("second RecordIdleSample = %d, want 2", n)
	}
	// A classifier failure resets the streak.
	h.ClearIdleSample(7, src)
	if n := h.RecordIdleSample(7, src); n != 1 {
		t.Fatalf("RecordIdleSample after clear = %d, want 1", n)
	}

	// Window closure drops the streak with the rest of the window's state.
	h.pruneClosedWindows(time.Now(), map[int]bool{})
	h.stateMu.Lock()
	_, present := h.idleSampleStreak[idleSampleRef{windowID: 7, key: src.Key}]
	h.stateMu.Unlock()
	if present {
		t.Fatalf("window closure did not drop the idle-sample streak")
	}
}
