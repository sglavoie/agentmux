package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

type notifyCall struct {
	title    string
	subtitle string
}

func newTestNotifier(cfg *config.NotificationConfig) (*Notifier, *[]notifyCall) {
	n := NewNotifier(cfg)
	var mu sync.Mutex
	var calls []notifyCall
	n.dispatch = func(title, subtitle string) error {
		mu.Lock()
		calls = append(calls, notifyCall{title, subtitle})
		mu.Unlock()
		return nil
	}
	return n, &calls
}

func waitCalls(calls *[]notifyCall, count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(*calls) >= count {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestNotifier_FiresForStop(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification for stop")
	}
}

func TestNotifier_FiresForPermission(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "permission", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification for permission")
	}
}

func TestNotifier_FiresForFailure(t *testing.T) {
	cfg := &config.NotificationConfig{Error: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "failure", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification for failure")
	}
}

func TestNotifier_FiresForStopFailure(t *testing.T) {
	enabled := true
	cfg := &config.NotificationConfig{StopFailure: &enabled}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop_failure", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification for stop_failure")
	}
	if (*calls)[0].subtitle != "Session stopped (failure)" {
		t.Errorf("subtitle = %q, want %q", (*calls)[0].subtitle, "Session stopped (failure)")
	}
}

func TestNotifier_StopFailureSuppressedByConfig(t *testing.T) {
	disabled := false
	cfg := &config.NotificationConfig{StopFailure: &disabled}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop_failure", false)
	time.Sleep(50 * time.Millisecond)
	if len(*calls) > 0 {
		t.Fatal("stop_failure should be suppressed when StopFailure=&false")
	}
}

func TestNotifier_StopFailureSuppressedByPipeMode(t *testing.T) {
	enabled := true
	cfg := &config.NotificationConfig{StopFailure: &enabled}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop_failure", true)
	time.Sleep(50 * time.Millisecond)
	if len(*calls) > 0 {
		t.Fatal("stop_failure should be suppressed in pipe mode")
	}
}

func TestNotifier_StopFailureCooldown(t *testing.T) {
	enabled := true
	cfg := &config.NotificationConfig{StopFailure: &enabled}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "stop_failure", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("first stop_failure notification should fire")
	}

	n.MaybeNotify(1, "W:api", "stop_failure", false)
	time.Sleep(50 * time.Millisecond)
	if len(*calls) != 1 {
		t.Fatalf("second stop_failure within cooldown should be suppressed, got %d calls", len(*calls))
	}
}

func TestNotifier_SuppressedByPipeMode(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true, Permission: true, Error: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop", true)
	time.Sleep(50 * time.Millisecond)
	if len(*calls) > 0 {
		t.Fatal("notification should be suppressed in pipe mode")
	}
}

func TestNotifier_SuppressedByConfig(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: false}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop", false)
	time.Sleep(50 * time.Millisecond)
	if len(*calls) > 0 {
		t.Fatal("notification should be suppressed when config flag is false")
	}
}

func TestNotifier_SuppressedByCooldown(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "stop", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("first notification should fire")
	}

	n.MaybeNotify(1, "W:api", "stop", false)
	time.Sleep(50 * time.Millisecond)
	if len(*calls) != 1 {
		t.Fatalf("second notification within cooldown should be suppressed, got %d calls", len(*calls))
	}
}

func TestNotifier_FiresAfterCooldownReset(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)
	t0 := time.Now().Add(-11 * time.Second)
	n.now = func() time.Time { return t0 }
	n.MaybeNotify(1, "W:api", "stop", false)

	n.now = time.Now
	n.MaybeNotify(1, "W:api", "stop", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("notification should fire after cooldown expired")
	}
}

func TestNotifier_NonNotifyableActionsSkipped(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true, Permission: true, Error: true}
	n, calls := newTestNotifier(cfg)
	for _, action := range []string{"active", "idle", "clear"} {
		n.MaybeNotify(1, "W:api", action, false)
	}
	time.Sleep(50 * time.Millisecond)
	if len(*calls) > 0 {
		t.Fatalf("non-notifyable actions should not fire, got %d calls", len(*calls))
	}
}

func TestNotifier_CooldownPerWindow(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "stop", false)
	n.MaybeNotify(2, "P:dotfiles", "stop", false)

	if !waitCalls(calls, 2, time.Second) {
		t.Fatalf("each window should get its own notification, got %d", len(*calls))
	}
}

func TestNotifier_SessionTitleInNotification(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification")
	}
	if (*calls)[0].title != "W:api" {
		t.Errorf("title = %q, want %q", (*calls)[0].title, "W:api")
	}
	if (*calls)[0].subtitle != "Session stopped" {
		t.Errorf("subtitle = %q, want %q", (*calls)[0].subtitle, "Session stopped")
	}
}

func TestNotifier_PermissionDebounceFires(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 50}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "permission", false)
	// Should not fire immediately.
	time.Sleep(10 * time.Millisecond)
	if len(*calls) > 0 {
		t.Fatal("permission notification should be debounced, not immediate")
	}
	// Should fire after the debounce window.
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected debounced notification to fire")
	}
}

func TestNotifier_PermissionDebounceCancelledByActive(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 200}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "permission", false)
	time.Sleep(20 * time.Millisecond)
	n.CancelPending(1) // simulates auto-approval (active transition)

	time.Sleep(300 * time.Millisecond) // wait past the debounce window
	if len(*calls) > 0 {
		t.Fatal("permission notification should have been cancelled")
	}
}

func TestNotifier_PermissionDebounceFiresWhenNoCancel(t *testing.T) {
	// Without cancellation the notification fires after the debounce expires.
	cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 50}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "permission", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification to fire when not cancelled")
	}
}

func TestNotifier_PermissionDebounceRefreshesOnRepeat(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 80}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "permission", false)
	time.Sleep(50 * time.Millisecond)
	n.MaybeNotify(1, "W:api", "permission", false)

	time.Sleep(40 * time.Millisecond)
	if len(*calls) > 0 {
		t.Fatal("repeat permission should refresh the debounce window")
	}
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected refreshed permission notification to fire")
	}
}

func TestNotifier_CancelPendingAllowsNextPermission(t *testing.T) {
	// After CancelPending, the next permission notification should still be able
	// to schedule and fire immediately after the configured debounce.
	cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 200}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "permission", false)
	n.CancelPending(1)

	n.MaybeNotify(1, "W:api", "permission", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected second permission to fire after cancellation")
	}
}

func TestNotifier_PermissionNoDebounce(t *testing.T) {
	// When PermissionDebounceMs is 0 the notification fires immediately.
	cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 0}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "permission", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected immediate notification when debounce is 0")
	}
}

func TestNotifier_ElicitationFiresWhenPermissionEnabled(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "elicitation", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification for elicitation when Permission=true")
	}
}

func TestNotifier_ElicitationSilencedWhenPermissionDisabled(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: false}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "elicitation", false)
	time.Sleep(50 * time.Millisecond)
	if len(*calls) > 0 {
		t.Fatal("elicitation should be suppressed when Permission=false")
	}
}

func TestNotifier_ElicitationLabel(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "elicitation", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification for elicitation")
	}
	if (*calls)[0].subtitle != "Claude is asking" {
		t.Errorf("subtitle = %q, want %q", (*calls)[0].subtitle, "Claude is asking")
	}
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
	// The cooldown is per-(window, action). A recent stop notification must
	// not silently suppress a subsequent permission notification.
	cfg := &config.NotificationConfig{Permission: true, Stop: true, PermissionDebounceMs: 50}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "stop", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected stop notification to fire")
	}

	n.MaybeNotify(1, "W:api", "permission", false)
	if !waitCalls(calls, 2, time.Second) {
		t.Fatal("expected permission notification to fire despite recent stop")
	}
	if (*calls)[1].subtitle != "Permission requested" {
		t.Fatalf("second call subtitle = %q, want %q", (*calls)[1].subtitle, "Permission requested")
	}
}

func TestNotifier_EmptySessionTitleFallback(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "", "stop", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification")
	}
	if (*calls)[0].title != "Agent Mux" {
		t.Errorf("title = %q, want %q", (*calls)[0].title, "Agent Mux")
	}
}
