package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestAuthoritativeIdleDiscardedWhenSequenceMoved covers the new-prompt race:
// evidence captured before a later reducer event lands is discarded, so newer
// activity is never overwritten.
func TestAuthoritativeIdleDiscardedWhenSequenceMoved(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "cur", "active", base)
	captured := idleShadowSeq(t, h, 7)

	// A newer prompt/activity lands after the evidence was captured.
	idleSeedHarness(t, h, 7, "cur", "active", base+1)

	obs := authoritativeIdle{
		WindowID:        7,
		Source:          authoritativeIdleTestSource("claude"),
		Harness:         "claude",
		CapturedSeq:     captured,
		At:              base + 5,
		RequiredSamples: 1,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("stale-sequence observation was applied")
	}
	if eff, visible := windowEffective(t, h, 7); eff.Icon != "bolt" || visible != "bolt" {
		t.Fatalf("effective = %+v visible %q, want the newer bolt preserved", eff, visible)
	}
}

// TestAuthoritativeIdleDiscardedWhenLowerExplicitSequenceLands covers the
// explicit-sequence variant of the new-prompt race: idle evidence is captured
// at a high window receipt generation, then a newer accepted event from a
// DIFFERENT source lands carrying a per-source wire sequence BELOW that
// generation. The reducer accepts it (per-source monotonicity is scoped to the
// new source), so the window mutated after capture — the receipt generation
// must advance even though the event's own sequence did not raise it, and the
// stale idle observation must be discarded rather than retire the newer work.
func TestAuthoritativeIdleDiscardedWhenLowerExplicitSequenceLands(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	base := time.Now().Unix()
	// Push the window receipt generation up with sequence-less activity from the
	// first interactive session, so a low explicit sequence from another source
	// cannot raise it.
	idleSeedHarness(t, h, 7, "old", "active", base)
	idleSeedHarness(t, h, 7, "old", "active", base+1)
	idleSeedHarness(t, h, 7, "old", "active", base+2)
	captured := idleShadowSeq(t, h, 7)
	if captured < 2 {
		t.Fatalf("receipt generation = %d, want a high value to shadow a low explicit sequence", captured)
	}

	// A newer accepted event from a different source with an explicit per-source
	// sequence at or below the captured generation. The reducer accepts it (this
	// source has no prior fact), so the window changed after capture.
	idleSeedHarnessSeq(t, h, 7, "new", "active", 1, base+3)
	if moved := idleShadowSeq(t, h, 7); moved <= captured {
		t.Fatalf("receipt generation = %d, want it advanced past the captured %d after the accepted mutation", moved, captured)
	}

	obs := authoritativeIdle{
		WindowID:        7,
		Source:          authoritativeIdleTestSource("claude"),
		Harness:         "claude",
		CapturedSeq:     captured,
		At:              base + 5,
		RequiredSamples: 1,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("stale observation applied after an accepted lower-sequence mutation")
	}
	if eff, visible := windowEffective(t, h, 7); eff.Icon != "bolt" || visible != "bolt" {
		t.Fatalf("effective = %+v visible %q, want the newer interactive bolt preserved", eff, visible)
	}
	h.stateMu.Lock()
	newFact := factOf(h.store[7].Reducer, protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:new"})
	h.stateMu.Unlock()
	if newFact == nil || newFact.Acked {
		t.Fatalf("newer interactive work was retired by the stale probe: %+v", newFact)
	}
}

// TestAuthoritativeIdleDiscardedWhenWatchdogExpiryLands proves a watchdog fact
// expiry — which acknowledges live source facts in place and carries no
// per-source sequence of its own — advances the window-wide receipt generation,
// so an idle observation captured before the sweep is discarded rather than
// allowed to settle a synthetic stop over the freshly self-healed state.
func TestAuthoritativeIdleDiscardedWhenASLLeaseSeedLands(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "cur", "active", base)
	captured := idleShadowSeq(t, h, 7)

	// Reconciliation discovers a brand-new run and seeds its lease onto the
	// window for the first time (no prior fact), at sequence 0.
	h.stateMu.Lock()
	seeded := h.seedASLLeaseLocked(time.Unix(base+1, 0), aslSeed{
		windowID:  7,
		key:       "run-1",
		candidate: agentloop.Candidate{RunID: "run-1", Phase: agentloop.PhaseBetween},
	})
	moved := h.store[7].ShadowSeq
	h.stateMu.Unlock()
	if !seeded {
		t.Fatalf("first-time ASL lease seed did not apply")
	}
	if moved <= captured {
		t.Fatalf("receipt generation = %d, want it advanced past the captured %d after a first-time ASL seed", moved, captured)
	}

	obs := authoritativeIdle{
		WindowID:        7,
		Source:          authoritativeIdleTestSource("claude"),
		Harness:         "claude",
		CapturedSeq:     captured,
		At:              base + 5,
		RequiredSamples: 1,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("stale observation applied after a first-time ASL lease seed mutated the window")
	}
}
