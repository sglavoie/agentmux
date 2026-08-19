package daemon

// Fake-clock tests for the independent watchdog loop: expiry driven by
// accumulated in-process elapsed time, sleep-gap handling, per-fact expiry,
// ASL lease protection, delayed-render independence, and the closed-window
// tombstone lifecycle. Ticks are driven by calling watchdogTick directly with
// fabricated (now, elapsed) pairs — no ambient sleeps, no ticker goroutine.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/palette"
)

// stubLoopDiscovery replaces on-disk ASL discovery for the duration of the
// test so a post-gap reconciliation cannot scan the developer's real machine.
// It reports whether discovery was invoked.
func stubLoopDiscovery(t *testing.T) *bool {
	t.Helper()
	called := false
	old := daemonLoopDiscover
	daemonLoopDiscover = func(agentloop.DiscoverOptions) ([]agentloop.Candidate, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() { daemonLoopDiscover = old })
	return &called
}

// seedReducerFact installs a reducer fact on window 42 under stateMu and
// publishes the initial reduction.
func seedReducerFact(h *Handler, windowID int, now time.Time, events ...ReducerEvent) {
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(windowID)
	if st.Reducer == nil {
		st.Reducer = NewReducer(h.reducerCfg)
	}
	for _, ev := range events {
		st.Reducer.Apply(ev)
	}
	h.publishEffectiveLocked(windowID, st, st.Reducer.Reduce(now.Unix()))
	h.stateMu.Unlock()
}

func effectiveState(h *Handler, windowID int) EffectiveState {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.store[windowID].Effective
}

func windowEventNames(h *Handler, windowID int) []string {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	var names []string
	if st, ok := h.store[windowID]; ok {
		for _, ev := range st.EventLog {
			names = append(names, ev.Event)
		}
	}
	return names
}

func containsEvent(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// TestWatchdogTick_ExpiresOnAccumulatedIdle drives the 1-second watchdog with
// a fake clock: a bolt fact (and the legacy scalar) stays put until the
// accumulated in-process idle time reaches the threshold, then the fact
// expires, the scalar clears, and both synthetic events land in the ring.
func TestWatchdogTick_ExpiresOnAccumulatedIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stubLoopDiscovery(t)
		h, fake, rec := newSweepTestHandlerWithMap(map[string]time.Duration{"active": 5 * time.Second}, nil)
		base := time.Now()
		fake.mu.Lock()
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))
		fake.mu.Unlock()
		h.publishSnapshot(fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false)), base)

		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.LastAction = "active"
		st.LastSessionTitle = "W:api"
		st.Activity = base
		h.stateMu.Unlock()
		seedReducerFact(h, 42, base, ReducerEvent{
			Source: SourceID{Kind: SourceHarness, Key: "main"},
			Action: "active", Seq: 1, At: base.Unix(),
		})

		now := base
		for i := 0; i < 4; i++ {
			now = now.Add(watchdogInterval)
			h.watchdogTick(now, watchdogInterval)
		}
		synctest.Wait()
		if got := effectiveState(h, 42).Icon; got != "bolt" {
			t.Fatalf("icon after 4s accumulated idle = %q; want bolt", got)
		}
		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches before threshold = %d; want 0", got)
		}

		now = now.Add(watchdogInterval)
		h.watchdogTick(now, watchdogInterval)
		synctest.Wait()

		if got := effectiveState(h, 42).Icon; got != "" {
			t.Fatalf("icon after threshold = %q; want empty", got)
		}
		h.stateMu.Lock()
		lastAction := h.store[42].LastAction
		notified := h.store[42].NotifiedStale
		meta := h.windowMetadata[42]
		h.stateMu.Unlock()
		if lastAction != "" {
			t.Fatalf("lastAction = %q; want cleared", lastAction)
		}
		if !notified {
			t.Fatalf("notifiedStale = false; want true")
		}
		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1", got)
		}
		names := windowEventNames(h, 42)
		if !containsEvent(names, eventSourceExpired) || !containsEvent(names, eventWatchdogStale) {
			t.Fatalf("event log = %v; want both %s and %s", names, eventSourceExpired, eventWatchdogStale)
		}
		if meta.Watchdog.SourceExpiries != 1 || meta.Watchdog.StaleSweeps != 1 {
			t.Fatalf("durable watchdog counters = %+v; want one expiry and one sweep", meta.Watchdog)
		}
	})
}

// TestSweepStale_CompactsAcknowledgedFactsWithoutStaleThresholds pins the
// fixed retention policy to the periodic watchdog. An otherwise-idle window
// must drop an acknowledged cutoff when it crosses one hour even when the user
// disables every configurable active/subagent/compact stale threshold.
func TestSweepStale_CompactsAcknowledgedFactsWithoutStaleThresholds(t *testing.T) {
	h, _, _ := newSweepTestHandler(0)
	base := time.Unix(1_700_000_000, 0)

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.Reducer = NewReducer(h.reducerCfg)
	st.Reducer.Restore([]SourceFactSnapshot{{
		Kind: SourceHarness, Key: "codex:old", Action: "active", Icon: "bolt",
		Seq: 7, CausalOrder: 7, At: base.Unix(), Acked: true, AckedAt: base.Unix(),
	}})
	st.ShadowSeq = 7
	h.publishEffectiveLocked(42, st, st.Reducer.Reduce(base.Unix()))
	st.Dirty = false
	st.DirtySince = time.Time{}
	h.stateMu.Unlock()

	changed := h.SweepStaleWithSnapshot(base.Add(time.Duration(acknowledgedFactRetentionSeconds)*time.Second), nil)
	if !changed {
		t.Fatal("retention-only watchdog pass reported no change")
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st = h.store[42]
	if facts := st.Reducer.Facts(); len(facts) != 0 {
		t.Fatalf("facts after retention boundary = %+v; want none", facts)
	}
	if st.ShadowSeq != 8 {
		t.Fatalf("ShadowSeq = %d; want 8 after retention mutation", st.ShadowSeq)
	}
	if !st.Dirty || st.DirtySince.IsZero() {
		t.Fatalf("retention mutation was not queued for persistence: dirty=%t since=%v", st.Dirty, st.DirtySince)
	}
}

// TestWatchdogTick_SleepGapDoesNotExpire pins the sleep-like-gap contract: a
// tick arriving hours late credits only one interval toward idleness, never
// rewrites activity timestamps, and refreshes the Kitty snapshot and the ASL
// reconciliation before judging expiry.
func TestWatchdogTick_SleepGapDoesNotExpire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		discovered := stubLoopDiscovery(t)
		h, fake, rec := newSweepTestHandlerWithMap(map[string]time.Duration{"active": 5 * time.Second}, nil)
		base := time.Now()
		fake.mu.Lock()
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))
		fake.mu.Unlock()
		h.publishSnapshot(fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false)), base)

		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.LastAction = "active"
		st.LastSessionTitle = "W:api"
		st.Activity = base
		h.stateMu.Unlock()
		seedReducerFact(h, 42, base, ReducerEvent{
			Source: SourceID{Kind: SourceHarness, Key: "main"},
			Action: "active", Seq: 1, At: base.Unix(),
		})

		now := base
		for i := 0; i < 2; i++ {
			now = now.Add(watchdogInterval)
			h.watchdogTick(now, watchdogInterval)
		}

		// The machine sleeps two hours: one tick arrives with a huge elapsed
		// value and a wall clock far past every threshold.
		lists := fake.listCallCount()
		now = now.Add(2 * time.Hour)
		h.watchdogTick(now, 2*time.Hour)
		synctest.Wait()

		if got := effectiveState(h, 42).Icon; got != "bolt" {
			t.Fatalf("icon after sleep gap = %q; want bolt (gap must not count as idle)", got)
		}
		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches after sleep gap = %d; want 0", got)
		}
		if got := fake.listCallCount(); got != lists+1 {
			t.Fatalf("post-gap ListWindows calls = %d; want %d (evidence refresh)", got, lists+1)
		}
		if !*discovered {
			t.Fatalf("post-gap tick did not force ASL reconciliation")
		}
		h.stateMu.Lock()
		activity := h.store[42].Activity
		h.stateMu.Unlock()
		if !activity.Equal(base) {
			t.Fatalf("activity timestamp rewritten to %v; want untouched %v", activity, base)
		}

		// Only genuinely observed in-process time expires it: 2s before the
		// gap + 1s gap credit = 3s, so two more 1s ticks reach the 5s
		// threshold.
		for i := 0; i < 2; i++ {
			now = now.Add(watchdogInterval)
			h.watchdogTick(now, watchdogInterval)
		}
		synctest.Wait()
		if got := effectiveState(h, 42).Icon; got != "" {
			t.Fatalf("icon after post-gap accumulation = %q; want empty", got)
		}
	})
}

// TestWatchdogStale_ASLExecutingLeaseProtectsBolt: while an ASL run holds a
// fresh turn/verification lease, another source's stale bolt is not cleared —
// heartbeats prove work is still running even if the harness emits no hooks.
// Once the lease's own fact goes stale it expires too (losing ⚡ but keeping
// 🔁), and the protection lapses with it.
func TestWatchdogStale_ASLExecutingLeaseProtectsBolt(t *testing.T) {
	stubLoopDiscovery(t)
	h, fake, _ := newSweepTestHandlerWithMap(map[string]time.Duration{"active": 5 * time.Second}, nil)
	base := time.Now()
	fake.mu.Lock()
	fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))
	fake.mu.Unlock()
	h.publishSnapshot(fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false)), base)

	seedReducerFact(h, 42, base,
		ReducerEvent{
			Source: SourceID{Kind: SourceASL, Key: "run-1"},
			Seq:    1, At: base.Unix(), RunID: "run-1", Phase: ASLPhaseTurn,
		},
		ReducerEvent{
			Source: SourceID{Kind: SourceHarness, Key: "main"},
			Action: "active", Seq: 1, At: base.Add(-time.Hour).Unix(),
		},
	)

	// First tick: the harness bolt is hours past threshold, but the turn
	// lease is 1s old — the bolt survives.
	now := base.Add(watchdogInterval)
	h.watchdogTick(now, watchdogInterval)
	if got := effectiveState(h, 42); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("state under fresh turn lease = %+v; want bolt + looping", got)
	}
	if names := windowEventNames(h, 42); containsEvent(names, eventSourceExpired) {
		t.Fatalf("bolt expired under a fresh ASL turn lease: %v", names)
	}

	// Tick until the ASL turn fact itself exceeds the threshold (heartbeats
	// have stopped): the lease fact expires and the protection lapses, so
	// both facts ack; the loop marker survives the expiry.
	for i := 0; i < 5; i++ {
		now = now.Add(watchdogInterval)
		h.watchdogTick(now, watchdogInterval)
	}
	got := effectiveState(h, 42)
	if got.Icon != "" {
		t.Fatalf("icon after lease went stale = %q; want empty", got.Icon)
	}
	if !got.Looping {
		t.Fatalf("looping lost after watchdog expiry; the lease must survive until finish/reconciliation")
	}
	h.stateMu.Lock()
	facts := h.store[42].Reducer.Facts()
	h.stateMu.Unlock()
	for _, fact := range facts {
		if !fact.Acked {
			t.Fatalf("fact %s:%s not acked after expiry", fact.Kind, fact.Key)
		}
		if fact.Kind == SourceASL && !fact.LeaseLive {
			t.Fatalf("ASL lease released by watchdog expiry; want live")
		}
	}
}

// TestSweepStale_ExpiresIndividualFacts: one stale source fact expires on its
// own threshold while a fresh fact keeps the tab. No whole-window clear, no
// notification.
func TestSweepStale_ExpiresIndividualFacts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, _, rec := newSweepTestHandlerWithMap(map[string]time.Duration{
			"active":   10 * time.Minute,
			"subagent": 30 * time.Minute,
		}, nil)
		now := time.Now()

		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.LastAction = "subagent"
		st.LastSessionTitle = "W:api"
		st.Activity = now
		h.stateMu.Unlock()
		seedReducerFact(h, 42, now,
			ReducerEvent{
				Source: SourceID{Kind: SourceHarness, Key: "main"},
				Action: "active", Seq: 1, At: now.Add(-15 * time.Minute).Unix(),
			},
			ReducerEvent{
				Source: SourceID{Kind: SourceSubagent, Key: "agent-1"},
				Action: "subagent", Seq: 1, At: now.Unix(),
			},
		)

		h.SweepStaleWithSnapshot(now, nil)
		synctest.Wait()

		got := effectiveState(h, 42)
		if got.Icon != "subagent" {
			t.Fatalf("icon = %q; want subagent (fresh fact keeps the tab)", got.Icon)
		}
		h.stateMu.Lock()
		lastAction := h.store[42].LastAction
		h.stateMu.Unlock()
		if lastAction != "subagent" {
			t.Fatalf("lastAction = %q; want subagent (no whole-window clear)", lastAction)
		}
		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (per-fact expiry never notifies)", got)
		}
		names := windowEventNames(h, 42)
		if !containsEvent(names, eventSourceExpired) {
			t.Fatalf("event log = %v; want %s for the stale bolt", names, eventSourceExpired)
		}
		if containsEvent(names, eventWatchdogStale) {
			t.Fatalf("event log = %v; whole-window %s must not fire", names, eventWatchdogStale)
		}
	})
}

// TestSweepStale_StampsShadowMetadata pins that watchdog-driven publishes
// stamp the shadow export metadata alongside the renderer scalars. The
// 30-minute monitored audit caught the miss live: a watchdog expiry updated
// DesiredIcon through publishEffectiveLocked but left Shadow.EffectiveIcon at
// the last event-stamped value, so the window reported a persistent
// shadow_current_mismatch until its next real event.
func TestSweepStale_StampsShadowMetadata(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, _, _ := newSweepTestHandlerWithMap(map[string]time.Duration{
			"active": 10 * time.Minute,
		}, nil)
		now := time.Now()

		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.LastAction = "active"
		st.LastSessionTitle = "W:api"
		st.Activity = now.Add(-15 * time.Minute)
		h.stateMu.Unlock()
		seedReducerFact(h, 42, now,
			ReducerEvent{
				Source: SourceID{Kind: SourceHarness, Key: "main"},
				Action: "active", Seq: 1, At: now.Add(-15 * time.Minute).Unix(),
			},
		)

		h.SweepStaleWithSnapshot(now.Add(time.Minute), nil)
		synctest.Wait()

		h.stateMu.Lock()
		desired := desiredIconForState(h.store[42])
		meta := h.windowMetadata[42]
		h.stateMu.Unlock()
		if meta.Shadow.EffectiveIcon != desired {
			t.Fatalf("shadow effective icon %q != renderer desired %q after sweep (shadow_current_mismatch)",
				meta.Shadow.EffectiveIcon, desired)
		}
		if meta.Shadow.EffectiveGen == 0 {
			t.Fatalf("shadow effective generation not stamped by sweep publish")
		}
	})
}
