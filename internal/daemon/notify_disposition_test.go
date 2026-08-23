package daemon

// Disposition reporting: every notification decision — scheduled, dispatched,
// or suppressed with a named reason — is reported to the observer carrying the
// originating trace, so a notification that never appeared can be explained
// from the journal alone. The observer is operator-supplied and may block for
// arbitrarily long, so it must be invoked off Notifier.mu or one slow observer
// would stall every other window's notifications.

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

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
