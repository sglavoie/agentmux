package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// bringUpDegradedOnGatedClient wires a gatedStartupListKittyClient that blocks
// the first (startup) list, comes up degraded through the bounded restore, and
// waits until the restore goroutine has parked on the blocked list — the exact
// window in which a real hook lands before any persisted-fact restore.
func bringUpDegradedOnGatedClient(t *testing.T, h *Handler, windows []kitty.OSWindow, now time.Time) *gatedStartupListKittyClient {
	t.Helper()
	client := &gatedStartupListKittyClient{
		windows:         windows,
		firstEntered:    make(chan struct{}, 1),
		releaseFirst:    make(chan struct{}),
		restoreRendered: make(chan struct{}),
	}
	h.useKittyClient(client)
	t.Cleanup(func() {
		select {
		case <-client.releaseFirst:
		default:
			close(client.releaseFirst)
		}
	})
	ready, reason := h.restoreStateFromKittyBounded(now, 30*time.Millisecond)
	if ready || reason == "" {
		t.Fatalf("bounded restore = (%v, %q); want degraded with a reason", ready, reason)
	}
	h.MarkDegraded(reason)
	select {
	case <-client.firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("bounded restore never parked on the blocked first list")
	}
	return client
}

// seedInProcessReducerTransition models the degraded-startup hook: it creates the
// window's in-process reducer (as a real hook would, for its own source only)
// and folds `ev` — a user_clear, a prompt, or an interactive terminal — through
// Apply so the reducer records the transition's supersession cutoff before the
// first persisted-fact restore runs.
func seedInProcessReducerTransition(t *testing.T, h *Handler, windowID int, ev protocol.ReducerEvent) *WindowState {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.ensureWindowStateLocked(windowID)
	if st.Reducer == nil {
		st.Reducer = NewReducer(h.reducerCfg)
	}
	st.Reducer.Apply(ev)
	st.StateSet = true
	return st
}

// waitLateStartupRestore releases the blocked startup list and waits for the
// late-completing restore's render, so a test can assert the merge is idempotent
// across the maintenance-recovery and late-startup restore paths.
func waitLateStartupRestore(t *testing.T, client *gatedStartupListKittyClient) {
	t.Helper()
	close(client.releaseFirst)
	select {
	case <-client.restoreRendered:
	case <-time.After(2 * time.Second):
		t.Fatal("late startup restore never completed its render")
	}
}

// TestMaintenanceTick_MergeHonorsPreRestoreUserClear pins the user_clear leg of
// F2-degraded-startup-merge-bypasses-window-supersession. A /clear (user_clear)
// accepted in-process before the first persisted-fact restore acknowledges every
// fact then on screen but leaves no fact a later import could collide with. The
// recovery merge must therefore acknowledge the older persisted facts it imports
// (an interactive bolt and a terminal stop) so the tab cannot regain an icon,
// while a genuinely live ASL loop lease is preserved — a live loop, not a
// resurrected one.
func TestMaintenanceTick_MergeHonorsPreRestoreUserClear(t *testing.T) {
	h := newTestHandler()
	now := time.Now()
	old := now.Add(-time.Minute).Unix()

	// Persisted durable state for window 42, all OLDER than the clear: an
	// interactive harness bolt, a terminal probe stop, and a live between-turns
	// ASL lease.
	r := NewReducer(ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 1, At: old})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceProbe, Key: "screen"}, Action: "stop", Seq: 1, At: old})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 1, At: old})
	windows := encodeMergeWindow(t, r, 1, now)

	client := bringUpDegradedOnGatedClient(t, h, windows, now)

	// A /clear lands FIRST, before any restore. user_clear creates no fact; it
	// records the clear cutoff on the (otherwise empty) in-process reducer.
	seedInProcessReducerTransition(t, h, 42, protocol.ReducerEvent{
		Source: userActionSource(), Action: "user_clear", Seq: 5, At: now.Unix(),
	})

	// Maintenance recovery merges the disjoint persisted facts into the cleared
	// window before marking ready.
	h.maintenanceTick(now)
	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
	}
	h.stateMu.Lock()
	merged, mergedOK := h.store[42]
	h.stateMu.Unlock()
	if !mergedOK || merged.Reducer == nil {
		t.Fatal("maintenance merge did not populate the window")
	}

	// The clear cutoff acknowledged the merged pre-clear facts: no icon returns.
	if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "" {
		t.Fatalf("merged Reduce = %+v; want icon-less (a merged pre-clear fact regained an icon)", eff)
	}
	// The merged interactive bolt is present but acknowledged.
	if bolt := mergedFactBySource(merged.Reducer.Facts(), protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}); bolt == nil || !bolt.Acked {
		t.Fatalf("merged pre-clear interactive bolt not acknowledged: %+v", bolt)
	}
	// The merged terminal stop was acknowledged then compacted away, exactly as a
	// live user_clear + Compact retires an acknowledged terminal tombstone.
	if stop := mergedFactBySource(merged.Reducer.Facts(), protocol.SourceID{Kind: protocol.SourceProbe, Key: "screen"}); stop != nil {
		t.Fatalf("acknowledged terminal stop survived Compact: %+v", stop)
	}
	// The live ASL lease is preserved: user_clear leaves LeaseLive set, so the
	// loop the tab keeps is a genuinely live one, not a resurrected marker.
	if exists, live := merged.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("live ASL lease not preserved through the clear merge: exists=%v live=%v", exists, live)
	}
	if eff := merged.Reducer.Reduce(now.Unix()); !eff.Looping {
		t.Fatalf("merged Reduce = %+v; want the live lease's loop preserved", eff)
	}

	// The late startup restore re-enters the merge and must not resurrect an icon.
	beforeCount := len(merged.Reducer.Facts())
	waitLateStartupRestore(t, client)
	h.stateMu.Lock()
	after := h.store[42]
	h.stateMu.Unlock()
	if got := len(after.Reducer.Facts()); got != beforeCount {
		t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
	}
	if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "" {
		t.Fatalf("late startup restore regained an icon: %+v", eff)
	}
}

// TestMaintenanceTick_MergeHonorsPreRestorePromptSupersession pins the prompt leg
// of F2-degraded-startup-merge-bypasses-window-supersession. A direct user prompt
// accepted in-process before the first restore acknowledges stuck terminal facts
// on every other source and retires older interactive active facts. The recovery
// merge must apply that supersession to the older persisted facts it imports so
// the window ends on the prompt's own ⚡ — while a concurrent pipe-mode child and
// a live ASL lease are preserved.
func TestMaintenanceTick_MergeHonorsPreRestorePromptSupersession(t *testing.T) {
	h := newTestHandler()
	now := time.Now()
	old := now.Add(-time.Minute).Unix()

	// Persisted durable state for window 42, all OLDER than the prompt: a terminal
	// stop on a different session, a stale interactive bolt on a third session, a
	// concurrent pipe-mode `claude -p` child, and a live between-turns ASL lease.
	r := NewReducer(ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s0"}, Action: "stop", Seq: 1, At: old})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s2"}, Action: "active", Seq: 1, At: old})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:p1"}, Action: "active", Seq: 1, At: old, PipeMode: true})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 1, At: old})
	windows := encodeMergeWindow(t, r, 1, now)

	client := bringUpDegradedOnGatedClient(t, h, windows, now)

	// A prompt lands FIRST, before any restore, on a fresh interactive session.
	seedInProcessReducerTransition(t, h, 42, protocol.ReducerEvent{
		Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "active",
		Prompt: true, SessionID: "s1", Seq: 5, At: now.Unix(),
	})

	h.maintenanceTick(now)
	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
	}
	h.stateMu.Lock()
	merged := h.store[42]
	h.stateMu.Unlock()
	if merged == nil || merged.Reducer == nil {
		t.Fatal("maintenance merge did not populate the window")
	}
	facts := merged.Reducer.Facts()

	// The prompt's own ⚡ wins: the older terminal stop was acknowledged (and
	// compacted away) and the stale interactive bolt was retired.
	if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "bolt" {
		t.Fatalf("merged Reduce = %+v; want the prompt's bolt", eff)
	}
	if stop := mergedFactBySource(facts, protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s0"}); stop != nil {
		t.Fatalf("prompt did not acknowledge the older terminal stop before merge: %+v", stop)
	}
	if stale := mergedFactBySource(facts, protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s2"}); stale == nil || !stale.Acked {
		t.Fatalf("stale interactive bolt not retired by the pre-restore prompt: %+v", stale)
	}
	// Concurrent protected state survives: the pipe-mode child keeps its bolt and
	// the live ASL lease keeps looping.
	if pipe := mergedFactBySource(facts, protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:p1"}); pipe == nil || pipe.Acked || !pipe.PipeMode {
		t.Fatalf("concurrent pipe-mode child was wrongly retired: %+v", pipe)
	}
	if exists, live := merged.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("live ASL lease not preserved through the prompt merge: exists=%v live=%v", exists, live)
	}
	if eff := merged.Reducer.Reduce(now.Unix()); !eff.Looping {
		t.Fatalf("merged Reduce = %+v; want the live lease's loop preserved", eff)
	}

	beforeCount := len(facts)
	waitLateStartupRestore(t, client)
	h.stateMu.Lock()
	after := h.store[42]
	h.stateMu.Unlock()
	if got := len(after.Reducer.Facts()); got != beforeCount {
		t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
	}
	if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "bolt" || !eff.Looping {
		t.Fatalf("late startup restore diverged: %+v; want bolt with a live lease", eff)
	}
}

// TestMaintenanceTick_MergeHonorsPreRestoreTerminalRetirement pins the terminal
// leg of F2-degraded-startup-merge-bypasses-window-supersession. A native
// interactive terminal (stop) accepted in-process before the first restore
// retires older interactive active facts so its 🛑 is not masked. The recovery
// merge must apply that retirement to the stale interactive bolt it imports so
// the terminal result surfaces (bolt outranks stop) rather than being masked.
func TestMaintenanceTick_MergeHonorsPreRestoreTerminalRetirement(t *testing.T) {
	h := newTestHandler()
	now := time.Now()
	old := now.Add(-time.Minute).Unix()

	// Persisted durable state for window 42: a stale interactive bolt on an older
	// session that, without retirement, would mask the current stop.
	r := NewReducer(ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s2"}, Action: "active", Seq: 1, At: old})
	windows := encodeMergeWindow(t, r, 1, now)

	client := bringUpDegradedOnGatedClient(t, h, windows, now)

	// A native interactive terminal lands FIRST, before any restore.
	seedInProcessReducerTransition(t, h, 42, protocol.ReducerEvent{
		Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "stop",
		SessionID: "s1", Seq: 5, At: now.Unix(),
	})

	h.maintenanceTick(now)
	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
	}
	h.stateMu.Lock()
	merged := h.store[42]
	h.stateMu.Unlock()
	if merged == nil || merged.Reducer == nil {
		t.Fatal("maintenance merge did not populate the window")
	}

	// The stale bolt was retired so the current session's stop surfaces.
	if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "stop" {
		t.Fatalf("merged Reduce = %+v; want stop (a merged stale interactive bolt masked the terminal)", eff)
	}
	if stale := mergedFactBySource(merged.Reducer.Facts(), protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s2"}); stale == nil || !stale.Acked {
		t.Fatalf("stale interactive bolt not retired by the pre-restore terminal: %+v", stale)
	}

	beforeCount := len(merged.Reducer.Facts())
	waitLateStartupRestore(t, client)
	h.stateMu.Lock()
	after := h.store[42]
	h.stateMu.Unlock()
	if got := len(after.Reducer.Facts()); got != beforeCount {
		t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
	}
	if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "stop" {
		t.Fatalf("late startup restore stopped surfacing the terminal: %+v", eff)
	}
}

// TestMaintenanceTick_MergeHonorsSameTimestampHigherPersistedSequence pins
// F2-degraded-merge-cross-source-sequence-tie. During degraded startup the
// fresh in-process reducer starts at low per-source sequences while the older
// persisted generation carries arbitrarily high unrelated ones. When the
// pre-restore transition and an older persisted fact share the same Unix
// second, the supersession replay must not read the persisted fact's higher
// cross-source sequence as "newer": the clear, prompt, and terminal cutoffs
// must still supersede it, while genuinely concurrent pipe-mode and live ASL
// lease contributions stay untouched and the late startup restore stays
// idempotent.
func TestMaintenanceTick_MergeHonorsSameTimestampHigherPersistedSequence(t *testing.T) {
	t.Run("clear", func(t *testing.T) {
		h := newTestHandler()
		now := time.Now()
		at := now.Unix()

		// Persisted durable state for window 42, SAME second as the clear but with
		// far higher per-source sequences: an interactive harness bolt, a terminal
		// probe stop, and a live between-turns ASL lease.
		r := NewReducer(ReducerConfig{})
		r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 500, At: at})
		r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceProbe, Key: "screen"}, Action: "stop", Seq: 600, At: at})
		r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 700, At: at})
		windows := encodeMergeWindow(t, r, 1, now)

		client := bringUpDegradedOnGatedClient(t, h, windows, now)

		// The /clear lands FIRST on the fresh reducer, at a LOW sequence — the
		// new in-process generation has just started counting.
		seedInProcessReducerTransition(t, h, 42, protocol.ReducerEvent{
			Source: userActionSource(), Action: "user_clear", Seq: 1, At: at,
		})

		h.maintenanceTick(now)
		if r2, _ := h.Readiness(); !r2 {
			t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
		}
		h.stateMu.Lock()
		merged := h.store[42]
		h.stateMu.Unlock()
		if merged == nil || merged.Reducer == nil {
			t.Fatal("maintenance merge did not populate the window")
		}

		// The clear supersedes the same-second persisted facts despite their
		// numerically higher unrelated sequences: no icon returns.
		if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "" {
			t.Fatalf("merged Reduce = %+v; want icon-less (a same-second higher-seq persisted fact escaped the clear)", eff)
		}
		if bolt := mergedFactBySource(merged.Reducer.Facts(), protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}); bolt == nil || !bolt.Acked {
			t.Fatalf("same-second higher-seq interactive bolt not acknowledged by the clear: %+v", bolt)
		}
		if stop := mergedFactBySource(merged.Reducer.Facts(), protocol.SourceID{Kind: protocol.SourceProbe, Key: "screen"}); stop != nil {
			t.Fatalf("acknowledged terminal stop survived Compact: %+v", stop)
		}
		// The live ASL lease is preserved: user_clear leaves LeaseLive set.
		if exists, live := merged.Reducer.LeaseState("run-1"); !exists || !live {
			t.Fatalf("live ASL lease not preserved through the clear merge: exists=%v live=%v", exists, live)
		}
		if eff := merged.Reducer.Reduce(now.Unix()); !eff.Looping {
			t.Fatalf("merged Reduce = %+v; want the live lease's loop preserved", eff)
		}

		beforeCount := len(merged.Reducer.Facts())
		waitLateStartupRestore(t, client)
		h.stateMu.Lock()
		after := h.store[42]
		h.stateMu.Unlock()
		if got := len(after.Reducer.Facts()); got != beforeCount {
			t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
		}
		if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "" {
			t.Fatalf("late startup restore regained an icon: %+v", eff)
		}
	})

	t.Run("prompt", func(t *testing.T) {
		h := newTestHandler()
		now := time.Now()
		at := now.Unix()

		// Persisted durable state for window 42, SAME second as the prompt but with
		// far higher per-source sequences: a terminal stop, a stale interactive
		// bolt, a concurrent pipe-mode child, and a live between-turns ASL lease.
		r := NewReducer(ReducerConfig{})
		r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s0"}, Action: "stop", Seq: 500, At: at})
		r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s2"}, Action: "active", Seq: 600, At: at})
		r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:p1"}, Action: "active", Seq: 700, At: at, PipeMode: true})
		r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 800, At: at})
		windows := encodeMergeWindow(t, r, 1, now)

		client := bringUpDegradedOnGatedClient(t, h, windows, now)

		// The prompt lands FIRST, on a fresh interactive session at a LOW sequence.
		seedInProcessReducerTransition(t, h, 42, protocol.ReducerEvent{
			Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "active",
			Prompt: true, SessionID: "s1", Seq: 1, At: at,
		})

		h.maintenanceTick(now)
		if r2, _ := h.Readiness(); !r2 {
			t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
		}
		h.stateMu.Lock()
		merged := h.store[42]
		h.stateMu.Unlock()
		if merged == nil || merged.Reducer == nil {
			t.Fatal("maintenance merge did not populate the window")
		}
		facts := merged.Reducer.Facts()

		// The prompt's own ⚡ wins despite the persisted facts' higher sequences.
		if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "bolt" {
			t.Fatalf("merged Reduce = %+v; want the prompt's bolt", eff)
		}
		if stop := mergedFactBySource(facts, protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s0"}); stop != nil {
			t.Fatalf("prompt did not acknowledge the same-second higher-seq terminal stop: %+v", stop)
		}
		if stale := mergedFactBySource(facts, protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s2"}); stale == nil || !stale.Acked {
			t.Fatalf("same-second higher-seq stale interactive bolt not retired by the prompt: %+v", stale)
		}
		// Concurrent protected state survives: the pipe-mode child keeps its bolt
		// and the live ASL lease keeps looping.
		if pipe := mergedFactBySource(facts, protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:p1"}); pipe == nil || pipe.Acked || !pipe.PipeMode {
			t.Fatalf("concurrent pipe-mode child was wrongly retired: %+v", pipe)
		}
		if exists, live := merged.Reducer.LeaseState("run-1"); !exists || !live {
			t.Fatalf("live ASL lease not preserved through the prompt merge: exists=%v live=%v", exists, live)
		}
		if eff := merged.Reducer.Reduce(now.Unix()); !eff.Looping {
			t.Fatalf("merged Reduce = %+v; want the live lease's loop preserved", eff)
		}

		beforeCount := len(facts)
		waitLateStartupRestore(t, client)
		h.stateMu.Lock()
		after := h.store[42]
		h.stateMu.Unlock()
		if got := len(after.Reducer.Facts()); got != beforeCount {
			t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
		}
		if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "bolt" || !eff.Looping {
			t.Fatalf("late startup restore diverged: %+v; want bolt with a live lease", eff)
		}
	})

	t.Run("terminal", func(t *testing.T) {
		h := newTestHandler()
		now := time.Now()
		at := now.Unix()

		// Persisted durable state for window 42: a stale interactive bolt in the
		// SAME second as the terminal but with a far higher per-source sequence.
		r := NewReducer(ReducerConfig{})
		r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s2"}, Action: "active", Seq: 500, At: at})
		windows := encodeMergeWindow(t, r, 1, now)

		client := bringUpDegradedOnGatedClient(t, h, windows, now)

		// A native interactive terminal lands FIRST, at a LOW sequence.
		seedInProcessReducerTransition(t, h, 42, protocol.ReducerEvent{
			Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "stop",
			SessionID: "s1", Seq: 1, At: at,
		})

		h.maintenanceTick(now)
		if r2, _ := h.Readiness(); !r2 {
			t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
		}
		h.stateMu.Lock()
		merged := h.store[42]
		h.stateMu.Unlock()
		if merged == nil || merged.Reducer == nil {
			t.Fatal("maintenance merge did not populate the window")
		}

		// The stale bolt was retired despite its higher unrelated sequence, so the
		// current session's stop surfaces (bolt would otherwise outrank stop).
		if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "stop" {
			t.Fatalf("merged Reduce = %+v; want stop (a same-second higher-seq stale bolt masked the terminal)", eff)
		}
		if stale := mergedFactBySource(merged.Reducer.Facts(), protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s2"}); stale == nil || !stale.Acked {
			t.Fatalf("same-second higher-seq stale interactive bolt not retired by the terminal: %+v", stale)
		}

		beforeCount := len(merged.Reducer.Facts())
		waitLateStartupRestore(t, client)
		h.stateMu.Lock()
		after := h.store[42]
		h.stateMu.Unlock()
		if got := len(after.Reducer.Facts()); got != beforeCount {
			t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
		}
		if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "stop" {
			t.Fatalf("late startup restore stopped surfacing the terminal: %+v", eff)
		}
	})
}
