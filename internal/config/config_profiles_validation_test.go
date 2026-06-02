package config_test

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestValidate_UnknownHookDisabledEvent(t *testing.T) {
	// A typo in hooks.overrides.<account>.disabled_events silently turns into a
	// no-op: the named event isn't in the harness's known set, so nothing is
	// disabled and the user wonders why their hook still fires. Validation must
	// catch this and report the typo with a hint to the valid set.
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
		Hooks: config.HooksConfig{
			Overrides: map[string]config.AccountHookOverride{
				"work": {DisabledEvents: []string{"Stop", "Stopp"}},
			},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unknown disabled hook event, got nil")
	}
	if !strings.Contains(err.Error(), "Stopp") {
		t.Errorf("expected error to mention the offending event %q, got %q", "Stopp", err.Error())
	}
	if strings.Contains(err.Error(), "\"Stop\"") {
		// The known event "Stop" must NOT be flagged.
		t.Errorf("validation should not complain about known event %q; got %q", "Stop", err.Error())
	}
}

// TestValidate_EmptyNamesNotReportedAsDuplicate pins the contract that
// multiple unnamed profiles surface as "name is required" only, not as a
// misleading 'duplicate profile name ""' on top. The duplicate check must
// skip the empty-name case so the user sees the real fix (name the
// profiles) instead of a phantom duplicate report.
func TestValidate_EmptyNamesNotReportedAsDuplicate(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Harness: "claude", HomeDir: "/tmp/a"},
			{Harness: "claude", HomeDir: "/tmp/b"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unnamed profiles, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "name is required") {
		t.Errorf("expected per-entry 'name is required' error, got %q", msg)
	}
	if strings.Contains(msg, "duplicate profile name") {
		t.Errorf("validation must not report duplicates for empty names; got %q", msg)
	}
}

// TestValidate_DuplicateProfileNameCaseInsensitive pins the dedup contract on
// profile names: SessionKey lowercases the profile name when building dedup
// keys, so two profiles whose names differ only in case ("work" and "Work")
// share a runtime namespace. Without overlapping project titles the project-
// side dedup check wouldn't fire, so validation must catch the profile-level
// collision directly.
func TestValidate_DuplicateProfileNameCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/a"},
			{Name: "Work", Harness: "claude", HomeDir: "/tmp/b"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for case-insensitive duplicate profile name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate profile name") {
		t.Errorf("expected 'duplicate profile name' in error, got %q", err.Error())
	}
	for _, want := range []string{"unique launch ID", "vta-claude", "vta-codex"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected duplicate profile hint to include %q, got %q", want, err.Error())
		}
	}
}

// TestValidate_HookOverrideKeyCaseInsensitive pins the contract that
// [hooks.overrides.NAME] keys resolve to profiles case-insensitively, matching
// hooks.accountOverride at merge time. Before the fix, validation rejected a
// [hooks.overrides.Work] entry against profile "work" as "unknown profile",
// even though the override would be applied correctly at merge time.
func TestValidate_HookOverrideKeyCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
		Hooks: config.HooksConfig{
			Overrides: map[string]config.AccountHookOverride{
				"Work": {DisabledEvents: []string{"Stop"}},
			},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("unexpected error for case-insensitive override key: %v", err)
	}
}

// TestValidate_DuplicateHookOverrideKeyCaseInsensitive pins the contract that
// two [hooks.overrides.NAME] entries differing only in case are reported as a
// config error. At merge time, hooks.accountOverride iterates the map and
// returns whichever case-folded entry the Go map iterator visits first — a
// random choice — so the user would silently get one set of disabled events or
// the other on a per-run basis. Surface it instead.
func TestValidate_DuplicateHookOverrideKeyCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
		Hooks: config.HooksConfig{
			Overrides: map[string]config.AccountHookOverride{
				"work": {DisabledEvents: []string{"Stop"}},
				"Work": {DisabledEvents: []string{"PreToolUse"}},
			},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for case-only duplicate override key, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate hooks.overrides key") {
		t.Errorf("expected 'duplicate hooks.overrides key' in error, got %q", err.Error())
	}
}

func TestValidate_UnknownHookOverrideAccount(t *testing.T) {
	// An override keyed by a profile that doesn't exist is dead weight; the user
	// renamed the profile or made a typo. Surface it.
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
		Hooks: config.HooksConfig{
			Overrides: map[string]config.AccountHookOverride{
				"worrk": {DisabledEvents: []string{"Stop"}},
			},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unknown hooks.overrides profile, got nil")
	}
	if !strings.Contains(err.Error(), "worrk") {
		t.Errorf("expected error to mention the unknown profile %q, got %q", "worrk", err.Error())
	}
}
