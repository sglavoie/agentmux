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
		n.dispatch = func(ctx context.Context, _ int, _, _, _ string) (bool, error) {
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
			return false, ctx.Err()
		}

		n.MaybeNotify(1, "W:api", "", "stop", false)
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
			n.MaybeNotify(1, "W:api", "", "stop", false)
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
		n.MaybeNotify(1, "", "", "stop", false)
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

// TestNotifier_RecorderClickKnobFlowsThroughDispatch confirms the
// setClickResult knob on the recorder propagates into both the dispatcher's
// return value and the recorded notifyCall. Session 3 uses this knob to
// assert that focus is invoked on click; locking the wiring here means
// future regressions surface in this small unit test instead of in the
// integration coverage.
func TestNotifier_RecorderClickKnobFlowsThroughDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n, calls := newTestNotifier(cfg)
		calls.setClickResult(true)

		n.MaybeNotify(1, "W:api", "", "stop", false)
		synctest.Wait()

		snap := calls.snapshot()
		if len(snap) != 1 {
			t.Fatalf("got %d calls, want 1", len(snap))
		}
		if !snap[0].clicked {
			t.Errorf("notifyCall.clicked = false, want true after setClickResult(true)")
		}
	})
}

// TestNotifier_ParentContextCancellationStopsDispatch locks in the fix for
// the latent bug where runDispatch derived its bounded context from
// context.Background(). Under the 5-minute alerter timeout, that meant
// `amx daemon stop` could leave an alerter subprocess running for up to five
// minutes. The dispatch ctx must now be a child of the parent installed via
// SetParentContext, so cancelling the daemon's ctx propagates immediately.
func TestNotifier_ParentContextCancellationStopsDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{Stop: true}
		n := NewNotifier(cfg)
		// Generous per-dispatch timeout so this test would hang for the full
		// timeout if parent-ctx cancellation did not propagate.
		n.dispatchTimeout = time.Hour
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		n.SetParentContext(parent)

		returned := make(chan error, 1)
		n.dispatch = func(ctx context.Context, _ int, _, _, _ string) (bool, error) {
			<-ctx.Done()
			returned <- ctx.Err()
			return false, ctx.Err()
		}

		n.MaybeNotify(1, "W:api", "", "stop", false)
		// Let the dispatch goroutine reach <-ctx.Done().
		synctest.Wait()

		select {
		case <-returned:
			t.Fatal("dispatch returned before parent cancel; the timeout fired instead of cancellation propagating")
		default:
		}

		cancel()
		synctest.Wait()

		select {
		case err := <-returned:
			if err == nil {
				t.Fatal("expected ctx.Err() after parent cancel, got nil")
			}
		default:
			t.Fatal("dispatch did not return after parent ctx cancel — runDispatch is detached from the daemon lifecycle")
		}
	})
}

// TestParseAlerterOutput pins which alerter stdout tokens are interpreted as
// an explicit click. Only @CONTENTCLICKED and @ACTIONCLICKED indicate the
// user wants the source window focused; every other outcome (timeout, close,
// reply, content-close, empty, garbled) must remain false so a passive
// dismissal cannot yank focus away from whatever the user is currently doing.
func TestParseAlerterOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"content-clicked", "@CONTENTCLICKED", true},
		{"action-clicked", "@ACTIONCLICKED", true},
		{"timeout", "@TIMEOUT", false},
		{"closed", "@CLOSED", false},
		{"replied", "@REPLIED", false},
		{"content-closed", "@CONTENTCLOSED", false},
		{"empty", "", false},
		{"whitespace-only", "   \t\n  ", false},
		{"garbage", "what is this", false},
		{"trailing-newline", "@CONTENTCLICKED\n", true},
		{"leading-whitespace", "  @CONTENTCLICKED", true},
		{"two-lines-keeps-first", "@CONTENTCLICKED\n@REPLIED", true},
		{"two-lines-non-click-first", "@TIMEOUT\n@CONTENTCLICKED", false},
		// alerter emits uppercase tokens; case-sensitive matching is
		// intentional so a future stdout format change does not silently
		// start treating arbitrary text as a click.
		{"lowercase-content-clicked", "@contentclicked", false},
		{"lowercase-action-clicked", "@actionclicked", false},
		{"mixed-case-content-clicked", "@ContentClicked", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseAlerterOutput([]byte(tc.in)); got != tc.want {
				t.Errorf("parseAlerterOutput(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
