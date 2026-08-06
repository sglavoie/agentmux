package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestNotificationDispatchFailurePersistsTerminalJournalOutcome(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewNotifier(&config.NotificationConfig{Stop: true})
		n.dispatch = func(context.Context, int, string, string, string) (dispatchResult, error) {
			return dispatchResult{}, errors.New("backend unavailable")
		}
		h := NewHandler(NewStateGuard(time.Hour), n)
		journal, err := diagnostics.NewJournal(diagnostics.PathsForRoot(t.TempDir()), diagnostics.JournalOptions{})
		if err != nil {
			t.Fatal(err)
		}
		h.setDecisionJournal(journal)

		h.maybeNotifyTraced(42, "W:api", "work", "stop", false, NotificationTrace{
			TraceID: "trace-notify-fail", RequestID: "request-notify-fail",
		})
		synctest.Wait()

		records, err := journal.Read(context.Background(), diagnostics.Selector{
			TraceID: "trace-notify-fail", Stages: []string{"daemon.notification.dispatch"},
		})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, record := range records {
			if record.Outcome == "failed" && record.Reason == "send_failed" {
				found = true
			}
		}
		if !found {
			t.Fatalf("journal lacks terminal notification failure: %+v", records)
		}
	})
}

func TestAlerterArgs_UsesDoubleDashFlags(t *testing.T) {
	got := alerterArgs("W:api", "Session stopped")
	want := []string{"--title", "W:api", "--subtitle", "Session stopped", "--message", "", "--timeout", "300", "--sender", "net.kovidgoyal.kitty"}
	if len(got) != len(want) {
		t.Fatalf("alerterArgs len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("alerterArgs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAlerterDispatchTimeoutIncludesProtocolGrace(t *testing.T) {
	if got, want := alerterProtocolTimeout, 300*time.Second; got != want {
		t.Fatalf("alerterProtocolTimeout = %s, want %s", got, want)
	}
	if got, want := alerterTimeoutGrace, time.Minute; got != want {
		t.Fatalf("alerterTimeoutGrace = %s, want %s", got, want)
	}
	if got, want := alerterDispatchTimeout, alerterProtocolTimeout+time.Minute; got != want {
		t.Fatalf("alerterDispatchTimeout = %s, want %s", got, want)
	}
}

// TestNotifier_DispatchHonorsContextTimeout locks in the dispatch goroutine's
// bound — every dispatcher invocation must run under a context that expires.
// Without this, a hung Alerter call would leak its goroutine for the
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
		dispositions := make(chan NotificationDisposition, 16)
		n.SetObserver(func(disposition NotificationDisposition) { dispositions <- disposition })
		n.dispatch = func(ctx context.Context, _ int, _, _, _ string) (dispatchResult, error) {
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
			return dispatchResult{}, ctx.Err()
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
		if !drainDispositionKeys(dispositions)["dispatch:failed:backend_timeout"] {
			t.Fatal("wrapper expiry was not recorded as dispatch:failed:backend_timeout")
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
// context.Background(). Under the alerter protocol timeout plus grace, that meant
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
		dispositions := make(chan NotificationDisposition, 8)
		n.SetObserver(func(disposition NotificationDisposition) { dispositions <- disposition })
		n.dispatch = func(ctx context.Context, _ int, _, _, _ string) (dispatchResult, error) {
			<-ctx.Done()
			returned <- ctx.Err()
			return dispatchResult{}, ctx.Err()
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
		if !drainDispositionKeys(dispositions)["dispatch:cancelled:daemon_shutdown"] {
			t.Fatal("parent cancellation was not recorded as dispatch:cancelled:daemon_shutdown")
		}
	})
}

func TestNotifier_DispatchSuppressesFocusForTerminalInteraction(t *testing.T) {
	cases := []struct {
		name        string
		interaction interactionOutcome
		wantReason  string
	}{
		{"timeout", interactionTimeout, "interaction_timeout"},
		{"dismissed", interactionDismissed, "dismissed"},
		{"replied", interactionReplied, "replied"},
		{"unavailable", interactionUnavailable, "interaction_unavailable"},
		{"unknown", interactionUnknown, "interaction_unknown"},
		{"unrecognized", interactionOutcome("future-token"), "interaction_unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := NewNotifier(&config.NotificationConfig{Stop: true})
			n.dispatch = func(context.Context, int, string, string, string) (dispatchResult, error) {
				return deliveredResult(tc.interaction), nil
			}
			var dispositions []NotificationDisposition
			n.SetObserver(func(disposition NotificationDisposition) {
				dispositions = append(dispositions, disposition)
			})

			n.runDispatch(NotificationTarget{WindowID: 42, Account: "work"}, "W:api", "Session stopped", "stop", NotificationTrace{})

			seen := make(map[string]bool)
			for _, disposition := range dispositions {
				seen[disposition.Stage+":"+disposition.Outcome+":"+disposition.Reason] = true
			}
			if !seen["dispatch:sent:send_success"] {
				t.Fatalf("successful delivery missing from dispositions: %+v", dispositions)
			}
			want := "focus:suppressed:" + tc.wantReason
			if !seen[want] {
				t.Fatalf("missing %q from dispositions: %+v", want, dispositions)
			}
		})
	}
}

// TestParseAlerterOutput pins the delivery and interaction result for every
// documented alerter terminal token. Unknown successful output remains
// delivered but must never request focus.
func TestParseAlerterOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want dispatchResult
	}{
		{"content-clicked", "@CONTENTCLICKED", deliveredResult(interactionClicked)},
		{"action-clicked", "@ACTIONCLICKED", deliveredResult(interactionClicked)},
		{"timeout", "@TIMEOUT", deliveredResult(interactionTimeout)},
		{"closed", "@CLOSED", deliveredResult(interactionDismissed)},
		{"replied", "@REPLIED", deliveredResult(interactionReplied)},
		{"content-closed", "@CONTENTCLOSED", deliveredResult(interactionDismissed)},
		{"empty", "", deliveredResult(interactionUnknown)},
		{"whitespace-only", "   \t\n  ", deliveredResult(interactionUnknown)},
		{"garbage", "what is this", deliveredResult(interactionUnknown)},
		{"trailing-newline", "@CONTENTCLICKED\n", deliveredResult(interactionClicked)},
		{"leading-whitespace", "  @CONTENTCLICKED", deliveredResult(interactionClicked)},
		{"two-lines-keeps-first", "@CONTENTCLICKED\n@REPLIED", deliveredResult(interactionClicked)},
		{"two-lines-non-click-first", "@TIMEOUT\n@CONTENTCLICKED", deliveredResult(interactionTimeout)},
		// alerter emits uppercase tokens; case-sensitive matching is
		// intentional so a future stdout format change does not silently
		// start treating arbitrary text as a click.
		{"lowercase-content-clicked", "@contentclicked", deliveredResult(interactionUnknown)},
		{"lowercase-action-clicked", "@actionclicked", deliveredResult(interactionUnknown)},
		{"mixed-case-content-clicked", "@ContentClicked", deliveredResult(interactionUnknown)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseAlerterOutput([]byte(tc.in)); got != tc.want {
				t.Errorf("parseAlerterOutput(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func drainDispositionKeys(dispositions <-chan NotificationDisposition) map[string]bool {
	seen := make(map[string]bool)
	for {
		select {
		case disposition := <-dispositions:
			seen[disposition.Stage+":"+disposition.Outcome+":"+disposition.Reason] = true
		default:
			return seen
		}
	}
}
