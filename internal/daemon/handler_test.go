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
