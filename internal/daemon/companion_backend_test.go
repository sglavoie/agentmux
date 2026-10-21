package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/testutil"
)

// alerterCalls counts Alerter dispatches made by a test notifier.
type alerterCalls struct {
	mu sync.Mutex
	n  int
}

func (a *alerterCalls) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}

// newNotifyingTestServer serves a Handler whose notifier sends stop and
// failure notifications, with a counting stand-in for Alerter.
func newNotifyingTestServer(t *testing.T) (string, *Handler, *alerterCalls) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "amx-appnotify-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	socketPath := filepath.Join(dir, "d.sock")
	notifier := notification.NewNotifier(&config.NotificationConfig{Stop: true, Error: true})
	calls := &alerterCalls{}
	notifier.SetBackend(func(context.Context, int, string, string, string) (bool, error) {
		calls.mu.Lock()
		calls.n++
		calls.mu.Unlock()
		return false, nil
	})
	h := NewHandler(activity.NewStateGuard(time.Hour), notifier)
	server, err := ipc.NewServer(socketPath, h)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		server.Close()
		_ = os.RemoveAll(dir)
	})
	return socketPath, h, calls
}

func subscribeBackend(t *testing.T, sock, client string) *ipc.Subscription {
	t.Helper()
	sub, err := ipc.NewClientWithSocket(sock).Subscribe(context.Background(), protocol.SubscribeRequest{
		Protocol: 1, Client: client, NotificationBackend: true, NotificationsAuthorized: true,
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	return sub
}

func nextNotice(t *testing.T, sub *ipc.Subscription) notification.AppNotice {
	t.Helper()
	frame := nextFrameOfType(t, sub, protocol.FrameNotification)
	var notice notification.AppNotice
	if err := json.Unmarshal(frame.Data, &notice); err != nil {
		t.Fatalf("decode notification frame %s: %v", frame.Data, err)
	}
	return notice
}

func dispose(h *Handler, id, disposition string) protocol.Response {
	return h.Handle(protocol.Request{Type: protocol.TypeNotificationDisposition, Disposition: &protocol.DispositionRequest{
		NotificationID: id, Disposition: disposition,
	}})
}

func notificationEffect(h *Handler, stage string) (notification.NotificationDisposition, bool) {
	h.asyncMu.Lock()
	defer h.asyncMu.Unlock()
	for _, d := range h.notificationEffects {
		if d.Stage == stage {
			return d, true
		}
	}
	return notification.NotificationDisposition{}, false
}

// TestAppNotificationReachesASubscriberAndSettles is the fake-subscriber
// integration: a real subscription claims the backend role, a stop event
// arrives as a notification frame, and the app's dispositions settle it
// without Alerter ever running.
func TestAppNotificationReachesASubscriberAndSettles(t *testing.T) {
	t.Parallel()

	sock, h, alerter := newNotifyingTestServer(t)
	sub := subscribeBackend(t, sock, "fake-app")
	if !h.companion.Ready() {
		t.Fatal("an authorized backend subscriber did not take the role")
	}

	h.notifier.MaybeNotify(7, "W:api", "work", "stop", false)
	notice := nextNotice(t, sub)
	if notice.NotificationID == "" || notice.WindowID != 7 || notice.Action != "stop" || notice.Title != "W:api" {
		t.Fatalf("notice = %+v", notice)
	}
	if resp := dispose(h, notice.NotificationID, protocol.DispositionDelivered); !resp.OK {
		t.Fatalf("delivered: %+v", resp)
	}
	testutil.Eventually(t, "the app's delivered report to record send_success", func() bool {
		d, ok := notificationEffect(h, "dispatch")
		return ok && d.Outcome == "sent" && d.Backend == notification.BackendApp
	})
	if resp := dispose(h, notice.NotificationID, protocol.DispositionDismissed); !resp.OK {
		t.Fatalf("dismissed: %+v", resp)
	}
	testutil.Eventually(t, "the settled delivery to be forgotten", func() bool { return h.companion.pendingDeliveries() == 0 })
	if alerter.count() != 0 {
		t.Fatalf("alerter ran %d times although the app delivered", alerter.count())
	}
}

// TestNoSubscriberUsesAlerter: with nobody holding the role, nothing changes.
func TestNoSubscriberUsesAlerter(t *testing.T) {
	t.Parallel()

	sock, h, alerter := newNotifyingTestServer(t)
	plain, err := ipc.NewClientWithSocket(sock).Subscribe(context.Background(), protocol.SubscribeRequest{Protocol: 1})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = plain.Close() }()
	if h.companion.Ready() {
		t.Fatal("a subscriber that did not claim the role holds it")
	}
	h.notifier.MaybeNotify(7, "W:api", "work", "stop", false)
	testutil.Eventually(t, "the alerter to be used with no backend subscriber", func() bool { return alerter.count() == 1 })
	if _, ok := notificationEffect(h, "dispatch"); !ok {
		t.Fatal("no dispatch disposition recorded")
	}
}

// TestAppNotificationRetractedOnSupersede: a newer event for the window sends
// notification_retract for the notice still showing.
func TestAppNotificationRetractedOnSupersede(t *testing.T) {
	t.Parallel()

	sock, h, _ := newNotifyingTestServer(t)
	sub := subscribeBackend(t, sock, "fake-app")

	h.notifier.MaybeNotify(7, "W:api", "work", "stop", false)
	first := nextNotice(t, sub)
	dispose(h, first.NotificationID, protocol.DispositionDelivered)

	h.notifier.MaybeNotify(7, "W:api", "work", "failure", false)
	frame := nextFrameOfType(t, sub, protocol.FrameNotificationRetract)
	var retract protocol.NotificationRetract
	if err := json.Unmarshal(frame.Data, &retract); err != nil {
		t.Fatalf("decode retract: %v", err)
	}
	if retract.NotificationID != first.NotificationID || retract.Reason != "superseded" {
		t.Fatalf("retract = %+v; want %s superseded", retract, first.NotificationID)
	}
	second := nextNotice(t, sub)
	if second.Action != "failure" {
		t.Fatalf("second notice = %+v", second)
	}
	// A late report about the retracted notice is accepted and ignored.
	if resp := dispose(h, first.NotificationID, protocol.DispositionClicked); !resp.OK {
		t.Fatalf("late disposition for a retracted notice: %+v", resp)
	}
	dispose(h, second.NotificationID, protocol.DispositionDismissed)
}

// TestNotificationDispositionIsIdempotent: delivered once, the first terminal
// disposition wins, and reports about unknown notifications are ok.
func TestNotificationDispositionIsIdempotent(t *testing.T) {
	t.Parallel()

	sock, h, _ := newNotifyingTestServer(t)
	sub := subscribeBackend(t, sock, "fake-app")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interactions, err := h.companion.Deliver(ctx, notification.AppNotice{NotificationID: "n-test", Action: "stop"})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	nextNotice(t, sub)

	for _, disposition := range []string{
		protocol.DispositionDelivered,
		protocol.DispositionDelivered,
		protocol.DispositionClicked,
		protocol.DispositionDismissed,
		protocol.DispositionFailed,
	} {
		if resp := dispose(h, "n-test", disposition); !resp.OK {
			t.Fatalf("%s: %+v", disposition, resp)
		}
	}
	var got []string
	for len(interactions) > 0 {
		got = append(got, (<-interactions).Disposition)
	}
	if len(got) != 2 || got[0] != protocol.DispositionDelivered || got[1] != protocol.DispositionClicked {
		t.Fatalf("interactions = %v; want delivered then the first terminal (clicked)", got)
	}
	if resp := dispose(h, "n-unknown", protocol.DispositionClicked); !resp.OK {
		t.Fatalf("unknown id: %+v", resp)
	}
}

// TestNotificationDispositionTerminalImpliesDelivered: a click that overtakes
// its delivered report still counts as a delivery.
func TestNotificationDispositionTerminalImpliesDelivered(t *testing.T) {
	t.Parallel()

	sock, h, _ := newNotifyingTestServer(t)
	subscribeBackend(t, sock, "fake-app")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interactions, err := h.companion.Deliver(ctx, notification.AppNotice{NotificationID: "n-click"})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	dispose(h, "n-click", protocol.DispositionClicked)
	if a, b := (<-interactions).Disposition, (<-interactions).Disposition; a != protocol.DispositionDelivered || b != protocol.DispositionClicked {
		t.Fatalf("interactions = %s, %s", a, b)
	}
}

func TestNotificationDispositionRejectsMalformedRequests(t *testing.T) {
	t.Parallel()

	_, h, _ := newNotifyingTestServer(t)
	for name, req := range map[string]protocol.Request{
		"no block":       {Type: protocol.TypeNotificationDisposition},
		"no id":          {Type: protocol.TypeNotificationDisposition, Disposition: &protocol.DispositionRequest{Disposition: "clicked"}},
		"no disposition": {Type: protocol.TypeNotificationDisposition, Disposition: &protocol.DispositionRequest{NotificationID: "n-1"}},
		"unknown value":  {Type: protocol.TypeNotificationDisposition, Disposition: &protocol.DispositionRequest{NotificationID: "n-1", Disposition: "exploded"}},
	} {
		if resp := h.Handle(req); resp.OK {
			t.Errorf("%s: accepted %+v", name, resp)
		}
	}
}

// TestNewestBackendClaimantHoldsTheRole: the newest authorized claimant gets
// notifications, and the previous one inherits the role when it leaves; a
// delivery bound to the leaver ends instead of moving.
func TestNewestBackendClaimantHoldsTheRole(t *testing.T) {
	t.Parallel()

	sock, h, _ := newNotifyingTestServer(t)
	older := subscribeBackend(t, sock, "older")
	newer := subscribeBackend(t, sock, "newer")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interactions, err := h.companion.Deliver(ctx, notification.AppNotice{NotificationID: "n-newer"})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if got := nextNotice(t, newer); got.NotificationID != "n-newer" {
		t.Fatalf("newer got %+v", got)
	}

	_ = newer.Close()
	select {
	case in := <-interactions:
		if in.Disposition != notification.AppGone {
			t.Fatalf("interaction = %+v; want %s", in, notification.AppGone)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a delivery bound to a departed subscriber never ended")
	}
	testutil.Eventually(t, "the departed subscriber to unregister", func() bool { return h.companionHealth().Subscribers == 1 })
	if !h.companion.Ready() {
		t.Fatal("the remaining claimant did not inherit the role")
	}
	if _, err := h.companion.Deliver(ctx, notification.AppNotice{NotificationID: "n-older"}); err != nil {
		t.Fatalf("Deliver after handover: %v", err)
	}
	if got := nextNotice(t, older); got.NotificationID != "n-older" {
		t.Fatalf("older got %+v", got)
	}

	_ = older.Close()
	testutil.Eventually(t, "the role to end with its last claimant", func() bool { return !h.companion.Ready() })
	if _, err := h.companion.Deliver(ctx, notification.AppNotice{NotificationID: "n-none"}); err == nil {
		t.Fatal("Deliver succeeded with no backend")
	}
}

func TestNotificationTestRequestReportsTheApp(t *testing.T) {
	t.Parallel()

	sock, h, _ := newNotifyingTestServer(t)
	if resp := h.Handle(protocol.Request{Type: protocol.TypeNotificationTest}); !resp.OK || resp.NotificationTest == nil || resp.NotificationTest.Delivered || resp.NotificationTest.Reason != "app_unavailable" {
		t.Fatalf("without an app: %+v", resp)
	}

	sub := subscribeBackend(t, sock, "fake-app")
	go func() {
		frame, err := sub.Next()
		for err == nil && frame.Type != protocol.FrameNotification {
			frame, err = sub.Next()
		}
		if err != nil {
			return
		}
		var notice notification.AppNotice
		if json.Unmarshal(frame.Data, &notice) == nil {
			dispose(h, notice.NotificationID, protocol.DispositionDelivered)
		}
	}()
	result, err := ipc.NewClientWithSocket(sock).NotificationTest()
	if err != nil {
		t.Fatalf("NotificationTest: %v", err)
	}
	if !result.Delivered || result.Backend != notification.BackendApp {
		t.Fatalf("result = %+v; want delivered by the app", result)
	}
}
