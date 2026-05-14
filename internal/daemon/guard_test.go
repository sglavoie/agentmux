package daemon

import (
	"testing"
	"time"
)

func TestGuard_AllowsInitialUpdate(t *testing.T) {
	g := NewStateGuard(time.Second)
	ok, eff := g.TryUpdate(1, "active", "")
	if !ok || eff != "active" {
		t.Fatalf("want (true, active), got (%v, %q)", ok, eff)
	}
}

func TestGuard_BlocksLowerPriority(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "permission", "") // priority 3

	ok, eff := g.TryUpdate(1, "active", "") // priority 1 — should be blocked
	if ok || eff != "" {
		t.Fatalf("want (false, \"\"), got (%v, %q)", ok, eff)
	}
}

func TestGuard_AllowsHigherPriority(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "active", "") // priority 1

	ok, eff := g.TryUpdate(1, "failure", "") // priority 4 — should be allowed
	if !ok || eff != "failure" {
		t.Fatalf("want (true, failure), got (%v, %q)", ok, eff)
	}
}

func TestGuard_AllowsAfterExpiry(t *testing.T) {
	g := NewStateGuard(1 * time.Millisecond)
	g.TryUpdate(1, "permission", "")

	time.Sleep(5 * time.Millisecond)

	ok, eff := g.TryUpdate(1, "active", "")
	if !ok || eff != "active" {
		t.Fatalf("want (true, active) after expiry, got (%v, %q)", ok, eff)
	}
}

func TestGuard_PostPermissionStopSuppressed(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "permission", "")

	ok, eff := g.TryUpdate(1, "stop", "")
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
	g.TryUpdate(1, "stop", "")

	// UserPromptSubmit should break through the stop guard.
	ok, eff := g.TryUpdate(1, "active", "UserPromptSubmit")
	if !ok || eff != "active" {
		t.Fatalf("want (true, active) for prompt bypass, got (%v, %q)", ok, eff)
	}
	// Guard should now hold "active", not "stop".
	if cur := g.Current(1); cur != "active" {
		t.Fatalf("want guard=active after prompt bypass, got %q", cur)
	}
}

func TestGuard_PromptDoesNotBypassHigherGuard(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "permission", "") // priority 3

	// UserPromptSubmit only bypasses "stop" guards, not higher ones.
	ok, eff := g.TryUpdate(1, "active", "UserPromptSubmit")
	if ok || eff != "" {
		t.Fatalf("want (false, \"\") for prompt vs permission guard, got (%v, %q)", ok, eff)
	}
}

func TestGuard_ClearAlwaysSucceeds(t *testing.T) {
	for _, guard := range []string{"stop", "permission", "failure", "active"} {
		g := NewStateGuard(time.Hour)
		g.TryUpdate(1, guard, "")

		ok, eff := g.TryUpdate(1, "clear", "")
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
		g.TryUpdate(1, guard, "")

		ok, eff := g.TryUpdate(1, "idle", "")
		if !ok || eff != "idle" {
			t.Errorf("idle should always succeed vs guard=%q, got (%v, %q)", guard, ok, eff)
		}
		if cur := g.Current(1); cur != "" {
			t.Errorf("guard should be removed after idle vs guard=%q, got %q", guard, cur)
		}
	}
}

func TestGuard_PerWindowIsolation(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "permission", "")

	// Window 2 should be unaffected.
	ok, eff := g.TryUpdate(2, "active", "")
	if !ok || eff != "active" {
		t.Fatalf("guard on window 1 should not affect window 2, got (%v, %q)", ok, eff)
	}
}

func TestGuard_SamePriorityAllowed(t *testing.T) {
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "stop", "") // priority 2

	ok, eff := g.TryUpdate(1, "stop", "") // same priority
	if !ok || eff != "stop" {
		t.Fatalf("same-priority update should be allowed, got (%v, %q)", ok, eff)
	}
}

func TestGuard_AllPriorityCombo(t *testing.T) {
	// Pairs where the guard SHOULD block: higher-priority guard vs lower-priority action.
	// Excludes: idle/clear (always succeed), permission→stop (post-permission suppression),
	// and stop→active via UserPromptSubmit (prompt bypass).
	blocked := []struct{ guard, action string }{
		{"stop", "active"},
		{"permission", "active"},
		{"failure", "active"},
		{"failure", "stop"},
		{"failure", "permission"},
	}
	for _, tc := range blocked {
		g := NewStateGuard(time.Hour)
		g.TryUpdate(1, tc.guard, "")
		ok, _ := g.TryUpdate(1, tc.action, "")
		if ok {
			t.Errorf("guard=%q should block action=%q", tc.guard, tc.action)
		}
	}

	// idle and clear always succeed regardless of guard.
	for _, guard := range []string{"active", "stop", "permission", "failure"} {
		for _, action := range []string{"idle", "clear"} {
			g := NewStateGuard(time.Hour)
			g.TryUpdate(1, guard, "")
			ok, _ := g.TryUpdate(1, action, "")
			if !ok {
				t.Errorf("guard=%q should not block %q", guard, action)
			}
		}
	}

	// UserPromptSubmit bypasses stop guard only.
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "stop", "")
	ok, eff := g.TryUpdate(1, "active", "UserPromptSubmit")
	if !ok || eff != "active" {
		t.Errorf("UserPromptSubmit should bypass stop guard, got (%v, %q)", ok, eff)
	}
}
