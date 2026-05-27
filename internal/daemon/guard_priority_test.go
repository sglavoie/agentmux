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
