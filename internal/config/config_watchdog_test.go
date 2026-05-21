package config_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestLoad_StaleActiveThresholdUserValuePreserved(t *testing.T) {
	content := `
[defaults]
stale_active_threshold = 5

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.StaleActiveThreshold; got != 5 {
		t.Errorf("StaleActiveThreshold: got %d, want 5", got)
	}
}

func TestLoad_WatchdogDefaultsApplied(t *testing.T) {
	content := `
[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.Watchdog.Active; got != 600 {
		t.Errorf("Watchdog.Active default: got %d, want 600", got)
	}
	if got := cfg.Defaults.Watchdog.Subagent; got != 1800 {
		t.Errorf("Watchdog.Subagent default: got %d, want 1800", got)
	}
	if got := cfg.Defaults.Watchdog.Compact; got != 180 {
		t.Errorf("Watchdog.Compact default: got %d, want 180", got)
	}
	if got := cfg.Defaults.Watchdog.HookSilence; got != 14400 {
		t.Errorf("Watchdog.HookSilence default: got %d, want 14400", got)
	}
	if cfg.Defaults.Notifications.HookSilence == nil {
		t.Fatal("Notifications.HookSilence: got nil, want non-nil pointer to true")
	}
	if !*cfg.Defaults.Notifications.HookSilence {
		t.Errorf("Notifications.HookSilence default: got false, want true")
	}
}

func TestLoad_WatchdogUserOverridePreserved(t *testing.T) {
	content := `
[defaults.watchdog]
active = 900
subagent = 3600
compact = 90
hook_silence = 7200

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.Watchdog.Active; got != 900 {
		t.Errorf("Active: got %d, want 900", got)
	}
	if got := cfg.Defaults.Watchdog.Subagent; got != 3600 {
		t.Errorf("Subagent: got %d, want 3600", got)
	}
	if got := cfg.Defaults.Watchdog.Compact; got != 90 {
		t.Errorf("Compact: got %d, want 90", got)
	}
	if got := cfg.Defaults.Watchdog.HookSilence; got != 7200 {
		t.Errorf("HookSilence: got %d, want 7200", got)
	}
}

// TestLoad_WatchdogLegacyStaleActiveMirrored pins the migration contract: a
// config carrying only `stale_active_threshold = N` (no `[defaults.watchdog]`)
// must load N into Watchdog.Active so S2's daemon read switch doesn't drop the
// user's tuned value. A deprecation must also fire so the user sees the
// migration prompt in the TUI banner.
func TestLoad_WatchdogLegacyStaleActiveMirrored(t *testing.T) {
	content := `
[defaults]
stale_active_threshold = 450

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.Watchdog.Active; got != 450 {
		t.Errorf("Watchdog.Active mirror: got %d, want 450", got)
	}
	// Non-legacy fields should still receive their package defaults.
	if got := cfg.Defaults.Watchdog.Subagent; got != 1800 {
		t.Errorf("Watchdog.Subagent default after mirror: got %d, want 1800", got)
	}
	keys := make(map[string]bool, len(cfg.Deprecations))
	for _, d := range cfg.Deprecations {
		keys[d.Key] = true
	}
	if !keys["defaults.stale_active_threshold"] {
		t.Errorf("Deprecations missing stale_active_threshold (got %+v)", cfg.Deprecations)
	}
}

// TestLoad_WatchdogNewBlockWinsOverLegacy pins the precedence rule: when both
// `stale_active_threshold` and `[defaults.watchdog].active` are set, the new
// block wins. The deprecation must still fire so the user notices the legacy
// key is being ignored rather than silently shadowed.
func TestLoad_WatchdogNewBlockWinsOverLegacy(t *testing.T) {
	content := `
[defaults]
stale_active_threshold = 100

[defaults.watchdog]
active = 750

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.Watchdog.Active; got != 750 {
		t.Errorf("Watchdog.Active precedence: got %d, want 750 (new block wins)", got)
	}
	keys := make(map[string]bool, len(cfg.Deprecations))
	for _, d := range cfg.Deprecations {
		keys[d.Key] = true
	}
	if !keys["defaults.stale_active_threshold"] {
		t.Errorf("Deprecations missing stale_active_threshold even though both keys set (got %+v)", cfg.Deprecations)
	}
}

// TestLoad_WatchdogPerProfileOverride pins that per-profile watchdog overrides
// load onto the matching Account entry in cfg.Profiles. S2 reads these as
// per-profile overlays on top of cfg.Defaults.Watchdog.
func TestLoad_WatchdogPerProfileOverride(t *testing.T) {
	content := `
[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
watchdog = { active = 300, subagent = 900 }

[[profiles]]
name = "personal"
home_dir = "~/.claude-personal"
color = "green"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Profiles) != 2 {
		t.Fatalf("profiles: got %d, want 2", len(cfg.Profiles))
	}
	if got := cfg.Profiles[0].Watchdog.Active; got != 300 {
		t.Errorf("Profiles[0].Watchdog.Active: got %d, want 300", got)
	}
	if got := cfg.Profiles[0].Watchdog.Subagent; got != 900 {
		t.Errorf("Profiles[0].Watchdog.Subagent: got %d, want 900", got)
	}
	// Per-profile zero values stay zero (no defaults applied at profile scope).
	if got := cfg.Profiles[0].Watchdog.Compact; got != 0 {
		t.Errorf("Profiles[0].Watchdog.Compact: got %d, want 0 (per-profile zero stays zero)", got)
	}
	if got := cfg.Profiles[1].Watchdog.Active; got != 0 {
		t.Errorf("Profiles[1].Watchdog.Active: got %d, want 0 (no per-profile override)", got)
	}
}
