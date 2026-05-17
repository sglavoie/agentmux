package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestValidate_InvalidHarness(t *testing.T) {
	path := filepath.Join(testdataDir, "invalid_harness_config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for invalid harness, got nil")
	}
	if !strings.Contains(err.Error(), "harness") {
		t.Errorf("error %q does not mention harness", err.Error())
	}
}

func TestValidate_MissingHomeDirAfterNormalization(t *testing.T) {
	// normalizeProfiles always fills HomeDir (from ConfigDir, then DefaultHomeDir),
	// so a profile with no home_dir or config_dir specified does not fail validation
	// for home_dir. This test documents that invariant.
	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "belt", Harness: "claude", Color: "blue"},
		},
	}
	err := config.Validate(cfg)
	if err != nil {
		if strings.Contains(err.Error(), "home_dir") {
			t.Errorf("unexpected home_dir error — normalization should have filled HomeDir: %v", err)
		}
	}
}

func TestValidate_DuplicateProjectTitlePerProfile(t *testing.T) {
	// Two projects with the same (profile, title) pair are ambiguous: launch dedup
	// keys on this pair, so duplicates would mask one of them and silently confuse
	// the user. Validation must surface this rather than wait for runtime confusion.
	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work"},
			{Title: "api", Directory: "/tmp/b", Profile: "work"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for duplicate project title under same profile, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), "api") {
		t.Errorf("expected duplicate/api in error, got %q", err.Error())
	}
}

func TestValidate_DuplicateProjectTitleAcrossProfilesAllowed(t *testing.T) {
	// Same title under different profiles is allowed: launch dedup keys on
	// (account, title), so they are distinct sessions.
	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/w", Color: "blue"},
			{Name: "personal", Harness: "claude", HomeDir: "/tmp/p", Color: "green"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work"},
			{Title: "api", Directory: "/tmp/b", Profile: "personal"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("unexpected error for same-title-across-profiles: %v", err)
	}
}

func TestValidate_UnknownHookDisabledEvent(t *testing.T) {
	// A typo in hooks.overrides.<account>.disabled_events silently turns into a
	// no-op: the named event isn't in the harness's known set, so nothing is
	// disabled and the user wonders why their hook still fires. Validation must
	// catch this and report the typo with a hint to the valid set.
	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
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

func TestValidate_UnknownHookOverrideAccount(t *testing.T) {
	// An override keyed by a profile that doesn't exist is dead weight; the user
	// renamed the profile or made a typo. Surface it.
	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
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
