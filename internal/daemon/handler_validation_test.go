package daemon

import (
	"testing"
)

func TestHandler_UnknownType(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: "bogus"})
	if resp.OK {
		t.Fatal("expected ok=false for unknown type")
	}
}

func TestHandler_MissingAction(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: TypeStateUpdate})
	if resp.OK {
		t.Fatal("expected ok=false when action is missing")
	}
}

func TestHandler_UnknownAction(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "nope"})
	if resp.OK {
		t.Fatal("expected ok=false for unknown action")
	}
}
