package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
)

type notifyCall struct {
	title    string
	subtitle string
}

func newTestNotifier(cfg *config.NotificationConfig) (*Notifier, *[]notifyCall) {
	n := NewNotifier(cfg)
	var mu sync.Mutex
	var calls []notifyCall
	n.osascript = func(title, subtitle string) error {
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
	if (*calls)[0].subtitle != "Claude stopped" {
		t.Errorf("subtitle = %q, want %q", (*calls)[0].subtitle, "Claude stopped")
	}
}

func TestNotifier_EmptySessionTitleFallback(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "", "stop", false)
	if !waitCalls(calls, 1, time.Second) {
		t.Fatal("expected notification")
	}
	if (*calls)[0].title != "Claude Code" {
		t.Errorf("title = %q, want %q", (*calls)[0].title, "Claude Code")
	}
}
