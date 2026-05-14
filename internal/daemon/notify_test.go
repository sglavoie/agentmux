package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
)

func newTestNotifier(cfg *config.NotificationConfig) (*Notifier, *[]string) {
	n := NewNotifier(cfg)
	var mu sync.Mutex
	var calls []string
	n.osascript = func(title, msg string) error {
		mu.Lock()
		calls = append(calls, title)
		mu.Unlock()
		return nil
	}
	return n, &calls
}

func waitCalls(calls *[]string, count int, timeout time.Duration) bool {
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
	n.MaybeNotify(1, "work", "stop", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification for stop")
	}
}

func TestNotifier_FiresForPermission(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "work", "permission", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification for permission")
	}
}

func TestNotifier_FiresForFailure(t *testing.T) {
	cfg := &config.NotificationConfig{Error: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "work", "failure", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification for failure")
	}
}

func TestNotifier_SuppressedByPipeMode(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true, Permission: true, Error: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "work", "stop", true)
	time.Sleep(50 * time.Millisecond)
	if len(*calls) > 0 {
		t.Fatal("notification should be suppressed in pipe mode")
	}
}

func TestNotifier_SuppressedByConfig(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: false}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "work", "stop", false)
	time.Sleep(50 * time.Millisecond)
	if len(*calls) > 0 {
		t.Fatal("notification should be suppressed when config flag is false")
	}
}

func TestNotifier_SuppressedByCooldown(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)

	// First call fires.
	n.MaybeNotify(1, "work", "stop", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("first notification should fire")
	}

	// Second call within cooldown should be suppressed.
	n.MaybeNotify(1, "work", "stop", false)
	time.Sleep(50 * time.Millisecond)
	if len(*calls) != 1 {
		t.Fatalf("second notification within cooldown should be suppressed, got %d calls", len(*calls))
	}
}

func TestNotifier_FiresAfterCooldownReset(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)
	// Fake a past notification by backdating the clock.
	t0 := time.Now().Add(-11 * time.Second)
	n.now = func() time.Time { return t0 }
	n.MaybeNotify(1, "work", "stop", false)

	n.now = time.Now
	// Should fire because cooldown has expired.
	n.MaybeNotify(1, "work", "stop", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("notification should fire after cooldown expired")
	}
}

func TestNotifier_NonNotifyableActionsSkipped(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true, Permission: true, Error: true}
	n, calls := newTestNotifier(cfg)
	for _, action := range []string{"active", "idle", "clear"} {
		n.MaybeNotify(1, "work", action, false)
	}
	time.Sleep(50 * time.Millisecond)
	if len(*calls) > 0 {
		t.Fatalf("non-notifyable actions should not fire, got %d calls", len(*calls))
	}
}

func TestNotifier_CooldownPerWindow(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "work", "stop", false)
	n.MaybeNotify(2, "work", "stop", false) // different window

	if !waitCalls(calls, 2, time.Second) {
		t.Fatalf("each window should get its own notification, got %d", len(*calls))
	}
}
