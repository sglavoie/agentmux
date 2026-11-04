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

func TestManagedHookRoutesByRuntimeSession(t *testing.T) {
	t.Setenv(runtimesession.EnvVar, testRuntimeSession)
	client := &fakeDaemonClient{resp: protocol.Response{OK: true}}

	result := deliverStop(t, client, &deliveryRecorder{traceID: "trace-1", requestID: "request-1", event: "Stop"})
	if result.Status != DeliveryAccepted || client.calls != 1 {
		t.Fatalf("managed delivery = %+v (calls %d); want accepted", result, client.calls)
	}
	req := client.reqs[0]
	if req.RuntimeSessionID != testRuntimeSession || req.WindowID != 0 {
		t.Fatalf("managed request identity = runtime %q window %d", req.RuntimeSessionID, req.WindowID)
	}
}

func TestManagedHookIgnoresUnrelatedTerminalVariables(t *testing.T) {
	t.Setenv("UNRELATED_TERMINAL_ADDRESS", "unix:/tmp/terminal")
	client := &fakeDaemonClient{resp: protocol.Response{OK: true}}
	recorder := &deliveryRecorder{traceID: "trace-1", requestID: "request-1", event: "Stop", runtimeSessionID: testRuntimeSession}

	if result := deliverStop(t, client, recorder); result.Status != DeliveryAccepted {
		t.Fatalf("result = %+v", result)
	}
	if req := client.reqs[0]; req.RuntimeSessionID != testRuntimeSession || req.WindowID != 0 {
		t.Fatalf("managed request carried terminal identity: window %d", req.WindowID)
	}
}

func TestMalformedRuntimeSessionIsPermanentWithoutDelivery(t *testing.T) {
	t.Setenv(runtimesession.EnvVar, "rs-not-a-runtime-session")
	client := &fakeDaemonClient{resp: protocol.Response{OK: true}}

	result := deliverStop(t, client, &deliveryRecorder{traceID: "trace-1", requestID: "request-1", event: "Stop"})
	if result.Status != DeliveryPermanentFailure || result.Reason != "invalid_runtime_session" || client.calls != 0 {
		t.Fatalf("malformed runtime session = %+v (calls %d); want permanent invalid_runtime_session, no send", result, client.calls)
	}
}

func TestHookWithoutRuntimeSessionIsRejected(t *testing.T) {
	t.Setenv(runtimesession.EnvVar, "")
	client := &fakeDaemonClient{resp: protocol.Response{OK: true}}
	if result := deliverStop(t, client, &deliveryRecorder{}); result.Reason != "missing_runtime_session" || client.calls != 0 {
		t.Fatalf("unmanaged hook = %+v; want missing_runtime_session and no send", result)
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
