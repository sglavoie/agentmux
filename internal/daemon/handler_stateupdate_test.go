package daemon

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

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

	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission"})
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
		{"compact", "compact"},
		{"subagent", "subagent"},
		{"stop", "stop"},
		{"permission", "key"},
		{"failure", "warning"},
		{"stop_failure", "error"},
		{"elicitation", "question"},
		// idle and clear with no prior guard preserve whatever icon is already
		// in the title (nothing to clear).
		{"idle", IconStatePreserve},
		{"clear", IconStatePreserve},
		// user_clear is the TUI-initiated true wipe — icon goes empty.
		{"user_clear", ""},
	}
	for _, tc := range cases {
		h := newTestHandler()
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: tc.action})
		if !resp.OK || !resp.Allowed || resp.IconState != tc.iconState {
			t.Errorf("action %q: want icon_state=%q, got %+v", tc.action, tc.iconState, resp)
		}
	}
}

// TestHandler_IdleClearsActiveIcon verifies that an idle event clears the bolt
// when the previous guard was active/subagent/compact, but preserves stop/
// permission/failure icons (the user still needs to see them).
// Both the within-guard-window case (prevGuard set) and the expired-guard case
// (lastAction fallback) are covered.
func TestHandler_IdleClearsActiveIcon(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cases := []struct {
			prev      string
			wantIcon  string
			wantLabel string
		}{
			{"active", "", "idle after active clears bolt"},
			{"subagent", "", "idle after subagent clears icon"},
			{"compact", "", "idle after compact clears icon"},
			{"stop", IconStatePreserve, "idle after stop preserves stop icon"},
			{"permission", IconStatePreserve, "idle after permission preserves key icon"},
			{"failure", IconStatePreserve, "idle after failure preserves warning icon"},
		}
		// Test with a long guard (prevGuard still set when idle arrives).
		for _, tc := range cases {
			h := newTestHandler()
			h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: tc.prev})
			resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "idle"})
			if !resp.OK || !resp.Allowed || resp.IconState != tc.wantIcon {
				t.Errorf("%s (guard active): want icon_state=%q, got %+v", tc.wantLabel, tc.wantIcon, resp)
			}
		}

		// Test with an expired guard (idle arrives after guard_duration; must use
		// lastAction fallback — this was the stale-bolt bug on Claude sessions).
		for _, tc := range cases {
			guard := NewStateGuard(time.Nanosecond) // expires immediately
			notifier := NewNotifier(&config.NotificationConfig{})
			notifier.dispatch = func(_ context.Context, _ int, _, _, _ string) (bool, error) { return false, nil }
			h := NewHandler(guard, notifier)

			h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: tc.prev})
			time.Sleep(5 * time.Millisecond) // let the guard expire
			resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "idle"})
			if !resp.OK || !resp.Allowed || resp.IconState != tc.wantIcon {
				t.Errorf("%s (guard expired): want icon_state=%q, got %+v", tc.wantLabel, tc.wantIcon, resp)
			}
		}
	})
}

func TestHandler_CleanupUsesCurrentTitleIconAfterDaemonRestart(t *testing.T) {
	cases := []struct {
		name     string
		cleanup  string
		current  string
		harness  string
		wantIcon string
	}{
		{"idle clears stale bolt", "idle", "bolt", "", ""},
		{"idle clears stale compact", "idle", "compact", "", ""},
		{"idle promotes stale Copilot compact to stop", "idle", "compact", "copilot", "stop"},
		{"clear promotes stale Copilot compact to stop", "clear", "compact", "copilot", "stop"},
		{"idle clears stale subagent", "idle", "subagent", "", ""},
		{"clear clears stale bolt", "clear", "bolt", "", ""},
		{"clear preserves stop icon", "clear", "stop", "", IconStatePreserve},
		{"idle preserves permission icon", "idle", "key", "", IconStatePreserve},
		{"idle preserves warning icon", "idle", "warning", "", IconStatePreserve},
		{"idle preserves error icon", "idle", "error", "", IconStatePreserve},
		{"idle preserves elicitation icon", "idle", "question", "", IconStatePreserve},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler()
			resp := h.Handle(Request{
				Type:             TypeStateUpdate,
				WindowID:         1,
				Action:           tc.cleanup,
				Harness:          tc.harness,
				CurrentIconState: tc.current,
			})
			if !resp.OK || !resp.Allowed || resp.IconState != tc.wantIcon {
				t.Fatalf("want icon_state=%q, got %+v", tc.wantIcon, resp)
			}
		})
	}
}

func TestHandler_StateUpdateRecordsActivity(t *testing.T) {
	h := newTestHandler()
	before := time.Now()
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active"})
	if !resp.OK || !resp.Allowed {
		t.Fatalf("want allowed update, got %+v", resp)
	}

	h.stateMu.Lock()
	got, ok := h.activity[42]
	h.stateMu.Unlock()
	if !ok {
		t.Fatalf("expected activity[42] to be recorded")
	}
	if got.Before(before) {
		t.Fatalf("activity time %v is before request time %v", got, before)
	}
}

func TestHandler_StateUpdateBlockedDoesNotRecordActivity(t *testing.T) {
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "failure"})

	h.stateMu.Lock()
	delete(h.activity, 1)
	h.stateMu.Unlock()

	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission"})
	if !resp.OK || resp.Allowed {
		t.Fatalf("expected blocked update, got %+v", resp)
	}

	h.stateMu.Lock()
	_, ok := h.activity[1]
	h.stateMu.Unlock()
	if ok {
		t.Fatalf("blocked update should not record activity")
	}
}

// TestHandler_StateUpdateRefreshesMutedWindowsCache verifies that an inbound
// Request with Muted=true populates the per-window cache that Notifier.IsMuted
// consults, and that a subsequent request with Muted=false clears the entry.
// This is the daemon-side half of session 3's gating contract: the title
// remains the durable source of truth (via cc_mute on the kitty window), but
// MaybeNotify and the sweep paths need an in-memory cache to consult.
func TestHandler_StateUpdateRefreshesMutedWindowsCache(t *testing.T) {
	h := newTestHandler()

	if h.IsMuted(42) {
		t.Fatal("window should default to unmuted")
	}

	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Muted: true})
	if !resp.OK || !resp.Allowed {
		t.Fatalf("muted state update should still be allowed, got %+v", resp)
	}
	if !h.IsMuted(42) {
		t.Fatal("mutedWindows cache should reflect Muted=true after handleStateUpdate")
	}

	// A separate window with Muted=false must not be polluted by the muted entry.
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 43, Action: "active"})
	if h.IsMuted(43) {
		t.Fatal("unmuted sibling window should not be marked muted")
	}

	// Toggling Muted back to false on the same window clears the cache.
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Muted: false})
	if h.IsMuted(42) {
		t.Fatal("mutedWindows cache should be cleared after Muted=false update")
	}
}

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

func TestHandler_PreserveCurrentIcon(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", PreservesIcon: true})
	if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
		t.Errorf("PreservesIcon: want icon_state=%q, got %+v", IconStatePreserve, resp)
	}
}

func TestHandler_PreserveCurrentRestoresPreviousState(t *testing.T) {
	cases := []struct {
		name       string
		prevAction string
		wantIdle   string
	}{
		{"active remains clearable", "active", ""},
		{"subagent remains clearable", "subagent", ""},
		{"permission remains sticky", "permission", IconStatePreserve},
		{"failure remains sticky", "failure", IconStatePreserve},
		{"elicitation remains sticky", "elicitation", IconStatePreserve},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			guard := NewStateGuard(50 * time.Millisecond)
			notifier := NewNotifier(&config.NotificationConfig{})
			notifier.dispatch = func(_ context.Context, _ int, _, _, _ string) (bool, error) { return false, nil }
			h := NewHandler(guard, notifier)

			resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: tc.prevAction})
			if !resp.OK || !resp.Allowed {
				t.Fatalf("seed %s: want allowed=true, got %+v", tc.prevAction, resp)
			}
			time.Sleep(60 * time.Millisecond)

			resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "TaskCompleted", PreservesIcon: true})
			if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
				t.Fatalf("preserve-current after %s: want icon_state=%q, got %+v", tc.prevAction, IconStatePreserve, resp)
			}

			resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "idle"})
			if !resp.OK || !resp.Allowed || resp.IconState != tc.wantIdle {
				t.Fatalf("idle after preserve-current and %s: want icon_state=%q, got %+v", tc.prevAction, tc.wantIdle, resp)
			}
		})
	}
}

func TestHandler_ChildCompleteEventsDoNotNotifyStop(t *testing.T) {
	cases := []struct {
		name  string
		seed  func(*Handler)
		event string
	}{
		{
			name: "TeammateIdle preserves parent activity",
			seed: func(h *Handler) {
				h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
			},
			event: "TeammateIdle",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				guard := NewStateGuard(time.Hour)
				cfg := &config.NotificationConfig{Stop: true}
				notifier := NewNotifier(cfg)
				var mu sync.Mutex
				var calls []string
				notifier.dispatch = func(_ context.Context, _ int, _, _, subtitle string) (bool, error) {
					mu.Lock()
					calls = append(calls, subtitle)
					mu.Unlock()
					return false, nil
				}
				h := NewHandler(guard, notifier)

				tc.seed(h)

				resp := h.Handle(Request{
					Type:          TypeStateUpdate,
					WindowID:      1,
					Action:        "active",
					Event:         tc.event,
					PreservesIcon: true,
					SessionTitle:  "W:api",
				})
				if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
					t.Fatalf("%s: want icon_state=%q, got %+v", tc.name, IconStatePreserve, resp)
				}

				synctest.Wait()

				mu.Lock()
				defer mu.Unlock()
				if len(calls) != 0 {
					t.Fatalf("%s: unexpected notifications %v", tc.name, calls)
				}
			})
		})
	}
}

func TestHandler_ClaudeResetFlowUsesUserClear(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(Request{
		Type:      TypeStateUpdate,
		WindowID:  42,
		Action:    "active",
		Event:     "UserPromptSubmit",
		IsPrompt:  true,
		ResetFlow: true,
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("reset prompt should still show activity, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         42,
		Action:           "stop",
		Event:            "Stop",
		CurrentIconState: "bolt",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "" {
		t.Fatalf("Stop during reset flow should clear instead of stop, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         42,
		Action:           "stop",
		Event:            "SessionEnd",
		CurrentIconState: "",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "" {
		t.Fatalf("SessionEnd during reset cleanup should stay cleared, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         42,
		Action:           "idle",
		Event:            "SessionStart",
		CurrentIconState: "",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "" {
		t.Fatalf("SessionStart cleanup should finish reset with cleared icon, got %+v", resp)
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if _, ok := h.lastAction[42]; ok {
		t.Fatal("lastAction should be cleared after reset flow finishes")
	}
}
