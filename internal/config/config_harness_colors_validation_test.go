package config_test

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestValidate_MissingHarnessColorListsReferringProfiles pins that the
// validator reports a missing [harness_colors] entry for any harness used by
// at least one profile, and the message names every profile referencing the
// harness in sorted order so the user can locate them.
func TestValidate_MissingHarnessColorListsReferringProfiles(t *testing.T) {
	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/w"},
			{Name: "personal", Harness: "claude", HomeDir: "/tmp/p"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for missing harness color, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[harness_colors] is missing color for harness \"claude\"") {
		t.Errorf("expected missing-harness error, got %q", msg)
	}
	if !strings.Contains(msg, "personal, work") {
		t.Errorf("expected sorted profile list in error, got %q", msg)
	}
}

// TestValidate_HarnessColorsUnknownHarnessRejected pins that an entry in
// [harness_colors] keyed by an unknown harness name is reported with the set
// of valid harnesses appended for guidance.
func TestValidate_HarnessColorsUnknownHarnessRejected(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue", "bogus": "red"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unknown harness key, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[harness_colors] unknown harness \"bogus\"") {
		t.Errorf("expected unknown-harness error, got %q", msg)
	}
	if !strings.Contains(msg, "valid harnesses:") {
		t.Errorf("expected message to list valid harnesses, got %q", msg)
	}
}

// TestLoad_NormalizesHarnessColorKeys pins that [harness_colors] keys follow
// the same case-insensitive normalization as profile harness names and
// Config.HarnessColor lookups. Without this, a key like "Claude" validates as
// a known harness but is still missed by the later normalized lookup for
// "claude".
func TestLoad_NormalizesHarnessColorKeys(t *testing.T) {
	path := writeTempConfig(t, `
[harness_colors]
Claude = "blue"

[[profiles]]
name = "work"
harness = "claude"
home_dir = "/tmp/h"
color = "blue"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := cfg.HarnessColor("claude"); got != "blue" {
		t.Errorf("HarnessColor(%q): got %q, want %q", "claude", got, "blue")
	}
	if _, ok := cfg.HarnessColors["Claude"]; ok {
		t.Errorf("HarnessColors retained unnormalized key %q: %#v", "Claude", cfg.HarnessColors)
	}
}

// TestValidate_HarnessColorsDuplicateNormalizedKeysRejected pins that two
// spellings of the same harness key do not collapse silently during load.
func TestValidate_HarnessColorsDuplicateNormalizedKeysRejected(t *testing.T) {
	path := writeTempConfig(t, `
[harness_colors]
Claude = "blue"
claude = "red"

[[profiles]]
name = "work"
harness = "claude"
home_dir = "/tmp/h"
color = "blue"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for duplicate normalized harness color keys, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "duplicate keys normalize to the same harness") {
		t.Errorf("expected duplicate-key error, got %q", msg)
	}
	if !strings.Contains(msg, "Claude") || !strings.Contains(msg, "claude") {
		t.Errorf("expected error to include both raw keys, got %q", msg)
	}
}

// TestValidate_HarnessColorsInvalidColorRejected pins that a [harness_colors]
// value that is not in the palette is rejected with a per-harness location and
// the palette list appended.
func TestValidate_HarnessColorsInvalidColorRejected(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "magenta"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for invalid harness color, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[harness_colors].claude") {
		t.Errorf("expected per-harness location in error, got %q", msg)
	}
	if !strings.Contains(msg, "magenta") {
		t.Errorf("expected offending color in error, got %q", msg)
	}
}

// TestValidate_HarnessColorsExtraEntryForUnusedHarnessAllowed pins that
// supplying a [harness_colors] entry for a harness no profile references is
// allowed — the user may be staging a future profile, and the entry is inert
// until referenced.
func TestValidate_HarnessColorsExtraEntryForUnusedHarnessAllowed(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue", "copilot": "purple"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("extra harness_colors entry for unused harness must validate cleanly: %v", err)
	}
}

// TestValidate_HarnessColorsTwoProfilesShareOneEntry pins that two profiles
// backed by the same harness require only one [harness_colors] entry — the
// whole point of the migration is that color is keyed by harness, not by
// profile.
func TestValidate_HarnessColorsTwoProfilesShareOneEntry(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/w", Color: "blue"},
			{Name: "personal", Harness: "claude", HomeDir: "/tmp/p", Color: "blue"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("two profiles sharing a harness must validate with one entry: %v", err)
	}
}
