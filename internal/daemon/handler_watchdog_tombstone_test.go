package daemon

// Fake-clock tests for the independent watchdog loop: expiry driven by
// accumulated in-process elapsed time, sleep-gap handling, per-fact expiry,
// ASL lease protection, delayed-render independence, and the closed-window
// tombstone lifecycle. Ticks are driven by calling watchdogTick directly with
// fabricated (now, elapsed) pairs — no ambient sleeps, no ticker goroutine.

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestWatchdogTick_DelayedRenderDoesNotDelayExpiry: expiry is evaluated and
// recorded in daemon state while a Kitty title write is wedged mid-render —
// the watchdog loop never waits on rendering.
func TestWatchdogTick_DelayedRenderDoesNotDelayExpiry(t *testing.T) {
	stubLoopDiscovery(t)
	guard := activity.NewStateGuard(time.Hour)
	h := NewHandler(guard, nil)
	fake := &fakeKittyClient{}
	base := time.Now()
	// The live tab shows no icon while the state wants bolt, so the render
	// pass must write the title — and wedge on it.
	fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "", false))
	h.SetKittyClient(fake)
	h.SetStaleThresholds(map[string]time.Duration{"active": 5 * time.Second}, nil)

	block := make(chan struct{})
	entered := make(chan struct{})
	var enteredOnce sync.Once
	fake.mu.Lock()
	fake.beforeSetTitle = func(string, string) {
		enteredOnce.Do(func() { close(entered) })
		<-block
	}
	fake.mu.Unlock()

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.LastAction = "active"
	st.LastSessionTitle = "W:api"
	st.Activity = base
	st.Reducer = state.NewReducer(h.reducerCfg)
	st.Reducer.Apply(protocol.ReducerEvent{
		Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "main"},
		Action: "active", Seq: 1, At: base.Unix(),
	})
	h.publishEffectiveLocked(42, st, st.Reducer.Reduce(base.Unix()))
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()

	// Wedge a synchronous render on the blocked title write.
	renderDone := make(chan struct{})
	go func() {
		windows, _ := fake.ListWindows()
		h.renderSnapshot(windows)
		close(renderDone)
	}()
	<-entered

	// The render worker is stuck inside SetTabTitle; the watchdog still
	// expires on schedule.
	now := base
	for i := 0; i < 5; i++ {
		now = now.Add(watchdogInterval)
		h.watchdogTick(now, watchdogInterval)
	}
	if got := effectiveState(h, 42).Icon; got != "" {
		t.Fatalf("icon after threshold with wedged render = %q; want empty", got)
	}
	h.stateMu.Lock()
	lastAction := h.store[42].LastAction
	h.stateMu.Unlock()
	if lastAction != "" {
		t.Fatalf("lastAction = %q; want cleared despite wedged render", lastAction)
	}

	close(block)
	<-renderDone
}

// TestSweepHookSilence_SleepGapDoesNotFire: an account observed fresh before a
// sleep gap does not trip the hook-silence notification when the wall clock
// jumps past the threshold.
func TestSweepHookSilence_SleepGapDoesNotFire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stubLoopDiscovery(t)
		h, fake, rec := newHookSilenceTestHandler(4*time.Hour, nil)
		base := time.Now()
		windows := []kitty.OSWindow{{
			Tabs: []kitty.Tab{{
				Windows: []kitty.Window{{ID: 42, UserVars: kitty.UserVars{Account: "work"}}},
			}},
		}}
		fake.mu.Lock()
		fake.windows = windows
		fake.mu.Unlock()
		h.publishSnapshot(windows, base)

		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = base
		h.stateMu.Unlock()

		// One normal tick arms the accumulator, then the machine sleeps eight
		// hours.
		now := base.Add(watchdogInterval)
		h.watchdogTick(now, watchdogInterval)
		now = now.Add(8 * time.Hour)
		h.watchdogTick(now, 8*time.Hour)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches after sleep gap = %d; want 0", got)
		}
		h.stateMu.Lock()
		seen := h.lastSeenEventAt["work"]
		h.stateMu.Unlock()
		if !seen.Equal(base) {
			t.Fatalf("lastSeenEventAt rewritten to %v; want untouched %v", seen, base)
		}
	})
}

// TestPruneClosedWindows_CreatesTombstoneAndPurgesLiveState: closing a window
// cancels its queued render, removes socket/live state and ASL miss streaks,
// and retains its durable metadata behind a tombstone stamped WindowClosed.
func TestPruneClosedWindows_CreatesTombstoneAndPurgesLiveState(t *testing.T) {
	h, _, _ := newSweepTestHandler(10 * time.Minute)
	now := time.Now()

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.LastSessionTitle = "W:api"
	h.touchWindowMetadataLocked(protocol.Request{WindowID: 42, SessionTitle: "W:api", Account: "work"})
	h.kittySockets[42] = "unix:/tmp/kitty-1"
	ref := aslLeaseRef{windowID: 42, key: "run-1"}
	h.aslMissStreak[ref] = 1
	h.aslSupervisorTargets[ref] = aslSupervisorTarget{
		windowID: 42,
		candidate: agentloop.Candidate{
			RunID:    "run-1",
			StateDir: t.TempDir(),
		},
	}
	h.aslSupervisorClosureCutoffs[ref] = 8
	h.enqueueRenderLocked(42, st)
	h.stateMu.Unlock()

	h.pruneClosedWindows(now, map[int]bool{})

	h.stateMu.Lock()
	_, inStore := h.store[42]
	_, hasSocket := h.kittySockets[42]
	streaks := len(h.aslMissStreak)
	supervisorTargets := len(h.aslSupervisorTargets)
	replyCutoffs := len(h.aslSupervisorClosureCutoffs)
	meta, hasMeta := h.windowMetadata[42]
	_, hasTombstone := h.windowTombstones[42]
	h.stateMu.Unlock()

	if inStore {
		t.Fatalf("store entry survived prune")
	}
	if hasSocket {
		t.Fatalf("kitty socket survived prune")
	}
	if streaks != 0 {
		t.Fatalf("ASL miss streaks = %d; want 0", streaks)
	}
	if supervisorTargets != 0 || replyCutoffs != 0 {
		t.Fatalf(
			"ASL supervisor state survived prune: targets=%d cutoffs=%d",
			supervisorTargets,
			replyCutoffs,
		)
	}
	if !h.renderQueue.Empty() {
		t.Fatalf("queued render for closed window not cancelled")
	}
	if !hasMeta {
		t.Fatalf("durable metadata dropped at close; want retained behind tombstone")
	}
	if meta.Durable.SessionTitle != "W:api" {
		t.Fatalf("durable session title = %q; want W:api", meta.Durable.SessionTitle)
	}
	if !hasTombstone {
		t.Fatalf("no tombstone recorded for closed window")
	}
	if meta.Watchdog.LastEvent != eventWindowClosed || meta.Watchdog.ClosedAt != now.Unix() {
		t.Fatalf("watchdog metadata = %+v; want %s at %d", meta.Watchdog, eventWindowClosed, now.Unix())
	}
}

// TestTombstone_WindowReuseRevives: a tombstoned ID that comes back to life —
// via a fresh event creating state, or Kitty reporting it live again — stops
// being a tombstone and resumes live diagnostics.
func TestTombstone_WindowReuseRevives(t *testing.T) {
	h, _, _ := newSweepTestHandler(10 * time.Minute)
	now := time.Now()

	h.stateMu.Lock()
	h.ensureWindowStateLocked(42)
	h.stateMu.Unlock()
	h.pruneClosedWindows(now, map[int]bool{})

	// A fresh event re-creates the window state: the tombstone is gone.
	h.stateMu.Lock()
	h.ensureWindowStateLocked(42)
	_, hasTombstone := h.windowTombstones[42]
	closedAt := h.windowMetadata[42].Watchdog.ClosedAt
	h.stateMu.Unlock()
	if hasTombstone {
		t.Fatalf("tombstone survived window reuse via new event")
	}
	if closedAt != 0 {
		t.Fatalf("ClosedAt = %d after reuse; want 0", closedAt)
	}

	// Close again, then have Kitty report the ID live: prune revives it.
	h.pruneClosedWindows(now, map[int]bool{})
	h.stateMu.Lock()
	_, hasTombstone = h.windowTombstones[42]
	h.stateMu.Unlock()
	if !hasTombstone {
		t.Fatalf("second close did not tombstone")
	}
	h.pruneClosedWindows(now, map[int]bool{42: true})
	h.stateMu.Lock()
	_, hasTombstone = h.windowTombstones[42]
	closedAt = h.windowMetadata[42].Watchdog.ClosedAt
	h.stateMu.Unlock()
	if hasTombstone || closedAt != 0 {
		t.Fatalf("tombstone survived Kitty reporting the window live again (tombstone=%t closedAt=%d)", hasTombstone, closedAt)
	}
}

// TestTombstone_RetentionExpiryDropsMetadata: a tombstone (and the durable
// metadata it retains) is dropped after the 24-hour retention.
func TestTombstone_RetentionExpiryDropsMetadata(t *testing.T) {
	h, _, _ := newSweepTestHandler(10 * time.Minute)
	now := time.Now()

	h.stateMu.Lock()
	h.ensureWindowStateLocked(42)
	h.stateMu.Unlock()
	h.pruneClosedWindows(now, map[int]bool{})

	// Just under retention: still exported.
	h.pruneClosedWindows(now.Add(windowTombstoneRetention-time.Minute), map[int]bool{})
	h.stateMu.Lock()
	_, hasMeta := h.windowMetadata[42]
	h.stateMu.Unlock()
	if !hasMeta {
		t.Fatalf("metadata dropped before retention elapsed")
	}

	h.pruneClosedWindows(now.Add(windowTombstoneRetention), map[int]bool{})
	h.stateMu.Lock()
	_, hasMeta = h.windowMetadata[42]
	_, hasTombstone := h.windowTombstones[42]
	order := len(h.tombstoneOrder)
	h.stateMu.Unlock()
	if hasMeta || hasTombstone || order != 0 {
		t.Fatalf("tombstone not fully expired after retention (meta=%t tombstone=%t order=%d)", hasMeta, hasTombstone, order)
	}
}

// TestTombstone_CountBoundEvictsOldest: the tombstone set is bounded at
// maxWindowTombstones entries; overflow evicts the oldest together with their
// metadata.
func TestTombstone_CountBoundEvictsOldest(t *testing.T) {
	h, _, _ := newSweepTestHandler(10 * time.Minute)
	now := time.Now()

	// Close one window first so it is deterministically the oldest tombstone.
	h.stateMu.Lock()
	h.ensureWindowStateLocked(1)
	h.stateMu.Unlock()
	h.pruneClosedWindows(now, map[int]bool{})

	h.stateMu.Lock()
	for id := 2; id <= maxWindowTombstones+1; id++ {
		h.ensureWindowStateLocked(id)
	}
	h.stateMu.Unlock()
	h.pruneClosedWindows(now.Add(time.Second), map[int]bool{})

	h.stateMu.Lock()
	tombstones := len(h.windowTombstones)
	metas := len(h.windowMetadata)
	_, oldestSurvives := h.windowTombstones[1]
	h.stateMu.Unlock()
	if tombstones != maxWindowTombstones {
		t.Fatalf("tombstones = %d; want bounded at %d", tombstones, maxWindowTombstones)
	}
	if metas != maxWindowTombstones {
		t.Fatalf("metadata entries = %d; want bounded at %d", metas, maxWindowTombstones)
	}
	if oldestSurvives {
		t.Fatalf("oldest tombstone survived the count bound; want evicted first")
	}
}
