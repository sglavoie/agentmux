package notification

// Per-action notification behaviour: which actions notify at all, what text
// they carry, and the config flag and pipe-mode gate each one answers to. Stop
// failures, stale sweeps, and hook silence each have their own opt-out, so each
// is walked through fire / config-disabled / pipe-mode / cooldown. The gates
// that apply to every action regardless of kind live in notify_gates_test.go,
// and disposition reporting in notify_disposition_test.go.

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
		n.MaybeNotify(1, "W:api", "", "stop", false)
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
		n.MaybeNotify(1, "W:api", "", "permission", false)
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
		n.MaybeNotify(1, "W:api", "", "failure", false)
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
		n.MaybeNotify(1, "W:api", "", "stop_failure", false)
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
		n.MaybeNotify(1, "W:api", "", "stop_failure", false)
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
		n.MaybeNotify(1, "W:api", "", "stop_failure", true)
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

		n.MaybeNotify(1, "W:api", "", "stop_failure", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("first stop_failure notification should fire")
		}

		n.MaybeNotify(1, "W:api", "", "stop_failure", false)
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
		n.MaybeNotify(1, "W:api", "", "stale", false)
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
		n.MaybeNotify(1, "W:api", "", "stale", false)
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
		n.MaybeNotify(1, "W:api", "", "stale", true)
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

		n.MaybeNotify(1, "W:api", "", "stale", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("first stale notification should fire")
		}

		n.MaybeNotify(1, "W:api", "", "stale", false)
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
		n.MaybeNotify(1, "work", "work", "hook_silence", false)
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
		n.MaybeNotify(1, "work", "work", "hook_silence", false)
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
		n.MaybeNotify(1, "work", "work", "hook_silence", true)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("hook_silence should be suppressed in pipe mode")
		}
	})
}

func TestNotifier_SessionTitleInNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "", "stop", false)
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

func TestElicitationHasItsOwnSwitch(t *testing.T) {
	off := false
	n, _ := newTestNotifier(&config.NotificationConfig{Permission: true, Elicitation: &off})
	if !n.actionEnabled("permission") || n.actionEnabled("elicitation") {
		t.Fatal("elicitation = false must silence elicitation without touching permission")
	}
	n, _ = newTestNotifier(&config.NotificationConfig{Permission: true})
	if !n.actionEnabled("elicitation") {
		t.Fatal("an unset elicitation must keep following permission")
	}
}

func TestCooldownComesFromConfig(t *testing.T) {
	if n, _ := newTestNotifier(&config.NotificationConfig{CooldownSeconds: 45}); n.cooldown != 45*time.Second {
		t.Fatalf("cooldown = %v, want 45s", n.cooldown)
	}
	if n, _ := newTestNotifier(&config.NotificationConfig{}); n.cooldown != defaultCooldown {
		t.Fatalf("unset cooldown = %v, want the default %v", n.cooldown, defaultCooldown)
	}
}
