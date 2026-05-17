package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
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

func (r *callRecorder) wait(count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if r.len() >= count {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
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
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop", false)
	if !calls.wait(1, time.Second) {
		t.Fatal("expected notification for stop")
	}
}

func TestNotifier_FiresForPermission(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "permission", false)
	if !calls.wait(1, time.Second) {
		t.Fatal("expected notification for permission")
	}
}

func TestNotifier_FiresForFailure(t *testing.T) {
	cfg := &config.NotificationConfig{Error: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "failure", false)
	if !calls.wait(1, time.Second) {
		t.Fatal("expected notification for failure")
	}
}

func TestNotifier_FiresForStopFailure(t *testing.T) {
	enabled := true
	cfg := &config.NotificationConfig{StopFailure: &enabled}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop_failure", false)
	if !calls.wait(1, time.Second) {
		t.Fatal("expected notification for stop_failure")
	}
	if calls.snapshot()[0].subtitle != "Session stopped (failure)" {
		t.Errorf("subtitle = %q, want %q", calls.snapshot()[0].subtitle, "Session stopped (failure)")
	}
}

func TestNotifier_StopFailureSuppressedByConfig(t *testing.T) {
	disabled := false
	cfg := &config.NotificationConfig{StopFailure: &disabled}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop_failure", false)
	time.Sleep(50 * time.Millisecond)
	if calls.len() > 0 {
		t.Fatal("stop_failure should be suppressed when StopFailure=&false")
	}
}

func TestNotifier_StopFailureSuppressedByPipeMode(t *testing.T) {
	enabled := true
	cfg := &config.NotificationConfig{StopFailure: &enabled}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop_failure", true)
	time.Sleep(50 * time.Millisecond)
	if calls.len() > 0 {
		t.Fatal("stop_failure should be suppressed in pipe mode")
	}
}

func TestNotifier_StopFailureCooldown(t *testing.T) {
	enabled := true
	cfg := &config.NotificationConfig{StopFailure: &enabled}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "stop_failure", false)
	if !calls.wait(1, time.Second) {
		t.Fatal("first stop_failure notification should fire")
	}

	n.MaybeNotify(1, "W:api", "stop_failure", false)
	time.Sleep(50 * time.Millisecond)
	if calls.len() != 1 {
		t.Fatalf("second stop_failure within cooldown should be suppressed, got %d calls", calls.len())
	}
}

func TestNotifier_SuppressedByPipeMode(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true, Permission: true, Error: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop", true)
	time.Sleep(50 * time.Millisecond)
	if calls.len() > 0 {
		t.Fatal("notification should be suppressed in pipe mode")
	}
}

func TestNotifier_SuppressedByConfig(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: false}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop", false)
	time.Sleep(50 * time.Millisecond)
	if calls.len() > 0 {
		t.Fatal("notification should be suppressed when config flag is false")
	}
}

func TestNotifier_SuppressedByCooldown(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "stop", false)
	if !calls.wait(1, time.Second) {
		t.Fatal("first notification should fire")
	}

	n.MaybeNotify(1, "W:api", "stop", false)
	time.Sleep(50 * time.Millisecond)
	if calls.len() != 1 {
		t.Fatalf("second notification within cooldown should be suppressed, got %d calls", calls.len())
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
	if !calls.wait(1, time.Second) {
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
	if calls.len() > 0 {
		t.Fatalf("non-notifyable actions should not fire, got %d calls", calls.len())
	}
}

func TestNotifier_CooldownPerWindow(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "stop", false)
	n.MaybeNotify(2, "P:dotfiles", "stop", false)

	if !calls.wait(2, time.Second) {
		t.Fatalf("each window should get its own notification, got %d", calls.len())
	}
}

func TestNotifier_SessionTitleInNotification(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "stop", false)
	if !calls.wait(1, time.Second) {
		t.Fatal("expected notification")
	}
	if calls.snapshot()[0].title != "W:api" {
		t.Errorf("title = %q, want %q", calls.snapshot()[0].title, "W:api")
	}
	if calls.snapshot()[0].subtitle != "Session stopped" {
		t.Errorf("subtitle = %q, want %q", calls.snapshot()[0].subtitle, "Session stopped")
	}
}

func TestNotifier_PermissionDebounceFires(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 50}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "permission", false)
	// Should not fire immediately.
	time.Sleep(10 * time.Millisecond)
	if calls.len() > 0 {
		t.Fatal("permission notification should be debounced, not immediate")
	}
	// Should fire after the debounce window.
	if !calls.wait(1, time.Second) {
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
	if calls.len() > 0 {
		t.Fatal("permission notification should have been cancelled")
	}
}

func TestNotifier_PermissionDebounceFiresWhenNoCancel(t *testing.T) {
	// Without cancellation the notification fires after the debounce expires.
	cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 50}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "permission", false)
	if !calls.wait(1, time.Second) {
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
	if calls.len() > 0 {
		t.Fatal("repeat permission should refresh the debounce window")
	}
	if !calls.wait(1, time.Second) {
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
	if !calls.wait(1, time.Second) {
		t.Fatal("expected second permission to fire after cancellation")
	}
}

func TestNotifier_PermissionNoDebounce(t *testing.T) {
	// When PermissionDebounceMs is 0 the notification fires immediately.
	cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 0}
	n, calls := newTestNotifier(cfg)

	n.MaybeNotify(1, "W:api", "permission", false)
	if !calls.wait(1, time.Second) {
		t.Fatal("expected immediate notification when debounce is 0")
	}
}

func TestNotifier_ElicitationFiresWhenPermissionEnabled(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "elicitation", false)
	if !calls.wait(1, time.Second) {
		t.Fatal("expected notification for elicitation when Permission=true")
	}
}

func TestNotifier_ElicitationSilencedWhenPermissionDisabled(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: false}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "elicitation", false)
	time.Sleep(50 * time.Millisecond)
	if calls.len() > 0 {
		t.Fatal("elicitation should be suppressed when Permission=false")
	}
}

func TestNotifier_ElicitationLabel(t *testing.T) {
	cfg := &config.NotificationConfig{Permission: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "W:api", "elicitation", false)
	if !calls.wait(1, time.Second) {
		t.Fatal("expected notification for elicitation")
	}
	if calls.snapshot()[0].subtitle != "Claude is asking" {
		t.Errorf("subtitle = %q, want %q", calls.snapshot()[0].subtitle, "Claude is asking")
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
	if !calls.wait(1, time.Second) {
		t.Fatal("expected stop notification to fire")
	}

	n.MaybeNotify(1, "W:api", "permission", false)
	if !calls.wait(2, time.Second) {
		t.Fatal("expected permission notification to fire despite recent stop")
	}
	if calls.snapshot()[1].subtitle != "Permission requested" {
		t.Fatalf("second call subtitle = %q, want %q", calls.snapshot()[1].subtitle, "Permission requested")
	}
}

// TestNotifier_DispatchHonorsContextTimeout locks in the dispatch goroutine's
// bound — every dispatcher invocation must run under a context that expires.
// Without this, a hung osascript/alerter call would leak its goroutine for the
// life of the daemon. The synthetic dispatcher used here blocks on ctx.Done so
// the assertion is "the timeout fires", not "the call returns on its own".
func TestNotifier_DispatchHonorsContextTimeout(t *testing.T) {
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

	select {
	case err := <-returned:
		if err == nil {
			t.Fatal("expected ctx.Err() from cancelled dispatch, got nil")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("dispatch goroutine did not return after the context timeout — leak detected")
	}

	// Now drive a burst to confirm dispatches do not accumulate without bound:
	// each one must exit within roughly its timeout window.
	for i := 0; i < 3; i++ {
		// Reset cooldown so each call schedules its own dispatch.
		n.ResetCooldown(1)
		n.MaybeNotify(1, "W:api", "stop", false)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if inflight.Load() == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := inflight.Load(); got != 0 {
		t.Fatalf("dispatch goroutines did not all exit after the timeout window: %d still inflight", got)
	}
	if peak := peakInflight.Load(); peak > 3 {
		// Three bursts (plus the initial one already returned) means at most 3
		// concurrent goroutines under correct backpressure; more would indicate
		// the timeout is not bounding anything.
		t.Fatalf("peak inflight dispatches = %d, expected <= 3", peak)
	}
}

func TestNotifier_EmptySessionTitleFallback(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true}
	n, calls := newTestNotifier(cfg)
	n.MaybeNotify(1, "", "stop", false)
	if !calls.wait(1, time.Second) {
		t.Fatal("expected notification")
	}
	if calls.snapshot()[0].title != "Agent Mux" {
		t.Errorf("title = %q, want %q", calls.snapshot()[0].title, "Agent Mux")
	}
}
