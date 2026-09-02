package notification

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// fakeFocusClient records FocusTab / FocusWindow calls so tests can assert
// the exact match expressions produced by Notifier.focusForClick.
type fakeFocusClient struct {
	mu            sync.Mutex
	live          []kitty.OSWindow
	tabs          []string
	windows       []string
	tabSockets    []string
	windowSockets []string
}

func (f *fakeFocusClient) ListWindows(string) ([]kitty.OSWindow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live, nil
}

func (f *fakeFocusClient) FocusTab(socket, matchExpr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tabs = append(f.tabs, matchExpr)
	f.tabSockets = append(f.tabSockets, socket)
	return nil
}

func (f *fakeFocusClient) FocusWindow(socket, matchExpr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.windows = append(f.windows, matchExpr)
	f.windowSockets = append(f.windowSockets, socket)
	return nil
}

func (f *fakeFocusClient) snapshot() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tabs := append([]string(nil), f.tabs...)
	windows := append([]string(nil), f.windows...)
	return tabs, windows
}

// syntheticProfileWindowID stands in for the daemon's hook-silence dedup key.
// Real Kitty window IDs are positive, so the watchdog derives a negative
// synthetic ID per profile; the notifier must never treat one as a pane.
const syntheticProfileWindowID = -424242

func focusTestWindows(ids ...int) []kitty.OSWindow {
	windows := make([]kitty.Window, 0, len(ids))
	for _, id := range ids {
		windows = append(windows, kitty.Window{ID: id})
	}
	return []kitty.OSWindow{{Tabs: []kitty.Tab{{Windows: windows}}}}
}

func focusTarget(windowID int, account, socket string) NotificationTarget {
	return NotificationTarget{WindowID: windowID, Account: account, KittyListenOn: socket, Navigable: true}
}

// TestNotifier_FocusOnClick_RealWindow drives the happy path: dispatcher
// reports a click for a real (positive) window ID, so the notifier issues
// the canonical two-step focus pattern (FocusTab then FocusWindow), both
// keyed on the window ID.
func TestNotifier_FocusOnClick_RealWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)
		focus := &fakeFocusClient{live: focusTestWindows(42)}
		n.SetFocus(focus)
		calls.setClickResult(true)

		n.MaybeNotifyTarget(focusTarget(42, "work", "unix:/tmp/kitty-42"), "W:api", "stop", false)
		synctest.Wait()

		tabs, windows := focus.snapshot()
		if want := []string{kitty.MatchTabByWindowID(42)}; !equalSlice(tabs, want) {
			t.Errorf("FocusTab calls = %v, want %v", tabs, want)
		}
		if want := []string{kitty.MatchWindowByID(42)}; !equalSlice(windows, want) {
			t.Errorf("FocusWindow calls = %v, want %v", windows, want)
		}
		if !equalSlice(focus.tabSockets, []string{"unix:/tmp/kitty-42"}) || !equalSlice(focus.windowSockets, []string{"unix:/tmp/kitty-42"}) {
			t.Errorf("focus sockets = tabs %v windows %v, want captured socket", focus.tabSockets, focus.windowSockets)
		}
	})
}

// TestNotifier_NoFocusWithoutClick verifies that when the dispatcher reports
// no click, the notifier never touches the focus client — a passive
// dismissal must not steal focus from the user's current task.
func TestNotifier_NoFocusWithoutClick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, _ := newTestNotifier(cfg)
		focus := &fakeFocusClient{live: focusTestWindows(42)}
		n.SetFocus(focus)

		n.MaybeNotifyTarget(focusTarget(42, "work", "unix:/tmp/kitty-42"), "W:api", "stop", false)
		synctest.Wait()

		tabs, windows := focus.snapshot()
		if len(tabs) != 0 || len(windows) != 0 {
			t.Errorf("no focus calls expected; got tabs=%v windows=%v", tabs, windows)
		}
	})
}

// TestNotifier_NoFocusOnDispatchError covers the case where the dispatcher
// returns an error alongside a click — the focus pipeline must not run when
// the underlying notification failed, since the click bit is meaningless
// without a successful dispatch.
func TestNotifier_NoFocusOnDispatchError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n := NewNotifier(cfg)
		n.dispatch = func(_ context.Context, _ int, _, _, _ string) (dispatchResult, error) {
			return deliveredResult(interactionClicked), errors.New("boom")
		}
		focus := &fakeFocusClient{live: focusTestWindows(42)}
		n.SetFocus(focus)

		n.MaybeNotifyTarget(focusTarget(42, "work", "unix:/tmp/kitty-42"), "W:api", "stop", false)
		synctest.Wait()

		tabs, windows := focus.snapshot()
		if len(tabs) != 0 || len(windows) != 0 {
			t.Errorf("no focus calls expected on dispatch error; got tabs=%v windows=%v", tabs, windows)
		}
	})
}

// TestNotifier_FocusOnClick_HookSilenceIsInformational locks in that a
// profile-wide alert never guesses which pane the user intended.
func TestNotifier_FocusOnClick_HookSilenceIsInformational(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		enabled := true
		cfg := &config.NotificationConfig{HookSilence: &enabled}
		n, calls := newTestNotifier(cfg)
		focus := &fakeFocusClient{live: focusTestWindows(42, 43)}
		n.SetFocus(focus)
		calls.setClickResult(true)

		n.MaybeNotify(syntheticProfileWindowID, "work", "work", "hook_silence", false)
		synctest.Wait()

		tabs, windows := focus.snapshot()
		if len(tabs) != 0 || len(windows) != 0 {
			t.Errorf("hook-silence click focused an arbitrary target: tabs=%v windows=%v", tabs, windows)
		}
	})
}

// TestNotifier_FocusOnClick_NoTargetIsNoop locks in that a click without a
// real window ID and without an account is silently ignored — the dispatcher
// returns a delivered click but there is nothing to focus, so neither branch
// should fire and the goroutine must not panic.
func TestNotifier_FocusOnClick_NoTargetIsNoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)
		focus := &fakeFocusClient{live: focusTestWindows(42, 43)}
		n.SetFocus(focus)
		calls.setClickResult(true)

		n.MaybeNotify(0, "W:api", "", "stop", false)
		synctest.Wait()

		tabs, windows := focus.snapshot()
		if len(tabs) != 0 || len(windows) != 0 {
			t.Errorf("no focus calls expected without a target; got tabs=%v windows=%v", tabs, windows)
		}
	})
}

// TestNotifier_FocusOnClick_ConcurrentDistinctWindows fires two clickable
// notifications for different windows back-to-back and confirms both focus
// invocations land and the cooldown map records both windows independently.
// This guards against any future change that serializes dispatches behind a
// single mutex without releasing it for the focus step, or that accidentally
// keys the cooldown map by something other than the window/action pair.
func TestNotifier_FocusOnClick_ConcurrentDistinctWindows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)
		focus := &fakeFocusClient{live: focusTestWindows(42, 43)}
		n.SetFocus(focus)
		calls.setClickResult(true)

		n.MaybeNotifyTarget(focusTarget(42, "work", "unix:/tmp/kitty-a"), "W:api", "stop", false)
		n.MaybeNotifyTarget(focusTarget(43, "play", "unix:/tmp/kitty-b"), "P:web", "stop", false)
		synctest.Wait()

		tabs, windows := focus.snapshot()
		// Each click triggers FocusTab then FocusWindow; order between the
		// two notifications is not guaranteed but the *set* of match exprs
		// must be exactly these two windows.
		gotTabs := map[string]int{}
		for _, e := range tabs {
			gotTabs[e]++
		}
		wantTabs := map[string]int{
			kitty.MatchTabByWindowID(42): 1,
			kitty.MatchTabByWindowID(43): 1,
		}
		if !sameCount(gotTabs, wantTabs) {
			t.Errorf("FocusTab calls = %v, want %v", gotTabs, wantTabs)
		}
		gotWindows := map[string]int{}
		for _, e := range windows {
			gotWindows[e]++
		}
		wantWindows := map[string]int{
			kitty.MatchWindowByID(42): 1,
			kitty.MatchWindowByID(43): 1,
		}
		if !sameCount(gotWindows, wantWindows) {
			t.Errorf("FocusWindow calls = %v, want %v", gotWindows, wantWindows)
		}

		// Both windows must have independent cooldown entries — the second
		// call must not have been swallowed by the first window's entry.
		n.mu.Lock()
		got42 := n.last[notifyKey{windowID: 42, action: "stop"}]
		got43 := n.last[notifyKey{windowID: 43, action: "stop"}]
		entries := len(n.last)
		n.mu.Unlock()
		if got42.IsZero() {
			t.Errorf("cooldown entry missing for window 42")
		}
		if got43.IsZero() {
			t.Errorf("cooldown entry missing for window 43")
		}
		if entries != 2 {
			t.Errorf("n.last entries = %d, want 2 (one per window)", entries)
		}
	})
}

func TestNotifier_MissingSocketSuppressesPaneNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, calls := newTestNotifier(&config.NotificationConfig{Stop: true})
		n.SetFocus(&fakeFocusClient{live: focusTestWindows(42)})
		var dispositions []NotificationDisposition
		n.SetObserver(func(disposition NotificationDisposition) { dispositions = append(dispositions, disposition) })

		n.MaybeNotifyTarget(focusTarget(42, "work", ""), "W:api", "stop", false)
		synctest.Wait()

		if calls.len() != 0 {
			t.Fatalf("dispatch calls = %d, want none without reusable socket", calls.len())
		}
		if !hasDisposition(dispositions, "decision", "suppressed", "focus_unavailable") {
			t.Fatalf("dispositions = %+v, want focus_unavailable suppression", dispositions)
		}
	})
}

func TestNotifier_ClosedTargetDoesNotFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, calls := newTestNotifier(&config.NotificationConfig{Stop: true})
		focus := &fakeFocusClient{live: focusTestWindows(99)}
		n.SetFocus(focus)
		calls.setClickResult(true)
		var dispositions []NotificationDisposition
		n.SetObserver(func(disposition NotificationDisposition) { dispositions = append(dispositions, disposition) })

		n.MaybeNotifyTarget(focusTarget(42, "work", "unix:/tmp/kitty-42"), "W:api", "stop", false)
		synctest.Wait()

		tabs, windows := focus.snapshot()
		if len(tabs) != 0 || len(windows) != 0 {
			t.Fatalf("stale click focused fallback: tabs=%v windows=%v", tabs, windows)
		}
		if !hasDisposition(dispositions, "focus", "suppressed", "focus_target_stale") {
			t.Fatalf("dispositions = %+v, want stale-target suppression", dispositions)
		}
	})
}

func hasDisposition(dispositions []NotificationDisposition, stage, outcome, reason string) bool {
	for _, disposition := range dispositions {
		if disposition.Stage == stage && disposition.Outcome == outcome && disposition.Reason == reason {
			return true
		}
	}
	return false
}

func sameCount(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
