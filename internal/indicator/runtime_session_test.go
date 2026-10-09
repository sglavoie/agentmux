package indicator

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

const testRuntimeSession = "rs-0123456789abcdef0123456789abcdef"

func deliverStop(t *testing.T, client *fakeDaemonClient, recorder *deliveryRecorder) DeliveryResult {
	t.Helper()
	return deliverPayloadWithDeps(
		"Stop", 0, "claude", "/tmp/amx", `{}`, false, nil,
		"trace-1", "request-1", "", 2, "", "json", false, false, client,
		recorder, time.Now(),
	)
}

func TestManagedHookRoutesByRuntimeSessionOutsideKitty(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv(runtimesession.EnvVar, testRuntimeSession)
	client := &fakeDaemonClient{resp: protocol.Response{OK: true}}

	result := deliverStop(t, client, &deliveryRecorder{traceID: "trace-1", requestID: "request-1", event: "Stop"})
	if result.Status != DeliveryAccepted || client.calls != 1 {
		t.Fatalf("managed delivery outside Kitty = %+v (calls %d); want accepted", result, client.calls)
	}
	req := client.reqs[0]
	if req.RuntimeSessionID != testRuntimeSession || req.WindowID != 0 || req.KittyListenOn != "" {
		t.Fatalf("managed request identity = runtime %q window %d listen %q", req.RuntimeSessionID, req.WindowID, req.KittyListenOn)
	}
}

func TestManagedHookIgnoresInheritedKittyWindow(t *testing.T) {
	// A tmux server started from a Kitty window hands that window's variables
	// to every pane it spawns. They must not travel as routing identity.
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	t.Setenv("KITTY_WINDOW_ID", "7")
	client := &fakeDaemonClient{resp: protocol.Response{OK: true}}
	recorder := &deliveryRecorder{traceID: "trace-1", requestID: "request-1", event: "Stop", runtimeSessionID: testRuntimeSession}

	if result := deliverStop(t, client, recorder); result.Status != DeliveryAccepted {
		t.Fatalf("result = %+v", result)
	}
	if req := client.reqs[0]; req.RuntimeSessionID != testRuntimeSession || req.WindowID != 0 || req.KittyListenOn != "" {
		t.Fatalf("managed request carried Kitty identity: window %d listen %q", req.WindowID, req.KittyListenOn)
	}
}

func TestMalformedRuntimeSessionIsPermanentWithoutDelivery(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	t.Setenv("KITTY_WINDOW_ID", "7")
	t.Setenv(runtimesession.EnvVar, "rs-not-a-runtime-session")
	client := &fakeDaemonClient{resp: protocol.Response{OK: true}}

	result := deliverStop(t, client, &deliveryRecorder{traceID: "trace-1", requestID: "request-1", event: "Stop"})
	if result.Status != DeliveryPermanentFailure || result.Reason != "invalid_runtime_session" || client.calls != 0 {
		t.Fatalf("malformed runtime session = %+v (calls %d); want permanent invalid_runtime_session, no send", result, client.calls)
	}
}

func TestLegacyKittyHookIsUnchangedWithoutRuntimeSession(t *testing.T) {
	t.Setenv(runtimesession.EnvVar, "")
	t.Setenv("KITTY_LISTEN_ON", "")
	client := &fakeDaemonClient{resp: protocol.Response{OK: true}}
	if result := deliverStop(t, client, &deliveryRecorder{}); result.Reason != "outside_kitty" || client.calls != 0 {
		t.Fatalf("legacy hook outside Kitty = %+v; want outside_kitty", result)
	}

	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	t.Setenv("KITTY_WINDOW_ID", "7")
	if result := deliverStop(t, client, &deliveryRecorder{}); result.Status != DeliveryAccepted {
		t.Fatalf("legacy hook in Kitty = %+v", result)
	}
	if req := client.reqs[0]; req.RuntimeSessionID != "" || req.WindowID != 7 || req.KittyListenOn == "" {
		t.Fatalf("legacy request = runtime %q window %d listen %q", req.RuntimeSessionID, req.WindowID, req.KittyListenOn)
	}
}

func TestDaemonRuntimeRefusalsMapToDeliveryStatus(t *testing.T) {
	t.Setenv(runtimesession.EnvVar, testRuntimeSession)
	for _, tc := range []struct {
		reason    string
		retryable bool
		status    DeliveryStatus
	}{
		{protocol.OriginReasonRuntimeSessionPending, true, DeliveryRetryableFailure},
		{protocol.OriginReasonRuntimeSessionEnded, false, DeliveryPermanentFailure},
		{protocol.OriginReasonTargetConflict, false, DeliveryPermanentFailure},
	} {
		client := &fakeDaemonClient{resp: protocol.Response{
			OK: false, Error: tc.reason, Retryable: tc.retryable,
			Delivery: &protocol.DeliveryAcknowledgement{Status: protocol.DeliveryAcknowledgementRejected, Reason: tc.reason},
		}}
		result := deliverStop(t, client, &deliveryRecorder{})
		if result.Status != tc.status || result.Reason != tc.reason {
			t.Fatalf("%s: result = %+v; want %s", tc.reason, result, tc.status)
		}
	}
}
