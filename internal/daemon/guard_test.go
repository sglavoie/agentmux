package daemon

import (
	"testing"
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
	g := NewStateGuard(1 * time.Millisecond)
	g.TryUpdate(1, "permission", false)

	time.Sleep(5 * time.Millisecond)

	ok, eff := g.TryUpdate(1, "active", false)
	if !ok || eff != "active" {
		t.Fatalf("want (true, active) after expiry, got (%v, %q)", ok, eff)
	}
}

func TestGuard_PostPermissionStopSuppressed(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "permission", false)

	ok, eff := g.TryUpdate(1, "stop", false)
	if !ok || eff != "clear" {
		t.Fatalf("want (true, clear) for post-permission stop, got (%v, %q)", ok, eff)
	}
	// Guard should now be cleared.
	if cur := g.Current(1); cur != "" {
		t.Fatalf("want guard cleared after post-permission stop, got %q", cur)
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

func TestGuard_AllPriorityCombo(t *testing.T) {
	// Pairs where the guard SHOULD block: higher-priority guard vs lower-priority action.
	// Excludes: idle/clear (always succeed), permission→stop (post-permission suppression),
	// permission→active and failure→active (both clear the guard), and stop→active via
	// UserPromptSubmit (prompt bypass).
	blocked := []struct{ guard, action string }{
		{"stop", "active"},
		{"failure", "stop"},
		{"failure", "permission"},
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
	for _, guard := range []string{"active", "stop", "permission", "failure"} {
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
