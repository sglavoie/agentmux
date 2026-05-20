package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

type notifyCall struct {
	title    string
	subtitle string
}

// callRecorder is a thread-safe collector for dispatcher invocations. The
// notify dispatch runs in its own goroutine, so every read from the test
// must hold the same mutex as the write — otherwise the race detector flags
// it. Both notify_test.go and handler_test.go use this helper to avoid
// hand-rolling per-test mutex bookkeeping.
type callRecorder struct {
	mu    sync.Mutex
	calls []notifyCall
}

func (r *callRecorder) add(c notifyCall) {
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.mu.Unlock()
}

func (r *callRecorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// snapshot returns a copy so tests can iterate without holding the mutex.
func (r *callRecorder) snapshot() []notifyCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]notifyCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func newTestNotifier(cfg *config.NotificationConfig) (*Notifier, *callRecorder) {
	n := NewNotifier(cfg)
	rec := &callRecorder{}
	n.dispatch = func(_ context.Context, title, subtitle string) error {
		rec.add(notifyCall{title, subtitle})
		return nil
	}
	return n, rec
}

func TestNotifier_FiresForStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "stop", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected notification for stop")
		}
	})
}

func TestNotifier_FiresForPermission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Permission: true}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "permission", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected notification for permission")
		}
	})
}

func TestNotifier_FiresForFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Error: true}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "failure", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected notification for failure")
		}
	})
}

func TestNotifier_FiresForStopFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		enabled := true
		cfg := &config.NotificationConfig{StopFailure: &enabled}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "stop_failure", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected notification for stop_failure")
		}
		if calls.snapshot()[0].subtitle != "Session stopped (failure)" {
			t.Errorf("subtitle = %q, want %q", calls.snapshot()[0].subtitle, "Session stopped (failure)")
		}
	})
}

func TestNotifier_StopFailureSuppressedByConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		disabled := false
		cfg := &config.NotificationConfig{StopFailure: &disabled}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "stop_failure", false)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("stop_failure should be suppressed when StopFailure=&false")
		}
	})
}

func TestNotifier_StopFailureSuppressedByPipeMode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		enabled := true
		cfg := &config.NotificationConfig{StopFailure: &enabled}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "stop_failure", true)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("stop_failure should be suppressed in pipe mode")
		}
	})
}

func TestNotifier_StopFailureCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		enabled := true
		cfg := &config.NotificationConfig{StopFailure: &enabled}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "stop_failure", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("first stop_failure notification should fire")
		}

		n.MaybeNotify(1, "W:api", "stop_failure", false)
		synctest.Wait()
		if calls.len() != 1 {
			t.Fatalf("second stop_failure within cooldown should be suppressed, got %d calls", calls.len())
		}
	})
}

func TestMaybeNotify_StaleFiresWhenEnabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "stale", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected notification for stale")
		}
		if calls.snapshot()[0].subtitle != "Session idle" {
			t.Errorf("subtitle = %q, want %q", calls.snapshot()[0].subtitle, "Session idle")
		}
	})
}

func TestMaybeNotify_StaleSuppressedWhenDisabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		disabled := false
		cfg := &config.NotificationConfig{StaleActive: &disabled}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "stale", false)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("stale should be suppressed when StaleActive=&false")
		}
	})
}

func TestMaybeNotify_StaleSuppressedInPipeMode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "stale", true)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("stale should be suppressed in pipe mode")
		}
	})
}

func TestMaybeNotify_StaleCooldownPerWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "stale", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("first stale notification should fire")
		}

		n.MaybeNotify(1, "W:api", "stale", false)
		synctest.Wait()
		if calls.len() != 1 {
			t.Fatalf("second stale within cooldown should be suppressed, got %d calls", calls.len())
		}
	})
}

func TestNotifier_SuppressedByPipeMode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true, Permission: true, Error: true}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "stop", true)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("notification should be suppressed in pipe mode")
		}
	})
}

func TestNotifier_SuppressedByConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: false}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "stop", false)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("notification should be suppressed when config flag is false")
		}
	})
}

func TestNotifier_SuppressedByCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "stop", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("first notification should fire")
		}

		n.MaybeNotify(1, "W:api", "stop", false)
		synctest.Wait()
		if calls.len() != 1 {
			t.Fatalf("second notification within cooldown should be suppressed, got %d calls", calls.len())
		}
	})
}

func TestNotifier_FiresAfterCooldownReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)
		t0 := time.Now().Add(-11 * time.Second)
		n.now = func() time.Time { return t0 }
		n.MaybeNotify(1, "W:api", "stop", false)

		n.now = time.Now
		n.MaybeNotify(1, "W:api", "stop", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("notification should fire after cooldown expired")
		}
	})
}

func TestNotifier_NonNotifyableActionsSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true, Permission: true, Error: true}
		n, calls := newTestNotifier(cfg)
		for _, action := range []string{"active", "idle", "clear"} {
			n.MaybeNotify(1, "W:api", action, false)
		}
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatalf("non-notifyable actions should not fire, got %d calls", calls.len())
		}
	})
}

func TestNotifier_CooldownPerWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "stop", false)
		n.MaybeNotify(2, "P:dotfiles", "stop", false)

		synctest.Wait()
		if calls.len() < 2 {
			t.Fatalf("each window should get its own notification, got %d", calls.len())
		}
	})
}

func TestNotifier_SessionTitleInNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "stop", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected notification")
		}
		if calls.snapshot()[0].title != "W:api" {
			t.Errorf("title = %q, want %q", calls.snapshot()[0].title, "W:api")
		}
		if calls.snapshot()[0].subtitle != "Session stopped" {
			t.Errorf("subtitle = %q, want %q", calls.snapshot()[0].subtitle, "Session stopped")
		}
	})
}

func TestNotifier_PermissionDebounceFires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 50}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "permission", false)
		// Should not fire immediately.
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("permission notification should be debounced, not immediate")
		}
		// Should fire after the debounce window.
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.len() != 1 {
			t.Fatal("expected debounced notification to fire")
		}
	})
}

func TestNotifier_PermissionDebounceCancelledByActive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 200}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "permission", false)
		time.Sleep(20 * time.Millisecond)
		n.CancelPending(1) // simulates auto-approval (active transition)

		time.Sleep(time.Second) // fake — well past the debounce window
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("permission notification should have been cancelled")
		}
	})
}

func TestNotifier_PermissionDebounceFiresWhenNoCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Without cancellation the notification fires after the debounce expires.
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 50}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "permission", false)
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected notification to fire when not cancelled")
		}
	})
}

func TestNotifier_PermissionDebounceRefreshesOnRepeat(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 80}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "permission", false)
		time.Sleep(50 * time.Millisecond)
		n.MaybeNotify(1, "W:api", "permission", false)

		time.Sleep(40 * time.Millisecond)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("repeat permission should refresh the debounce window")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.len() != 1 {
			t.Fatal("expected refreshed permission notification to fire")
		}
	})
}

func TestNotifier_CancelPendingAllowsNextPermission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// After CancelPending, the next permission notification should still be able
		// to schedule and fire immediately after the configured debounce.
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 200}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "permission", false)
		n.CancelPending(1)

		n.MaybeNotify(1, "W:api", "permission", false)
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected second permission to fire after cancellation")
		}
	})
}

func TestNotifier_PermissionNoDebounce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// When PermissionDebounceMs is 0 the notification fires immediately.
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 0}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "permission", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected immediate notification when debounce is 0")
		}
	})
}

func TestNotifier_ElicitationFiresWhenPermissionEnabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Permission: true}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "elicitation", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected notification for elicitation when Permission=true")
		}
	})
}

func TestNotifier_ElicitationSilencedWhenPermissionDisabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Permission: false}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "elicitation", false)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("elicitation should be suppressed when Permission=false")
		}
	})
}

func TestNotifier_ElicitationLabel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Permission: true}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "elicitation", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected notification for elicitation")
		}
		if calls.snapshot()[0].subtitle != "Claude is asking" {
			t.Errorf("subtitle = %q, want %q", calls.snapshot()[0].subtitle, "Claude is asking")
		}
	})
}

func TestAlerterArgs_UsesDoubleDashFlags(t *testing.T) {
	got := alerterArgs("W:api", "Session stopped")
	want := []string{"--title", "W:api", "--subtitle", "Session stopped", "--message", ""}
	if len(got) != len(want) {
		t.Fatalf("alerterArgs len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("alerterArgs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNotifier_StopCooldownDoesNotSuppressPermission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// The cooldown is per-(window, action). A recent stop notification must
		// not silently suppress a subsequent permission notification.
		cfg := &config.NotificationConfig{Permission: true, Stop: true, PermissionDebounceMs: 50}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "stop", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected stop notification to fire")
		}

		n.MaybeNotify(1, "W:api", "permission", false)
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.len() < 2 {
			t.Fatal("expected permission notification to fire despite recent stop")
		}
		if calls.snapshot()[1].subtitle != "Permission requested" {
			t.Fatalf("second call subtitle = %q, want %q", calls.snapshot()[1].subtitle, "Permission requested")
		}
	})
}

// TestNotifier_DispatchHonorsContextTimeout locks in the dispatch goroutine's
// bound — every dispatcher invocation must run under a context that expires.
// Without this, a hung osascript/alerter call would leak its goroutine for the
// life of the daemon. The synthetic dispatcher used here blocks on ctx.Done so
// the assertion is "the timeout fires", not "the call returns on its own".
func TestNotifier_DispatchHonorsContextTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n := NewNotifier(cfg)
		n.dispatchTimeout = 30 * time.Millisecond

		var inflight atomic.Int32
		var peakInflight atomic.Int32
		returned := make(chan error, 4)
		n.dispatch = func(ctx context.Context, _, _ string) error {
			cur := inflight.Add(1)
			defer inflight.Add(-1)
			for {
				prev := peakInflight.Load()
				if cur <= prev || peakInflight.CompareAndSwap(prev, cur) {
					break
				}
			}
			<-ctx.Done()
			returned <- ctx.Err()
			return ctx.Err()
		}

		n.MaybeNotify(1, "W:api", "stop", false)
		// Advance fake time past the dispatch timeout, then drain spawned goroutines.
		time.Sleep(time.Second)
		synctest.Wait()
		select {
		case err := <-returned:
			if err == nil {
				t.Fatal("expected ctx.Err() from cancelled dispatch, got nil")
			}
		default:
			t.Fatal("dispatch goroutine did not return after the context timeout — leak detected")
		}

		// Now drive a burst to confirm dispatches do not accumulate without bound:
		// each one must exit within roughly its timeout window.
		for i := 0; i < 3; i++ {
			// Reset cooldown so each call schedules its own dispatch.
			n.ResetCooldown(1)
			n.MaybeNotify(1, "W:api", "stop", false)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := inflight.Load(); got != 0 {
			t.Fatalf("dispatch goroutines did not all exit after the timeout window: %d still inflight", got)
		}
		if peak := peakInflight.Load(); peak > 3 {
			// Three bursts (plus the initial one already returned) means at most 3
			// concurrent goroutines under correct backpressure; more would indicate
			// the timeout is not bounding anything.
			t.Fatalf("peak inflight dispatches = %d, expected <= 3", peak)
		}
	})
}

func TestNotifier_EmptySessionTitleFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "", "stop", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected notification")
		}
		if calls.snapshot()[0].title != "Agent Mux" {
			t.Errorf("title = %q, want %q", calls.snapshot()[0].title, "Agent Mux")
		}
	})
}

// TestQuoteAppleScript covers AppleScript-safe escaping for the osascript
// dispatcher. The previous implementation used Go's %q verb, which can emit
// \uXXXX / \xNN sequences (e.g. for the Unicode line separator U+2028) that
// AppleScript treats as a syntax error, causing the notification to silently
// fail. quoteAppleScript must only emit AppleScript's recognized escapes
// (\\, \", \n, \r, \t) and pass every other rune through verbatim.
func TestQuoteAppleScript(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", `""`},
		{"hello", `"hello"`},
		{`"`, `"\""`},
		{`back\slash`, `"back\\slash"`},
		{"tab\there", `"tab\there"`},
		{"line1\nline2", `"line1\nline2"`},
		{"carriage\rreturn", `"carriage\rreturn"`},
		// Non-ASCII printable characters must pass through verbatim — Go's %q
		// would have re-encoded the line-separator and confused AppleScript.
		{"  sep", `"` + "  sep" + `"`},
		{"café", `"café"`},
	}
	for _, tc := range cases {
		got := quoteAppleScript(tc.in)
		if got != tc.want {
			t.Errorf("quoteAppleScript(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
