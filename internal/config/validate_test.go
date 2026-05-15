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
