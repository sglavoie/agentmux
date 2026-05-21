package daemon

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestHandler_CopilotMidSubagentSuppression pins the contract for the Copilot
// mid-subagent gate in handleStateUpdate: while subagentCount[windowID] > 0,
// agentStop (Copilot's per-turn child-complete signal) and permissionRequest
// (speculative, fires for every tool call) must not overwrite the 🤖 guard, and
// any pending permission notification scheduled before the gate fires must be
// cleared. sessionEnd is intentionally excluded so a real session end still
// resets state. Each subtest stands alone — they share no handler state.
func TestHandler_CopilotMidSubagentSuppression(t *testing.T) {
	t.Run("agentStop during subagent suppressed", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})

		// agentStop resolves to a preserve-current active update. While a
		// subagent is running it must still be suppressed entirely — no
		// icon change, no counter clear.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "agentStop", PreservesIcon: true})
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

	t.Run("agentStop after subagent drains preserves bolt", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})
		// subagentStop drains counter to 0 and clears the guard.
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "subagentStop"})

		// agentStop now arrives with no subagents running — gate falls
		// through, but the child-complete event must preserve the parent bolt.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "agentStop", PreservesIcon: true})
		if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
			t.Fatalf("agentStop after drained subagents: want icon_state=%q, got %+v", IconStatePreserve, resp)
		}
	})

	t.Run("gate is no-op when no subagent is running", func(t *testing.T) {
		// agentStop on a fresh handler must remain a no-op preserve-current
		// update — proves the gate cannot misfire when subagentCount[windowID]
		// == 0.
		h := newTestHandler()
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "agentStop", PreservesIcon: true})
		if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
			t.Fatalf("agentStop with no subagent: want icon_state=%q, got %+v", IconStatePreserve, resp)
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
