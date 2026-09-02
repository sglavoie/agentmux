package activity

import (
	"testing"
	"time"
)

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
