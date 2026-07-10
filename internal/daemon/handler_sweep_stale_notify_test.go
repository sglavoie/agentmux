package daemon

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestSweepStale_PipeModeSilent verifies that a pipe-mode session has its
// state and title cleared but no macOS notification fires — matching the
// existing pipe-mode silence rule for stop/permission/failure.
func TestSweepStale_PipeModeSilent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		boltTitle := palette.BuildTitle("W:api", "bolt", false)
		clearedTitle := palette.BuildTitle("W:api", "", false)
		fake.windows = fakeOSWindowsWithTab(42, boltTitle)

		now := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(42).LastAction = "active"
		h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
		h.ensureWindowStateLocked(42).LastPipeMode = true
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (pipe-mode silent)", got)
		}
		titles := fake.titleCalls()
		if len(titles) != 1 || titles[0].title != clearedTitle {
			t.Fatalf("SetTabTitle = %+v; want one call with title %q", titles, clearedTitle)
		}
		h.stateMu.Lock()
		has := h.store[42].LastAction != ""
		h.stateMu.Unlock()
		if has {
			t.Fatalf("lastAction[42] still present after sweep")
		}
	})
}

// TestSweepStale_SingleShot verifies the single-shot semantics: calling sweep
// twice without any state change between calls fires exactly one notification.
// The second sweep is a complete no-op because the first sweep cleared
// lastAction[42] — the notifiedStale guard is belt-and-braces.
func TestSweepStale_SingleShot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(42).LastAction = "active"
		h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()
		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want exactly 1 across two sweeps", got)
		}
		if got := len(fake.titleCalls()); got != 1 {
			t.Fatalf("SetTabTitle calls = %d; want exactly 1 across two sweeps", got)
		}
	})
}

// TestSweepStale_ReNotifyAfterRecovery verifies that a fresh real event
// resets the notifiedStale guard so a subsequent stall produces a second
// notification. The full cycle exercises sweep → recovery → re-stall → sweep.
func TestSweepStale_ReNotifyAfterRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(42).LastAction = "active"
		h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("first sweep: dispatches = %d; want 1", got)
		}

		// Simulate a fresh active event: Handle clears NotifiedStale and
		// re-seeds LastAction = "active".
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", SessionTitle: "W:api"})

		h.stateMu.Lock()
		stillNotified := h.store[42].NotifiedStale
		h.stateMu.Unlock()
		if stillNotified {
			t.Fatalf("notifiedStale[42] should be cleared by fresh active update")
		}

		// Bypass the 10s per-action cooldown so the second dispatch is not
		// suppressed by the notifier. Synctest time has not advanced here
		// because no time.Sleep was called.
		h.notifier.ResetCooldown(42)

		// Re-stall: backdate activity and refresh the title fixture (the
		// previous sweep cleared the icon, so re-seed with the bolt title to
		// confirm the second sweep clears it again).
		fake.mu.Lock()
		fake.setTitleCalls = nil
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))
		fake.mu.Unlock()

		now2 := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now2.Add(-15 * time.Minute)
		h.stateMu.Unlock()

		h.SweepStale(now2)
		synctest.Wait()

		if got := rec.len(); got != 2 {
			t.Fatalf("second sweep: total dispatches = %d; want 2", got)
		}
		if got := len(fake.titleCalls()); got != 1 {
			t.Fatalf("second sweep: SetTabTitle calls = %d; want 1", got)
		}
	})
}

// TestSweepStale_NilKittyClient verifies that the sweep runs cleanly without a
// Kitty client (the test environment without Kitty). Notification still
// fires; only the title-clear step is skipped.
func TestSweepStale_NilKittyClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{})
		h := NewHandler(guard, notifier)
		h.SetStaleThresholds(map[string]time.Duration{"active": 10 * time.Minute}, nil)
		// Deliberately do NOT call SetKittyClient — h.kittyClient is nil.

		now := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(42).LastAction = "active"
		h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1", got)
		}
		h.stateMu.Lock()
		has := h.store[42].LastAction != ""
		h.stateMu.Unlock()
		if has {
			t.Fatalf("lastAction[42] still present after sweep")
		}
	})
}

// TestSweepStale_PreservesStarGlyph verifies that the star prefix on a tab
// title survives a sweep — only the state icon is stripped.
func TestSweepStale_PreservesStarGlyph(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, _ := newSweepTestHandler(10 * time.Minute)
		starredBolt := palette.BuildTitle("W:api", "bolt", true)
		starredCleared := palette.BuildTitle("W:api", "", true)
		fake.windows = fakeOSWindowsWithTab(42, starredBolt)

		now := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(42).LastAction = "active"
		h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		titles := fake.titleCalls()
		if len(titles) != 1 {
			t.Fatalf("SetTabTitle calls = %d; want 1", len(titles))
		}
		if titles[0].title != starredCleared {
			t.Fatalf("SetTabTitle title = %q; want %q (star preserved)", titles[0].title, starredCleared)
		}
	})
}

func TestSweepStale_PreservesLoopMarkerAndHarnessEmoji(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, _ := newSweepTestHandler(10 * time.Minute)
		activeTitle := palette.BuildTitleParts(palette.TitleParts{
			Base:    "🦊api",
			State:   "bolt",
			Starred: true,
			Muted:   true,
			Looping: true,
		})
		clearedTitle := palette.BuildTitleParts(palette.TitleParts{
			Base:    "🦊api",
			Starred: true,
			Muted:   true,
			Looping: true,
		})
		fake.windows = fakeOSWindowsWithTab(42, activeTitle)

		now := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(42).LastAction = "active"
		h.ensureWindowStateLocked(42).LastSessionTitle = "🦊api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		titles := fake.titleCalls()
		if len(titles) != 1 {
			t.Fatalf("SetTabTitle calls = %d; want 1", len(titles))
		}
		if titles[0].title != clearedTitle {
			t.Fatalf("SetTabTitle title = %q; want %q", titles[0].title, clearedTitle)
		}
	})
}

// TestSweepStale_ListWindowsErrorIsTolerated verifies that a ListWindows
// failure does not panic and does not block the sweep's other steps: the
// notification still fires and state is still cleared. The next sweep can
// retry the title clear if the daemon stays up.
func TestSweepStale_ListWindowsErrorIsTolerated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		fake.listErr = errors.New("kitten unavailable")

		now := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(42).LastAction = "active"
		h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 even on ListWindows error", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d; want 0 (list failed)", got)
		}
		h.stateMu.Lock()
		has := h.store[42].LastAction != ""
		notified := h.store[42].NotifiedStale
		h.stateMu.Unlock()
		if has {
			t.Fatalf("lastAction[42] still present after sweep")
		}
		if !notified {
			t.Fatalf("notifiedStale[42] = false; want true")
		}
	})
}
