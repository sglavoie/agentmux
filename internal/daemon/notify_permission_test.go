package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

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
