package daemon

// Snapshot freshness across every daemon publication path. The rule under test
// is one sentence: a snapshot is dated by the instant its ListWindows COMPLETED,
// the loop that asked for the read keeps its own tick or start time, and a
// failed read publishes nothing at all.
//
// The per-socket coordination that makes those five paths share one subprocess
// is exercised in snapshot_coordination_test.go.

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// listDelay is how long each publication path's ListWindows is made to block.
// It only has to be long enough that a capture time stamped before the call
// (the bug this pins) is unambiguously distinguishable from one stamped after.
const listDelay = 30 * time.Millisecond

// snapshotPublisher is one daemon path that lists Kitty and publishes the
// result. drive receives the tick/start time the production loop would pass and
// must run the path to completion.
type snapshotPublisher struct {
	name  string
	setup func(t *testing.T, h *Handler, fake *fakeKittyClient)
	drive func(h *Handler, tick time.Time)
}

func snapshotPublishers() []snapshotPublisher {
	markDirty := func(_ *testing.T, h *Handler, _ *fakeKittyClient) {
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.DesiredIcon = "bolt"
		st.StateSet = true
		h.markWindowDirtyLocked(42)
		h.stateMu.Unlock()
	}
	return []snapshotPublisher{
		{
			name:  "bounded startup restore",
			drive: func(h *Handler, tick time.Time) { h.restoreStateFromKittyBounded(tick, 5*time.Second) },
		},
		{
			name: "maintenance tick",
			setup: func(_ *testing.T, h *Handler, _ *fakeKittyClient) {
				h.MarkReady()
			},
			drive: func(h *Handler, tick time.Time) { h.maintenanceTick(tick) },
		},
		{
			name:  "render queue drain",
			setup: markDirty,
			drive: func(h *Handler, _ time.Time) { h.drainRenderQueue() },
		},
		{
			name:  "snapshot prime",
			drive: func(h *Handler, _ time.Time) { h.primeSnapshot() },
		},
		{
			name: "post-gap watchdog refresh",
			drive: func(h *Handler, tick time.Time) {
				h.watchdogTick(tick, watchdogGapThreshold+time.Second)
			},
		},
	}
}

// newFreshnessHandler wires a handler to a fake Kitty whose ListWindows takes
// listDelay to answer, without priming the cache.
func newFreshnessHandler(t *testing.T) (*Handler, *fakeKittyClient) {
	t.Helper()
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(42, "W:api")}
	fake.beforeListWindows = func() { time.Sleep(listDelay) }
	h.useKittyClient(fake)
	return h, fake
}

// TestSnapshotCaptureTimeIsStampedAtListCompletion pins the stamping rule on
// every publication path. Each path is handed a tick/start time that is already
// old when the list begins; the published capture time must instead be later
// than the moment the slow list actually returned.
func TestSnapshotCaptureTimeIsStampedAtListCompletion(t *testing.T) {
	for _, publisher := range snapshotPublishers() {
		t.Run(publisher.name, func(t *testing.T) {
			h, fake := newFreshnessHandler(t)
			if publisher.setup != nil {
				publisher.setup(t, h, fake)
			}

			// The tick time the loop carries is deliberately stale: production
			// ticks are minted before the list is issued, and a wedged Kitty can
			// separate the two by seconds.
			tick := time.Now().Add(-time.Hour)
			before := time.Now()
			publisher.drive(h, tick)
			after := time.Now()

			view := h.cachedSnapshotView()
			if !view.Ready() {
				t.Fatal("path published no snapshot")
			}
			if got := view.CapturedAt(); !got.After(before.Add(listDelay)) {
				t.Fatalf("capture time %v is not after the list completion (%v + %v); it was stamped before the read",
					got, before, listDelay)
			}
			if got := view.CapturedAt(); got.After(after) {
				t.Fatalf("capture time %v is after the path returned (%v)", got, after)
			}
			if age := view.Age(after); age > after.Sub(before) {
				t.Fatalf("age %v exceeds the whole call (%v); the tick time leaked into the stamp",
					age, after.Sub(before))
			}
		})
	}
}

// TestMaintenanceKeepsTickTimeSeparateFromCaptureTime pins the second half of
// the rule: publication uses the read's completion time while pruning keeps the
// tick time it was scheduled for. Folding the two together would date a
// window-closed tombstone by however long Kitty took to answer.
func TestMaintenanceKeepsTickTimeSeparateFromCaptureTime(t *testing.T) {
	h, _ := newFreshnessHandler(t)
	h.MarkReady()

	// Window 7 is tracked in-process but absent from the Kitty snapshot, so the
	// tick prunes it and records a tombstone stamped with the tick time.
	h.stateMu.Lock()
	h.ensureWindowStateLocked(7)
	h.stateMu.Unlock()

	tick := time.Now().Add(-time.Hour)
	h.maintenanceTick(tick)

	h.stateMu.Lock()
	tombstone, pruned := h.windowTombstones[7]
	h.stateMu.Unlock()
	if !pruned {
		t.Fatal("maintenance did not prune the window missing from the snapshot")
	}
	if !tombstone.Equal(tick) {
		t.Fatalf("tombstone stamped %v; want the tick time %v — pruning must not inherit the list's completion time",
			tombstone, tick)
	}
	if capturedAt := h.cachedSnapshotView().CapturedAt(); !capturedAt.After(tick) {
		t.Fatalf("capture time %v is not after the tick time %v; publication must not inherit the tick",
			capturedAt, tick)
	}
}

// TestFailedListPreservesLastGoodSnapshot walks every publication path with a
// failing Kitty and proves none of them disturbs the last successful windows,
// generation, or capture time.
func TestFailedListPreservesLastGoodSnapshot(t *testing.T) {
	for _, publisher := range snapshotPublishers() {
		t.Run(publisher.name, func(t *testing.T) {
			h := newTestHandler()
			fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(42, "W:api")}
			h.SetKittyClient(fake) // one successful list establishes the last good snapshot
			h.MarkReady()
			if publisher.setup != nil {
				publisher.setup(t, h, fake)
			}

			good := h.cachedSnapshotView()
			if !good.Ready() || good.Generation() == 0 {
				t.Fatalf("priming produced no snapshot: %+v", good)
			}

			fake.mu.Lock()
			fake.listErr = errors.New("kitty wedged")
			fake.mu.Unlock()

			publisher.drive(h, time.Now())

			after := h.cachedSnapshotView()
			if !after.Ready() {
				t.Fatal("a failed list turned a published snapshot cold")
			}
			if after.Generation() != good.Generation() {
				t.Fatalf("generation advanced on a failed list: %d -> %d", good.Generation(), after.Generation())
			}
			if !after.CapturedAt().Equal(good.CapturedAt()) {
				t.Fatalf("capture time moved on a failed list: %v -> %v", good.CapturedAt(), after.CapturedAt())
			}
			if len(after.Windows()) != len(good.Windows()) {
				t.Fatalf("windows changed on a failed list: %d -> %d", len(good.Windows()), len(after.Windows()))
			}
			if ready, _ := h.Readiness(); !ready {
				t.Fatal("a failed list demoted a ready daemon back to degraded")
			}
		})
	}
}

// TestColdSnapshotSurvivesFailureThenPublishesOnRecovery pins the cold half:
// before the first success there is nothing to preserve, and readiness only
// distinguishes cold from published — never fresh from stale.
func TestColdSnapshotSurvivesFailureThenPublishesOnRecovery(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(42, "W:api"), listErr: errors.New("kitty wedged")}
	h.useKittyClient(fake)

	ready, reason := h.restoreStateFromKittyBounded(time.Now(), 2*time.Second)
	if ready || reason == "" {
		t.Fatalf("bounded restore = (%v, %q); want degraded on a list error", ready, reason)
	}
	h.MarkDegraded(reason)

	cold := h.cachedSnapshotView()
	if cold.Ready() || cold.Generation() != 0 || !cold.CapturedAt().IsZero() {
		t.Fatalf("cold view = %+v; want an unpublished snapshot", cold)
	}

	fake.mu.Lock()
	fake.listErr = nil
	fake.mu.Unlock()

	tick := time.Now()
	h.maintenanceTick(tick)

	recovered := h.cachedSnapshotView()
	if !recovered.Ready() || recovered.Generation() != 1 {
		t.Fatalf("recovered view = (gen %d, ready %v); want the first publication",
			recovered.Generation(), recovered.Ready())
	}
	if !recovered.CapturedAt().After(tick) && !recovered.CapturedAt().Equal(tick) {
		t.Fatalf("recovered capture time %v predates the recovering tick %v", recovered.CapturedAt(), tick)
	}
	if ready, _ := h.Readiness(); !ready {
		t.Fatal("the first successful maintenance list did not recover readiness")
	}

	// An aged snapshot is stale, not cold: readiness must not move again.
	if age := recovered.Age(recovered.CapturedAt().Add(time.Hour)); age != time.Hour {
		t.Fatalf("age an hour after capture = %v; want 1h", age)
	}
	if ready, degraded := h.Readiness(); !ready || degraded != "" {
		t.Fatalf("readiness = (%v, %q) once the snapshot aged; staleness must not alter it", ready, degraded)
	}
}

// TestFailedDrainLeavesRenderPendingForTheNextMaintenanceCycle pins the render
// half of last-good preservation: a drain that cannot list returns before it
// acquires anything, so the queued work is still there for the next ordinary
// maintenance flush to converge.
func TestFailedDrainLeavesRenderPendingForTheNextMaintenanceCycle(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(42, "W:api")}
	h.SetKittyClient(fake)
	h.MarkReady()

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.DesiredIcon = "bolt"
	st.StateSet = true
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()

	fake.mu.Lock()
	fake.listErr = errors.New("kitty wedged")
	fake.mu.Unlock()

	h.drainRenderQueue()

	if h.renderQueue.Empty() {
		t.Fatal("a failed list emptied the render queue; the pending write was dropped")
	}
	h.stateMu.Lock()
	dirty := h.store[42].Dirty
	h.stateMu.Unlock()
	if !dirty {
		t.Fatal("a failed list cleared the dirty flag")
	}

	// The next normal maintenance cycle lists successfully, re-signals the
	// queue, and the retried drain converges.
	fake.mu.Lock()
	fake.listErr = nil
	fake.mu.Unlock()
	h.maintenanceTick(time.Now())
	h.drainRenderQueue()

	h.stateMu.Lock()
	st = h.store[42]
	converged := !st.Dirty && st.RenderedGen == st.Seq && st.PersistedGen == st.Seq
	h.stateMu.Unlock()
	if !converged {
		t.Fatalf("render did not converge on the next maintenance cycle: %+v", st)
	}
}

// TestConcurrentPublishersAndReadersShareOneCoherentSnapshot runs maintenance,
// render drains, watchdog ticks, hook acknowledgement (the notification path's
// snapshot read), and health queries together under the race detector. Once a
// snapshot exists it must never go cold, its generation must never go backwards,
// and no publication may land without a capture time.
//
// Cross-generation capture ORDER is deliberately not asserted: five independent
// listers stamp their own completion times, so a read that finished first can
// still publish second. That is a property of concurrent listers, not a defect —
// what the daemon guarantees is that each generation carries the completion time
// of the read that produced it.
func TestConcurrentPublishersAndReadersShareOneCoherentSnapshot(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(42, "W:api")}
	h.SetKittyClient(fake)
	h.MarkReady()

	const rounds = 40
	var publishers sync.WaitGroup
	run := func(work func()) {
		publishers.Add(1)
		go func() {
			defer publishers.Done()
			for i := 0; i < rounds; i++ {
				work()
			}
		}()
	}

	run(func() { h.maintenanceTick(time.Now()) })
	run(func() {
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.DesiredIcon = "bolt"
		st.StateSet = true
		h.markWindowDirtyLocked(42)
		h.stateMu.Unlock()
		h.drainRenderQueue()
	})
	run(func() { h.watchdogTick(time.Now(), watchdogGapThreshold+time.Second) })
	run(func() {
		h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "PreToolUse", PayloadJSON: "{}"})
	})
	run(func() { h.Handle(protocol.Request{Type: protocol.TypeHealth}) })

	done := make(chan struct{})
	go func() {
		publishers.Wait()
		close(done)
	}()

	var readers sync.WaitGroup
	for reader := 0; reader < 4; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			var lastGen uint64
			for {
				select {
				case <-done:
					return
				default:
				}
				view := h.cachedSnapshotView()
				if !view.Ready() {
					t.Error("a published snapshot reported cold mid-flight")
					return
				}
				if view.Generation() < lastGen {
					t.Errorf("generation went backwards: %d -> %d", lastGen, view.Generation())
					return
				}
				if view.CapturedAt().IsZero() {
					t.Errorf("generation %d published without a capture time", view.Generation())
					return
				}
				lastGen = view.Generation()
				// The pre-existing tuple read must agree with the view.
				if _, gen, ready := h.cachedSnapshot(); !ready || gen < lastGen {
					t.Errorf("Load() = (gen %d, ready %v); view already reported gen %d", gen, ready, lastGen)
					return
				}
			}
		}()
	}
	readers.Wait()
	<-done

	final := h.cachedSnapshotView()
	if !final.Ready() || final.Generation() == 0 || final.CapturedAt().IsZero() {
		t.Fatalf("final view = %+v; want a published snapshot with a capture time", final)
	}
}
