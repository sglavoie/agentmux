package daemon

import (
	"testing"
	"time"
)

func TestHandler_UnknownType(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: "bogus"})
	if resp.OK {
		t.Fatal("expected ok=false for unknown type")
	}
}

func TestHandler_InvalidRequestDoesNotSignalActivity(t *testing.T) {
	tests := []struct {
		name string
		req  Request
	}{
		{name: "unknown type", req: Request{Type: "bogus"}},
		{name: "missing window ID", req: Request{Type: TypeStateUpdate, Action: "active"}},
		{name: "negative window ID", req: Request{Type: TypeStateUpdate, WindowID: -1, Action: "active"}},
		{name: "missing action", req: Request{Type: TypeStateUpdate}},
		{name: "unknown action", req: Request{Type: TypeStateUpdate, WindowID: 1, Action: "nope"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler()
			calls := 0
			h.onActivity = func() { calls++ }

			resp := h.Handle(tt.req)
			if resp.OK {
				t.Fatalf("expected invalid request, got %+v", resp)
			}
			if calls != 0 {
				t.Fatalf("onActivity calls = %d; want 0", calls)
			}
		})
	}
}

func TestHandler_ValidRequestSignalsActivity(t *testing.T) {
	h := newTestHandler()
	calls := 0
	h.onActivity = func() { calls++ }

	resp := h.Handle(Request{Type: TypeHealth})
	if !resp.OK {
		t.Fatalf("expected health request to succeed, got %+v", resp)
	}
	if calls != 1 {
		t.Fatalf("onActivity calls = %d; want 1", calls)
	}
}

func TestHandler_StateUpdateWithoutNotifier(t *testing.T) {
	h := NewHandler(NewStateGuard(time.Hour), nil)

	resp := h.Handle(Request{
		Type:     TypeStateUpdate,
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
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1})
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
			resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: tt.windowID, Action: "active"})
			if resp.OK {
				t.Fatalf("expected ok=false for window_id %d", tt.windowID)
			}
		})
	}
}

func TestHandler_UnknownAction(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "nope"})
	if resp.OK {
		t.Fatal("expected ok=false for unknown action")
	}
}
