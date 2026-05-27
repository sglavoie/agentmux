package daemon

import (
	"testing"
	"testing/synctest"
	"time"
)

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
