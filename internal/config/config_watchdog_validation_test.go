package config_test

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestValidate_NegativeWatchdogThresholds pins that each watchdog sub-field
// rejects negative values with a per-field error message, matching the
// existing `stale_active_threshold` pattern. A negative threshold is always a
// typo — there is no sensible interpretation.
func TestValidate_NegativeWatchdogThresholds(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*config.Defaults)
		want string
	}{
		{"active", func(d *config.Defaults) { d.Watchdog.Active = -1 }, "defaults.watchdog.active must be >= 0"},
		{"subagent", func(d *config.Defaults) { d.Watchdog.Subagent = -1 }, "defaults.watchdog.subagent must be >= 0"},
		{"compact", func(d *config.Defaults) { d.Watchdog.Compact = -1 }, "defaults.watchdog.compact must be >= 0"},
		{"hook_silence", func(d *config.Defaults) { d.Watchdog.HookSilence = -1 }, "defaults.watchdog.hook_silence must be >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				HarnessEmojis: map[string]string{"claude": "🦊"},
				Profiles:      []config.Account{{Name: "work", Harness: "claude", HomeDir: "/tmp/h"}},
			}
			tc.mut(&cfg.Defaults)
			err := config.Validate(cfg)
			if err == nil {
				t.Fatalf("expected validation error for negative watchdog.%s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestValidate_PerProfileNegativeWatchdog pins that per-profile overrides
// are validated with the same rules as the defaults — a negative value
// inside [[profiles]].watchdog must be rejected with a profile-qualified
// error so the user can locate it.
func TestValidate_PerProfileNegativeWatchdog(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Watchdog: config.WatchdogConfig{Active: -5}},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for negative per-profile watchdog.active, got nil")
	}
	if !strings.Contains(err.Error(), "profile \"work\"") || !strings.Contains(err.Error(), "watchdog.active") {
		t.Errorf("error %q must qualify profile and field", err.Error())
	}
}

// TestValidate_ZeroWatchdogAccepted pins that 0 means "use default" — it must
// not trigger an error or a sub-30s warning. After applyDefaults the field is
// always populated to a sane value, so 0 at validate time would actually be
// the zero-value Defaults struct used by tests; either way 0 is benign.
func TestValidate_ZeroWatchdogAccepted(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Watchdog: config.WatchdogConfig{Active: 0, Subagent: 0, Compact: 0, HookSilence: 0}},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles:      []config.Account{{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"}},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("zero watchdog values must validate cleanly: %v", err)
	}
	for _, d := range cfg.Deprecations {
		if strings.HasPrefix(d.Key, "defaults.watchdog.") {
			t.Errorf("zero values must not emit a watchdog warning, got %+v", d)
		}
	}
}

// TestValidate_SubThirtyWatchdogWarns pins the soft-bound rule: a value in
// (0, 30) is accepted (no error) but appended to cfg.Deprecations as an
// advisory so the TUI banner surfaces the suspicious value. The check covers
// both default-scope and per-profile keys.
func TestValidate_SubThirtyWatchdogWarns(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Watchdog: config.WatchdogConfig{Active: 5}},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue", Watchdog: config.WatchdogConfig{Subagent: 10}},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("sub-30s watchdog values must validate (only warn): %v", err)
	}
	keys := make(map[string]config.Deprecation, len(cfg.Deprecations))
	for _, d := range cfg.Deprecations {
		keys[d.Key] = d
	}
	if _, ok := keys["defaults.watchdog.active"]; !ok {
		t.Errorf("missing warning for defaults.watchdog.active=5 (got %+v)", cfg.Deprecations)
	}
	if _, ok := keys["profiles.work.watchdog.subagent"]; !ok {
		t.Errorf("missing warning for per-profile watchdog.subagent=10 (got %+v)", cfg.Deprecations)
	}
}

// TestValidate_WatchdogWarningsIdempotent pins that calling Validate multiple
// times on the same *Config doesn't duplicate the sub-30s advisory entries in
// cfg.Deprecations. Validate appends watchdog warnings on every call, so
// without dedup the TUI banner would show the same key 2x, 3x, ... each time
// the user revalidates after an edit.
func TestValidate_WatchdogWarningsIdempotent(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Watchdog: config.WatchdogConfig{Active: 5}},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue", Watchdog: config.WatchdogConfig{Subagent: 10}},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("first Validate: %v", err)
	}
	first := append([]config.Deprecation(nil), cfg.Deprecations...)
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("second Validate: %v", err)
	}
	if len(cfg.Deprecations) != len(first) {
		t.Fatalf("Deprecations grew across Validate calls: got %d, want %d (first=%+v, after=%+v)",
			len(cfg.Deprecations), len(first), first, cfg.Deprecations)
	}
	// Count occurrences of each key — none should appear more than once.
	counts := map[string]int{}
	for _, d := range cfg.Deprecations {
		counts[d.Key]++
	}
	for k, n := range counts {
		if n > 1 {
			t.Errorf("Deprecation key %q appears %d times after re-validate; want 1", k, n)
		}
	}
}

// TestValidate_StripsStaleWatchdogWarningsOnRevalidate pins that when the user
// fixes a sub-30s watchdog value and revalidates, the prior advisory for that
// key is dropped — the banner reflects the current config, not historical
// state. Before the fix, stripWatchdogWarnings cleared only when collecting
// fresh entries, so a fixed key would still surface a stale warning from the
// previous Validate call.
func TestValidate_StripsStaleWatchdogWarningsOnRevalidate(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Watchdog: config.WatchdogConfig{Active: 5}},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("first Validate: %v", err)
	}
	found := false
	for _, d := range cfg.Deprecations {
		if d.Key == "defaults.watchdog.active" {
			found = true
		}
	}
	if !found {
		t.Fatalf("first Validate did not emit watchdog warning (got %+v)", cfg.Deprecations)
	}
	// User edits the config to a sane threshold and revalidates.
	cfg.Defaults.Watchdog.Active = 600
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("second Validate: %v", err)
	}
	for _, d := range cfg.Deprecations {
		if d.Key == "defaults.watchdog.active" {
			t.Errorf("stale watchdog warning persisted after threshold was fixed: %+v", d)
		}
	}
}
