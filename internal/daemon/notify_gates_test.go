package daemon

// The gates that apply to every notifyable action alike: pipe mode, the config
// flag, the per-window mute closure, the per-window cooldown, and the list of
// actions that are not notifyable at all. Cooldown is keyed per window and
// expires on its own clock; mute is read live, so flipping it re-opens the path
// with no other state change. An unavailable backend is the one suppression
// that must not record a cooldown, or a transient outage would silence the next
// real notification too.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestNotifier_SuppressedByPipeMode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true, Permission: true, Error: true}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "W:api", "", "stop", true)
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
		n.MaybeNotify(1, "W:api", "", "stop", false)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatal("notification should be suppressed when config flag is false")
		}
	})
}

func TestNotifier_BackendUnavailableSuppressesWithoutCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := newTestNotifier(&config.NotificationConfig{Stop: true})
		n.dispatch = nil
		var dispositions []NotificationDisposition
		n.SetObserver(func(disposition NotificationDisposition) { dispositions = append(dispositions, disposition) })

		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()

		if !hasDisposition(dispositions, "decision", "suppressed", "backend_unavailable") {
			t.Fatalf("dispositions = %+v, want backend_unavailable", dispositions)
		}
		n.mu.Lock()
		entries := len(n.last)
		n.mu.Unlock()
		if entries != 0 {
			t.Fatalf("cooldown entries = %d, want none for unavailable backend", entries)
		}
	})
}

func TestNotifier_SuppressedByCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "", "stop", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("first notification should fire")
		}

		n.MaybeNotify(1, "W:api", "", "stop", false)
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
		n.MaybeNotify(1, "W:api", "", "stop", false)

		n.now = time.Now
		n.MaybeNotify(1, "W:api", "", "stop", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("notification should fire after cooldown expired")
		}
	})
}

func TestNotifier_CooldownPerWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)

		n.MaybeNotify(1, "W:api", "", "stop", false)
		n.MaybeNotify(2, "P:dotfiles", "", "stop", false)

		synctest.Wait()
		if calls.len() < 2 {
			t.Fatalf("each window should get its own notification, got %d", calls.len())
		}
	})
}

func TestNotifier_NonNotifyableActionsSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true, Permission: true, Error: true}
		n, calls := newTestNotifier(cfg)
		for _, action := range []string{"active", "idle", "clear"} {
			n.MaybeNotify(1, "W:api", "", action, false)
		}
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatalf("non-notifyable actions should not fire, got %d calls", calls.len())
		}
	})
}

// TestMaybeNotify_SuppressedWhenMuted asserts that every notifyable action
// becomes a no-op when the IsMuted closure reports the window as muted.
// The mute gate sits between the pipeMode early-out and the notifyable-action
// check, so it must cover stop / permission / failure / stop_failure /
// elicitation / stale / hook_silence in one pass.
func TestMaybeNotify_SuppressedWhenMuted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		enabled := true
		cfg := &config.NotificationConfig{
			Stop:        true,
			Permission:  true,
			Error:       true,
			StopFailure: &enabled,
		}
		n, calls := newTestNotifier(cfg)
		n.SetIsMuted(func(id int) bool { return id == 1 })

		for _, action := range []string{
			"stop", "permission", "failure", "stop_failure",
			"elicitation", "stale", "hook_silence",
		} {
			n.MaybeNotify(1, "W:api", "work", action, false)
		}
		synctest.Wait()
		if calls.len() != 0 {
			t.Fatalf("muted window should suppress every notifyable action, got %d calls: %+v",
				calls.len(), calls.snapshot())
		}

		// Unmuted window with the same dispatcher still fires.
		n.MaybeNotify(2, "P:dotfiles", "work", "stop", false)
		synctest.Wait()
		if calls.len() != 1 {
			t.Fatalf("unmuted window should still notify, got %d calls", calls.len())
		}
	})
}

// TestMaybeNotify_MuteRespectedAfterUnmute confirms the gate is dynamic:
// flipping IsMuted from true to false re-opens the notification path without
// requiring any other state change.
func TestMaybeNotify_MuteRespectedAfterUnmute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)
		muted := true
		n.SetIsMuted(func(int) bool { return muted })

		n.MaybeNotify(1, "W:api", "", "stop", false)
		synctest.Wait()
		if calls.len() != 0 {
			t.Fatalf("muted stop should be suppressed, got %d calls", calls.len())
		}

		muted = false
		n.MaybeNotify(1, "W:api", "", "stop", false)
		synctest.Wait()
		if calls.len() != 1 {
			t.Fatalf("unmuted stop should fire once, got %d calls", calls.len())
		}
	})
}
