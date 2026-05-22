package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestHandler_ShutdownInvokedOnce locks in that back-to-back (or concurrent)
// shutdown requests invoke onShutdown exactly once. Today this is backed by
// sync.Once around the goroutine spawn — a defence-in-depth guard so callers
// cannot rely on context.CancelFunc's idempotence alone.
func TestHandler_ShutdownInvokedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTestHandler()
		var count atomic.Int32
		h.onShutdown = func() { count.Add(1) }

		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp := h.Handle(Request{Type: TypeShutdown})
				if !resp.OK {
					t.Errorf("want ok=true, got %+v", resp)
				}
			}()
		}
		wg.Wait()
		synctest.Wait() // drain the single onShutdown goroutine

		if got := count.Load(); got != 1 {
			t.Fatalf("onShutdown should fire exactly once across concurrent shutdown requests, got %d", got)
		}
	})
}

func TestHandler_PermissionSurvivesActiveFollowUp(t *testing.T) {
	// Claude Code's real prompt sequence is PreToolUse → Notification
	// permission_prompt → (user approves) → PostToolUse. The PostToolUse
	// arrives as action="active" and must not cancel the pending notification;
	// otherwise the user never sees it.
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 100}
		notifier := NewNotifier(cfg)
		var mu sync.Mutex
		var calls []string
		notifier.dispatch = func(_ context.Context, _, subtitle string) error {
			mu.Lock()
			calls = append(calls, subtitle)
			mu.Unlock()
			return nil
		}
		h := NewHandler(guard, notifier)

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission"})
		time.Sleep(20 * time.Millisecond)
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostToolUse"})

		time.Sleep(time.Second)
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		if len(calls) != 1 || calls[0] != "Permission requested" {
			t.Fatalf("expected permission notification to fire despite active follow-up, got %v", calls)
		}
	})
}

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
		guard := NewStateGuard(time.Hour)
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 100}
		notifier := NewNotifier(cfg)
		var calls []string
		notifier.dispatch = func(_ context.Context, _, subtitle string) error {
			calls = append(calls, subtitle)
			return nil
		}
		h := NewHandler(guard, notifier)

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "preToolUse"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission", Event: "permissionRequest"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "postToolUse"})

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
		guard := NewStateGuard(time.Hour)
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 100}
		notifier := NewNotifier(cfg)
		var calls []string
		notifier.dispatch = func(_ context.Context, _, subtitle string) error {
			calls = append(calls, subtitle)
			return nil
		}
		h := NewHandler(guard, notifier)

		h.Handle(Request{
			Type:             TypeStateUpdate,
			WindowID:         1,
			Harness:          "copilot",
			Action:           "active",
			Event:            "postToolUse",
			CurrentIconState: "bolt",
		})

		resp := h.Handle(Request{
			Type:             TypeStateUpdate,
			WindowID:         1,
			Harness:          "copilot",
			Action:           "permission",
			Event:            "permissionRequest",
			CurrentIconState: "bolt",
		})
		if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
			t.Fatalf("Copilot permissionRequest should preserve the bolt while permission is still ambiguous, got %+v", resp)
		}

		resp = h.Handle(Request{
			Type:             TypeStateUpdate,
			WindowID:         1,
			Harness:          "copilot",
			Action:           "active",
			Event:            "postToolUse",
			CurrentIconState: "bolt",
		})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("Copilot postToolUse after auto-approved prompt: want bolt, got %+v", resp)
		}

		resp = h.Handle(Request{
			Type:             TypeStateUpdate,
			WindowID:         1,
			Harness:          "copilot",
			Action:           "permission",
			Event:            "notification",
			NotificationType: "permission_prompt",
			CurrentIconState: "bolt",
		})
		if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
			t.Fatalf("late Copilot permission_prompt after auto-approved tool use: want preserve, got %+v", resp)
		}

		resp = h.Handle(Request{
			Type:             TypeStateUpdate,
			WindowID:         1,
			Harness:          "copilot",
			Action:           "stop",
			Event:            "agentStop",
			CurrentIconState: "bolt",
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
	h := newTestHandler()

	resp := h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "active",
		Event:            "postToolUse",
		CurrentIconState: "bolt",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("setup postToolUse: want bolt, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "permission",
		Event:            "permissionRequest",
		CurrentIconState: "bolt",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
		t.Fatalf("Copilot permissionRequest should preserve bolt until a real prompt appears, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "permission",
		Event:            "notification",
		NotificationType: "permission_prompt",
		CurrentIconState: "bolt",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "key" {
		t.Fatalf("Copilot permission_prompt without auto-approved flow: want key, got %+v", resp)
	}
}

func TestHandler_CopilotPendingPromptBlocksTurnNoiseUntilUserResponds(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "permission",
		Event:            "notification",
		NotificationType: "elicitation_dialog",
		CurrentIconState: "bolt",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "key" {
		t.Fatalf("Copilot plan prompt: want key icon, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "stop",
		Event:            "agentStop",
		CurrentIconState: "key",
	})
	if !resp.OK || resp.Allowed {
		t.Fatalf("Copilot agentStop while prompt pending: want allowed=false, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "active",
		Event:            "postToolUse",
		CurrentIconState: "key",
	})
	if !resp.OK || resp.Allowed {
		t.Fatalf("Copilot trailing active while prompt pending: want allowed=false, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "active",
		Event:            "userPromptSubmitted",
		IsPrompt:         true,
		CurrentIconState: "key",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("Copilot user response should clear prompt icon, got %+v", resp)
	}
}

func TestHandler_PermissionCancelledByTerminalAction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		cfg := &config.NotificationConfig{Permission: true, Stop: true, PermissionDebounceMs: 100}
		notifier := NewNotifier(cfg)
		var mu sync.Mutex
		var calls []string
		notifier.dispatch = func(_ context.Context, _, subtitle string) error {
			mu.Lock()
			calls = append(calls, subtitle)
			mu.Unlock()
			return nil
		}
		h := NewHandler(guard, notifier)

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission"})
		time.Sleep(20 * time.Millisecond)
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop"})

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

func TestHandler_ResetFlowSuppressesStopNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		h := NewHandler(guard, notifier)

		h.Handle(Request{
			Type:         TypeStateUpdate,
			WindowID:     1,
			Action:       "active",
			Event:        "UserPromptSubmit",
			IsPrompt:     true,
			ResetFlow:    true,
			SessionTitle: "W:api",
		})

		resp := h.Handle(Request{
			Type:             TypeStateUpdate,
			WindowID:         1,
			Action:           "stop",
			Event:            "Stop",
			CurrentIconState: "bolt",
			SessionTitle:     "W:api",
		})
		if !resp.OK || !resp.Allowed || resp.IconState != "" {
			t.Fatalf("reset stop should clear without notifying, got %+v", resp)
		}

		synctest.Wait()
		if calls := rec.snapshot(); len(calls) != 0 {
			t.Fatalf("reset flow should suppress stop notifications, got %v", calls)
		}
	})
}

func TestHandler_CopilotResetFlowSuppressesTrailingIdleNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		h := NewHandler(guard, notifier)

		h.Handle(Request{
			Type:         TypeStateUpdate,
			WindowID:     1,
			Harness:      "copilot",
			Action:       "active",
			Event:        "userPromptSubmitted",
			IsPrompt:     true,
			ResetFlow:    true,
			SessionTitle: "W:api",
		})

		for _, req := range []Request{
			{
				Type:             TypeStateUpdate,
				WindowID:         1,
				Harness:          "copilot",
				Action:           "stop",
				Event:            "agentStop",
				CurrentIconState: "bolt",
				SessionTitle:     "W:api",
			},
			{
				Type:             TypeStateUpdate,
				WindowID:         1,
				Harness:          "copilot",
				Action:           "stop",
				Event:            "sessionEnd",
				CurrentIconState: "",
				SessionTitle:     "W:api",
			},
			{
				Type:             TypeStateUpdate,
				WindowID:         1,
				Harness:          "copilot",
				Action:           "idle",
				Event:            "sessionStart",
				CurrentIconState: "",
				SessionTitle:     "W:api",
			},
			{
				Type:             TypeStateUpdate,
				WindowID:         1,
				Harness:          "copilot",
				Action:           "stop",
				Event:            "notification",
				NotificationType: "agent_idle",
				CurrentIconState: "",
				SessionTitle:     "W:api",
			},
		} {
			resp := h.Handle(req)
			if !resp.OK || !resp.Allowed || resp.IconState != "" {
				t.Fatalf("%s during reset follow-up should stay cleared, got %+v", req.Event, resp)
			}
		}

		synctest.Wait()
		if calls := rec.snapshot(); len(calls) != 0 {
			t.Fatalf("reset follow-up should suppress trailing stop notifications, got %v", calls)
		}
	})
}

func TestHandler_CopilotUserExitSessionEndClearsWithoutNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		h := NewHandler(guard, notifier)

		resp := h.Handle(Request{
			Type:             TypeStateUpdate,
			WindowID:         1,
			Harness:          "copilot",
			Action:           "stop",
			Event:            "sessionEnd",
			Reason:           "user_exit",
			ResetFlow:        true,
			CurrentIconState: "bolt",
			SessionTitle:     "W:api",
		})
		if !resp.OK || !resp.Allowed || resp.IconState != "" {
			t.Fatalf("sessionEnd(user_exit) should clear without notifying, got %+v", resp)
		}

		synctest.Wait()
		if calls := rec.snapshot(); len(calls) != 0 {
			t.Fatalf("sessionEnd(user_exit) should suppress stop notifications, got %v", calls)
		}
	})
}

// TestHandler_ElicitationCancelsPendingPermission locks in that an elicitation
// event arriving while a permission notification is still pending (in its
// debounce window) supersedes that notification — the user should see the
// elicitation prompt, not a stale "Permission requested" toast.
//
// This is currently handled at the notify layer: MaybeNotify cancels the
// pending entry for any non-permission action that survives the early-return
// gates. The test pins that contract so a future refactor cannot quietly
// reintroduce the missed-cancel bug.
func TestHandler_ElicitationCancelsPendingPermission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 200}
		notifier := NewNotifier(cfg)
		var mu sync.Mutex
		var calls []string
		notifier.dispatch = func(_ context.Context, _, subtitle string) error {
			mu.Lock()
			calls = append(calls, subtitle)
			mu.Unlock()
			return nil
		}
		h := NewHandler(guard, notifier)

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission"})
		time.Sleep(20 * time.Millisecond)
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "elicitation"})

		// Wait well past the original debounce window so the permission timer
		// would have fired if it had not been cancelled.
		time.Sleep(time.Second)
		synctest.Wait()

		mu.Lock()
		defer mu.Unlock()
		sawElicitation := false
		for _, c := range calls {
			if c == "Permission requested" {
				t.Fatalf("elicitation should cancel pending permission, got %v", calls)
			}
			if c == "Claude is asking" {
				sawElicitation = true
			}
		}
		if !sawElicitation {
			t.Fatalf("expected elicitation notification to fire, got %v", calls)
		}
	})
}

// TestHandler_PermissionNotificationFiresWhenGuardBlocks verifies that when a
// higher-priority guard (failure, p=4) blocks a permission event (p=3), the
// icon transition is suppressed but the user-facing notification still fires —
// the guard only governs visual state, not whether the user gets alerted.
func TestHandler_PermissionNotificationFiresWhenGuardBlocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 50}
		notifier := NewNotifier(cfg)
		var mu sync.Mutex
		var calls []string
		notifier.dispatch = func(_ context.Context, _, subtitle string) error {
			mu.Lock()
			calls = append(calls, subtitle)
			mu.Unlock()
			return nil
		}
		h := NewHandler(guard, notifier)

		// Prime with failure so the guard sits at priority 4.
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "failure"})

		// Permission is blocked by the failure guard.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission"})
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

func TestHandleStateUpdate_AccountFieldPropagated(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
		Account:  "claude-work",
	})
	if !resp.OK {
		t.Fatalf("expected ok=true for state_update with Account set, got %+v", resp)
	}
}

// TestHandler_ActiveBlockedAfterStopGuardExpiry verifies that a non-prompt
// "active" event (e.g. PostToolBatch arriving late due to async execution) cannot
// revive the bolt after Stop, even after the stop guard has expired. An explicit
// prompt event must still be able to break through.
func TestHandler_ActiveBlockedAfterStopGuardExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Nanosecond) // expires immediately
		notifier := NewNotifier(&config.NotificationConfig{})
		notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
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

	resp := h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Action:           "active",
		CurrentIconState: "stop",
	})
	if !resp.OK || resp.Allowed {
		t.Fatalf("non-prompt active with current stop icon: want allowed=false, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Action:           "active",
		IsPrompt:         true,
		CurrentIconState: "stop",
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
		notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
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

	resp := h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Action:           "active",
		CurrentIconState: "warning",
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

	resp := h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Action:           "active",
		CurrentIconState: "error",
	})
	if !resp.OK || resp.Allowed {
		t.Fatalf("non-prompt active with current error icon: want allowed=false, got %+v", resp)
	}

	// A prompt event still breaks through.
	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Action:           "active",
		IsPrompt:         true,
		CurrentIconState: "error",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("prompt active with current error icon: want icon_state=bolt, got %+v", resp)
	}
}
