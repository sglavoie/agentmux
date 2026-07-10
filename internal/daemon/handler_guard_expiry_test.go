package daemon

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestHandler_ActiveBlockedAfterStopGuardExpiry verifies that a non-prompt
// "active" event (e.g. PostToolBatch arriving late due to async execution) cannot
// revive the bolt after Stop, even after the stop guard has expired. An explicit
// prompt event must still be able to break through.
func TestHandler_ActiveBlockedAfterStopGuardExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Nanosecond) // expires immediately
		notifier := NewNotifier(&config.NotificationConfig{})
		notifier.dispatch = func(_ context.Context, _ int, _, _, _ string) (bool, error) { return false, nil }
		h := NewHandler(guard, notifier)

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop"})
		time.Sleep(5 * time.Millisecond) // let the guard expire

		// Non-prompt active (e.g. PostToolBatch) must not revive the bolt.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("non-prompt active after expired stop guard: want allowed=false, got %+v", resp)
		}

		// A prompt event (UserPromptSubmit) must still break through.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", IsPrompt: true})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("prompt active after expired stop guard: want icon_state=bolt, got %+v", resp)
		}
	})
}

func TestHandler_ActiveBlockedByCurrentStopIconAfterDaemonRestart(t *testing.T) {
	h := newTestHandler()
	seedRestoredAction(t, h, "stop")

	resp := h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
	})
	if !resp.OK || resp.Allowed {
		t.Fatalf("non-prompt active with current stop icon: want allowed=false, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
		IsPrompt: true,
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("prompt active with current stop icon: want icon_state=bolt, got %+v", resp)
	}
}

// TestHandler_ActiveBlockedAfterStopFailureGuardExpiry verifies that stop_failure
// also permanently blocks non-prompt active events after guard expiry, since it is
// a terminal state. Prompts still break through so the user can retry.
func TestHandler_ActiveBlockedAfterStopFailureGuardExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Nanosecond)
		notifier := NewNotifier(&config.NotificationConfig{})
		notifier.dispatch = func(_ context.Context, _ int, _, _, _ string) (bool, error) { return false, nil }
		h := NewHandler(guard, notifier)

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop_failure"})
		time.Sleep(5 * time.Millisecond)

		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("non-prompt active after expired stop_failure guard: want allowed=false, got %+v", resp)
		}

		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", IsPrompt: true})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("prompt active after expired stop_failure guard: want icon_state=bolt, got %+v", resp)
		}
	})
}

func TestHandler_ActiveAllowedByCurrentWarningIconAfterDaemonRestart(t *testing.T) {
	h := newTestHandler()
	seedRestoredAction(t, h, "failure")

	resp := h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("non-prompt active with current warning icon: want icon_state=bolt allowed=true, got %+v", resp)
	}
}

// TestHandler_ActiveBlockedByCurrentErrorIconAfterDaemonRestart pins that a
// fresh daemon restart preserving the ❗ glyph in the tab title reconstructs a
// terminal stop_failure, so a benign non-prompt "active" event cannot clear an
// unacknowledged stop failure.
func TestHandler_ActiveBlockedByCurrentErrorIconAfterDaemonRestart(t *testing.T) {
	h := newTestHandler()
	seedRestoredAction(t, h, "stop_failure")

	resp := h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
	})
	if !resp.OK || resp.Allowed {
		t.Fatalf("non-prompt active with current error icon: want allowed=false, got %+v", resp)
	}

	// A prompt event still breaks through.
	resp = h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
		IsPrompt: true,
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("prompt active with current error icon: want icon_state=bolt, got %+v", resp)
	}
}

func seedRestoredAction(t *testing.T, h *Handler, action string) {
	t.Helper()
	h.stateMu.Lock()
	h.store[1] = &WindowState{LastAction: action, DesiredIcon: IconState(action)}
	h.stateMu.Unlock()
}
