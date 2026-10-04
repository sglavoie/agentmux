package ipc

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestNotificationTestRoundTrip(t *testing.T) {
	socket, requests := serveOneResponse(t, mustEncodeResponse(t, protocol.Response{OK: true, NotificationTest: &protocol.NotificationTestResult{Backend: "app", Delivered: true}}))
	result, err := NewClientWithSocket(socket).NotificationTest()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Delivered || result.Backend != "app" {
		t.Fatalf("result = %+v", result)
	}
	if req := <-requests; req.Type != protocol.TypeNotificationTest {
		t.Fatalf("request = %+v", req)
	}
}

func TestNotificationTestReportsRefusalAndOldDaemon(t *testing.T) {
	socket, _ := serveOneResponse(t, mustEncodeResponse(t, protocol.Response{OK: false, Error: "unknown request type: notification_test"}))
	if _, err := NewClientWithSocket(socket).NotificationTest(); err == nil || !strings.Contains(err.Error(), "unknown request type") {
		t.Fatalf("refusal error = %v", err)
	}
	socket, _ = serveOneResponse(t, mustEncodeResponse(t, protocol.Response{OK: true}))
	if _, err := NewClientWithSocket(socket).NotificationTest(); err == nil || !strings.Contains(err.Error(), "older daemon") {
		t.Fatalf("missing-block error = %v", err)
	}
}
