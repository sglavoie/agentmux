package ipc

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestNotificationSnoozeRoundTrip(t *testing.T) {
	socket, requests := serveOneResponse(t, mustEncodeResponse(t, protocol.Response{OK: true, Snooze: &protocol.SnoozeState{SnoozedUntil: 1700000000}}))
	state, err := NewClientWithSocket(socket).NotificationSnooze(protocol.SnoozeRequest{Mode: protocol.SnoozeModeUntil, Until: 1700000000})
	if err != nil {
		t.Fatal(err)
	}
	if state.SnoozedUntil != 1700000000 {
		t.Fatalf("state = %+v", state)
	}
	req := <-requests
	if req.Type != protocol.TypeNotificationSnooze || req.Snooze == nil || req.Snooze.Mode != protocol.SnoozeModeUntil || req.Snooze.Until != 1700000000 {
		t.Fatalf("request = %+v", req)
	}
}

func TestNotificationSnoozeReportsRefusalAndOldDaemon(t *testing.T) {
	socket, _ := serveOneResponse(t, mustEncodeResponse(t, protocol.Response{OK: false, Error: "notification_snooze: unknown snooze mode \"x\""}))
	if _, err := NewClientWithSocket(socket).NotificationSnooze(protocol.SnoozeRequest{Mode: "x"}); err == nil || !strings.Contains(err.Error(), "unknown snooze mode") {
		t.Fatalf("refusal error = %v", err)
	}
	socket, _ = serveOneResponse(t, mustEncodeResponse(t, protocol.Response{OK: true}))
	if _, err := NewClientWithSocket(socket).NotificationSnooze(protocol.SnoozeRequest{}); err == nil || !strings.Contains(err.Error(), "older daemon") {
		t.Fatalf("missing-block error = %v", err)
	}
}
