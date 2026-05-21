package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

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

// TestNotifier_DispatchHonorsContextTimeout locks in the dispatch goroutine's
// bound — every dispatcher invocation must run under a context that expires.
// Without this, a hung osascript/alerter call would leak its goroutine for the
// life of the daemon. The synthetic dispatcher used here blocks on ctx.Done so
// the assertion is "the timeout fires", not "the call returns on its own".
func TestNotifier_DispatchHonorsContextTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		// Advance fake time past the dispatch timeout, then drain spawned goroutines.
		time.Sleep(time.Second)
		synctest.Wait()
		select {
		case err := <-returned:
			if err == nil {
				t.Fatal("expected ctx.Err() from cancelled dispatch, got nil")
			}
		default:
			t.Fatal("dispatch goroutine did not return after the context timeout — leak detected")
		}

		// Now drive a burst to confirm dispatches do not accumulate without bound:
		// each one must exit within roughly its timeout window.
		for i := 0; i < 3; i++ {
			// Reset cooldown so each call schedules its own dispatch.
			n.ResetCooldown(1)
			n.MaybeNotify(1, "W:api", "stop", false)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := inflight.Load(); got != 0 {
			t.Fatalf("dispatch goroutines did not all exit after the timeout window: %d still inflight", got)
		}
		if peak := peakInflight.Load(); peak > 3 {
			// Three bursts (plus the initial one already returned) means at most 3
			// concurrent goroutines under correct backpressure; more would indicate
			// the timeout is not bounding anything.
			t.Fatalf("peak inflight dispatches = %d, expected <= 3", peak)
		}
	})
}

func TestNotifier_EmptySessionTitleFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "", "stop", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatal("expected notification")
		}
		if calls.snapshot()[0].title != "Agent Mux" {
			t.Errorf("title = %q, want %q", calls.snapshot()[0].title, "Agent Mux")
		}
	})
}

// TestQuoteAppleScript covers AppleScript-safe escaping for the osascript
// dispatcher. The previous implementation used Go's %q verb, which can emit
// \uXXXX / \xNN sequences (e.g. for the Unicode line separator U+2028) that
// AppleScript treats as a syntax error, causing the notification to silently
// fail. quoteAppleScript must only emit AppleScript's recognized escapes
// (\\, \", \n, \r, \t) and pass every other rune through verbatim.
func TestQuoteAppleScript(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", `""`},
		{"hello", `"hello"`},
		{`"`, `"\""`},
		{`back\slash`, `"back\\slash"`},
		{"tab\there", `"tab\there"`},
		{"line1\nline2", `"line1\nline2"`},
		{"carriage\rreturn", `"carriage\rreturn"`},
		// Non-ASCII printable characters must pass through verbatim — Go's %q
		// would have re-encoded the line-separator and confused AppleScript.
		{"  sep", `"` + "  sep" + `"`},
		{"café", `"café"`},
	}
	for _, tc := range cases {
		got := quoteAppleScript(tc.in)
		if got != tc.want {
			t.Errorf("quoteAppleScript(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
