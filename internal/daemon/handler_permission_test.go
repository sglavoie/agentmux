package daemon

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestHandler_AutoApprovedPermission verifies that when permissionRequest fires
// while an active guard is still live and it was set by PreToolUse (the
// auto-approved pattern), the key icon does NOT override the bolt, and a
// subsequent idle clears the icon rather than preserving the key.
// When the active guard was set by PostToolUse (a prior tool completed), or
// when the guard has already expired, permissionRequest follows the normal path
// and shows the key icon.
func TestHandler_AutoApprovedPermission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// --- Case 1: PreToolUse guard live → permission is auto-approved ---
		h := newTestHandler()
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})

		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission"})
		if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
			t.Fatalf("auto-approved permission (PreToolUse guard live): want icon_state=%q, got %+v", IconStatePreserve, resp)
		}

		// idle after auto-approved permission should clear (not preserve key).
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "idle"})
		if !resp.OK || !resp.Allowed || resp.IconState != "" {
			t.Fatalf("idle after auto-approved permission: want icon_state=\"\", got %+v", resp)
		}

		// --- Case 2: active guard expired → manual-permission path, key shows ---
		guard := NewStateGuard(time.Nanosecond)
		notifier := NewNotifier(&config.NotificationConfig{})
		notifier.dispatch = func(_ context.Context, _ int, _, _, _ string) (bool, error) { return false, nil }
		h2 := NewHandler(guard, notifier)

		h2.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
		time.Sleep(5 * time.Millisecond) // let the guard expire

		resp = h2.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission"})
		if !resp.OK || !resp.Allowed || resp.IconState != "key" {
			t.Fatalf("manual permission (guard expired): want icon_state=\"key\", got %+v", resp)
		}

		// --- Case 3: PostToolUse guard live → NOT auto-approved, key must show ---
		// This is the bug scenario: Tool A's PostToolUse renews the active guard,
		// then Tool B's PermissionRequest fires while that guard is still live.
		// The key icon must appear because the user needs to approve Tool B.
		h3 := newTestHandler()
		h3.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostToolUse"})

		resp = h3.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission"})
		if !resp.OK || !resp.Allowed || resp.IconState != "key" {
			t.Fatalf("manual permission (PostToolUse guard live): want icon_state=\"key\", got %+v", resp)
		}

		// --- Case 4: Codex PreToolUse → PermissionRequest is a real prompt ---
		// The Claude-only auto-approval heuristic must not apply to Codex, whose
		// normal real-permission flow is PreToolUse followed by PermissionRequest.
		h4 := newTestHandler()
		h4.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Harness: "codex", Action: "active", Event: "PreToolUse"})

		resp = h4.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Harness: "codex", Action: "permission", Event: "PermissionRequest"})
		if !resp.OK || !resp.Allowed || resp.IconState != "key" {
			t.Fatalf("codex permission (PreToolUse guard live): want icon_state=\"key\", got %+v", resp)
		}
	})
}
