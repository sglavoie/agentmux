package activity

import (
	"testing"
	"time"
)

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
