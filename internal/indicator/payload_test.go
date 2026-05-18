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

func TestDecodeHookPayload_ParsesReasonFields(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{
		"hook_event_name": "SessionEnd",
		"reason": "error",
		"stop_reason": "context_limit_reached"
	}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(reason fields) error: %v", err)
	}
	if payload.Reason != "error" {
		t.Fatalf("Reason = %q, want error", payload.Reason)
	}
	if payload.EffectiveStopReason() != "context_limit_reached" {
		t.Fatalf("EffectiveStopReason = %q, want context_limit_reached", payload.EffectiveStopReason())
	}
}

func TestDecodeHookPayload_InvalidJSON(t *testing.T) {
	if _, err := DecodeHookPayload(strings.NewReader("{")); err == nil {
		t.Fatal("DecodeHookPayload(invalid JSON) error = nil, want non-nil")
	}
}

// TestDecodeHookPayload_RejectsOversizedInput pins the upper bound on the
// hook payload reader. The daemon already caps requests at 1 MiB and a hook
// script feeding multi-GiB JSON would otherwise force the indicator process
// to buffer the whole stream before that limit could trigger.
func TestDecodeHookPayload_RejectsOversizedInput(t *testing.T) {
	// Craft a payload that is just over the limit by padding the "message"
	// field. Even though the JSON is structurally valid, the byte size
	// alone should trigger rejection before we attempt to unmarshal.
	padding := strings.Repeat("a", maxHookPayloadBytes)
	oversized := `{"message":"` + padding + `"}`
	if len(oversized) <= maxHookPayloadBytes {
		t.Fatalf("test setup: oversized payload (%d bytes) not above limit (%d)", len(oversized), maxHookPayloadBytes)
	}
	if _, err := DecodeHookPayload(strings.NewReader(oversized)); err == nil {
		t.Fatal("DecodeHookPayload(oversized) error = nil, want non-nil")
	}
}

// TestDecodeHookPayload_AcceptsLargeButValidInput is the matching positive
// case: a payload right at the edge of the limit must still parse, so the
// rejection threshold does not creep down accidentally.
func TestDecodeHookPayload_AcceptsLargeButValidInput(t *testing.T) {
	// Pick a message that, together with the surrounding JSON skeleton,
	// totals less than maxHookPayloadBytes by a comfortable margin.
	padding := strings.Repeat("b", maxHookPayloadBytes-64)
	body := `{"message":"` + padding + `"}`
	if len(body) > maxHookPayloadBytes {
		t.Fatalf("test setup: body (%d) exceeds limit (%d)", len(body), maxHookPayloadBytes)
	}
	p, err := DecodeHookPayload(strings.NewReader(body))
	if err != nil {
		t.Fatalf("DecodeHookPayload(near-limit) error: %v", err)
	}
	if len(p.Message) != len(padding) {
		t.Fatalf("Message len = %d, want %d", len(p.Message), len(padding))
	}
}
