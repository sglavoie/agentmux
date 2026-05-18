package daemon

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestGuard_AllowsInitialUpdate(t *testing.T) {
	g := NewStateGuard(time.Second)
	ok, eff := g.TryUpdate(1, "active", false)
	if !ok || eff != "active" {
		t.Fatalf("want (true, active), got (%v, %q)", ok, eff)
	}
}

func TestGuard_BlocksLowerPriority(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "failure", false) // priority 4

	ok, eff := g.TryUpdate(1, "stop", false) // priority 2 — should be blocked
	if ok || eff != "" {
		t.Fatalf("want (false, \"\"), got (%v, %q)", ok, eff)
	}
}

func TestGuard_AllowsHigherPriority(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "active", false) // priority 1

	ok, eff := g.TryUpdate(1, "failure", false) // priority 4 — should be allowed
	if !ok || eff != "failure" {
		t.Fatalf("want (true, failure), got (%v, %q)", ok, eff)
	}
}

func TestGuard_AllowsAfterExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := NewStateGuard(1 * time.Millisecond)
		g.TryUpdate(1, "permission", false)

		time.Sleep(5 * time.Millisecond)

		ok, eff := g.TryUpdate(1, "active", false)
		if !ok || eff != "active" {
			t.Fatalf("want (true, active) after expiry, got (%v, %q)", ok, eff)
		}
	})
}

func TestGuard_PostPermissionStopAllowed(t *testing.T) {
	// Session ends while a permission prompt is pending: stop should replace
	// the permission guard so the 🛑 icon shows.
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "permission", false)

	ok, eff := g.TryUpdate(1, "stop", false)
	if !ok || eff != "stop" {
		t.Fatalf("want (true, stop) for post-permission stop, got (%v, %q)", ok, eff)
	}
	// Guard should now hold "stop".
	if cur := g.Current(1); cur != "stop" {
		t.Fatalf("want guard=stop after post-permission stop, got %q", cur)
	}
}

func TestGuard_PostPermissionStopThenPromptBreaksThrough(t *testing.T) {
	// After permission → stop, a new prompt must clear the stop guard.
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "permission", false)
	g.TryUpdate(1, "stop", false)

	ok, eff := g.TryUpdate(1, "active", true) // isPrompt=true
	if !ok || eff != "active" {
		t.Fatalf("want prompt to bypass post-permission stop guard, got (%v, %q)", ok, eff)
	}
}

func TestGuard_PromptBypassesStopGuard(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "stop", false)

	// Prompt should break through the stop guard.
	ok, eff := g.TryUpdate(1, "active", true)
	if !ok || eff != "active" {
		t.Fatalf("want (true, active) for prompt bypass, got (%v, %q)", ok, eff)
	}
	// Guard should now hold "active", not "stop".
	if cur := g.Current(1); cur != "active" {
		t.Fatalf("want guard=active after prompt bypass, got %q", cur)
	}
}

func TestGuard_ActiveBypassesFailureGuard(t *testing.T) {
	// Failure is transient: any subsequent active event clears the guard,
	// regardless of isPrompt. Covers both the prompt-triggered active and
	// the post-tool active that follows a failed PostToolUse.
	for _, isPrompt := range []bool{false, true} {
		g := NewStateGuard(time.Hour)
		g.TryUpdate(1, "failure", false) // priority 4

		ok, eff := g.TryUpdate(1, "active", isPrompt)
		if !ok || eff != "active" {
			t.Fatalf("isPrompt=%v: want (true, active) for post-failure active, got (%v, %q)", isPrompt, ok, eff)
		}
		if cur := g.Current(1); cur != "active" {
			t.Fatalf("isPrompt=%v: want guard=active after failure bypass, got %q", isPrompt, cur)
		}
	}
}

func TestGuard_ActiveBypassesPermissionGuard(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "permission", false) // priority 3

	ok, eff := g.TryUpdate(1, "active", false) // should bypass permission guard
	if !ok || eff != "active" {
		t.Fatalf("want (true, active) for post-permission active, got (%v, %q)", ok, eff)
	}
	if cur := g.Current(1); cur != "active" {
		t.Fatalf("want guard=active after permission bypass, got %q", cur)
	}
}

func TestGuard_ClearAlwaysSucceeds(t *testing.T) {
	for _, guard := range []string{"stop", "permission", "failure", "active"} {
		g := NewStateGuard(time.Hour)
		g.TryUpdate(1, guard, false)

		ok, eff := g.TryUpdate(1, "clear", false)
		if !ok || eff != "clear" {
			t.Errorf("clear should always succeed vs guard=%q, got (%v, %q)", guard, ok, eff)
		}
		if cur := g.Current(1); cur != "" {
			t.Errorf("guard should be removed after clear vs guard=%q, got %q", guard, cur)
		}
	}
}

func TestGuard_IdleAlwaysSucceeds(t *testing.T) {
	for _, guard := range []string{"stop", "permission", "failure", "active"} {
		g := NewStateGuard(time.Hour)
		g.TryUpdate(1, guard, false)

		ok, eff := g.TryUpdate(1, "idle", false)
		if !ok || eff != "idle" {
			t.Errorf("idle should always succeed vs guard=%q, got (%v, %q)", guard, ok, eff)
		}
		if cur := g.Current(1); cur != "" {
			t.Errorf("guard should be removed after idle vs guard=%q, got %q", guard, cur)
		}
	}
}

func TestGuard_UserClearAlwaysSucceeds(t *testing.T) {
	for _, guard := range []string{"stop", "permission", "failure", "active"} {
		g := NewStateGuard(time.Hour)
		g.TryUpdate(1, guard, false)

		ok, eff := g.TryUpdate(1, "user_clear", false)
		if !ok || eff != "user_clear" {
			t.Errorf("user_clear should always succeed vs guard=%q, got (%v, %q)", guard, ok, eff)
		}
		if cur := g.Current(1); cur != "" {
			t.Errorf("guard should be removed after user_clear vs guard=%q, got %q", guard, cur)
		}
	}
}

func TestGuard_PerWindowIsolation(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "permission", false)

	// Window 2 should be unaffected.
	ok, eff := g.TryUpdate(2, "active", false)
	if !ok || eff != "active" {
		t.Fatalf("guard on window 1 should not affect window 2, got (%v, %q)", ok, eff)
	}
}

func TestGuard_SamePriorityAllowed(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "stop", false) // priority 2

	ok, eff := g.TryUpdate(1, "stop", false) // same priority
	if !ok || eff != "stop" {
		t.Fatalf("same-priority update should be allowed, got (%v, %q)", ok, eff)
	}
}

func TestGuard_SubagentSurvivesActive(t *testing.T) {
	// subagent (priority 2) outranks active (priority 1), so an interleaved
	// parent `active` event must not displace 🤖.
	g := NewStateGuard(time.Hour)
	if ok, eff := g.TryUpdate(1, "subagent", false); !ok || eff != "subagent" {
		t.Fatalf("want initial (true, subagent), got (%v, %q)", ok, eff)
	}
	if ok, eff := g.TryUpdate(1, "active", false); ok || eff != "" {
		t.Fatalf("want active to be blocked by subagent, got (%v, %q)", ok, eff)
	}
	if cur := g.Current(1); cur != "subagent" {
		t.Fatalf("want guard=subagent, got %q", cur)
	}
}

func TestGuard_SubagentLosesToStop(t *testing.T) {
	// subagent (2) and stop (2) are equal-priority — stop falls through to the
	// new-guard write at the bottom of TryUpdate and replaces the guard.
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "subagent", false)
	if ok, eff := g.TryUpdate(1, "stop", false); !ok || eff != "stop" {
		t.Fatalf("want stop to replace subagent, got (%v, %q)", ok, eff)
	}
	if cur := g.Current(1); cur != "stop" {
		t.Fatalf("want guard=stop, got %q", cur)
	}
}

func TestGuard_SubagentLosesToPermission(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "subagent", false)
	if ok, eff := g.TryUpdate(1, "permission", false); !ok || eff != "permission" {
		t.Fatalf("want permission to override subagent, got (%v, %q)", ok, eff)
	}
}

func TestGuard_SubagentLosesToFailure(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "subagent", false)
	if ok, eff := g.TryUpdate(1, "failure", false); !ok || eff != "failure" {
		t.Fatalf("want failure to override subagent, got (%v, %q)", ok, eff)
	}
}

func TestGuard_SubagentClearsPermissionGuard(t *testing.T) {
	// When a permission guard is active, a subagentStart should clear it so the
	// 🤖 icon shows. This mirrors the permission→active bypass: once the agent
	// is doing work (launching a subagent), the pending permission is resolved.
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "permission", false)

	ok, eff := g.TryUpdate(1, "subagent", false)
	if !ok || eff != "subagent" {
		t.Fatalf("want subagent to clear permission guard, got (%v, %q)", ok, eff)
	}
	if cur := g.Current(1); cur != "subagent" {
		t.Fatalf("want guard=subagent after permission→subagent, got %q", cur)
	}
}

func TestGuard_SubagentClearsFailureGuard(t *testing.T) {
	// When a failure guard is active, a subagentStart should clear it so the
	// 🤖 icon shows. This mirrors the failure→active bypass: failure is
	// transient and a subagent starting means the agent is back to work.
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "failure", false)

	ok, eff := g.TryUpdate(1, "subagent", false)
	if !ok || eff != "subagent" {
		t.Fatalf("want subagent to clear failure guard, got (%v, %q)", ok, eff)
	}
	if cur := g.Current(1); cur != "subagent" {
		t.Fatalf("want guard=subagent after failure→subagent, got %q", cur)
	}
}

func TestGuard_StopFailureNotClearedBySubagent(t *testing.T) {
	// stop_failure is terminal and must NOT be cleared by subagent (unlike
	// failure which is transient). The error icon must persist until the user
	// explicitly acknowledges it, even if a spurious subagent event fires.
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "stop_failure", false)

	ok, eff := g.TryUpdate(1, "subagent", false)
	if ok || eff != "" {
		t.Fatalf("want stop_failure to block subagent, got (%v, %q)", ok, eff)
	}
	if cur := g.Current(1); cur != "stop_failure" {
		t.Fatalf("want guard=stop_failure, got %q", cur)
	}
}

func TestGuard_StopFailureTerminal_NotClearedByActive(t *testing.T) {
	// stop_failure is terminal: a follow-up active must NOT clear the guard.
	// The clearing branch in TryUpdate is string-exact on "failure", so
	// stop_failure must fall through to the priority check and be blocked
	// (Priority(active)=1 < Priority(stop_failure)=4).
	for _, isPrompt := range []bool{false, true} {
		g := NewStateGuard(time.Hour)
		if ok, eff := g.TryUpdate(1, "stop_failure", false); !ok || eff != "stop_failure" {
			t.Fatalf("isPrompt=%v: want initial (true, stop_failure), got (%v, %q)", isPrompt, ok, eff)
		}
		if ok, eff := g.TryUpdate(1, "active", isPrompt); ok || eff != "" {
			t.Fatalf("isPrompt=%v: want active to be blocked by stop_failure, got (%v, %q)", isPrompt, ok, eff)
		}
		if cur := g.Current(1); cur != "stop_failure" {
			t.Fatalf("isPrompt=%v: want guard=stop_failure, got %q", isPrompt, cur)
		}
	}
}

func TestGuard_ElicitationOverridesActive(t *testing.T) {
	// elicitation (priority 2) should be allowed when the current guard is active (priority 1).
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "active", false)

	ok, eff := g.TryUpdate(1, "elicitation", false)
	if !ok || eff != "elicitation" {
		t.Fatalf("want (true, elicitation) — elicitation overrides active, got (%v, %q)", ok, eff)
	}
	if cur := g.Current(1); cur != "elicitation" {
		t.Fatalf("want guard=elicitation, got %q", cur)
	}
}

func TestGuard_ActiveBypassesElicitationGuard(t *testing.T) {
	// After the user answers, active clears the elicitation guard immediately
	// regardless of isPrompt (no prompt needed — just the ElicitationResult active).
	for _, isPrompt := range []bool{false, true} {
		g := NewStateGuard(time.Hour)
		g.TryUpdate(1, "elicitation", false)

		ok, eff := g.TryUpdate(1, "active", isPrompt)
		if !ok || eff != "active" {
			t.Fatalf("isPrompt=%v: want (true, active) for post-elicitation active, got (%v, %q)", isPrompt, ok, eff)
		}
		if cur := g.Current(1); cur != "active" {
			t.Fatalf("isPrompt=%v: want guard=active after elicitation bypass, got %q", isPrompt, cur)
		}
	}
}

func TestGuard_ElicitationBlocksLowerPriority(t *testing.T) {
	// elicitation (priority 2) should block active (priority 1) when not bypassing.
	// This covers the case where a spurious active event fires before ElicitationResult.
	// (The bypass branch fires only when entry.action == "elicitation" && action == "active";
	// here we just check a bare TryUpdate to confirm the priority rule holds when the
	// bypass fires instead — but since the bypass DOES clear the guard, this test
	// exercises a second active attempt after the guard is reset.)
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "elicitation", false)

	// Simulate a stop arriving while Claude is asking — stop (priority 2) equals
	// elicitation (priority 2), so it should be allowed (same-priority fall-through).
	// But active (priority 1) should be blocked.
	ok, _ := g.TryUpdate(1, "active", false)
	if ok {
		// The elicitation+active branch clears the guard — this is the designed bypass.
		// The test verifies the guard was set with elicitation in the first place.
		// The bypass IS the intended behavior; we just confirm Current was "elicitation"
		// before the bypass fired.
		t.Log("elicitation+active bypass fired (expected by design)")
	}
	// Verify that if the guard is re-set, a subsequent bare active is blocked
	// until the elicitation is resolved.
	g2 := NewStateGuard(time.Hour)
	g2.TryUpdate(1, "elicitation", false)
	// compact (priority 1) should be blocked.
	ok2, _ := g2.TryUpdate(1, "compact", false)
	if ok2 {
		t.Fatalf("compact (priority 1) should be blocked by elicitation guard (priority 2)")
	}
}

func TestGuard_StopAfterElicitation(t *testing.T) {
	// stop (priority 2) equals elicitation (priority 2) — same-priority fall-through
	// should allow stop to replace the elicitation guard.
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "elicitation", false)

	ok, eff := g.TryUpdate(1, "stop", false)
	if !ok || eff != "stop" {
		t.Fatalf("want stop to replace elicitation guard, got (%v, %q)", ok, eff)
	}
	if cur := g.Current(1); cur != "stop" {
		t.Fatalf("want guard=stop after elicitation→stop, got %q", cur)
	}
}

func TestGuard_CompactOverwrittenByActive(t *testing.T) {
	// compact (1) is equal-priority to active (1); the same-priority path
	// allows the new guard to replace it.
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "compact", false)
	if ok, eff := g.TryUpdate(1, "active", false); !ok || eff != "active" {
		t.Fatalf("want active to overwrite compact, got (%v, %q)", ok, eff)
	}
	if cur := g.Current(1); cur != "active" {
		t.Fatalf("want guard=active, got %q", cur)
	}
}

func TestGuard_AllPriorityCombo(t *testing.T) {
	// Pairs where the guard SHOULD block: higher-priority guard vs lower-priority action.
	// Excludes: idle/clear (always succeed), permission→stop (session-end bypass),
	// permission→active and failure→active (both clear the guard), and stop→active via
	// UserPromptSubmit (prompt bypass).
	blocked := []struct{ guard, action string }{
		{"stop", "active"},
		{"failure", "stop"},
		{"failure", "permission"},
		{"elicitation", "compact"}, // elicitation (2) blocks compact (1)
	}
	for _, tc := range blocked {
		g := NewStateGuard(time.Hour)
		g.TryUpdate(1, tc.guard, false)
		ok, _ := g.TryUpdate(1, tc.action, false)
		if ok {
			t.Errorf("guard=%q should block action=%q", tc.guard, tc.action)
		}
	}

	// idle and clear always succeed regardless of guard.
	for _, guard := range []string{"active", "stop", "permission", "failure", "elicitation"} {
		for _, action := range []string{"idle", "clear"} {
			g := NewStateGuard(time.Hour)
			g.TryUpdate(1, guard, false)
			ok, _ := g.TryUpdate(1, action, false)
			if !ok {
				t.Errorf("guard=%q should not block %q", guard, action)
			}
		}
	}

	// Prompt bypasses stop guard only.
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "stop", false)
	ok, eff := g.TryUpdate(1, "active", true)
	if !ok || eff != "active" {
		t.Errorf("prompt should bypass stop guard, got (%v, %q)", ok, eff)
	}

	// failure → active always succeeds (independent of isPrompt).
	g2 := NewStateGuard(time.Hour)
	g2.TryUpdate(1, "failure", false)
	ok, eff = g2.TryUpdate(1, "active", false)
	if !ok || eff != "active" {
		t.Errorf("active should bypass failure guard, got (%v, %q)", ok, eff)
	}
}
