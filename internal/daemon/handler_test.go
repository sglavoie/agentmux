package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
)

func newTestHandler() *Handler {
	guard := NewStateGuard(time.Hour)
	notifier := NewNotifier(&config.NotificationConfig{})
	notifier.osascript = func(_, _ string) error { return nil }
	return NewHandler(guard, notifier)
}

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

func TestHandler_StateUpdateAllowed(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active"})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("want {ok:true, allowed:true, icon_state:bolt}, got %+v", resp)
	}
}

func TestHandler_StateUpdateBlocked(t *testing.T) {
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "failure"})

	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active"})
	if !resp.OK || resp.Allowed {
		t.Fatalf("want {ok:true, allowed:false}, got %+v", resp)
	}
}

func TestHandler_AllActionIconStates(t *testing.T) {
	cases := []struct {
		action    string
		iconState string
	}{
		{"active", "bolt"},
		{"stop", "stop"},
		{"permission", "key"},
		{"failure", "error"},
		{"idle", ""},
		{"clear", ""},
	}
	for _, tc := range cases {
		h := newTestHandler()
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: tc.action})
		if !resp.OK || !resp.Allowed || resp.IconState != tc.iconState {
			t.Errorf("action %q: want icon_state=%q, got %+v", tc.action, tc.iconState, resp)
		}
	}
}

func TestHandler_PreserveCurrentIcon(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", PreservesIcon: true})
	if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
		t.Errorf("PreservesIcon: want icon_state=%q, got %+v", IconStatePreserve, resp)
	}
}

func TestHandler_PromptBypassesStopGuard(t *testing.T) {
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop"})

	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", IsPrompt: true})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("want {ok:true, allowed:true, icon_state:bolt} for prompt bypass, got %+v", resp)
	}
}

func TestHandler_StateQuery(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: TypeStateQuery})
	if !resp.OK {
		t.Fatalf("want ok=true for state_query, got %+v", resp)
	}
}

func TestHandler_Shutdown(t *testing.T) {
	h := newTestHandler()
	called := make(chan struct{}, 1)
	h.onShutdown = func() { called <- struct{}{} }

	resp := h.Handle(Request{Type: TypeShutdown})
	if !resp.OK {
		t.Fatalf("want ok=true for shutdown, got %+v", resp)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("onShutdown not called within 1s")
	}
}

func TestHandler_OnActivityCalled(t *testing.T) {
	h := newTestHandler()
	called := 0
	h.onActivity = func() { called++ }

	h.Handle(Request{Type: TypeStateQuery})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active"})

	if called != 2 {
		t.Fatalf("expected onActivity called 2 times, got %d", called)
	}
}
