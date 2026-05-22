package daemon

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

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

func TestHandler_CopilotCompactCleanupSynthesizesStop(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Harness:  "copilot",
		Action:   "compact",
		Event:    "preCompact",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "compact" {
		t.Fatalf("Copilot preCompact should show compact icon, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "idle",
		Event:            "notification",
		CurrentIconState: "compact",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "stop" {
		t.Fatalf("Copilot compaction cleanup should synthesize stop icon, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "active",
		Event:            "postToolUse",
		CurrentIconState: "stop",
	})
	if !resp.OK || resp.Allowed {
		t.Fatalf("Copilot post-compaction stop should block non-prompt active follow-up, got %+v", resp)
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
			notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
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
				notifier.dispatch = func(_ context.Context, _, subtitle string) error {
					mu.Lock()
					calls = append(calls, subtitle)
					mu.Unlock()
					return nil
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

// TestHandler_CopilotTurnEndClearsBolt pins the user-visible fix: when Copilot
// fires agentStop with no subagents in flight, the daemon must transition the
// tab from the bolt to the stop icon, not preserve the existing bolt.
// Regression source: commit 5858318.
func TestHandler_CopilotTurnEndClearsBolt(t *testing.T) {
	h := newTestHandler()
	// Seed the guard at "active" (bolt visible).
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Account: "work",
		Event: "preToolUse", Action: "active"})
	// agentStop arrives — now mapped to action "stop" with PreservesIcon=false.
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Account: "work",
		Event: "agentStop", Action: "stop", PreservesIcon: false})
	if !resp.OK || !resp.Allowed {
		t.Fatalf("agentStop should land: %+v", resp)
	}
	if resp.IconState != IconState("stop") {
		t.Errorf("IconState = %q, want stop", resp.IconState)
	}
}

// TestHandler_CopilotTurnEndDuringSubagentPreserves pins the mid-subagent
// suppression branch in handleStateUpdate (handler_state_update.go:152): while a
// subagent is running, an agentStop must not surface a terminal stop icon — the
// reference-counted 🤖 guard remains the source of truth until the last
// subagent drains. subagentCount is seeded directly to isolate the gate from
// the unrelated SubagentStart bookkeeping.
func TestHandler_CopilotTurnEndDuringSubagentPreserves(t *testing.T) {
	h := newTestHandler()
	h.stateMu.Lock()
	h.subagentCount[42] = 1
	h.stateMu.Unlock()

	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Account: "work",
		Event: "agentStop", Action: "stop", PreservesIcon: false})
	if !resp.OK {
		t.Fatalf("agentStop should be handled OK: %+v", resp)
	}
	if resp.Allowed {
		t.Errorf("Allowed = true; want false (gate must suppress)")
	}
	if resp.IconState == IconState("stop") {
		t.Errorf("IconState = stop; want preserve/empty (gate must not transition)")
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
