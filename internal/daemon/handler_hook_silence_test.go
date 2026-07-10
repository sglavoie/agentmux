package daemon

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// TestSweepHookSilence_SilentAccountNotifiesOnce covers the happy path: an
// account whose last event predates the threshold fires exactly one
// notification on the first sweep, and the latch prevents re-firing on a
// subsequent sweep within the same silence stretch.
func TestSweepHookSilence_SilentAccountNotifiesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, nil)
		fake.windows = fakeWindowsForAccount("work")

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("first sweep dispatches = %d; want 1", got)
		}
		got := rec.snapshot()[0]
		if got.title != "work" {
			t.Errorf("dispatch title = %q; want work", got.title)
		}
		if got.subtitle != "Hook silence detected" {
			t.Errorf("dispatch subtitle = %q; want %q", got.subtitle, "Hook silence detected")
		}

		// Latch suppresses subsequent ticks within the same stretch.
		h.SweepHookSilence(now)
		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("after second sweep total dispatches = %d; want 1 (latch should suppress)", got)
		}

		h.stateMu.Lock()
		latched := h.notifiedHookSilence["work"]
		h.stateMu.Unlock()
		if !latched {
			t.Fatalf("notifiedHookSilence[work] = false; want true after sweep fired")
		}
	})
}

// TestSweepHookSilence_LatchClearsOnFreshEventThenReFires verifies the full
// cycle: sweep → notify → fresh event clears latch → re-stall → sweep
// produces a second notification. The 10s per-(synthetic-window, action)
// cooldown is reset between stretches so it cannot suppress the second
// dispatch (the production 5-min cadence makes the cooldown irrelevant in
// real-world use, but tests advance time virtually).
func TestSweepHookSilence_LatchClearsOnFreshEventThenReFires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, nil)
		fake.windows = fakeWindowsForAccount("work")

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("first sweep dispatches = %d; want 1", got)
		}

		// Fresh event for "work" — S5 invariant: re-arms the latch.
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})

		h.stateMu.Lock()
		_, latched := h.notifiedHookSilence["work"]
		h.stateMu.Unlock()
		if latched {
			t.Fatalf("notifiedHookSilence[work] should be cleared by a fresh event")
		}

		// Bypass the notifier's per-(windowID, action) cooldown so a re-stall
		// produces a new dispatch. Reset against the same synthetic ID the
		// sweep uses internally.
		h.notifier.ResetCooldown(hookSilenceWindowID("work"))

		// Re-stall: backdate the per-account timestamp and sweep again.
		now2 := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now2.Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(now2)
		synctest.Wait()

		if got := rec.len(); got != 2 {
			t.Fatalf("after re-stall total dispatches = %d; want 2", got)
		}
	})
}

func TestSweepHookSilence_SuppressedEventCountsAsFresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, nil)
		fake.windows = fakeWindowsForAccount("work")

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop", Account: "work"})

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		delete(h.notifiedHookSilence, "work")
		h.stateMu.Unlock()

		resp := h.Handle(Request{
			Type:     TypeStateUpdate,
			WindowID: 42,
			Action:   "active",
			Event:    "PostToolUse",
			Account:  "work",
		})
		if !resp.OK || resp.Allowed {
			t.Fatalf("active after terminal should be suppressed, got %+v", resp)
		}

		h.SweepHookSilence(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (suppressed event should refresh hook-silence baseline)", got)
		}
	})
}

// TestSweepHookSilence_ZeroThresholdSkipped verifies that a stale account is
// skipped when neither the global nor a per-account override supplies a
// positive threshold — even though "work" is running and its lastSeenEventAt
// entry is well past any reasonable bound.
func TestSweepHookSilence_ZeroThresholdSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(0, nil)
		fake.windows = fakeWindowsForAccount("work")

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (zero threshold disables)", got)
		}
		h.stateMu.Lock()
		_, latched := h.notifiedHookSilence["work"]
		h.stateMu.Unlock()
		if latched {
			t.Fatalf("notifiedHookSilence[work] should not be set when threshold is zero")
		}
	})
}

// TestSweepHookSilence_NotRunningSkipped verifies the running-window gate:
// an account whose lastSeenEventAt is stale but which has no live Kitty
// window carrying its cc_account user var is silently skipped. This avoids
// spurious notifications for accounts the user has not launched today.
func TestSweepHookSilence_NotRunningSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, nil)
		// Only "personal" is running; "work" is silent but not launched.
		fake.windows = fakeWindowsForAccount("personal")

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.lastSeenEventAt["personal"] = now.Add(-1 * time.Minute)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (work is silent but not running; personal is fresh)", got)
		}
	})
}

// TestSweepHookSilence_SuppressedByConfig verifies the notifier still
// honors NotificationConfig.HookSilence = &false even when the sweep
// detects a real silence stretch. The latch is still set (the sweep does
// not consult notification config) so the user never gets a stale-state
// notification storm if they later toggle the flag back on.
func TestSweepHookSilence_SuppressedByConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		disabled := false
		notifier, rec := newTestNotifier(&config.NotificationConfig{HookSilence: &disabled})
		h := NewHandler(guard, notifier)
		fake := &fakeKittyClient{}
		h.SetKittyClient(fake)
		h.SetHookSilenceThresholds(1*time.Hour, nil)
		fake.windows = fakeWindowsForAccount("work")

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (HookSilence config = &false)", got)
		}
	})
}

// TestSweepHookSilence_PerAccountOverride verifies that a positive per-account
// override wins over the global default. With global = 1h and "work" = 30s,
// a 5-minute stale "work" fires while a 5-minute stale "personal" (still
// under the global) is left alone.
func TestSweepHookSilence_PerAccountOverride(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, map[string]time.Duration{
			"work": 30 * time.Second,
		})
		fake.windows = []kitty.OSWindow{
			{
				ID: 1,
				Tabs: []kitty.Tab{
					{ID: 100, Windows: []kitty.Window{{ID: 42, UserVars: kitty.UserVars{Account: "work"}}}},
					{ID: 101, Windows: []kitty.Window{{ID: 43, UserVars: kitty.UserVars{Account: "personal"}}}},
				},
			},
		}

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-5 * time.Minute)
		h.lastSeenEventAt["personal"] = now.Add(-5 * time.Minute)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (work fires; personal still under global)", got)
		}
		call := rec.snapshot()[0]
		if call.title != "work" {
			t.Errorf("dispatch title = %q; want work", call.title)
		}
		if call.account != "work" {
			t.Errorf("dispatch account = %q; want work (Session 2 threads account through MaybeNotify)", call.account)
		}
	})
}

// TestSweepHookSilence_NoEventEverSkipped verifies that a running account
// with no entry in lastSeenEventAt is skipped: the daemon has not seen any
// event for that account yet (e.g. just-started daemon), so there is no
// baseline to measure silence against.
func TestSweepHookSilence_NoEventEverSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, nil)
		fake.windows = fakeWindowsForAccount("work")
		// Intentionally do not seed h.lastSeenEventAt["work"].

		h.SweepHookSilence(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (no baseline for work)", got)
		}
	})
}

// TestSweepHookSilence_NilKittyClient verifies the no-op when no Kitty
// client has been injected (test environments without Kitty). The sweep
// short-circuits before any lock acquisition.
func TestSweepHookSilence_NilKittyClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{})
		h := NewHandler(guard, notifier)
		h.SetHookSilenceThresholds(1*time.Hour, nil)
		// Deliberately do NOT call SetKittyClient.

		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = time.Now().Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (nil kitty client)", got)
		}
	})
}

// TestSweepHookSilence_ListWindowsErrorTolerated verifies that a ListWindows
// failure is logged at debug and swallowed; the sweep does not panic and
// fires no notifications (it has no running-account set to compare against).
func TestSweepHookSilence_ListWindowsErrorTolerated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, nil)
		fake.listErr = errors.New("kitten unavailable")

		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = time.Now().Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (list-windows error swallowed)", got)
		}
	})
}

// TestHookSilenceWindowID_StableAndNegative verifies the synthetic window-ID
// scheme used for notifier dedup: same account → same ID across calls,
// different accounts → different IDs, and every ID is negative so it cannot
// collide with any real Kitty window ID (always positive).
func TestHookSilenceWindowID_StableAndNegative(t *testing.T) {
	a1 := hookSilenceWindowID("work")
	a2 := hookSilenceWindowID("work")
	b1 := hookSilenceWindowID("personal")
	if a1 != a2 {
		t.Errorf("hookSilenceWindowID(work) not stable: %d vs %d", a1, a2)
	}
	if a1 == b1 {
		t.Errorf("hookSilenceWindowID(work) collided with personal: %d", a1)
	}
	if a1 >= 0 || b1 >= 0 {
		t.Errorf("synthetic IDs should be negative; got work=%d personal=%d", a1, b1)
	}
	// Case-insensitive — the per-account threshold lookup lowercases too.
	if hookSilenceWindowID("work") != hookSilenceWindowID("WORK") {
		t.Errorf("hookSilenceWindowID should be case-insensitive")
	}
}

// TestSweepHookSilence_AccountKeyCaseInsensitive verifies that an event
// recorded with one casing for req.Account (e.g. "Work") and a Kitty user var
// with a different casing (e.g. "work") still match in the hook-silence
// sweep. Without case-folding, the sweep would never find a baseline for
// the running account and would silently skip notifications.
func TestSweepHookSilence_AccountKeyCaseInsensitive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, nil)
		// Kitty user var uses lowercase "work".
		fake.windows = fakeWindowsForAccount("work")

		now := time.Now()
		// Hook payload arrived with mixed-case "Work" — seeded via Handle so the
		// real code path normalizes the key.
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Account: "Work"})

		h.stateMu.Lock()
		// Backdate the event so the threshold trips.
		if _, ok := h.lastSeenEventAt["work"]; !ok {
			h.stateMu.Unlock()
			t.Fatalf("lastSeenEventAt should contain lowercased key 'work'; got %v", h.lastSeenEventAt)
		}
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (case mismatch must not skip)", got)
		}

		// Latch must be keyed by the lowercased account too so a fresh event
		// (in either case) clears it.
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Account: "WORK"})
		h.stateMu.Lock()
		_, latched := h.notifiedHookSilence["work"]
		h.stateMu.Unlock()
		if latched {
			t.Fatalf("notifiedHookSilence['work'] should be cleared by a case-different fresh event")
		}
	})
}
