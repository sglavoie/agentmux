package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

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

func TestMaybeNotify_HookSilenceFiresWhenEnabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "work", "hook_silence", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected notification for hook_silence")
		}
		if calls.snapshot()[0].subtitle != "Hook silence detected" {
			t.Errorf("subtitle = %q, want %q", calls.snapshot()[0].subtitle, "Hook silence detected")
		}
	})
}

func TestMaybeNotify_HookSilenceSuppressedWhenDisabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		disabled := false
		cfg := &config.NotificationConfig{HookSilence: &disabled}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "work", "hook_silence", false)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("hook_silence should be suppressed when HookSilence=&false")
		}
	})
}

func TestMaybeNotify_HookSilenceSuppressedInPipeMode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "work", "hook_silence", true)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("hook_silence should be suppressed in pipe mode")
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
