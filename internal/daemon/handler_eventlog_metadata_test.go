package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestHandler_EventsExport_PreservesMetadataAfterClosedWindowGC(t *testing.T) {
	h, fake, _ := newSweepTestHandler(0)
	fake.mu.Lock()
	fake.windows = fakeOSWindowsWithTab(1, "W:api")
	fake.mu.Unlock()

	h.Handle(protocol.Request{
		Type:         protocol.TypeStateUpdate,
		WindowID:     1,
		Action:       "active",
		Event:        "PreToolUse",
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "copilot",
	})

	fake.mu.Lock()
	fake.windows = nil
	fake.mu.Unlock()
	h.SweepStale(time.Now())

	resp := h.Handle(protocol.Request{Type: protocol.TypeEventsExport})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if len(resp.EventsByWindow) != 0 {
		t.Fatalf("EventsByWindow = %+v; want empty after closed-window GC", resp.EventsByWindow)
	}
	meta, ok := resp.WindowMetadata[1]
	if !ok {
		t.Fatalf("WindowMetadata[1] missing after closed-window GC; got %+v", resp.WindowMetadata)
	}
	if meta.Stats.EventCount != 1 {
		t.Fatalf("event_count after closed-window GC = %d; want 1", meta.Stats.EventCount)
	}
	if meta.Durable.SessionTitle != "W:api" || meta.Durable.Profile != "work" || meta.Durable.Harness != "copilot" {
		t.Fatalf("durable metadata after closed-window GC = %+v", meta.Durable)
	}
}
