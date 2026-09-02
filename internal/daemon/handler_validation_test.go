package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestHandler_UnknownType(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(protocol.Request{Type: "bogus"})
	if resp.OK {
		t.Fatal("expected ok=false for unknown type")
	}
}

func TestHandler_StateUpdateWithoutNotifier(t *testing.T) {
	h := NewHandler(activity.NewStateGuard(time.Hour), nil)

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Action:   "stop",
		Event:    "Stop",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "stop" {
		t.Fatalf("state_update without notifier = %+v; want ok allowed stop icon", resp)
	}
}

func TestHandler_MissingAction(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1})
	if resp.OK {
		t.Fatal("expected ok=false when action is missing")
	}
}

func TestHandler_InvalidWindowID(t *testing.T) {
	tests := []struct {
		name     string
		windowID int
	}{
		{name: "zero", windowID: 0},
		{name: "negative", windowID: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler()
			resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: tt.windowID, Action: "active"})
			if resp.OK {
				t.Fatalf("expected ok=false for window_id %d", tt.windowID)
			}
		})
	}
}

func TestHandler_UnknownAction(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "nope"})
	if resp.OK {
		t.Fatal("expected ok=false for unknown action")
	}
}
