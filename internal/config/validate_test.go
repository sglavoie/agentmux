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

// TestValidate_PreviewMinDimensions exercises the boundary cases for the
// preview.min_cols / preview.min_rows checks. The previous error message
// claimed "must be > 0" but the predicate only rejected negative values, so
// zero (which applyDefaults rewrites to the package default) was allowed.
// Validation must accept zero, reject negative values, and report the lower
// bound that actually matches the predicate.
func TestValidate_PreviewMinDimensions(t *testing.T) {
	zero := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	zero.Defaults.Preview.MinCols = 0
	zero.Defaults.Preview.MinRows = 0
	if err := config.Validate(zero); err != nil && (strings.Contains(err.Error(), "min_cols") || strings.Contains(err.Error(), "min_rows")) {
		t.Fatalf("zero min dimensions must be accepted (filled by applyDefaults later), got: %v", err)
	}

	neg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	neg.Defaults.Preview.MinCols = -1
	neg.Defaults.Preview.MinRows = -2
	err := config.Validate(neg)
	if err == nil {
		t.Fatal("expected validation error for negative min dimensions")
	}
	msg := err.Error()
	if !strings.Contains(msg, "min_cols must be >= 0") {
		t.Errorf("expected min_cols error to say '>= 0' (matches predicate), got: %s", msg)
	}
	if !strings.Contains(msg, "min_rows must be >= 0") {
		t.Errorf("expected min_rows error to say '>= 0' (matches predicate), got: %s", msg)
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

// TestValidate_DuplicateProjectTitleCaseInsensitive pins the dedup contract:
// launch-time SessionKey lowercases both the profile name and project title
// before deduping, so validation must use the same case-folding. Two projects
// "api" and "API" under profile "work" would silently collide at runtime if
// validation only checked exact-case equality.
func TestValidate_DuplicateProjectTitleCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work"},
			{Title: "API", Directory: "/tmp/b", Profile: "work"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for case-insensitive duplicate title under same profile, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate in error, got %q", err.Error())
	}
}

// TestValidate_DuplicateProjectTitleProfileCaseInsensitive pins the same dedup
// contract on the profile side: "Work" and "work" lowercase to the same key
// in SessionKey, so they share a runtime dedup namespace and validation must
// surface the collision rather than wait for confusing launch behavior.
func TestValidate_DuplicateProjectTitleProfileCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
			{Name: "Work", Harness: "claude", HomeDir: "/tmp/h2", Color: "green"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work"},
			{Title: "api", Directory: "/tmp/b", Profile: "Work"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for case-insensitive profile collision, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate in error, got %q", err.Error())
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

// TestValidate_EmptyNamesNotReportedAsDuplicate pins the contract that
// multiple unnamed profiles surface as "name is required" only, not as a
// misleading 'duplicate profile name ""' on top. The duplicate check must
// skip the empty-name case so the user sees the real fix (name the
// profiles) instead of a phantom duplicate report.
func TestValidate_EmptyNamesNotReportedAsDuplicate(t *testing.T) {
	cfg := &config.Config{
		Profiles: []config.Account{
			{Harness: "claude", HomeDir: "/tmp/a", Color: "blue"},
			{Harness: "claude", HomeDir: "/tmp/b", Color: "green"},
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
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/a", Color: "blue"},
			{Name: "Work", Harness: "claude", HomeDir: "/tmp/b", Color: "green"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for case-insensitive duplicate profile name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate profile name") {
		t.Errorf("expected 'duplicate profile name' in error, got %q", err.Error())
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
