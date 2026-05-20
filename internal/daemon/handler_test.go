package daemon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func newTestHandler() *Handler {
	guard := NewStateGuard(time.Hour)
	notifier := NewNotifier(&config.NotificationConfig{})
	notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
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
		{"stop", "stop"},
		{"permission", "key"},
		{"elicitation", "question"},
		{"failure", "error"},
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
			{"failure", IconStatePreserve, "idle after failure preserves error icon"},
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
			notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
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
		wantIcon string
	}{
		{"idle clears stale bolt", "idle", "bolt", ""},
		{"idle clears stale compact", "idle", "compact", ""},
		{"idle clears stale subagent", "idle", "subagent", ""},
		{"clear clears stale bolt", "clear", "bolt", ""},
		{"clear preserves stop icon", "clear", "stop", IconStatePreserve},
		{"idle preserves permission icon", "idle", "key", IconStatePreserve},
		{"idle preserves error icon", "idle", "error", IconStatePreserve},
		{"idle preserves elicitation icon", "idle", "question", IconStatePreserve},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler()
			resp := h.Handle(Request{
				Type:             TypeStateUpdate,
				WindowID:         1,
				Action:           tc.cleanup,
				CurrentIconState: tc.current,
			})
			if !resp.OK || !resp.Allowed || resp.IconState != tc.wantIcon {
				t.Fatalf("want icon_state=%q, got %+v", tc.wantIcon, resp)
			}
		})
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
		notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
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
	})
}

func TestHandler_ElicitationShowsQuestionIcon(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "elicitation"})
	if !resp.OK || !resp.Allowed || resp.IconState != "question" {
		t.Fatalf("want {ok:true, allowed:true, icon_state:question}, got %+v", resp)
	}
}

func TestHandler_ElicitationDuringActiveNotAutoApproved(t *testing.T) {
	// When an active guard is live, elicitation is NOT treated as auto-approved
	// (isAutoApprovedPerm checks for "permission", not "elicitation").
	// The ❓ icon must appear.
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active"})

	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "elicitation"})
	if !resp.OK || !resp.Allowed || resp.IconState != "question" {
		t.Fatalf("elicitation during active guard: want icon_state=question, got %+v", resp)
	}
}

func TestHandler_ElicitationResultClearsGuard(t *testing.T) {
	// After elicitation the guard clears immediately when active arrives,
	// so the bolt icon returns without waiting for guard expiry.
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "elicitation"})

	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active"})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("active after elicitation: want icon_state=bolt, got %+v", resp)
	}
}

func TestHandler_IdleAfterElicitationPreservesIcon(t *testing.T) {
	// idle after elicitation must NOT clear the ❓ — IconState("question") falls
	// through to the default branch in the idle switch, returning IconStatePreserve.
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "elicitation"})

	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "idle"})
	if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
		t.Fatalf("idle after elicitation: want icon_state=%q, got %+v", IconStatePreserve, resp)
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
	if resp.Activity == nil {
		t.Fatalf("want non-nil Activity map, got %+v", resp)
	}
	if len(resp.Activity) != 0 {
		t.Fatalf("want empty Activity map for fresh handler, got %+v", resp.Activity)
	}
}

func TestHandler_StateQueryReturnsActivity(t *testing.T) {
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 7, Action: "active"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 9, Action: "permission"})

	resp := h.Handle(Request{Type: TypeStateQuery})
	if !resp.OK {
		t.Fatalf("want ok=true, got %+v", resp)
	}
	if _, ok := resp.Activity[7]; !ok {
		t.Errorf("expected Activity[7], got %+v", resp.Activity)
	}
	if _, ok := resp.Activity[9]; !ok {
		t.Errorf("expected Activity[9], got %+v", resp.Activity)
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

// TestHandler_ActiveBlockedByCurrentErrorIconAfterDaemonRestart pins that a
// fresh daemon restart preserving the ❗ glyph in the tab title treats the
// reconstructed state as terminal stop_failure, so a benign non-prompt
// "active" event cannot clear an unacknowledged stop_failure. The error glyph
// is shared between failure and stop_failure; the conservative choice on
// ambiguous reconstruction is stop_failure (over-preserving a transient
// failure is far less harmful than wiping a terminal stop_failure).
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

// TestHandler_SubagentReferenceCount verifies that the robot emoji persists
// while any sub-agents are still running and clears when the last one stops.
func TestHandler_SubagentReferenceCount(t *testing.T) {
	t.Run("single agent start then stop shows then clears robot", func(t *testing.T) {
		h := newTestHandler()

		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
		if !resp.OK || !resp.Allowed || resp.IconState != "subagent" {
			t.Fatalf("SubagentStart: want icon_state=subagent, got %+v", resp)
		}

		// SubagentStop with count=1 → count hits 0 → guard cleared → "active" allowed.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("SubagentStop (last): want icon_state=bolt, got %+v", resp)
		}
	})

	t.Run("two concurrent agents: first stop suppressed, second allows transition", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})

		// First SubagentStop: count 2→1, still running — must be suppressed.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("first SubagentStop (count=1): want allowed=false, got %+v", resp)
		}

		// Second SubagentStop: count 1→0, last agent done — must be allowed.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("second SubagentStop (count=0): want icon_state=bolt, got %+v", resp)
		}
	})

	t.Run("parent active events blocked while agents running", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})

		// Parent PreToolUse "active" while subagent is running must be blocked.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("parent active during subagent: want allowed=false, got %+v", resp)
		}

		// After last SubagentStop, parent active events can proceed.
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("parent active after last SubagentStop: want icon_state=bolt, got %+v", resp)
		}
	})

	t.Run("SubagentStop without prior start does not clear unrelated guard", func(t *testing.T) {
		h := newTestHandler()

		// Establish a stop_failure guard (priority 4; "active" cannot bypass it).
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop_failure"})

		// SubagentStop with count=0: wasTracked=false, our code must not call
		// guard.Clear(), so stop_failure guard must block the "active".
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		// "active" (priority 1) < "stop_failure" (priority 4) → must be blocked.
		if !resp.OK || resp.Allowed {
			t.Fatalf("spurious SubagentStop must not clear stop_failure guard, got %+v", resp)
		}
	})

	t.Run("stop resets subagent counter", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})

		// Session stops — counter must reset.
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop"})

		// After stop, SubagentStop should be treated as untracked (wasTracked=false)
		// and the resulting "active" falls through to the normal priority check.
		// stop (priority 2) still holds the guard, so "active" (priority 1) is blocked.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("after stop resets counter, SubagentStop should not clear stop guard, got %+v", resp)
		}
	})

	t.Run("independent windows have separate counters", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 2, Action: "subagent", Event: "SubagentStart"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 2, Action: "subagent", Event: "SubagentStart"})

		// Window 1 has count=1; its SubagentStop should be the last.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("window 1 single SubagentStop: want icon_state=bolt, got %+v", resp)
		}

		// Window 2 has count=2; first stop is still suppressed.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 2, Action: "active", Event: "SubagentStop"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("window 2 first SubagentStop: want allowed=false, got %+v", resp)
		}
	})
}

// TestHandler_SubagentCounterRecoversAfterTerminalAction locks in the
// self-healing property of subagentCount: if any SubagentStop events are lost
// (daemon restart, crashed subprocess, dropped IPC frame), a subsequent
// terminal action must reset the counter so future SubagentStop → active
// transitions are not permanently suppressed.
func TestHandler_SubagentCounterRecoversAfterTerminalAction(t *testing.T) {
	h := newTestHandler()

	// Two SubagentStarts → counter=2. The matching SubagentStops never arrive
	// (simulating lost IPC frames).
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})

	// Terminal action arrives — counter must reset to 0.
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop"})

	// New prompt breaks through the stop guard.
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", IsPrompt: true})

	// A fresh subagent starts → counter should now be 1 (not 3 from the stuck state).
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})

	// The matching SubagentStop should be treated as the last agent finishing
	// (counter 1→0) and allow the resulting "active" with bolt icon. If the
	// counter had not been reset, this would still be > 0 and suppressed.
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("active after subagent-counter recovery: want icon_state=bolt, got %+v", resp)
	}
}

// TestHandler_IdleAfterSuppressedActivePreservesStopIcon verifies that when a
// non-prompt "active" event is suppressed because the previous state was
// "stop"/"stop_failure", a subsequent idle does not clear the stop icon. The
// bug: TryUpdate runs before the lastKnownAction check, so when we early-out
// for the suppressed active, the guard map still records "active" with a
// fresh TTL. A later idle within that TTL sees prevGuard="active", classifies
// prev as a runnable state, and asks the indicator to clear the icon — wiping
// the stop indicator that should persist until the user acknowledges it.
func TestHandler_IdleAfterSuppressedActivePreservesStopIcon(t *testing.T) {
	cases := []struct {
		name       string
		prevAction string
	}{
		{"stop", "stop"},
		{"stop_failure", "stop_failure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// Use a tiny but real guard duration so the first action's guard
				// expires before "active" arrives, but the suppressed "active"
				// re-arms a fresh guard whose TTL is still in effect when "idle"
				// arrives shortly after.
				guard := NewStateGuard(50 * time.Millisecond)
				notifier := NewNotifier(&config.NotificationConfig{})
				notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
				h := NewHandler(guard, notifier)

				h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: tc.prevAction})
				time.Sleep(60 * time.Millisecond) // let the prev guard expire

				// Non-prompt active arrives after guard expired — must be suppressed
				// because lastAction is "stop"/"stop_failure".
				resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active"})
				if !resp.OK || resp.Allowed {
					t.Fatalf("non-prompt active after %s: want allowed=false, got %+v", tc.prevAction, resp)
				}

				// A follow-up idle within the new guard TTL must preserve the
				// stop icon, NOT clear it. Without the fix, the suppressed
				// "active" leaves the guard armed at "active", and this idle
				// sees prevGuard="active" → iconState="" → stop icon erased.
				resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "idle"})
				if !resp.OK || !resp.Allowed {
					t.Fatalf("idle after suppressed active: want allowed=true, got %+v", resp)
				}
				if resp.IconState != IconStatePreserve {
					t.Fatalf("idle after suppressed active must preserve %s icon: want icon_state=%q, got %q",
						tc.prevAction, IconStatePreserve, resp.IconState)
				}
			})
		})
	}
}

// TestHandler_SubagentCountCleanedUpAfterLastStop verifies that the per-window
// subagentCount map entry is deleted when the count reaches zero via a normal
// SubagentStop, rather than being left as a zero-valued entry that survives
// until a terminal event. Without the cleanup, a long-running daemon
// processing many subagent cycles across many windows accumulates stale
// entries indefinitely (slow but real memory leak).
func TestHandler_SubagentCountCleanedUpAfterLastStop(t *testing.T) {
	h := newTestHandler()

	for windowID := 1; windowID <= 10; windowID++ {
		h.Handle(Request{Type: TypeStateUpdate, WindowID: windowID, Action: "subagent", Event: "SubagentStart"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: windowID, Action: "active", Event: "SubagentStop"})
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if got := len(h.subagentCount); got != 0 {
		t.Fatalf("subagentCount has %d residual entries after normal stop cycles; want 0", got)
	}
}

// TestHandler_CopilotMidSubagentSuppression pins the contract for the Copilot
// mid-subagent gate in handleStateUpdate: while subagentCount[windowID] > 0,
// agentStop (Copilot's per-turn signal) and permissionRequest (speculative,
// fires for every tool call) must not overwrite the 🤖 guard, and any pending
// permission notification scheduled before the gate fires must be cleared.
// sessionEnd is intentionally excluded so a real session end still resets
// state. Each subtest stands alone — they share no handler state.
func TestHandler_CopilotMidSubagentSuppression(t *testing.T) {
	t.Run("agentStop during subagent suppressed", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})

		// agentStop (action stop) is Copilot's per-turn signal; while a
		// subagent is running it must be suppressed entirely — no icon
		// change, no counter clear.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "agentStop"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("agentStop during subagent: want allowed=false, got %+v", resp)
		}
		h.stateMu.Lock()
		count := h.subagentCount[1]
		h.stateMu.Unlock()
		if count != 1 {
			t.Fatalf("agentStop must not clear subagentCount; want 1, got %d", count)
		}

		// Counter still 1 — a second subagentStart pushes it to 2, then
		// two subagentStops must drain to bolt (proves the gate left the
		// reference counter intact).
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})

		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "subagentStop"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("first subagentStop after gated agentStop: want allowed=false, got %+v", resp)
		}
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "subagentStop"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("last subagentStop after gated agentStop: want icon_state=bolt, got %+v", resp)
		}
	})

	// Reframed from the original session-3 plan ("notification fires"):
	// Session 2 chose CancelPending over MaybeNotify for the permission branch
	// (see session-2.md handoff and session-2.expand.md ISSUE-1). The contract
	// is therefore "no notification surfaces", not "notification fires after
	// debounce".
	t.Run("permissionRequest during subagent suppresses notification", func(t *testing.T) {
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

			h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})

			resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission", Event: "permissionRequest"})
			if !resp.OK || resp.Allowed {
				t.Fatalf("permissionRequest during subagent: want allowed=false, got %+v", resp)
			}

			// Drain any AfterFunc that might have been scheduled by the
			// normal path — none should exist because the gate returned
			// before MaybeNotify could schedule anything.
			time.Sleep(time.Second)
			synctest.Wait()

			mu.Lock()
			defer mu.Unlock()
			for _, c := range calls {
				if c == "Permission requested" {
					t.Fatalf("permissionRequest during subagent must not notify, got %v", calls)
				}
			}
		})
	})

	// Per session-2 handoff: the gate must also clear a pending permission
	// notification that was scheduled BEFORE subagents started. The
	// permissionRequest arriving mid-subagent invokes CancelPending and the
	// previously-scheduled AfterFunc must not fire.
	t.Run("permissionRequest during subagent clears previously-pending notification", func(t *testing.T) {
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

			// Schedule a real pending permission notification first
			// (debounced AfterFunc registered against window 1).
			h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission", Event: "permissionRequest"})

			// Subagent begins (action subagent is not notifyable, so the
			// pending permission is NOT cancelled by the normal path).
			h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})

			// permissionRequest during subagent hits the gate, which must
			// call CancelPending and stop the pending AfterFunc.
			resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission", Event: "permissionRequest"})
			if !resp.OK || resp.Allowed {
				t.Fatalf("permissionRequest during subagent: want allowed=false, got %+v", resp)
			}

			time.Sleep(time.Second)
			synctest.Wait()

			mu.Lock()
			defer mu.Unlock()
			for _, c := range calls {
				if c == "Permission requested" {
					t.Fatalf("gate must clear previously-pending permission, got %v", calls)
				}
			}
		})
	})

	t.Run("sessionEnd during subagent is NOT suppressed", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})

		// sessionEnd shares action "stop" with agentStop but is intentionally
		// excluded from the gate — a real session end must take effect.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "sessionEnd"})
		if !resp.OK || !resp.Allowed || resp.IconState != "stop" {
			t.Fatalf("sessionEnd during subagent: want icon_state=stop allowed=true, got %+v", resp)
		}

		h.stateMu.Lock()
		_, present := h.subagentCount[1]
		h.stateMu.Unlock()
		if present {
			t.Fatalf("sessionEnd must clear subagentCount entry; map still has window 1")
		}
	})

	t.Run("agentStop with stop_failure surfaces mid-subagent", func(t *testing.T) {
		// Mirrors the SubagentStop failure-surfacing rule: when Copilot's
		// agentStop carries a real failure (stopReason yielding action
		// stop_failure), the error icon must surface even while subagents are
		// still running. Without the carve-out, the gate would suppress the
		// crash and the user would never see it.
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})

		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop_failure", Event: "agentStop"})
		if !resp.OK || !resp.Allowed || resp.IconState != "error" {
			t.Fatalf("agentStop with stop_failure mid-subagent: want icon_state=error allowed=true, got %+v", resp)
		}
	})

	t.Run("agentStop after subagent drains is allowed", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})
		// subagentStop drains counter to 0 and clears the guard.
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "subagentStop"})

		// agentStop now arrives with no subagents running — gate falls
		// through, normal stop transition applies.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "agentStop"})
		if !resp.OK || !resp.Allowed || resp.IconState != "stop" {
			t.Fatalf("agentStop after drained subagents: want icon_state=stop, got %+v", resp)
		}
	})

	t.Run("gate is no-op when no subagent is running", func(t *testing.T) {
		// agentStop on a fresh handler must behave exactly like a normal
		// stop transition — proves the gate cannot misfire when
		// subagentCount[windowID] == 0.
		h := newTestHandler()
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "agentStop"})
		if !resp.OK || !resp.Allowed || resp.IconState != "stop" {
			t.Fatalf("agentStop with no subagent: want icon_state=stop, got %+v", resp)
		}

		// permissionRequest on a fresh handler must still flip to the key
		// icon — Claude's PermissionRequest and Copilot's
		// non-mid-subagent permissionRequest both rely on this.
		h2 := newTestHandler()
		resp = h2.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission", Event: "permissionRequest"})
		if !resp.OK || !resp.Allowed || resp.IconState != "key" {
			t.Fatalf("permissionRequest with no subagent: want icon_state=key, got %+v", resp)
		}
	})
}

// TestHandler_SubagentClearsHighPriorityGuard verifies the fix for the Copilot
// icon-persistence bug: when a permission or failure guard is active and
// subagentStart fires, the 🤖 icon must appear immediately instead of being
// blocked by the higher-priority guard.
//
// Real scenario: Copilot fires permissionRequest (speculative, or real) before
// subagentStart. Without the fix, the permission guard (priority 3) blocks the
// subagent icon (priority 2), leaving the 🔑 showing while a subagent runs.
func TestHandler_SubagentClearsHighPriorityGuard(t *testing.T) {
	t.Run("subagentStart clears permission guard and shows robot icon", func(t *testing.T) {
		h := newTestHandler()

		// Establish a permission guard (key icon showing).
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission", Event: "permissionRequest"})
		if !resp.OK || !resp.Allowed || resp.IconState != "key" {
			t.Fatalf("setup: want key icon, got %+v", resp)
		}

		// SubagentStart must clear the permission guard and show the robot.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})
		if !resp.OK || !resp.Allowed || resp.IconState != "subagent" {
			t.Fatalf("subagentStart after permission: want icon_state=subagent, got %+v", resp)
		}
		if cur := h.guard.Current(1); cur != "subagent" {
			t.Fatalf("guard should be subagent after subagentStart, got %q", cur)
		}

		// Mid-subagent permissionRequest must be suppressed (counter > 0).
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission", Event: "permissionRequest"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("mid-subagent permissionRequest: want allowed=false, got %+v", resp)
		}

		// subagentStop (last) must clear guard and show bolt.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "subagentStop"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("subagentStop: want icon_state=bolt, got %+v", resp)
		}
	})

	t.Run("subagentStart clears failure guard and shows robot icon", func(t *testing.T) {
		h := newTestHandler()

		// Establish a failure guard (error icon showing).
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "failure", Event: "postToolUseFailure"})
		if !resp.OK || !resp.Allowed || resp.IconState != "error" {
			t.Fatalf("setup: want error icon, got %+v", resp)
		}

		// SubagentStart must clear the failure guard and show the robot.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})
		if !resp.OK || !resp.Allowed || resp.IconState != "subagent" {
			t.Fatalf("subagentStart after failure: want icon_state=subagent, got %+v", resp)
		}
		if cur := h.guard.Current(1); cur != "subagent" {
			t.Fatalf("guard should be subagent after subagentStart, got %q", cur)
		}

		// subagentStop (last) must clear guard and show bolt.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "subagentStop"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("subagentStop: want icon_state=bolt, got %+v", resp)
		}
	})

	t.Run("stop_failure guard is NOT cleared by subagentStart", func(t *testing.T) {
		// stop_failure is terminal; a spurious subagentStart must not hide the
		// error icon — the user must explicitly acknowledge a terminal failure.
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop_failure"})

		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("subagentStart after stop_failure: want allowed=false, got %+v", resp)
		}
	})

	t.Run("Copilot sequence: speculative permissionRequest before subagentStart shows robot", func(t *testing.T) {
		// Reproduces the reported Copilot bug: active (postToolUse) →
		// permissionRequest (speculative; isAutoApprovedPerm=false because
		// prevEvent is postToolUse, not preToolUse) → subagentStart.
		// The robot must appear instead of the key persisting.
		h := newTestHandler()
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "postToolUse"})
		// permissionRequest with PostToolUse active guard → NOT auto-approved → key
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission", Event: "permissionRequest"})
		if !resp.OK || !resp.Allowed || resp.IconState != "key" {
			t.Fatalf("setup: want key icon, got %+v", resp)
		}
		// subagentStart must override the key icon with the robot.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})
		if !resp.OK || !resp.Allowed || resp.IconState != "subagent" {
			t.Fatalf("subagentStart: want icon_state=subagent, got %+v", resp)
		}
	})
}

// TestHandler_SubagentStopFailureSurfacesWhileSiblingsRun verifies that a
// SubagentStop carrying a real failure (action == "stop_failure") is NOT
// silently suppressed even when sibling subagents are still running. The
// "decrement and suppress" logic exists to hide benign per-agent completions
// under the parent 🤖 icon — applying it to failures would hide a child
// crash from the user. The error icon must appear; the sibling subagent must
// keep running; and when the surviving sibling later finishes successfully,
// the stop_failure guard must persist (the user has not acknowledged it).
func TestHandler_SubagentStopFailureSurfacesWhileSiblingsRun(t *testing.T) {
	h := newTestHandler()

	// Two concurrent subagents start.
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})

	// One subagent fails (e.g. SubagentStop with stopReason == "error" maps to
	// action stop_failure via ResolveAction). With sibling still running, the
	// failure must surface, not be suppressed.
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop_failure", Event: "SubagentStop"})
	if !resp.OK || !resp.Allowed || resp.IconState != "error" {
		t.Fatalf("subagent stop_failure with sibling running: want allowed=true icon=error, got %+v", resp)
	}

	// Sibling finishes normally. The decrement brings count to 0, but the
	// guard.Clear must NOT wipe the stop_failure guard the user has not yet
	// acknowledged. The follow-up "active" carried by SubagentStop must be
	// blocked by the stop_failure (priority 4) guard.
	resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
	if !resp.OK || resp.Allowed {
		t.Fatalf("active after sibling drain must be blocked by stop_failure, got %+v", resp)
	}
}

// TestHandler_TracksLastSessionTitle verifies that the session title from
// each state_update is recorded so the future stale-active sweep can fall
// back to it when the live kitten lookup fails.
func TestHandler_TracksLastSessionTitle(t *testing.T) {
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", SessionTitle: "W:api"})

	h.stateMu.Lock()
	got := h.lastSessionTitle[42]
	h.stateMu.Unlock()
	if got != "W:api" {
		t.Fatalf("lastSessionTitle[42] = %q; want %q", got, "W:api")
	}
}

// TestHandler_TracksPipeMode verifies that the pipe-mode flag from each
// state_update is recorded so stale notifications can honor the same
// silence rule used by other notifyable actions.
func TestHandler_TracksPipeMode(t *testing.T) {
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", PipeMode: true})

	h.stateMu.Lock()
	got := h.lastPipeMode[42]
	h.stateMu.Unlock()
	if !got {
		t.Fatalf("lastPipeMode[42] = false; want true")
	}
}

// TestHandler_NotifiedStaleClearedOnFreshEvent verifies that a fresh real
// event for a window resets the single-shot stale-notification guard so a
// later stall produces a new notification.
func TestHandler_NotifiedStaleClearedOnFreshEvent(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.notifiedStale[42] = true
	h.stateMu.Unlock()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active"})

	h.stateMu.Lock()
	_, present := h.notifiedStale[42]
	h.stateMu.Unlock()
	if present {
		t.Fatalf("notifiedStale[42] still present after fresh active event")
	}
}

// TestHandler_CleanupOnTerminalAction verifies that terminal actions remove
// the per-window entries from notifiedStale, lastSessionTitle, and
// lastPipeMode so the maps cannot grow unbounded across long-lived daemons.
func TestHandler_CleanupOnTerminalAction(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.notifiedStale[42] = true
	h.lastSessionTitle[42] = "W:api"
	h.lastPipeMode[42] = true
	h.stateMu.Unlock()

	// "stop" carries no SessionTitle / PipeMode in this test, so the
	// post-cleanup re-write at the activity site leaves notifiedStale empty
	// and lastSessionTitle / lastPipeMode at zero values.
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop"})

	h.stateMu.Lock()
	_, staleOK := h.notifiedStale[42]
	title := h.lastSessionTitle[42]
	pipe := h.lastPipeMode[42]
	h.stateMu.Unlock()

	if staleOK {
		t.Fatalf("notifiedStale[42] still present after stop")
	}
	if title != "" {
		t.Fatalf("lastSessionTitle[42] = %q after stop; want empty", title)
	}
	if pipe {
		t.Fatalf("lastPipeMode[42] = true after stop; want false")
	}
}

// newSweepTestHandler builds a Handler wired with a notifier that records
// dispatches and a fake Kitty client whose SetTabTitle calls are recorded.
// Threshold zero leaves the sweep disabled — used by the no-op test. The
// same threshold is applied to all three watchdog states so legacy tests
// that pre-date per-state thresholds continue to exercise the sweep
// uniformly; per-state behavior is covered by newSweepTestHandlerWithMap.
func newSweepTestHandler(threshold time.Duration) (*Handler, *fakeKittyClient, *callRecorder) {
	guard := NewStateGuard(time.Hour)
	notifier, rec := newTestNotifier(&config.NotificationConfig{})
	h := NewHandler(guard, notifier)
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	h.SetStaleThresholds(map[string]time.Duration{
		"active":   threshold,
		"subagent": threshold,
		"compact":  threshold,
	}, nil)
	return h, fake, rec
}

// TestSweepStale_NoOpWhenThresholdZero verifies that a zero threshold disables
// the sweep entirely: no notifications, no title writes, no state mutations,
// even when a session is wildly stale.
func TestSweepStale_NoOpWhenThresholdZero(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(0)
		now := time.Now()

		h.stateMu.Lock()
		h.activity[42] = now.Add(-15 * time.Minute)
		h.lastAction[42] = "active"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d; want 0", got)
		}
		h.stateMu.Lock()
		action := h.lastAction[42]
		h.stateMu.Unlock()
		if action != "active" {
			t.Fatalf("lastAction[42] = %q; want active (untouched)", action)
		}
	})
}

// TestSweepStale_ClearsActiveSessionAndNotifies verifies the happy path: a
// stale active session fires a single "Session idle" notification, has its
// daemon-side state cleared, and gets its tab-title icon stripped.
func TestSweepStale_ClearsActiveSessionAndNotifies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		boltTitle := palette.BuildTitle("W:api", "bolt", false)
		clearedTitle := palette.BuildTitle("W:api", "", false)
		fake.windows = fakeOSWindowsWithTab(42, boltTitle)

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-15 * time.Minute)
		h.lastAction[42] = "active"
		h.lastSessionTitle[42] = "W:api"
		h.lastPipeMode[42] = false
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		calls := rec.snapshot()
		if len(calls) != 1 {
			t.Fatalf("dispatches = %d; want 1 (%+v)", len(calls), calls)
		}
		if calls[0].title != "W:api" || calls[0].subtitle != "Session idle" {
			t.Fatalf("dispatch = %+v; want title=W:api subtitle=Session idle", calls[0])
		}

		h.stateMu.Lock()
		_, hasAction := h.lastAction[42]
		_, hasActiveEvt := h.lastActiveEvent[42]
		notified := h.notifiedStale[42]
		h.stateMu.Unlock()
		if hasAction {
			t.Fatalf("lastAction[42] still present after sweep")
		}
		if hasActiveEvt {
			t.Fatalf("lastActiveEvent[42] still present after sweep")
		}
		if !notified {
			t.Fatalf("notifiedStale[42] = false; want true")
		}

		titles := fake.titleCalls()
		if len(titles) != 1 {
			t.Fatalf("SetTabTitle calls = %d; want 1", len(titles))
		}
		if titles[0].title != clearedTitle {
			t.Fatalf("SetTabTitle title = %q; want %q", titles[0].title, clearedTitle)
		}
	})
}

// TestSweepStale_FreshActiveLeftAlone verifies that a recent active session
// is not touched by the sweep.
func TestSweepStale_FreshActiveLeftAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-1 * time.Minute)
		h.lastAction[42] = "active"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d; want 0", got)
		}
		h.stateMu.Lock()
		action := h.lastAction[42]
		h.stateMu.Unlock()
		if action != "active" {
			t.Fatalf("lastAction[42] = %q; want active (untouched)", action)
		}
	})
}

// TestSweepStale_IgnoresStopAndPermission verifies that stop and permission
// icons (which intentionally persist until acknowledged) are never cleared by
// the sweep, even when their state is older than the threshold.
func TestSweepStale_IgnoresStopAndPermission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		fake.windows = []kitty.OSWindow{
			{
				ID: 1,
				Tabs: []kitty.Tab{
					{ID: 100, Title: palette.BuildTitle("W:api", "stop", false), Windows: []kitty.Window{{ID: 42}}},
					{ID: 101, Title: palette.BuildTitle("W:api", "key", false), Windows: []kitty.Window{{ID: 43}}},
				},
			},
		}

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-15 * time.Minute)
		h.lastAction[42] = "stop"
		h.lastSessionTitle[42] = "W:api"
		h.activity[43] = now.Add(-15 * time.Minute)
		h.lastAction[43] = "permission"
		h.lastSessionTitle[43] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (stop/permission ignored)", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d; want 0", got)
		}
		h.stateMu.Lock()
		stopAction := h.lastAction[42]
		permAction := h.lastAction[43]
		h.stateMu.Unlock()
		if stopAction != "stop" {
			t.Fatalf("lastAction[42] = %q; want stop (untouched)", stopAction)
		}
		if permAction != "permission" {
			t.Fatalf("lastAction[43] = %q; want permission (untouched)", permAction)
		}
	})
}

// TestSweepStale_SubagentAndCompactAlsoCleared verifies that the sweep treats
// subagent and compact identically to active: all three are "agent-in-flight"
// states that get cleared when stale.
func TestSweepStale_SubagentAndCompactAlsoCleared(t *testing.T) {
	cases := []struct {
		action   string
		iconName string
	}{
		{"active", "bolt"},
		{"subagent", "subagent"},
		{"compact", "compact"},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, fake, rec := newSweepTestHandler(10 * time.Minute)
				inTitle := palette.BuildTitle("W:api", tc.iconName, false)
				outTitle := palette.BuildTitle("W:api", "", false)
				fake.windows = fakeOSWindowsWithTab(42, inTitle)

				now := time.Now()
				h.stateMu.Lock()
				h.activity[42] = now.Add(-15 * time.Minute)
				h.lastAction[42] = tc.action
				h.lastSessionTitle[42] = "W:api"
				h.stateMu.Unlock()

				h.SweepStale(now)
				synctest.Wait()

				if got := rec.len(); got != 1 {
					t.Fatalf("dispatches = %d; want 1", got)
				}
				titles := fake.titleCalls()
				if len(titles) != 1 || titles[0].title != outTitle {
					t.Fatalf("SetTabTitle = %+v; want one call with title %q", titles, outTitle)
				}
				h.stateMu.Lock()
				_, has := h.lastAction[42]
				h.stateMu.Unlock()
				if has {
					t.Fatalf("lastAction[42] still present after sweep")
				}
			})
		})
	}
}

// TestSweepStale_PipeModeSilent verifies that a pipe-mode session has its
// state and title cleared but no macOS notification fires — matching the
// existing pipe-mode silence rule for stop/permission/failure.
func TestSweepStale_PipeModeSilent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		boltTitle := palette.BuildTitle("W:api", "bolt", false)
		clearedTitle := palette.BuildTitle("W:api", "", false)
		fake.windows = fakeOSWindowsWithTab(42, boltTitle)

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-15 * time.Minute)
		h.lastAction[42] = "active"
		h.lastSessionTitle[42] = "W:api"
		h.lastPipeMode[42] = true
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (pipe-mode silent)", got)
		}
		titles := fake.titleCalls()
		if len(titles) != 1 || titles[0].title != clearedTitle {
			t.Fatalf("SetTabTitle = %+v; want one call with title %q", titles, clearedTitle)
		}
		h.stateMu.Lock()
		_, has := h.lastAction[42]
		h.stateMu.Unlock()
		if has {
			t.Fatalf("lastAction[42] still present after sweep")
		}
	})
}

// TestSweepStale_SingleShot verifies the single-shot semantics: calling sweep
// twice without any state change between calls fires exactly one notification.
// The second sweep is a complete no-op because the first sweep cleared
// lastAction[42] — the notifiedStale guard is belt-and-braces.
func TestSweepStale_SingleShot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-15 * time.Minute)
		h.lastAction[42] = "active"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()
		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want exactly 1 across two sweeps", got)
		}
		if got := len(fake.titleCalls()); got != 1 {
			t.Fatalf("SetTabTitle calls = %d; want exactly 1 across two sweeps", got)
		}
	})
}

// TestSweepStale_ReNotifyAfterRecovery verifies that a fresh real event
// resets the notifiedStale guard so a subsequent stall produces a second
// notification. The full cycle exercises sweep → recovery → re-stall → sweep.
func TestSweepStale_ReNotifyAfterRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-15 * time.Minute)
		h.lastAction[42] = "active"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("first sweep: dispatches = %d; want 1", got)
		}

		// Simulate a fresh active event: per session-3 logic, this clears
		// notifiedStale[42] and re-seeds lastAction[42] = "active".
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", SessionTitle: "W:api"})

		h.stateMu.Lock()
		_, stillNotified := h.notifiedStale[42]
		h.stateMu.Unlock()
		if stillNotified {
			t.Fatalf("notifiedStale[42] should be cleared by fresh active update")
		}

		// Bypass the 10s per-action cooldown so the second dispatch is not
		// suppressed by the notifier. Synctest time has not advanced here
		// because no time.Sleep was called.
		h.notifier.ResetCooldown(42)

		// Re-stall: backdate activity and refresh the title fixture (the
		// previous sweep cleared the icon, so re-seed with the bolt title to
		// confirm the second sweep clears it again).
		fake.mu.Lock()
		fake.setTitleCalls = nil
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))
		fake.mu.Unlock()

		now2 := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now2.Add(-15 * time.Minute)
		h.stateMu.Unlock()

		h.SweepStale(now2)
		synctest.Wait()

		if got := rec.len(); got != 2 {
			t.Fatalf("second sweep: total dispatches = %d; want 2", got)
		}
		if got := len(fake.titleCalls()); got != 1 {
			t.Fatalf("second sweep: SetTabTitle calls = %d; want 1", got)
		}
	})
}

// TestSweepStale_NilKittyClient verifies that the sweep runs cleanly without a
// Kitty client (the test environment without Kitty). Notification still
// fires; only the title-clear step is skipped.
func TestSweepStale_NilKittyClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{})
		h := NewHandler(guard, notifier)
		h.SetStaleThresholds(map[string]time.Duration{"active": 10 * time.Minute}, nil)
		// Deliberately do NOT call SetKittyClient — h.kittyClient is nil.

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-15 * time.Minute)
		h.lastAction[42] = "active"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1", got)
		}
		h.stateMu.Lock()
		_, has := h.lastAction[42]
		h.stateMu.Unlock()
		if has {
			t.Fatalf("lastAction[42] still present after sweep")
		}
	})
}

// TestSweepStale_PreservesStarGlyph verifies that the star prefix on a tab
// title survives a sweep — only the state icon is stripped.
func TestSweepStale_PreservesStarGlyph(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, _ := newSweepTestHandler(10 * time.Minute)
		starredBolt := palette.BuildTitle("W:api", "bolt", true)
		starredCleared := palette.BuildTitle("W:api", "", true)
		fake.windows = fakeOSWindowsWithTab(42, starredBolt)

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-15 * time.Minute)
		h.lastAction[42] = "active"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		titles := fake.titleCalls()
		if len(titles) != 1 {
			t.Fatalf("SetTabTitle calls = %d; want 1", len(titles))
		}
		if titles[0].title != starredCleared {
			t.Fatalf("SetTabTitle title = %q; want %q (star preserved)", titles[0].title, starredCleared)
		}
	})
}

// TestSweepStale_ListWindowsErrorIsTolerated verifies that a ListWindows
// failure does not panic and does not block the sweep's other steps: the
// notification still fires and state is still cleared. The next sweep can
// retry the title clear if the daemon stays up.
func TestSweepStale_ListWindowsErrorIsTolerated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		fake.listErr = errors.New("kitten unavailable")

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-15 * time.Minute)
		h.lastAction[42] = "active"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 even on ListWindows error", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d; want 0 (list failed)", got)
		}
		h.stateMu.Lock()
		_, has := h.lastAction[42]
		notified := h.notifiedStale[42]
		h.stateMu.Unlock()
		if has {
			t.Fatalf("lastAction[42] still present after sweep")
		}
		if !notified {
			t.Fatalf("notifiedStale[42] = false; want true")
		}
	})
}

// newSweepTestHandlerWithMap is a sibling of newSweepTestHandler that takes
// per-action thresholds so per-state tests can configure different durations
// for active/subagent/compact independently.
func newSweepTestHandlerWithMap(global map[string]time.Duration, perAccount map[string]map[string]time.Duration) (*Handler, *fakeKittyClient, *callRecorder) {
	guard := NewStateGuard(time.Hour)
	notifier, rec := newTestNotifier(&config.NotificationConfig{})
	h := NewHandler(guard, notifier)
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	h.SetStaleThresholds(global, perAccount)
	return h, fake, rec
}

// TestSweepStale_PerStateThresholds verifies that per-action thresholds are
// honored independently: with compact = 30s and subagent = 30m, a 5-minute
// idle compact session is swept while a 5-minute idle subagent is left alone.
func TestSweepStale_PerStateThresholds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global := map[string]time.Duration{
			"active":   10 * time.Minute,
			"subagent": 30 * time.Minute,
			"compact":  30 * time.Second,
		}
		h, fake, rec := newSweepTestHandlerWithMap(global, nil)
		fake.windows = []kitty.OSWindow{
			{
				ID: 1,
				Tabs: []kitty.Tab{
					{ID: 100, Title: palette.BuildTitle("W:compact", "compact", false), Windows: []kitty.Window{{ID: 42}}},
					{ID: 101, Title: palette.BuildTitle("W:subagent", "subagent", false), Windows: []kitty.Window{{ID: 43}}},
				},
			},
		}

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-5 * time.Minute)
		h.lastAction[42] = "compact"
		h.lastSessionTitle[42] = "W:compact"
		h.activity[43] = now.Add(-5 * time.Minute)
		h.lastAction[43] = "subagent"
		h.lastSessionTitle[43] = "W:subagent"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (compact only)", got)
		}
		calls := rec.snapshot()
		if calls[0].title != "W:compact" {
			t.Fatalf("dispatch title = %q; want W:compact", calls[0].title)
		}

		titles := fake.titleCalls()
		if len(titles) != 1 {
			t.Fatalf("SetTabTitle calls = %d; want 1", len(titles))
		}

		h.stateMu.Lock()
		_, compactStillTracked := h.lastAction[42]
		subagentAction := h.lastAction[43]
		h.stateMu.Unlock()
		if compactStillTracked {
			t.Fatalf("lastAction[42] still present after sweep (compact should be cleared)")
		}
		if subagentAction != "subagent" {
			t.Fatalf("lastAction[43] = %q; want subagent (left alone)", subagentAction)
		}
	})
}

// TestSweepStale_PerAccountOverrideBeatsGlobal verifies that a per-account
// override duration is consulted instead of the global default. The global
// "active" threshold is 10 minutes, but the "work" account overrides it to
// 30 seconds, so a 2-minute idle active session under "work" is swept.
func TestSweepStale_PerAccountOverrideBeatsGlobal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global := map[string]time.Duration{"active": 10 * time.Minute}
		perAccount := map[string]map[string]time.Duration{
			"work": {"active": 30 * time.Second},
		}
		h, fake, rec := newSweepTestHandlerWithMap(global, perAccount)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-2 * time.Minute)
		h.lastAction[42] = "active"
		h.lastAccount[42] = "work"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (per-account override should fire)", got)
		}
		h.stateMu.Lock()
		_, has := h.lastAction[42]
		h.stateMu.Unlock()
		if has {
			t.Fatalf("lastAction[42] still present after sweep")
		}
	})
}

// TestSweepStale_PerAccountOverrideCaseInsensitive verifies that per-account
// lookups normalize the account name before comparison (matching the rest of
// the config package's case-insensitive treatment of profile names).
func TestSweepStale_PerAccountOverrideCaseInsensitive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global := map[string]time.Duration{"active": 10 * time.Minute}
		// Account stored uppercase in the request; override registered
		// lowercase via SetStaleThresholds.
		perAccount := map[string]map[string]time.Duration{
			"WORK": {"active": 30 * time.Second},
		}
		h, fake, rec := newSweepTestHandlerWithMap(global, perAccount)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-2 * time.Minute)
		h.lastAction[42] = "active"
		h.lastAccount[42] = "Work"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (case-insensitive match should fire)", got)
		}
	})
}

// TestSweepStale_MissingAccountOverrideFallsBackToGlobal verifies that a
// session whose account has no override uses the global default threshold.
func TestSweepStale_MissingAccountOverrideFallsBackToGlobal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global := map[string]time.Duration{"active": 1 * time.Minute}
		perAccount := map[string]map[string]time.Duration{
			"other": {"active": 1 * time.Hour},
		}
		h, fake, rec := newSweepTestHandlerWithMap(global, perAccount)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("P:dotfiles", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-5 * time.Minute)
		h.lastAction[42] = "active"
		h.lastAccount[42] = "personal"
		h.lastSessionTitle[42] = "P:dotfiles"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (global fallback should fire)", got)
		}
	})
}

// TestSweepStale_PerAccountZeroFallsBackToGlobal verifies that a per-account
// override with a zero/missing inner entry falls back to the global default
// rather than disabling sweeping for that action.
func TestSweepStale_PerAccountZeroFallsBackToGlobal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global := map[string]time.Duration{
			"active":   1 * time.Minute,
			"subagent": 1 * time.Minute,
		}
		// "work" overrides subagent only; active should fall back to global.
		perAccount := map[string]map[string]time.Duration{
			"work": {"subagent": 1 * time.Hour},
		}
		h, fake, rec := newSweepTestHandlerWithMap(global, perAccount)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-5 * time.Minute)
		h.lastAction[42] = "active"
		h.lastAccount[42] = "work"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (active should fall back to global)", got)
		}
	})
}

// TestSweepStale_AllZeroThresholdsDisablesSweep verifies that the early-exit
// guard fires when every configured threshold is zero, preserving the
// existing "negative/zero global value disables sweep" behavior.
func TestSweepStale_AllZeroThresholdsDisablesSweep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global := map[string]time.Duration{
			"active":   0,
			"subagent": 0,
			"compact":  0,
		}
		h, fake, rec := newSweepTestHandlerWithMap(global, nil)

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-15 * time.Minute)
		h.lastAction[42] = "active"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d; want 0", got)
		}
		h.stateMu.Lock()
		action := h.lastAction[42]
		h.stateMu.Unlock()
		if action != "active" {
			t.Fatalf("lastAction[42] = %q; want active (untouched)", action)
		}
	})
}

// TestHandler_TracksLastAccount verifies that handleStateUpdate records the
// account name so the sweep can resolve per-account threshold overrides.
func TestHandler_TracksLastAccount(t *testing.T) {
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})

	h.stateMu.Lock()
	got := h.lastAccount[42]
	h.stateMu.Unlock()
	if got != "work" {
		t.Fatalf("lastAccount[42] = %q; want work", got)
	}
}

// TestHandler_LastAccountClearedOnTerminal verifies that terminal actions
// remove the per-window account entry so the maps cannot grow unbounded.
func TestHandler_LastAccountClearedOnTerminal(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.lastAccount[42] = "work"
	h.stateMu.Unlock()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop"})

	h.stateMu.Lock()
	_, present := h.lastAccount[42]
	h.stateMu.Unlock()
	if present {
		t.Fatalf("lastAccount[42] still present after stop")
	}
}

// TestHandler_StateQueryPopulatesWatchdogGlobalDefault verifies that
// handleStateQuery returns the global threshold when no per-account override
// is configured. Both the action name and the resolved threshold are
// included so the TUI can render an age column without re-reading config.
func TestHandler_StateQueryPopulatesWatchdogGlobalDefault(t *testing.T) {
	h := newTestHandler()
	h.SetStaleThresholds(map[string]time.Duration{
		"active":   10 * time.Minute,
		"subagent": 30 * time.Minute,
		"compact":  30 * time.Second,
	}, nil)
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Account: "personal"})

	resp := h.Handle(Request{Type: TypeStateQuery})
	if !resp.OK {
		t.Fatalf("want ok=true, got %+v", resp)
	}
	entry, ok := resp.Watchdog[42]
	if !ok {
		t.Fatalf("Watchdog[42] missing; got %+v", resp.Watchdog)
	}
	if entry.LastAction != "active" {
		t.Errorf("LastAction = %q; want active", entry.LastAction)
	}
	if entry.ThresholdSec != int((10 * time.Minute).Seconds()) {
		t.Errorf("ThresholdSec = %d; want %d (global active)", entry.ThresholdSec, int((10*time.Minute).Seconds()))
	}
	if entry.LastEventAt == 0 {
		t.Errorf("LastEventAt = 0; want non-zero unix timestamp")
	}
}

// TestHandler_StateQueryWatchdogHonorsPerAccountOverride verifies that the
// per-account override wins over the global default, matching SweepStale's
// resolution logic.
func TestHandler_StateQueryWatchdogHonorsPerAccountOverride(t *testing.T) {
	h := newTestHandler()
	h.SetStaleThresholds(
		map[string]time.Duration{"active": 10 * time.Minute},
		map[string]map[string]time.Duration{
			"work": {"active": 30 * time.Second},
		},
	)
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})

	resp := h.Handle(Request{Type: TypeStateQuery})
	entry, ok := resp.Watchdog[42]
	if !ok {
		t.Fatalf("Watchdog[42] missing; got %+v", resp.Watchdog)
	}
	if entry.ThresholdSec != 30 {
		t.Errorf("ThresholdSec = %d; want 30 (per-account override)", entry.ThresholdSec)
	}
}

// TestHandler_StateQueryWatchdogSkipsNonActiveActions verifies that only the
// three sweep-eligible actions (active/subagent/compact) appear in the
// watchdog map. Stop/permission/failure-class windows are tracked in
// lastAction but should not surface here — they don't have an age budget.
func TestHandler_StateQueryWatchdogSkipsNonActiveActions(t *testing.T) {
	h := newTestHandler()
	h.SetStaleThresholds(map[string]time.Duration{"active": 10 * time.Minute}, nil)
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "permission"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 43, Action: "failure"})

	resp := h.Handle(Request{Type: TypeStateQuery})
	if _, ok := resp.Watchdog[42]; ok {
		t.Errorf("Watchdog[42] should be absent for permission action; got %+v", resp.Watchdog[42])
	}
	if _, ok := resp.Watchdog[43]; ok {
		t.Errorf("Watchdog[43] should be absent for failure action; got %+v", resp.Watchdog[43])
	}
}

// TestHandler_TracksLastSeenEventAt verifies that handleStateUpdate records
// the most recent event timestamp per account. The hook-silence sweep (S6)
// will compare this against the configured threshold.
func TestHandler_TracksLastSeenEventAt(t *testing.T) {
	h := newTestHandler()

	before := time.Now()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})
	after := time.Now()

	h.stateMu.Lock()
	got, present := h.lastSeenEventAt["work"]
	h.stateMu.Unlock()
	if !present {
		t.Fatalf("lastSeenEventAt[work] missing after state update")
	}
	if got.Before(before) || got.After(after) {
		t.Fatalf("lastSeenEventAt[work] = %v; want between %v and %v", got, before, after)
	}
}

// TestHandler_LastSeenEventAtIsolatedPerAccount verifies that an event for
// account A does not touch account B's timestamp. The hook-silence detector
// needs strict per-account isolation to detect single-account hook drift.
func TestHandler_LastSeenEventAtIsolatedPerAccount(t *testing.T) {
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})

	h.stateMu.Lock()
	workBefore := h.lastSeenEventAt["work"]
	h.stateMu.Unlock()

	// Advance an event for a different account.
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 43, Action: "active", Account: "personal"})

	h.stateMu.Lock()
	workAfter := h.lastSeenEventAt["work"]
	_, personalPresent := h.lastSeenEventAt["personal"]
	h.stateMu.Unlock()

	if !workAfter.Equal(workBefore) {
		t.Errorf("personal event mutated lastSeenEventAt[work]: before=%v after=%v", workBefore, workAfter)
	}
	if !personalPresent {
		t.Errorf("lastSeenEventAt[personal] missing after personal event")
	}
}

// TestHandler_LastSeenEventAtSkippedWhenAccountEmpty verifies that events
// without an account name (e.g. synthetic probes) do not seed the per-account
// map. The map is keyed by real account names only.
func TestHandler_LastSeenEventAtSkippedWhenAccountEmpty(t *testing.T) {
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active"})

	h.stateMu.Lock()
	_, emptyKeyPresent := h.lastSeenEventAt[""]
	mapLen := len(h.lastSeenEventAt)
	h.stateMu.Unlock()

	if emptyKeyPresent {
		t.Errorf("lastSeenEventAt[\"\"] should not be populated for account-less events")
	}
	if mapLen != 0 {
		t.Errorf("lastSeenEventAt has %d entries; want 0 for account-less event", mapLen)
	}
}

// TestHandler_LastSeenEventAtSurvivesTerminal verifies that the per-account
// map outlives terminal actions on a window. Per-window state is cleared on
// stop/failure/idle/clear, but the per-account map must persist so the
// hook-silence detector can still observe activity from the account.
func TestHandler_LastSeenEventAtSurvivesTerminal(t *testing.T) {
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})

	h.stateMu.Lock()
	before, present := h.lastSeenEventAt["work"]
	h.stateMu.Unlock()
	if !present {
		t.Fatalf("lastSeenEventAt[work] missing before terminal action")
	}

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop"})

	h.stateMu.Lock()
	after, stillPresent := h.lastSeenEventAt["work"]
	h.stateMu.Unlock()
	if !stillPresent {
		t.Fatalf("lastSeenEventAt[work] cleared by terminal action; should persist across sessions")
	}
	if !after.Equal(before) {
		t.Errorf("lastSeenEventAt[work] mutated by terminal action: before=%v after=%v", before, after)
	}
}

// TestHandler_NotifiedHookSilenceLatchReArmed verifies that a fresh event for
// an account clears the one-shot hook-silence latch so a future silence
// stretch can produce another notification.
func TestHandler_NotifiedHookSilenceLatchReArmed(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.notifiedHookSilence["work"] = true
	h.stateMu.Unlock()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})

	h.stateMu.Lock()
	_, latched := h.notifiedHookSilence["work"]
	h.stateMu.Unlock()
	if latched {
		t.Fatalf("notifiedHookSilence[work] should be cleared by a fresh event for the account")
	}
}

// TestHandler_NotifiedHookSilenceUntouchedByOtherAccount verifies that a
// fresh event for one account does not re-arm the latch for another. The
// latch is strictly per-account.
func TestHandler_NotifiedHookSilenceUntouchedByOtherAccount(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.notifiedHookSilence["work"] = true
	h.stateMu.Unlock()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 43, Action: "active", Account: "personal"})

	h.stateMu.Lock()
	stillLatched := h.notifiedHookSilence["work"]
	h.stateMu.Unlock()
	if !stillLatched {
		t.Fatalf("notifiedHookSilence[work] should not be touched by personal event")
	}
}

// TestHandler_HookSilenceNotificationDispatches verifies that the notifier
// dispatches the "hook_silence" action through MaybeNotify when enabled,
// using the new label.
func TestHandler_HookSilenceNotificationDispatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "work", "hook_silence", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatalf("expected hook_silence notification to fire")
		}
		got := calls.snapshot()[0]
		if got.title != "work" {
			t.Errorf("title = %q; want %q", got.title, "work")
		}
		if got.subtitle != "Hook silence detected" {
			t.Errorf("subtitle = %q; want %q", got.subtitle, "Hook silence detected")
		}
	})
}

// TestHandler_HookSilenceNotificationSuppressedByConfig verifies that the
// notifier honors NotificationConfig.HookSilence = &false.
func TestHandler_HookSilenceNotificationSuppressedByConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		disabled := false
		cfg := &config.NotificationConfig{HookSilence: &disabled}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "work", "hook_silence", false)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatalf("hook_silence should be suppressed when HookSilence=&false")
		}
	})
}

// newHookSilenceTestHandler builds a Handler wired with a recording notifier
// and a fake Kitty client. The global / perAccount thresholds drive
// SweepHookSilence; tests populate fake.windows to control which accounts the
// sweep considers "running" and seed h.lastSeenEventAt under stateMu.
func newHookSilenceTestHandler(global time.Duration, perAccount map[string]time.Duration) (*Handler, *fakeKittyClient, *callRecorder) {
	guard := NewStateGuard(time.Hour)
	notifier, rec := newTestNotifier(&config.NotificationConfig{})
	h := NewHandler(guard, notifier)
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	h.SetHookSilenceThresholds(global, perAccount)
	return h, fake, rec
}

// fakeWindowsForAccount builds a kitten-@-ls-shaped windows list with a
// single tab whose only window carries the given cc_account user var.
// Mirrors fakeOSWindowsWithTab but populates UserVars.Account so the
// hook-silence sweep can enumerate the account.
func fakeWindowsForAccount(account string) []kitty.OSWindow {
	return []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID: 100,
					Windows: []kitty.Window{
						{ID: 42, UserVars: kitty.UserVars{Account: account}},
					},
				},
			},
		},
	}
}

// TestSweepHookSilence_SilentAccountNotifiesOnce covers the happy path: an
// account whose last event predates the threshold fires exactly one
// notification on the first sweep, and the latch prevents re-firing on a
// subsequent sweep within the same silence stretch.
func TestSweepHookSilence_SilentAccountNotifiesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, nil)
		fake.windows = fakeWindowsForAccount("work")

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("first sweep dispatches = %d; want 1", got)
		}
		got := rec.snapshot()[0]
		if got.title != "work" {
			t.Errorf("dispatch title = %q; want work", got.title)
		}
		if got.subtitle != "Hook silence detected" {
			t.Errorf("dispatch subtitle = %q; want %q", got.subtitle, "Hook silence detected")
		}

		// Latch suppresses subsequent ticks within the same stretch.
		h.SweepHookSilence(now)
		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("after second sweep total dispatches = %d; want 1 (latch should suppress)", got)
		}

		h.stateMu.Lock()
		latched := h.notifiedHookSilence["work"]
		h.stateMu.Unlock()
		if !latched {
			t.Fatalf("notifiedHookSilence[work] = false; want true after sweep fired")
		}
	})
}

// TestSweepHookSilence_LatchClearsOnFreshEventThenReFires verifies the full
// cycle: sweep → notify → fresh event clears latch → re-stall → sweep
// produces a second notification. The 10s per-(synthetic-window, action)
// cooldown is reset between stretches so it cannot suppress the second
// dispatch (the production 5-min cadence makes the cooldown irrelevant in
// real-world use, but tests advance time virtually).
func TestSweepHookSilence_LatchClearsOnFreshEventThenReFires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, nil)
		fake.windows = fakeWindowsForAccount("work")

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("first sweep dispatches = %d; want 1", got)
		}

		// Fresh event for "work" — S5 invariant: re-arms the latch.
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})

		h.stateMu.Lock()
		_, latched := h.notifiedHookSilence["work"]
		h.stateMu.Unlock()
		if latched {
			t.Fatalf("notifiedHookSilence[work] should be cleared by a fresh event")
		}

		// Bypass the notifier's per-(windowID, action) cooldown so a re-stall
		// produces a new dispatch. Reset against the same synthetic ID the
		// sweep uses internally.
		h.notifier.ResetCooldown(hookSilenceWindowID("work"))

		// Re-stall: backdate the per-account timestamp and sweep again.
		now2 := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now2.Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(now2)
		synctest.Wait()

		if got := rec.len(); got != 2 {
			t.Fatalf("after re-stall total dispatches = %d; want 2", got)
		}
	})
}

// TestSweepHookSilence_ZeroThresholdSkipped verifies that a stale account is
// skipped when neither the global nor a per-account override supplies a
// positive threshold — even though "work" is running and its lastSeenEventAt
// entry is well past any reasonable bound.
func TestSweepHookSilence_ZeroThresholdSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(0, nil)
		fake.windows = fakeWindowsForAccount("work")

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (zero threshold disables)", got)
		}
		h.stateMu.Lock()
		_, latched := h.notifiedHookSilence["work"]
		h.stateMu.Unlock()
		if latched {
			t.Fatalf("notifiedHookSilence[work] should not be set when threshold is zero")
		}
	})
}

// TestSweepHookSilence_NotRunningSkipped verifies the running-window gate:
// an account whose lastSeenEventAt is stale but which has no live Kitty
// window carrying its cc_account user var is silently skipped. This avoids
// spurious notifications for accounts the user has not launched today.
func TestSweepHookSilence_NotRunningSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, nil)
		// Only "personal" is running; "work" is silent but not launched.
		fake.windows = fakeWindowsForAccount("personal")

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.lastSeenEventAt["personal"] = now.Add(-1 * time.Minute)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (work is silent but not running; personal is fresh)", got)
		}
	})
}

// TestSweepHookSilence_SuppressedByConfig verifies the notifier still
// honors NotificationConfig.HookSilence = &false even when the sweep
// detects a real silence stretch. The latch is still set (the sweep does
// not consult notification config) so the user never gets a stale-state
// notification storm if they later toggle the flag back on.
func TestSweepHookSilence_SuppressedByConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		disabled := false
		notifier, rec := newTestNotifier(&config.NotificationConfig{HookSilence: &disabled})
		h := NewHandler(guard, notifier)
		fake := &fakeKittyClient{}
		h.SetKittyClient(fake)
		h.SetHookSilenceThresholds(1*time.Hour, nil)
		fake.windows = fakeWindowsForAccount("work")

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (HookSilence config = &false)", got)
		}
	})
}

// TestSweepHookSilence_PerAccountOverride verifies that a positive per-account
// override wins over the global default. With global = 1h and "work" = 30s,
// a 5-minute stale "work" fires while a 5-minute stale "personal" (still
// under the global) is left alone.
func TestSweepHookSilence_PerAccountOverride(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, map[string]time.Duration{
			"work": 30 * time.Second,
		})
		fake.windows = []kitty.OSWindow{
			{
				ID: 1,
				Tabs: []kitty.Tab{
					{ID: 100, Windows: []kitty.Window{{ID: 42, UserVars: kitty.UserVars{Account: "work"}}}},
					{ID: 101, Windows: []kitty.Window{{ID: 43, UserVars: kitty.UserVars{Account: "personal"}}}},
				},
			},
		}

		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-5 * time.Minute)
		h.lastSeenEventAt["personal"] = now.Add(-5 * time.Minute)
		h.stateMu.Unlock()

		h.SweepHookSilence(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (work fires; personal still under global)", got)
		}
		if title := rec.snapshot()[0].title; title != "work" {
			t.Errorf("dispatch title = %q; want work", title)
		}
	})
}

// TestSweepHookSilence_NoEventEverSkipped verifies that a running account
// with no entry in lastSeenEventAt is skipped: the daemon has not seen any
// event for that account yet (e.g. just-started daemon), so there is no
// baseline to measure silence against.
func TestSweepHookSilence_NoEventEverSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, nil)
		fake.windows = fakeWindowsForAccount("work")
		// Intentionally do not seed h.lastSeenEventAt["work"].

		h.SweepHookSilence(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (no baseline for work)", got)
		}
	})
}

// TestSweepHookSilence_NilKittyClient verifies the no-op when no Kitty
// client has been injected (test environments without Kitty). The sweep
// short-circuits before any lock acquisition.
func TestSweepHookSilence_NilKittyClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{})
		h := NewHandler(guard, notifier)
		h.SetHookSilenceThresholds(1*time.Hour, nil)
		// Deliberately do NOT call SetKittyClient.

		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = time.Now().Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (nil kitty client)", got)
		}
	})
}

// TestSweepHookSilence_ListWindowsErrorTolerated verifies that a ListWindows
// failure is logged at debug and swallowed; the sweep does not panic and
// fires no notifications (it has no running-account set to compare against).
func TestSweepHookSilence_ListWindowsErrorTolerated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newHookSilenceTestHandler(1*time.Hour, nil)
		fake.listErr = errors.New("kitten unavailable")

		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = time.Now().Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.SweepHookSilence(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (list-windows error swallowed)", got)
		}
	})
}

// TestHandler_EventLog_AppendOnAllowed verifies that an allowed PreToolUse →
// active transition appends a non-suppressed SessionEvent carrying the request's
// tool_name through to the ring buffer.
func TestHandler_EventLog_AppendOnAllowed(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
		Event:    "PreToolUse",
		ToolName: "Bash",
	})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d; want 1 (%+v)", len(resp.Events), resp.Events)
	}
	ev := resp.Events[0]
	if ev.Suppressed {
		t.Errorf("event suppressed=true; want false on allowed path")
	}
	if ev.Action != "active" || ev.Event != "PreToolUse" || ev.ToolName != "Bash" {
		t.Errorf("event = %+v; want action=active event=PreToolUse tool_name=Bash", ev)
	}
	if ev.At <= 0 {
		t.Errorf("event At = %d; want > 0", ev.At)
	}
}

// TestHandler_EventLog_AppendOnGuardSuppressed exercises the guard-suppressed
// return at the top of handleStateUpdate: a subagent guard suppresses a
// follow-up "active" (priority 1 < subagent priority 2), and the suppressed
// transition is still recorded in the buffer with Suppressed=true.
func TestHandler_EventLog_AppendOnGuardSuppressed(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostToolUse", ToolName: "Read"})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("events = %d; want 2 (%+v)", len(resp.Events), resp.Events)
	}
	if resp.Events[0].Suppressed {
		t.Errorf("first event suppressed=true; want false (subagent allowed)")
	}
	if !resp.Events[1].Suppressed {
		t.Errorf("second event suppressed=false; want true (active suppressed by subagent guard)")
	}
	if resp.Events[1].Action != "active" || resp.Events[1].ToolName != "Read" {
		t.Errorf("second event = %+v; want action=active tool_name=Read", resp.Events[1])
	}
}

// TestHandler_EventLog_AppendOnCopilotMidSubagentSuppression confirms that the
// Copilot agentStop-during-subagent gate also records its suppression — without
// this, the user has no signal in the detail pane explaining why the bolt did
// not flip to a stop icon.
func TestHandler_EventLog_AppendOnCopilotMidSubagentSuppression(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "agentStop"})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("events = %d; want 2 (%+v)", len(resp.Events), resp.Events)
	}
	if resp.Events[1].Event != "agentStop" || !resp.Events[1].Suppressed {
		t.Errorf("second event = %+v; want event=agentStop suppressed=true", resp.Events[1])
	}
}

// TestHandler_EventLog_TrimsAtMax pushes maxEventsPerWindow + 5 events through
// the allowed path and asserts the buffer retains exactly maxEventsPerWindow
// (oldest dropped from the front).
func TestHandler_EventLog_TrimsAtMax(t *testing.T) {
	h := newTestHandler()

	total := maxEventsPerWindow + 5
	for i := 0; i < total; i++ {
		h.Handle(Request{
			Type:     TypeStateUpdate,
			WindowID: 1,
			Action:   "active",
			Event:    "PreToolUse",
			ToolName: "tool",
		})
	}

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != maxEventsPerWindow {
		t.Fatalf("events = %d; want %d", len(resp.Events), maxEventsPerWindow)
	}
}

// TestHandler_EventLog_CleanupOnStop pushes two non-terminal events and then a
// terminal "stop"; the buffer must be empty afterwards because terminal events
// trigger the cleanup block and do not re-seed the ring.
func TestHandler_EventLog_CleanupOnStop(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostToolUse"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "Stop"})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 0 {
		t.Fatalf("events after stop = %d; want 0 (%+v)", len(resp.Events), resp.Events)
	}
}

// TestHandler_EventLog_CleanupOnSweepStale verifies that SweepStale clears the
// per-window ring alongside the other per-window maps. Without this, a swept
// window's history would persist in memory indefinitely.
func TestHandler_EventLog_CleanupOnSweepStale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, _, _ := newSweepTestHandler(10 * time.Minute)

		h.Handle(Request{
			Type:         TypeStateUpdate,
			WindowID:     1,
			Action:       "active",
			Event:        "PreToolUse",
			ToolName:     "Bash",
			SessionTitle: "W:api",
		})

		// Backdate the activity timestamp so the sweep treats the window as stale.
		h.stateMu.Lock()
		h.activity[1] = time.Now().Add(-15 * time.Minute)
		h.stateMu.Unlock()

		h.SweepStale(time.Now())
		synctest.Wait()

		resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
		if !resp.OK {
			t.Fatalf("session detail: want OK=true, got %+v", resp)
		}
		if len(resp.Events) != 0 {
			t.Fatalf("events after SweepStale = %d; want 0 (%+v)", len(resp.Events), resp.Events)
		}
	})
}

// TestHandler_SessionDetail_UnknownWindow asserts that querying detail for a
// window that has never produced an event returns a non-nil empty slice. The
// TUI relies on this to distinguish "no daemon error" from "no events".
func TestHandler_SessionDetail_UnknownWindow(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 99})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if resp.Events == nil {
		t.Fatal("Events = nil; want non-nil empty slice")
	}
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d; want 0", len(resp.Events))
	}
}

// TestHookSilenceWindowID_StableAndNegative verifies the synthetic window-ID
// scheme used for notifier dedup: same account → same ID across calls,
// different accounts → different IDs, and every ID is negative so it cannot
// collide with any real Kitty window ID (always positive).
func TestHookSilenceWindowID_StableAndNegative(t *testing.T) {
	a1 := hookSilenceWindowID("work")
	a2 := hookSilenceWindowID("work")
	b1 := hookSilenceWindowID("personal")
	if a1 != a2 {
		t.Errorf("hookSilenceWindowID(work) not stable: %d vs %d", a1, a2)
	}
	if a1 == b1 {
		t.Errorf("hookSilenceWindowID(work) collided with personal: %d", a1)
	}
	if a1 >= 0 || b1 >= 0 {
		t.Errorf("synthetic IDs should be negative; got work=%d personal=%d", a1, b1)
	}
	// Case-insensitive — the per-account threshold lookup lowercases too.
	if hookSilenceWindowID("work") != hookSilenceWindowID("WORK") {
		t.Errorf("hookSilenceWindowID should be case-insensitive")
	}
}
