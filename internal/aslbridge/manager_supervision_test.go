package aslbridge

// Supervision across sessions: the restart schedule, what resets it, and what a
// disconnect does to data the daemon is already holding. Timing here is decided
// by a manually advanced clock rather than wall time, so the stale window and
// the healthy-restart reset are asserted exactly instead of approximately.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// testClock is a manually advanced clock.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestRestartBackoffFollowsSchedule(t *testing.T) {
	var (
		mu     sync.Mutex
		delays []time.Duration
	)
	done := make(chan struct{})
	launcher := func(context.Context) (*Child, error) {
		fake := newFakeBridge(t, func(f *fakeBridge) {
			f.hello()
			f.exit()
		})
		return fake.child(), nil
	}
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: launcher,
		Sleep: func(_ context.Context, d time.Duration) bool {
			mu.Lock()
			delays = append(delays, d)
			full := len(delays) == 4
			mu.Unlock()
			if full {
				close(done)
				return false
			}
			return true
		},
	})
	<-done
	m.Close()

	mu.Lock()
	got := append([]time.Duration(nil), delays...)
	mu.Unlock()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delays = %v, want %v", got, want)
		}
	}
	if m.Status().RestartCount != len(want) {
		t.Fatalf("restart count = %d, want %d", m.Status().RestartCount, len(want))
	}
}

func TestHealthySessionResetsBackoff(t *testing.T) {
	clock := newTestClock()
	var (
		mu     sync.Mutex
		delays []time.Duration
	)
	done := make(chan struct{})
	launcher := func(context.Context) (*Child, error) {
		fake := newFakeBridge(t, func(f *fakeBridge) {
			f.hello()
			req := f.nextRequestOf(MethodRunsSnapshot)
			f.respond(req["id"].(string), emptySnapshot())
			// The clock only moves once the daemon has already recorded the
			// connection, so every session is unambiguously "healthy".
			clock.advance(HealthyResetInterval + time.Second)
			f.exit()
		})
		return fake.child(), nil
	}
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: launcher, Now: clock.Now,
		Sleep: func(_ context.Context, d time.Duration) bool {
			mu.Lock()
			delays = append(delays, d)
			full := len(delays) == 3
			mu.Unlock()
			if full {
				close(done)
				return false
			}
			return true
		},
	})
	<-done
	m.Close()

	mu.Lock()
	got := append([]time.Duration(nil), delays...)
	mu.Unlock()
	for i, d := range got {
		if d != time.Second {
			t.Fatalf("delay %d = %s, want the schedule reset to 1s (all: %v)", i, d, got)
		}
	}
}

func TestDisconnectServesStaleDataThenClearsIt(t *testing.T) {
	clock := newTestClock()
	fake := newFakeBridge(t, serveSnapshots)
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(), Now: clock.Now,
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitFor(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

	fake.exit()
	waitFor(t, "disconnect", func() bool { return !m.Status().Available })

	// Mutations are disabled at once, but the last projection survives as
	// visibly stale read-only data.
	if _, err := m.Act(context.Background(), ActionRequest{
		Method: MethodRunAbort, PlanDir: "/p", OperationID: "op", ActionToken: "tok",
	}); err == nil {
		t.Fatal("a disconnected bridge must refuse mutations")
	}
	_, meta, err := m.Snapshot()
	if err != nil {
		t.Fatalf("stale snapshot error = %v, want the stale window to serve it", err)
	}
	if !meta.Stale {
		t.Fatal("a snapshot served after disconnect must be marked stale")
	}
	if !m.Status().SnapshotStale {
		t.Fatal("status must mark the retained snapshot stale")
	}

	clock.advance(StaleWindow + time.Second)
	if _, _, err := m.Snapshot(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired snapshot error = %v, want ErrUnavailable", err)
	}
	if m.Status().SnapshotAgeSeconds != nil {
		t.Fatal("an expired snapshot must not report an age")
	}
}

func TestActionsRequireAFreshSnapshotAfterReconnect(t *testing.T) {
	// The bridge answers the handshake but never the snapshot, so the daemon is
	// connected without current state: actions must stay disabled.
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		<-f.exited
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitFor(t, "connection", func() bool { return m.Status().Available })

	if m.Status().MutationsEnabled {
		t.Fatal("a handshake alone must not re-enable mutations")
	}
	if _, err := m.Act(context.Background(), ActionRequest{
		Method: MethodRunAbort, PlanDir: "/p", OperationID: "op", ActionToken: "tok",
	}); !errors.Is(err, ErrMutationsDisabled) {
		t.Fatalf("Act error = %v, want ErrMutationsDisabled", err)
	}
}

func TestCloseJoinsSupervisionAndIsIdempotent(t *testing.T) {
	fake := newFakeBridge(t, serveSnapshots)
	m := New(Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(), Logger: discardLogger()})
	m.Start(context.Background())
	waitFor(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

	done := make(chan struct{})
	go func() {
		m.Close()
		m.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not join bridge goroutines")
	}
	if m.Status().Available {
		t.Fatal("a closed manager must not report availability")
	}
}

func TestBackoffForSaturates(t *testing.T) {
	if got := backoffFor(-1); got != time.Second {
		t.Fatalf("backoffFor(-1) = %s", got)
	}
	if got := backoffFor(len(restartBackoff) + 5); got != 30*time.Second {
		t.Fatalf("saturated backoff = %s, want 30s", got)
	}
}
