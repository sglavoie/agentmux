package config_test

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestValidate_HarnessColorsOptionalWhenAbsent(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("missing [harness_colors] table should be accepted: %v", err)
	}
}

func TestValidate_MissingHarnessColorListsReferringProfiles(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		HarnessEmojis: map[string]string{"claude": "🦊", "copilot": "🚁"},
		Profiles: []config.Account{
			{Name: "work", Harness: "copilot", HomeDir: "/tmp/w", Color: "blue"},
			{Name: "personal", Harness: "copilot", HomeDir: "/tmp/p", Color: "yellow"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for missing harness color, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `[harness_colors] is missing color for harness "copilot"`) {
		t.Errorf("expected missing-harness error, got %q", msg)
	}
	if !strings.Contains(msg, "personal, work") {
		t.Errorf("expected sorted profile list in error, got %q", msg)
	}
}

func TestValidate_HarnessColorsUnknownHarnessRejected(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue", "bogus": "red"},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unknown harness key, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `[harness_colors] unknown harness "bogus"`) {
		t.Errorf("expected unknown-harness error, got %q", msg)
	}
	if !strings.Contains(msg, "valid harnesses:") {
		t.Errorf("expected message to list valid harnesses, got %q", msg)
	}
}

func TestValidate_InvalidProfileHarnessDoesNotRequestHarnessColor(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "bogus", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for invalid profile harness, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `profile "work": invalid harness "bogus"`) {
		t.Errorf("expected invalid profile harness error, got %q", msg)
	}
	if strings.Contains(msg, `[harness_colors] is missing color for harness "bogus"`) {
		t.Errorf("unexpected missing harness color error for invalid harness: %q", msg)
	}
}

func TestLoad_NormalizesHarnessColorKeys(t *testing.T) {
	path := writeTempConfig(t, `
[harness_colors]
Claude = "blue"

[harness_emojis]
claude = "🦊"

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

func TestValidate_HarnessColorsDuplicateNormalizedKeysRejected(t *testing.T) {
	path := writeTempConfig(t, `
[harness_colors]
Claude = "blue"
claude = "red"

[harness_emojis]
claude = "🦊"

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
	if !strings.Contains(msg, "[harness_colors] duplicate keys normalize to the same harness") {
		t.Errorf("expected duplicate-key error, got %q", msg)
	}
	if !strings.Contains(msg, "Claude") || !strings.Contains(msg, "claude") {
		t.Errorf("expected error to include both raw keys, got %q", msg)
	}
}

func TestValidate_HarnessColorsInvalidColorRejected(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "magenta"},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
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

func TestValidate_HarnessColorsExtraEntryForUnusedHarnessAllowed(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue", "copilot": "purple"},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("extra harness_colors entry for unused harness must validate cleanly: %v", err)
	}
}

func TestValidate_HarnessColorsTwoProfilesShareOneEntry(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/w", Color: "blue"},
			{Name: "personal", Harness: "claude", HomeDir: "/tmp/p", Color: "yellow"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("two profiles sharing a harness must validate with one entry: %v", err)
	}
}
