package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestSweepStale_PerStateThresholds verifies that per-action thresholds are
// honored independently: with compact = 30s and subagent = 30m, a 5-minute
// idle compact session is swept while a 5-minute idle subagent is left alone.
func TestSweepStale_PerStateThresholds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global := map[string]time.Duration{
			"active":   10 * time.Minute,
			"subagent": 30 * time.Minute,
			"compact":  30 * time.Second,
		}
		h, fake, rec := newSweepTestHandlerWithMap(global, nil)
		fake.windows = []kitty.OSWindow{
			{
				ID: 1,
				Tabs: []kitty.Tab{
					{ID: 100, Title: palette.BuildTitle("W:compact", "compact", false), Windows: []kitty.Window{{ID: 42}}},
					{ID: 101, Title: palette.BuildTitle("W:subagent", "subagent", false), Windows: []kitty.Window{{ID: 43}}},
				},
			},
		}

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-5 * time.Minute)
		h.lastAction[42] = "compact"
		h.lastSessionTitle[42] = "W:compact"
		h.activity[43] = now.Add(-5 * time.Minute)
		h.lastAction[43] = "subagent"
		h.lastSessionTitle[43] = "W:subagent"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (compact only)", got)
		}
		calls := rec.snapshot()
		if calls[0].title != "W:compact" {
			t.Fatalf("dispatch title = %q; want W:compact", calls[0].title)
		}

		titles := fake.titleCalls()
		if len(titles) != 1 {
			t.Fatalf("SetTabTitle calls = %d; want 1", len(titles))
		}

		h.stateMu.Lock()
		_, compactStillTracked := h.lastAction[42]
		subagentAction := h.lastAction[43]
		h.stateMu.Unlock()
		if compactStillTracked {
			t.Fatalf("lastAction[42] still present after sweep (compact should be cleared)")
		}
		if subagentAction != "subagent" {
			t.Fatalf("lastAction[43] = %q; want subagent (left alone)", subagentAction)
		}
	})
}

// TestSweepStale_PerAccountOverrideBeatsGlobal verifies that a per-account
// override duration is consulted instead of the global default. The global
// "active" threshold is 10 minutes, but the "work" account overrides it to
// 30 seconds, so a 2-minute idle active session under "work" is swept.
func TestSweepStale_PerAccountOverrideBeatsGlobal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global := map[string]time.Duration{"active": 10 * time.Minute}
		perAccount := map[string]map[string]time.Duration{
			"work": {"active": 30 * time.Second},
		}
		h, fake, rec := newSweepTestHandlerWithMap(global, perAccount)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-2 * time.Minute)
		h.lastAction[42] = "active"
		h.lastAccount[42] = "work"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (per-account override should fire)", got)
		}
		h.stateMu.Lock()
		_, has := h.lastAction[42]
		h.stateMu.Unlock()
		if has {
			t.Fatalf("lastAction[42] still present after sweep")
		}
	})
}

// TestSweepStale_PerAccountOverrideCaseInsensitive verifies that per-account
// lookups normalize the account name before comparison (matching the rest of
// the config package's case-insensitive treatment of profile names).
func TestSweepStale_PerAccountOverrideCaseInsensitive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global := map[string]time.Duration{"active": 10 * time.Minute}
		// Account stored uppercase in the request; override registered
		// lowercase via SetStaleThresholds.
		perAccount := map[string]map[string]time.Duration{
			"WORK": {"active": 30 * time.Second},
		}
		h, fake, rec := newSweepTestHandlerWithMap(global, perAccount)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-2 * time.Minute)
		h.lastAction[42] = "active"
		h.lastAccount[42] = "Work"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (case-insensitive match should fire)", got)
		}
	})
}

// TestSweepStale_MissingAccountOverrideFallsBackToGlobal verifies that a
// session whose account has no override uses the global default threshold.
func TestSweepStale_MissingAccountOverrideFallsBackToGlobal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global := map[string]time.Duration{"active": 1 * time.Minute}
		perAccount := map[string]map[string]time.Duration{
			"other": {"active": 1 * time.Hour},
		}
		h, fake, rec := newSweepTestHandlerWithMap(global, perAccount)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("P:dotfiles", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-5 * time.Minute)
		h.lastAction[42] = "active"
		h.lastAccount[42] = "personal"
		h.lastSessionTitle[42] = "P:dotfiles"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (global fallback should fire)", got)
		}
	})
}

// TestSweepStale_PerAccountZeroFallsBackToGlobal verifies that a per-account
// override with a zero/missing inner entry falls back to the global default
// rather than disabling sweeping for that action.
func TestSweepStale_PerAccountZeroFallsBackToGlobal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global := map[string]time.Duration{
			"active":   1 * time.Minute,
			"subagent": 1 * time.Minute,
		}
		// "work" overrides subagent only; active should fall back to global.
		perAccount := map[string]map[string]time.Duration{
			"work": {"subagent": 1 * time.Hour},
		}
		h, fake, rec := newSweepTestHandlerWithMap(global, perAccount)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-5 * time.Minute)
		h.lastAction[42] = "active"
		h.lastAccount[42] = "work"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1 (active should fall back to global)", got)
		}
	})
}

// TestSweepStale_AllZeroThresholdsDisablesSweep verifies that the early-exit
// guard fires when every configured threshold is zero, preserving the
// existing "negative/zero global value disables sweep" behavior.
func TestSweepStale_AllZeroThresholdsDisablesSweep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global := map[string]time.Duration{
			"active":   0,
			"subagent": 0,
			"compact":  0,
		}
		h, fake, rec := newSweepTestHandlerWithMap(global, nil)

		now := time.Now()
		h.stateMu.Lock()
		h.activity[42] = now.Add(-15 * time.Minute)
		h.lastAction[42] = "active"
		h.lastSessionTitle[42] = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d; want 0", got)
		}
		h.stateMu.Lock()
		action := h.lastAction[42]
		h.stateMu.Unlock()
		if action != "active" {
			t.Fatalf("lastAction[42] = %q; want active (untouched)", action)
		}
	})
}
