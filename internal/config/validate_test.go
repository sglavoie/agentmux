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
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "belt", Harness: "claude"},
		},
	}
	err := config.Validate(cfg)
	if err != nil {
		if strings.Contains(err.Error(), "home_dir") {
			t.Errorf("unexpected home_dir error — normalization should have filled HomeDir: %v", err)
		}
	}
}

func TestValidate_SessionArchiveNegativeKeepDays(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	cfg.Defaults.SessionArchive.KeepDays = -1
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for negative session_archive.keep_days")
	}
	if !strings.Contains(err.Error(), "session_archive.keep_days must be >= 0") {
		t.Errorf("expected session_archive.keep_days error, got: %v", err)
	}
}

func TestValidate_SessionArchiveNegativeScanMaxAgeDays(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	cfg.Defaults.SessionArchive.ScanMaxAgeDays = -2
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for scan_max_age_days = -2")
	}
	if !strings.Contains(err.Error(), "session_archive.scan_max_age_days must be >= -1") {
		t.Errorf("expected session_archive.scan_max_age_days error, got: %v", err)
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
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
	}
	zero.Defaults.Preview.MinCols = 0
	zero.Defaults.Preview.MinRows = 0
	if err := config.Validate(zero); err != nil && (strings.Contains(err.Error(), "min_cols") || strings.Contains(err.Error(), "min_rows")) {
		t.Fatalf("zero min dimensions must be accepted (filled by applyDefaults later), got: %v", err)
	}

	neg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
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

// TestValidate_ProjectProfileAccountConflict pins that a project specifying
// both profile and account with non-matching values is reported as an error.
// normalizeProjects only mirrors profile↔account when one of them is empty,
// so two different values would be silently retained — runtime paths read
// Profile (via EffectiveProfile) for dashboard display and Account for sort/
// launch context, so a mismatch would split the same project across two
// profiles. The user must collapse them to one value or use only profile.
func TestValidate_ProjectProfileAccountConflict(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "X", Harness: "claude", HomeDir: "/tmp/x"},
			{Name: "Y", Harness: "claude", HomeDir: "/tmp/y"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "X", Account: "Y"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for conflicting profile/account on project, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "profile") || !strings.Contains(msg, "account") {
		t.Errorf("error %q must mention both profile and account", msg)
	}
	if !strings.Contains(msg, "api") {
		t.Errorf("error %q must mention the offending project title", msg)
	}
}

// TestValidate_ProjectProfileAccountMatchingAccepted pins the matching case:
// when profile and account are equal (case-insensitively), validation must
// accept the duplication — it's redundant but unambiguous, and many legacy
// configs were written with both keys set to the same value.
func TestValidate_ProjectProfileAccountMatchingAccepted(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/w", Color: "blue"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work", Account: "Work"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("matching profile/account (case-insensitively) must validate: %v", err)
	}
}

// TestValidate_NegativePermissionDebounce pins that a negative
// permission_debounce_ms is rejected at validate time. Without this check the
// value flows through unchanged and is later interpreted as a duration in the
// daemon's debounce path, where a negative debounce yields confusing
// instant-fire behavior with no signal to the user.
func TestValidate_NegativePermissionDebounce(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Notifications: config.NotificationConfig{PermissionDebounceMs: -1},
		},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for negative permission_debounce_ms, got nil")
	}
	if !strings.Contains(err.Error(), "permission_debounce_ms must be >= 0") {
		t.Errorf("error %q does not mention permission_debounce_ms bound", err.Error())
	}
}
