package probes

import (
	"sync"
	"testing"
	"time"
)

// The view is the whole published snapshot read as one value. These tests pin
// the two distinctions it exists to make: cold (never published) versus stale
// (published long ago), and capture time versus publication generation.

func TestSnapshotView_ColdStoreHasNoCaptureTime(t *testing.T) {
	var s SnapshotStore
	view := s.View()
	if view.Ready() {
		t.Fatal("cold store reports ready")
	}
	if view.Generation() != 0 {
		t.Fatalf("cold generation = %d; want 0", view.Generation())
	}
	if !view.CapturedAt().IsZero() {
		t.Fatalf("cold capture time = %v; want the zero time", view.CapturedAt())
	}
	if len(view.Windows()) != 0 {
		t.Fatalf("cold windows = %d; want none", len(view.Windows()))
	}
	if age := view.Age(time.Unix(1_700_000_000, 0)); age != 0 {
		t.Fatalf("cold age = %v; want 0", age)
	}
}

func TestSnapshotView_CarriesWindowsGenerationAndCaptureTime(t *testing.T) {
	var s SnapshotStore
	first := time.Unix(1_700_000_000, 0)
	s.publish(fakeOSWindowsWithTab(42, "api"), first)

	view := s.View()
	if !view.Ready() || view.Generation() != 1 {
		t.Fatalf("first publish = (gen %d, ready %v); want (1, true)", view.Generation(), view.Ready())
	}
	if !view.CapturedAt().Equal(first) {
		t.Fatalf("first capture time = %v; want %v", view.CapturedAt(), first)
	}
	if len(view.Windows()) != 1 {
		t.Fatalf("first windows = %d; want 1", len(view.Windows()))
	}

	second := first.Add(2 * time.Second)
	s.publish(fakeOSWindowsWithTab(43, "web"), second)
	view = s.View()
	if view.Generation() != 2 || !view.CapturedAt().Equal(second) {
		t.Fatalf("second publish = (gen %d, at %v); want (2, %v)", view.Generation(), view.CapturedAt(), second)
	}

	// A view is one value taken under one lock acquisition: the generation and
	// the capture time it reports always describe the same publication.
	if got := view.Windows()[0].Tabs[0].Windows[0].ID; got != 43 {
		t.Fatalf("second publish window = %d; want 43", got)
	}
}

// TestSnapshotView_StaleSnapshotStaysReady pins the readiness rule the daemon
// depends on: only a never-published snapshot is cold. However old a capture
// time gets, the view stays ready, so age can never demote a running daemon
// back to degraded.
func TestSnapshotView_StaleSnapshotStaysReady(t *testing.T) {
	var s SnapshotStore
	captured := time.Unix(1_700_000_000, 0)
	s.publish(fakeOSWindowsWithTab(42, "api"), captured)

	view := s.View()
	if age := view.Age(captured.Add(time.Hour)); age != time.Hour {
		t.Fatalf("age an hour after capture = %v; want 1h", age)
	}
	if !view.Ready() {
		t.Fatal("an hour-old snapshot reports cold; staleness must not un-ready a published snapshot")
	}
}

// TestSnapshotView_AgeClampsBackwardsClock covers a wall-clock adjustment that
// moves time behind the capture instant. A snapshot that has not aged yet is
// reported as brand new rather than as negatively aged.
func TestSnapshotView_AgeClampsBackwardsClock(t *testing.T) {
	var s SnapshotStore
	captured := time.Unix(1_700_000_000, 0)
	s.publish(fakeOSWindowsWithTab(42, "api"), captured)

	if age := s.View().Age(captured.Add(-time.Minute)); age != 0 {
		t.Fatalf("age before the capture instant = %v; want 0", age)
	}
}

// TestSnapshotView_MatchesLoadAndScreenProbeAPI pins that adding the view left
// the two existing read paths intact and reading the same publication.
func TestSnapshotView_MatchesLoadAndScreenProbeAPI(t *testing.T) {
	var s SnapshotStore
	captured := time.Unix(1_700_000_000, 0)
	s.PublishWithReceipts(fakeOSWindowsWithTab(42, "api"), map[int]uint64{42: 7}, captured)

	view := s.View()
	windows, gen, ready := s.Load()
	if gen != view.Generation() || ready != view.Ready() || len(windows) != len(view.Windows()) {
		t.Fatalf("Load() = (gen %d, ready %v, windows %d); view = (gen %d, ready %v, windows %d)",
			gen, ready, len(windows), view.Generation(), view.Ready(), len(view.Windows()))
	}
	probeSnapshot, probeReady := s.ScreenProbeSnapshot()
	if !probeReady || probeSnapshot.Receipt(42) != 7 {
		t.Fatalf("ScreenProbeSnapshot() = (receipt %d, ready %v); want (7, true)",
			probeSnapshot.Receipt(42), probeReady)
	}
	if len(probeSnapshot.Windows()) != len(view.Windows()) {
		t.Fatalf("screen-probe windows = %d; view windows = %d",
			len(probeSnapshot.Windows()), len(view.Windows()))
	}
}

// TestSnapshotView_ConcurrentReadsAndPublishes is the race-detector guard for
// the added read path: publications and the three reader APIs run together, and
// every view must report a generation and a capture time from the same
// publication rather than a torn pair.
func TestSnapshotView_ConcurrentReadsAndPublishes(t *testing.T) {
	var s SnapshotStore
	base := time.Unix(1_700_000_000, 0)
	// Capture times advance in lockstep with generations, so a torn read is
	// detectable: gen N must always carry base+N seconds.
	captureFor := func(gen uint64) time.Time { return base.Add(time.Duration(gen) * time.Second) }

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for gen := uint64(1); ; gen++ {
			select {
			case <-stop:
				return
			default:
			}
			s.publish(fakeOSWindowsWithTab(42, "api"), captureFor(gen))
		}
	}()

	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				view := s.View()
				if !view.Ready() {
					continue
				}
				if want := captureFor(view.Generation()); !view.CapturedAt().Equal(want) {
					t.Errorf("gen %d carries capture time %v; want %v",
						view.Generation(), view.CapturedAt(), want)
					return
				}
				s.Load()
				s.ScreenProbeSnapshot()
			}
		}()
	}
	for i := 0; i < 500; i++ {
		s.View()
	}
	close(stop)
	wg.Wait()
}
