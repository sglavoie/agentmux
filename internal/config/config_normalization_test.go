package config_test

import (
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestLoad_NormalizesDefaultHarness(t *testing.T) {
	content := `
[[profiles]]
name = "personal"
home_dir = "~/.claude"
color = "green"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Profiles) != 1 {
		t.Fatalf("profiles: got %d, want 1", len(cfg.Profiles))
	}
	if got := cfg.Profiles[0].Harness; got != "claude" {
		t.Errorf("Harness: got %q, want %q", got, "claude")
	}
}

func TestLoad_NormalizesLegacyAccountsToProfiles(t *testing.T) {
	path := filepath.Join(testdataDir, "legacy_flags_config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, p := range cfg.Profiles {
		if p.Harness != "claude" {
			t.Errorf("profile %q: Harness = %q, want %q", p.Name, p.Harness, "claude")
		}
	}
}
