package activity

import (
	"testing"
	"time"
)

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
