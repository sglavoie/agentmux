package daemon

import (
	"testing"
	"time"
)

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
