package daemon

import (
	"fmt"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestRestoreFromUserVarsRestoresStateAndFutureGuard(t *testing.T) {
	h := newTestHandler()
	now := time.Now()
	guardExpiry := now.Add(time.Minute)
	encoded, err := WindowState{
		Activity:       now.Add(-time.Minute),
		LastAction:     "permission",
		LastAccount:    "work",
		LastHarness:    "codex",
		Looping:        true,
		GuardAction:    "permission",
		GuardExpiresAt: guardExpiry,
	}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}

	h.restoreFromUserVars([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			Windows: []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "codex", AMXState: encoded},
			}},
		}},
	}}, now)

	h.stateMu.Lock()
	st, ok := h.store[42]
	h.stateMu.Unlock()
	if !ok {
		t.Fatalf("restored state missing")
	}
	if st.LastAction != "permission" || st.DesiredIcon != "key" || !st.Looping || !st.LoopingSet || !st.Dirty {
		t.Fatalf("restored state = %+v, want action=permission desired=key looping dirty", st)
	}
	if got := h.guard.Current(42); got != "permission" {
		t.Fatalf("guard.Current(42) = %q, want permission", got)
	}
}

func TestRestoreFromUserVarsSkipsExpiredGuardAndUnmanagedWindows(t *testing.T) {
	h := newTestHandler()
	now := time.Now()
	encoded, err := WindowState{
		LastAction:     "active",
		GuardAction:    "active",
		GuardExpiresAt: now.Add(-time.Second),
	}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}

	h.restoreFromUserVars([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			Windows: []kitty.Window{
				{ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: "codex", AMXState: encoded}},
				{ID: 43, UserVars: kitty.UserVars{AMXState: encoded}},
			},
		}},
	}}, now)

	h.stateMu.Lock()
	_, managedOK := h.store[42]
	_, unmanagedOK := h.store[43]
	h.stateMu.Unlock()
	if !managedOK {
		t.Fatalf("managed window was not restored")
	}
	if unmanagedOK {
		t.Fatalf("unmanaged window was restored")
	}
	if got := h.guard.Current(42); got != "" {
		t.Fatalf("expired guard restored as %q", got)
	}
}

func TestRestoreFromUserVarsRestoresReducerFactsLeasesAndGeneration(t *testing.T) {
	h := newTestHandler()
	cfg := ReducerConfig{GuardDuration: 3, StaleActive: 600}
	h.SetReducerConfig(cfg)
	now := time.Now()

	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 2, TurnID: "t1", At: now.Unix()})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 1, At: now.Unix()})
	encoded, err := WindowState{
		LastAction:   "active",
		Reducer:      r,
		Effective:    r.Reduce(now.Unix()),
		EffectiveGen: 4,
		ShadowSeq:    2,
	}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}

	h.restoreFromUserVars([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			Windows: []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: encoded},
			}},
		}},
	}}, now)

	h.stateMu.Lock()
	st, ok := h.store[42]
	h.stateMu.Unlock()
	if !ok {
		t.Fatalf("restored state missing")
	}
	if st.Reducer == nil {
		t.Fatalf("reducer facts were not restored")
	}
	facts := st.Reducer.Facts()
	if len(facts) != 2 {
		t.Fatalf("restored facts = %+v, want harness fact + ASL lease", facts)
	}
	if facts[0].Kind != SourceASL || !facts[0].LeaseLive {
		t.Fatalf("ASL lease not restored: %+v", facts[0])
	}
	if st.EffectiveGen != 4 || st.ShadowSeq != 2 {
		t.Fatalf("generation = %d seq %d, want 4/2", st.EffectiveGen, st.ShadowSeq)
	}
	if eff := st.Reducer.Reduce(now.Unix()); eff.Icon != "bolt" || !eff.Looping {
		t.Fatalf("restored Reduce = %+v, want bolt with live lease", eff)
	}
	if st.Reducer.cfg != cfg {
		t.Fatalf("restored reducer cfg = %+v, want daemon thresholds %+v", st.Reducer.cfg, cfg)
	}
}

// TestRestoreFromUserVarsRepublishesReducedStateAfterTombstoneExpiry pins the
// restore-time republish. A window whose persisted unacked terminal stop aged
// past the tombstone retention while the daemon was down must restore with the
// REDUCED visible state (empty icon), not the legacy LastAction scalar.
// Observed live on the first installed restart: three idle windows restored
// "stop" from the scalar while their reducers compacted to empty, and because
// effective state was only published on the next event, the idle windows held
// that shadow_current_mismatch forever — a permanent doctor/audit failure. A
// fresh (unexpired) stop must keep rendering stop through the same path.
func TestRestoreFromUserVarsRepublishesReducedStateAfterTombstoneExpiry(t *testing.T) {
	h := newTestHandler()
	now := time.Now()

	encode := func(stopAt time.Time) string {
		r := NewReducer(ReducerConfig{})
		r.Apply(ReducerEvent{
			Source: SourceID{Kind: SourceHarness, Key: "claude:s1"},
			Action: "stop", Seq: 1, At: stopAt.Unix(),
		})
		encoded, err := WindowState{
			LastAction: "stop",
			Reducer:    r,
			Effective:  r.Reduce(stopAt.Unix()),
			ShadowSeq:  1,
		}.MarshalUserVar()
		if err != nil {
			t.Fatalf("MarshalUserVar() error: %v", err)
		}
		return encoded
	}
	aged := encode(now.Add(-time.Duration(tombstoneRetentionSeconds+60) * time.Second))
	fresh := encode(now.Add(-time.Minute))

	h.restoreFromUserVars([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			Windows: []kitty.Window{
				{ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: aged}},
				{ID: 43, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: fresh}},
			},
		}},
	}}, now)

	h.stateMu.Lock()
	agedSt := h.store[42]
	freshSt := h.store[43]
	h.stateMu.Unlock()
	if agedSt == nil || freshSt == nil {
		t.Fatalf("restored state missing: aged=%v fresh=%v", agedSt, freshSt)
	}
	if !agedSt.StateSet || agedSt.DesiredIcon != "" || desiredIconForState(agedSt) != "" {
		t.Fatalf("aged restore = StateSet=%v DesiredIcon=%q desired=%q, want authoritative empty icon",
			agedSt.StateSet, agedSt.DesiredIcon, desiredIconForState(agedSt))
	}
	if len(agedSt.Reducer.Facts()) != 0 {
		t.Fatalf("aged restore kept facts %+v, want compacted empty", agedSt.Reducer.Facts())
	}
	if freshSt.DesiredIcon != "stop" || desiredIconForState(freshSt) != "stop" {
		t.Fatalf("fresh restore desired = %q/%q, want stop", freshSt.DesiredIcon, desiredIconForState(freshSt))
	}

	// The shadow metadata must be stamped at restore exactly as a live
	// reduction stamps it: the shadow_current_mismatch invariant compares the
	// renderer's desired icon to Shadow.EffectiveIcon whenever facts exist, so
	// an unstamped (zero) shadow makes every restored window with facts a
	// false-positive doctor/audit failure until its first post-restart event.
	h.stateMu.Lock()
	freshMeta := h.windowMetadata[43]
	h.stateMu.Unlock()
	if freshMeta.Shadow.EffectiveIcon != "stop" {
		t.Fatalf("restored shadow effective icon = %q, want stop stamped at restore", freshMeta.Shadow.EffectiveIcon)
	}
	if freshMeta.Renderer.DesiredIcon != freshMeta.Shadow.EffectiveIcon {
		t.Fatalf("restored renderer desired %q != shadow effective %q (shadow_current_mismatch false positive)",
			freshMeta.Renderer.DesiredIcon, freshMeta.Shadow.EffectiveIcon)
	}
}

func TestRestoreFromUserVarsMigratesV1PayloadToSyntheticLegacySource(t *testing.T) {
	h := newTestHandler()
	cfg := ReducerConfig{GuardDuration: 7}
	h.SetReducerConfig(cfg)
	now := time.Now()

	// Schema v1: no "v" discriminator, scalar action + loop marker only. The
	// timestamp must sit near now: restore republishes through Compact, so an
	// ancient epoch would tombstone-expire the migrated terminal fact (the
	// aged-restore test below pins that path deliberately).
	raw := fmt.Sprintf(`{"a":"stop","l":true,"acct":"work","h":"codex","t":%d}`, now.Add(-time.Minute).Unix())
	h.restoreFromUserVars([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			Windows: []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "codex", AMXState: raw},
			}},
		}},
	}}, now)

	h.stateMu.Lock()
	st, ok := h.store[42]
	h.stateMu.Unlock()
	if !ok {
		t.Fatalf("restored state missing")
	}
	// The legacy scalar survives for export/debugging, but the restored
	// visible state is the republished reduced state: a live between-turns
	// ASL lease suppresses the terminal stop icon (plan policy "between
	// turns: 🔁"), so the migrated 🛑🔁 restores as bare 🔁 until
	// reconciliation confirms or releases the lease.
	if st.LastAction != "stop" || st.DesiredIcon != "" || !st.Looping {
		t.Fatalf("restored scalars = action=%q desired=%q looping=%v, want stop/\"\"/true", st.LastAction, st.DesiredIcon, st.Looping)
	}
	if st.Reducer == nil {
		t.Fatalf("v1 payload did not migrate into reducer facts")
	}
	facts := st.Reducer.Facts()
	if len(facts) != 2 {
		t.Fatalf("migrated facts = %+v, want legacy stop + ASL lease", facts)
	}
	// Migrated facts are re-keyed onto the window's live legacy key so
	// post-restart events from the same window collide with the restored fact
	// (same-source terminal suppression, lease release).
	if facts[1].Kind != SourceLegacy || facts[1].Key != windowKey(42) || !facts[1].Terminated {
		t.Fatalf("legacy migrated fact = %+v", facts[1])
	}
	if facts[0].Kind != SourceASL || facts[0].Key != windowKey(42) || !facts[0].LeaseLive {
		t.Fatalf("ASL migrated fact = %+v", facts[0])
	}
	if st.Effective.Icon != "" || !st.Effective.Looping {
		t.Fatalf("migrated effective = %+v, want lease-suppressed icon + looping", st.Effective)
	}
	if st.Reducer.cfg != cfg {
		t.Fatalf("migrated reducer cfg = %+v, want daemon thresholds %+v", st.Reducer.cfg, cfg)
	}
}
