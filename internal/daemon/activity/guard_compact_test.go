package activity

import (
	"testing"
	"time"
)

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
