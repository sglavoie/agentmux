package daemon

import (
	"context"
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
