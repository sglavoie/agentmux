package config_test

import (
	"path/filepath"
	"testing"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
)

const testdataDir = "../../testdata"

func TestLoad_ValidConfig(t *testing.T) {
	path := filepath.Join(testdataDir, "valid_config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Accounts) != 2 {
		t.Errorf("accounts: got %d, want 2", len(cfg.Accounts))
	}
	if len(cfg.Sessions) != 2 {
		t.Errorf("sessions: got %d, want 2", len(cfg.Sessions))
	}
	if cfg.Defaults.Sort != "alpha" {
		t.Errorf("sort: got %q, want %q", cfg.Defaults.Sort, "alpha")
	}
	if cfg.Defaults.RefreshInterval != 2 {
		t.Errorf("refresh_interval: got %d, want 2", cfg.Defaults.RefreshInterval)
	}
}

func TestLoad_AppliesDefaults(t *testing.T) {
	// Write a minimal config with no defaults set.
	path := filepath.Join(testdataDir, "invalid_config_bad_color.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Defaults should be applied.
	if cfg.Defaults.Sort != "alpha" {
		t.Errorf("default sort: got %q, want %q", cfg.Defaults.Sort, "alpha")
	}
	if cfg.Defaults.RefreshInterval != 2 {
		t.Errorf("default refresh_interval: got %d, want 2", cfg.Defaults.RefreshInterval)
	}
	if cfg.Defaults.GuardDuration != 1 {
		t.Errorf("default guard_duration: got %d, want 1", cfg.Defaults.GuardDuration)
	}
}

func TestLoad_NonexistentFile(t *testing.T) {
	_, err := config.Load("/no/such/file.toml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	path := filepath.Join(testdataDir, "valid_config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := config.Validate(cfg); err != nil {
		t.Errorf("Validate: unexpected error: %v", err)
	}
}

func TestValidate_UnknownAccount(t *testing.T) {
	path := filepath.Join(testdataDir, "invalid_config_unknown_account.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unknown account, got nil")
	}
}

func TestValidate_BadColor(t *testing.T) {
	path := filepath.Join(testdataDir, "invalid_config_bad_color.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for bad color, got nil")
	}
}

func TestValidate_MissingTitle(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", ConfigDir: "~/.config/claude-work", Color: "blue"}},
		Sessions: []config.Session{{Title: "", Directory: "~/projects", Account: "work"}},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for missing title, got nil")
	}
}

func TestValidate_DuplicateAccountName(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work", Color: "blue"},
			{Name: "work", ConfigDir: "~/.config/claude-work2", Color: "green"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for duplicate account name, got nil")
	}
}

func TestValidate_InvalidSort(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "random"},
		Accounts: []config.Account{{Name: "work", ConfigDir: "~/.config/claude-work", Color: "blue"}},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for invalid sort, got nil")
	}
}
