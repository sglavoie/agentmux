package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
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
				resp := h.Handle(protocol.Request{Type: protocol.TypeShutdown})
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

		h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
		h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "permission"})
		time.Sleep(20 * time.Millisecond)
		h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostToolUse"})

		time.Sleep(time.Second)
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		if len(calls) != 1 || calls[0] != "Permission requested" {
			t.Fatalf("expected permission notification to fire despite active follow-up, got %v", calls)
		}
	})
}

func TestHandler_CopilotPendingPromptBlocksTurnNoiseUntilUserResponds(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(protocol.Request{
		Type:             protocol.TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "elicitation",
		Event:            "notification",
		NotificationType: "elicitation_dialog",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "question" {
		t.Fatalf("Copilot plan prompt: want question icon, got %+v", resp)
	}

	resp = h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Harness:  "copilot",
		Action:   "stop",
		Event:    "agentStop",
	})
	if !resp.OK || resp.Allowed {
		t.Fatalf("Copilot agentStop while prompt pending: want allowed=false, got %+v", resp)
	}

	resp = h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Harness:  "copilot",
		Action:   "active",
		Event:    "postToolUse",
	})
	if !resp.OK || resp.Allowed {
		t.Fatalf("Copilot trailing active while prompt pending: want allowed=false, got %+v", resp)
	}

	resp = h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Harness:  "copilot",
		Action:   "active",
		Event:    "userPromptSubmitted",
		IsPrompt: true,
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("Copilot user response should clear prompt icon, got %+v", resp)
	}
}

func TestHandler_ResetFlowSuppressesStopNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		h := NewHandler(guard, notifier)

		h.Handle(protocol.Request{
			Type:         protocol.TypeStateUpdate,
			WindowID:     1,
			Action:       "active",
			Event:        "UserPromptSubmit",
			IsPrompt:     true,
			ResetFlow:    true,
			SessionTitle: "W:api",
		})

		resp := h.Handle(protocol.Request{
			Type:     protocol.TypeStateUpdate,
			WindowID: 1,
			Action:   "stop",
			Event:    "Stop",

			SessionTitle: "W:api",
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

		h.Handle(protocol.Request{
			Type:         protocol.TypeStateUpdate,
			WindowID:     1,
			Harness:      "copilot",
			Action:       "active",
			Event:        "userPromptSubmitted",
			IsPrompt:     true,
			ResetFlow:    true,
			SessionTitle: "W:api",
		})

		for _, req := range []protocol.Request{
			{
				Type:     protocol.TypeStateUpdate,
				WindowID: 1,
				Harness:  "copilot",
				Action:   "stop",
				Event:    "agentStop",

				SessionTitle: "W:api",
			},
			{
				Type:     protocol.TypeStateUpdate,
				WindowID: 1,
				Harness:  "copilot",
				Action:   "stop",
				Event:    "sessionEnd",

				SessionTitle: "W:api",
			},
			{
				Type:     protocol.TypeStateUpdate,
				WindowID: 1,
				Harness:  "copilot",
				Action:   "idle",
				Event:    "sessionStart",

				SessionTitle: "W:api",
			},
			{
				Type:             protocol.TypeStateUpdate,
				WindowID:         1,
				Harness:          "copilot",
				Action:           "stop",
				Event:            "notification",
				NotificationType: "agent_idle",

				SessionTitle: "W:api",
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

		resp := h.Handle(protocol.Request{
			Type:      protocol.TypeStateUpdate,
			WindowID:  1,
			Harness:   "copilot",
			Action:    "stop",
			Event:     "sessionEnd",
			Reason:    "user_exit",
			ResetFlow: true,

			SessionTitle: "W:api",
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
		h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "elicitation"})

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
			if c == "User input requested" {
				sawElicitation = true
			}
		}
		if !sawElicitation {
			t.Fatalf("expected elicitation notification to fire, got %v", calls)
		}
	})
}

func TestHandleStateUpdate_AccountFieldPropagated(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
		Account:  "claude-work",
	})
	if !resp.OK {
		t.Fatalf("expected ok=true for state_update with Account set, got %+v", resp)
	}
}
