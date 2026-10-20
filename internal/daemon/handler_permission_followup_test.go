package daemon

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestHandler_CopilotAutoApprovedPermissionSuppressesNotification verifies that
// Copilot's preToolUse → permissionRequest → postToolUse sequence does NOT fire
// a "Permission requested" notification. Copilot emits permissionRequest as a
// speculative pre-tool hook even when no user input is required, which made the
// debounce timer fire 500 ms later despite the bolt icon already showing.
//
// The companion test TestHandler_PermissionSurvivesActiveFollowUp pins that
// Claude's PascalCase PreToolUse → permission → PostToolUse sequence still
// fires its notification (real user-facing prompt). Event-name casing is the
// harness signal that separates the two.
func TestHandler_CopilotAutoApprovedPermissionSuppressesNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := activity.NewStateGuard(time.Hour)
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 100}
		notifier := notification.NewNotifier(cfg)
		var calls []string
		notifier.SetBackend(func(_ context.Context, _ int, _, _, subtitle string) (bool, error) {
			calls = append(calls, subtitle)
			return false, nil
		})
		h := NewHandler(guard, notifier)

		h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", Event: "preToolUse"})
		h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "permission", Event: "permissionRequest"})
		h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", Event: "postToolUse"})

		// Advance fake time well past the debounce window; a leaked AfterFunc
		// would fire here, and synctest.Wait then drains its dispatch goroutine.
		time.Sleep(time.Second)
		synctest.Wait()

		for _, c := range calls {
			if c == "Permission requested" {
				t.Fatalf("Copilot auto-approved permission should not notify, got %v", calls)
			}
		}
	})
}

func TestHandler_CopilotLatePermissionPromptAfterAutoApprovedToolPreservesBolt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := activity.NewStateGuard(time.Hour)
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 100}
		notifier := notification.NewNotifier(cfg)
		var calls []string
		notifier.SetBackend(func(_ context.Context, _ int, _, _, subtitle string) (bool, error) {
			calls = append(calls, subtitle)
			return false, nil
		})
		h := NewHandler(guard, notifier)

		h.Handle(protocol.Request{
			Type:     protocol.TypeStateUpdate,
			WindowID: 1,
			Harness:  "copilot",
			Action:   "active",
			Event:    "postToolUse",
		})

		resp := h.Handle(protocol.Request{
			Type:     protocol.TypeStateUpdate,
			WindowID: 1,
			Harness:  "copilot",
			Action:   "permission",
			Event:    "permissionRequest",
		})
		if !resp.OK || !resp.Allowed || resp.IconState != protocol.IconStatePreserve {
			t.Fatalf("Copilot permissionRequest should preserve the bolt while permission is still ambiguous, got %+v", resp)
		}

		resp = h.Handle(protocol.Request{
			Type:     protocol.TypeStateUpdate,
			WindowID: 1,
			Harness:  "copilot",
			Action:   "active",
			Event:    "postToolUse",
		})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("Copilot postToolUse after auto-approved prompt: want bolt, got %+v", resp)
		}

		resp = h.Handle(protocol.Request{
			Type:             protocol.TypeStateUpdate,
			WindowID:         1,
			Harness:          "copilot",
			Action:           "permission",
			Event:            "notification",
			NotificationType: "permission_prompt",
		})
		if !resp.OK || !resp.Allowed || resp.IconState != protocol.IconStatePreserve {
			t.Fatalf("late Copilot permission_prompt after auto-approved tool use: want preserve, got %+v", resp)
		}

		resp = h.Handle(protocol.Request{
			Type:     protocol.TypeStateUpdate,
			WindowID: 1,
			Harness:  "copilot",
			Action:   "stop",
			Event:    "agentStop",
		})
		if !resp.OK || !resp.Allowed || resp.IconState != "stop" {
			t.Fatalf("agentStop after suppressed late permission prompt: want stop, got %+v", resp)
		}

		time.Sleep(time.Second)
		synctest.Wait()

		for _, c := range calls {
			if c == "Permission requested" {
				t.Fatalf("late auto-approved Copilot permission_prompt should not notify, got %v", calls)
			}
		}
	})
}

func TestHandler_CopilotPermissionPromptAfterPrePermissionSignalShowsKey(t *testing.T) {
	t.Parallel()

	h := newTestHandler()

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Harness:  "copilot",
		Action:   "active",
		Event:    "postToolUse",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("setup postToolUse: want bolt, got %+v", resp)
	}

	resp = h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Harness:  "copilot",
		Action:   "permission",
		Event:    "permissionRequest",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != protocol.IconStatePreserve {
		t.Fatalf("Copilot permissionRequest should preserve bolt until a real prompt appears, got %+v", resp)
	}

	resp = h.Handle(protocol.Request{
		Type:             protocol.TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "permission",
		Event:            "notification",
		NotificationType: "permission_prompt",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "key" {
		t.Fatalf("Copilot permission_prompt without auto-approved flow: want key, got %+v", resp)
	}
}

func TestHandler_PermissionCancelledByTerminalAction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := activity.NewStateGuard(time.Hour)
		cfg := &config.NotificationConfig{Permission: true, Stop: true, PermissionDebounceMs: 100}
		notifier := notification.NewNotifier(cfg)
		var mu sync.Mutex
		var calls []string
		notifier.SetBackend(func(_ context.Context, _ int, _, _, subtitle string) (bool, error) {
			mu.Lock()
			calls = append(calls, subtitle)
			mu.Unlock()
			return false, nil
		})
		h := NewHandler(guard, notifier)

		h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "permission"})
		time.Sleep(20 * time.Millisecond)
		h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "stop"})

		time.Sleep(time.Second)
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range calls {
			if c == "Permission requested" {
				t.Fatalf("expected stop to cancel pending permission, got %v", calls)
			}
		}
	})
}

// TestHandler_PermissionNotificationFiresWhenGuardBlocks verifies that when a
// higher-priority guard (failure, p=4) blocks a permission event (p=3), the
// icon transition is suppressed but the user-facing notification still fires —
// the guard only governs visual state, not whether the user gets alerted.
func TestHandler_PermissionNotificationFiresWhenGuardBlocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := activity.NewStateGuard(time.Hour)
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 50}
		notifier := notification.NewNotifier(cfg)
		var mu sync.Mutex
		var calls []string
		notifier.SetBackend(func(_ context.Context, _ int, _, _, subtitle string) (bool, error) {
			mu.Lock()
			calls = append(calls, subtitle)
			mu.Unlock()
			return false, nil
		})
		h := NewHandler(guard, notifier)

		// Prime with failure so the guard sits at priority 4.
		h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "failure"})

		// Permission is blocked by the failure guard.
		resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "permission"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("permission blocked by failure guard: want allowed=false, got %+v", resp)
		}

		time.Sleep(time.Second)
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range calls {
			if c == "Permission requested" {
				return
			}
		}
		t.Fatalf("expected permission notification to fire when guard blocks transition, got %v", calls)
	})
}
