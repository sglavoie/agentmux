package indicator

import (
	"strings"
	"testing"
)

func TestDecodeHookPayload_Empty(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(" \n\t "))
	if err != nil {
		t.Fatalf("DecodeHookPayload(empty) error: %v", err)
	}
	if payload != (HookPayload{}) {
		t.Fatalf("DecodeHookPayload(empty) = %+v, want zero payload", payload)
	}
}

func TestDecodeHookPayload_ParsesNotificationFields(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{
		"hook_event_name": "Notification",
		"notification_type": "agent_idle",
		"title": "Claude finished",
		"message": "Waiting for input"
	}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(notification) error: %v", err)
	}
	if payload.HookEventName != "Notification" {
		t.Fatalf("HookEventName = %q, want Notification", payload.HookEventName)
	}
	if payload.EffectiveNotificationType() != "agent_idle" {
		t.Fatalf("EffectiveNotificationType = %q, want agent_idle", payload.EffectiveNotificationType())
	}
	if payload.Title != "Claude finished" || payload.Message != "Waiting for input" {
		t.Fatalf("unexpected decoded notification payload: %+v", payload)
	}
}

func TestDecodeHookPayload_InvalidJSON(t *testing.T) {
	if _, err := DecodeHookPayload(strings.NewReader("{")); err == nil {
		t.Fatal("DecodeHookPayload(invalid JSON) error = nil, want non-nil")
	}
}
