package daemon

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestHandler_AutoApprovedPermission verifies that a Claude/default
// permissionRequest fired while an active PreToolUse guard is still live is a
// real, user-blocking prompt: it surfaces the key icon immediately. After the
// user approves, PostToolUse (active) replaces the key with the bolt, and a
// subsequent idle clears the icon. When the active guard was set by PostToolUse
// (a prior tool completed), when the guard has already expired, or for Codex,
// permissionRequest likewise follows the normal path and shows the key icon.
func TestHandler_AutoApprovedPermission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// --- Case 1: PreToolUse guard live → real prompt, key must show ---
		h := newTestHandler()
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})

		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission"})
		if !resp.OK || !resp.Allowed || resp.IconState != "key" {
			t.Fatalf("permission (PreToolUse guard live): want icon_state=\"key\", got %+v", resp)
		}

		// After the user approves, PostToolUse (active) replaces key → bolt.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostToolUse"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("active after approved permission: want icon_state=\"bolt\", got %+v", resp)
		}

		// The tool completing (idle) then clears the icon.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "idle"})
		if !resp.OK || !resp.Allowed || resp.IconState != "" {
			t.Fatalf("idle after approved permission: want icon_state=\"\", got %+v", resp)
		}

		// --- Case 2: active guard expired → manual-permission path, key shows ---
		guard := NewStateGuard(time.Nanosecond)
		notifier := NewNotifier(&config.NotificationConfig{})
		notifier.dispatch = func(_ context.Context, _ int, _, _, _ string) (dispatchResult, error) {
			return deliveredResult(interactionUnavailable), nil
		}
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

// TestHandler_ClaudePermissionRequestSurfacesKey mirrors the real Claude daemon
// journal sequence captured for a user-blocking prompt: PreToolUse (active) →
// PermissionRequest (permission) while the active guard is still live. This used
// to be swallowed by the auto-approval heuristic (icon stayed "bolt"); it must
// now fold into the reducer as a real permission fact and surface 🔑. A later
// delayed Notification(permission_prompt) keeps the key, and PostToolUse (the
// user having approved) replaces it with the bolt.
func TestHandler_ClaudePermissionRequestSurfacesKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier := NewNotifier(&config.NotificationConfig{})
		notifier.dispatch = func(_ context.Context, _ int, _, _, _ string) (dispatchResult, error) {
			return deliveredResult(interactionUnavailable), nil
		}
		h := NewHandler(guard, notifier)

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})

		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission", Event: "PermissionRequest"})
		if !resp.OK || !resp.Allowed || resp.IconState != "key" {
			t.Fatalf("claude PermissionRequest (PreToolUse guard live): want icon_state=\"key\", got %+v", resp)
		}
		if eff, visible := windowEffective(t, h, 1); eff.Icon != "key" || visible != "key" {
			t.Fatalf("after PermissionRequest: reducer icon %q visible %q, want key/key", eff.Icon, visible)
		}

		// Claude's delayed Notification(permission_prompt) arrives ~6s later; it
		// must keep the key alive, not repaint it.
		resp = h.Handle(Request{
			Type:             TypeStateUpdate,
			WindowID:         1,
			Action:           "permission",
			Event:            "Notification",
			NotificationType: "permission_prompt",
		})
		if !resp.OK || !resp.Allowed {
			t.Fatalf("claude delayed permission notification: want allowed, got %+v", resp)
		}
		if eff, visible := windowEffective(t, h, 1); eff.Icon != "key" || visible != "key" {
			t.Fatalf("after delayed permission_prompt: reducer icon %q visible %q, want key/key", eff.Icon, visible)
		}

		// The user approves: PostToolUse (active) replaces key → bolt.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostToolUse"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("active after approved permission: want icon_state=\"bolt\", got %+v", resp)
		}
		if eff, visible := windowEffective(t, h, 1); eff.Icon != "bolt" || visible != "bolt" {
			t.Fatalf("after PostToolUse: reducer icon %q visible %q, want bolt/bolt", eff.Icon, visible)
		}
	})
}
