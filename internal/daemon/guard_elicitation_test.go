package daemon

import (
	"testing"
	"time"
)

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
	// elicitation (priority 2) blocks lower-priority actions like compact (priority 1).
	// Note: active is a special case — it always clears the elicitation guard via the
	// elicitation+active bypass branch; see TestGuard_ActiveBypassesElicitationGuard.
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "elicitation", false)
	ok, _ := g.TryUpdate(1, "compact", false)
	if ok {
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
