package notification

import (
	"testing"
	"testing/synctest"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// A Kitty collision slot is the notifier's key, but a click focuses the real
// Kitty window the target carries beside it, on that window's own socket.
func TestNotifier_FocusOnClick_CollisionSlotFocusesTheKittyWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const slot = 1 << 32
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)
		focus := &fakeFocusClient{live: focusTestWindows(7)}
		n.SetFocus(focus)
		calls.setClickResult(true)

		target := focusTarget(slot, "work", "unix:/tmp/kitty-b")
		target.KittyWindowID = 7
		n.MaybeNotifyTarget(target, "W:api", "stop", false)
		synctest.Wait()

		if got := calls.snapshot(); len(got) != 1 || got[0].windowID != slot {
			t.Fatalf("dispatch calls = %+v, want one keyed by slot %d", got, slot)
		}
		if _, windows := focus.snapshot(); !equalSlice(windows, []string{kitty.MatchWindowByID(7)}) {
			t.Errorf("FocusWindow calls = %v, want Kitty window 7", windows)
		}
		if !equalSlice(focus.windowSockets, []string{"unix:/tmp/kitty-b"}) {
			t.Errorf("focus sockets = %v, want the second instance's socket", focus.windowSockets)
		}
	})
}
