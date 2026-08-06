package daemon

import (
	"sync"
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

func TestNotifierRecordsExplicitDispositionReasons(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, _ := newTestNotifier(cfg)
		var dispositions []NotificationDisposition
		n.SetObserver(func(disposition NotificationDisposition) {
			dispositions = append(dispositions, disposition)
		})
		trace := NotificationTrace{TraceID: "trace-notify", RequestID: "request-notify"}

		n.MaybeNotifyTraced(1, "W:api", "work", "stop", false, trace)
		synctest.Wait()
		n.MaybeNotifyTraced(1, "W:api", "work", "stop", false, trace)
		n.MaybeNotifyTraced(2, "W:api", "work", "stop", true, trace)
		n.MaybeNotifyTraced(3, "W:api", "work", "active", false, trace)
		disabled, _ := newTestNotifier(&config.NotificationConfig{Stop: false})
		disabled.SetObserver(func(disposition NotificationDisposition) { dispositions = append(dispositions, disposition) })
		disabled.MaybeNotifyTraced(4, "W:api", "work", "stop", false, trace)
		muted, _ := newTestNotifier(&config.NotificationConfig{Stop: true})
		muted.SetIsMuted(func(int) bool { return true })
		muted.SetObserver(func(disposition NotificationDisposition) { dispositions = append(dispositions, disposition) })
		muted.MaybeNotifyTraced(5, "W:api", "work", "stop", false, trace)
		synctest.Wait()

		seen := map[string]bool{}
		for _, disposition := range dispositions {
			if disposition.Trace.TraceID != trace.TraceID || disposition.Trace.RequestID != trace.RequestID {
				t.Fatalf("lost notification correlation: %+v", disposition)
			}
			seen[disposition.Stage+":"+disposition.Outcome+":"+disposition.Reason] = true
		}
		for _, want := range []string{
			"decision:scheduled:dispatch",
			"dispatch:attempted:backend_started",
			"dispatch:sent:send_success",
			"focus:suppressed:interaction_unavailable",
			"decision:suppressed:cooldown",
			"decision:suppressed:pipe_mode",
			"decision:suppressed:unsupported_action",
			"decision:suppressed:config_disabled",
			"decision:suppressed:muted",
		} {
			if !seen[want] {
				t.Fatalf("missing disposition %q in %+v", want, dispositions)
			}
		}
	})
}

func TestNotificationDispositionObserverDoesNotBlockNotifierLock(t *testing.T) {
	assertOffLock := func(t *testing.T, n *Notifier, trigger func()) {
		t.Helper()
		observerStarted := make(chan struct{}, 1)
		releaseObserver := make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseObserver) }) }
		defer release()
		n.SetObserver(func(NotificationDisposition) {
			select {
			case observerStarted <- struct{}{}:
			default:
			}
			<-releaseObserver
		})

		triggerDone := make(chan struct{})
		go func() {
			trigger()
			close(triggerDone)
		}()
		select {
		case <-observerStarted:
		case <-time.After(time.Second):
			t.Fatal("disposition observer was not invoked")
		}

		lockDone := make(chan struct{})
		go func() {
			n.ResetCooldown(1)
			close(lockDone)
		}()
		select {
		case <-lockDone:
		case <-time.After(250 * time.Millisecond):
			t.Fatal("disposition observer held Notifier.mu")
		}

		release()
		select {
		case <-triggerDone:
		case <-time.After(time.Second):
			t.Fatal("notification trigger did not finish")
		}
	}

	t.Run("dispatch decision", func(t *testing.T) {
		n, _ := newTestNotifier(&config.NotificationConfig{Stop: true})
		assertOffLock(t, n, func() {
			n.MaybeNotifyTraced(1, "W:api", "work", "stop", false, NotificationTrace{TraceID: "trace-dispatch"})
		})
	})

	t.Run("pending cancellation", func(t *testing.T) {
		n, _ := newTestNotifier(&config.NotificationConfig{Permission: true, PermissionDebounceMs: 60_000})
		n.MaybeNotifyTraced(1, "W:api", "work", "permission", false, NotificationTrace{TraceID: "trace-pending"})
		assertOffLock(t, n, func() { n.CancelPending(1) })
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
