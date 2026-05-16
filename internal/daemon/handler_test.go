package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

func newTestHandler() *Handler {
	guard := NewStateGuard(time.Hour)
	notifier := NewNotifier(&config.NotificationConfig{})
	notifier.dispatch = func(_, _ string) error { return nil }
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
		notifier.dispatch = func(_, _ string) error { return nil }
		h := NewHandler(guard, notifier)

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: tc.prev})
		time.Sleep(5 * time.Millisecond) // let the guard expire
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "idle"})
		if !resp.OK || !resp.Allowed || resp.IconState != tc.wantIcon {
			t.Errorf("%s (guard expired): want icon_state=%q, got %+v", tc.wantLabel, tc.wantIcon, resp)
		}
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
	notifier.dispatch = func(_, _ string) error { return nil }
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

func TestHandler_AutoApprovedPermissionNoNotification(t *testing.T) {
	// Auto-approval sequence: permission → active. The pending permission
	// notification should be cancelled before it fires.
	guard := NewStateGuard(time.Hour)
	cfg := &config.NotificationConfig{Permission: true, PermissionDebounceMs: 200}
	notifier := NewNotifier(cfg)
	var calls []string
	notifier.dispatch = func(_, subtitle string) error {
		calls = append(calls, subtitle)
		return nil
	}
	h := NewHandler(guard, notifier)

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "permission"})
	// Simulate auto-approval arriving within the debounce window.
	time.Sleep(20 * time.Millisecond)
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active"})

	time.Sleep(300 * time.Millisecond) // wait past the debounce window
	if len(calls) > 0 {
		t.Fatalf("expected no permission notification for auto-approved permission, got %v", calls)
	}
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
