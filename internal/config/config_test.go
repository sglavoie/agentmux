package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
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
	if cfg.Defaults.IdleThreshold != 300 {
		t.Errorf("default idle_threshold: got %d, want 300", cfg.Defaults.IdleThreshold)
	}
	if cfg.Defaults.Notifications.PermissionDebounceMs != 500 {
		t.Errorf("default permission_debounce_ms: got %d, want 500", cfg.Defaults.Notifications.PermissionDebounceMs)
	}
}

func TestLoad_NormalizesProfileArgsAndClonesAliases(t *testing.T) {
	path := filepath.Join(testdataDir, "profile_args_config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Profiles) != 2 {
		t.Fatalf("profiles: got %d, want 2", len(cfg.Profiles))
	}
	if len(cfg.Accounts) != 2 {
		t.Fatalf("accounts: got %d, want 2", len(cfg.Accounts))
	}

	wantProfileArgs := []string{"--model", "gpt-5"}
	if !reflect.DeepEqual(cfg.Profiles[1].Args, wantProfileArgs) {
		t.Errorf("Profiles[1].Args: got %v, want %v", cfg.Profiles[1].Args, wantProfileArgs)
	}
	if !reflect.DeepEqual(cfg.Accounts[1].Args, wantProfileArgs) {
		t.Errorf("Accounts[1].Args: got %v, want %v", cfg.Accounts[1].Args, wantProfileArgs)
	}

	if got := cfg.Projects[0].Profile; got != "copilot-work" {
		t.Errorf("Projects[0].Profile: got %q, want %q", got, "copilot-work")
	}
	if got := cfg.Sessions[0].Account; got != "copilot-work" {
		t.Errorf("Sessions[0].Account: got %q, want %q", got, "copilot-work")
	}

	wantProjectArgs := []string{"--project-flag"}
	if !reflect.DeepEqual(cfg.Projects[0].Args, wantProjectArgs) {
		t.Errorf("Projects[0].Args: got %v, want %v", cfg.Projects[0].Args, wantProjectArgs)
	}
	if !reflect.DeepEqual(cfg.Sessions[0].Args, wantProjectArgs) {
		t.Errorf("Sessions[0].Args: got %v, want %v", cfg.Sessions[0].Args, wantProjectArgs)
	}

	cfg.Accounts[1].Args[0] = "--changed-account"
	if got := cfg.Profiles[1].Args[0]; got != "--model" {
		t.Errorf("Profiles[1].Args shared backing array: got %q, want %q", got, "--model")
	}

	cfg.Sessions[0].Args[0] = "--changed-project"
	if got := cfg.Projects[0].Args[0]; got != "--project-flag" {
		t.Errorf("Projects[0].Args shared backing array: got %q, want %q", got, "--project-flag")
	}
}

func TestLoad_NormalizesLegacyClaudeFlags(t *testing.T) {
	path := filepath.Join(testdataDir, "legacy_flags_config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wantDefaults := []string{"--shared-legacy"}
	if !reflect.DeepEqual(cfg.Defaults.Args, wantDefaults) {
		t.Errorf("Defaults.Args: got %v, want %v", cfg.Defaults.Args, wantDefaults)
	}
	if !reflect.DeepEqual(cfg.Defaults.ClaudeFlags, wantDefaults) {
		t.Errorf("Defaults.ClaudeFlags: got %v, want %v", cfg.Defaults.ClaudeFlags, wantDefaults)
	}

	wantProject := []string{"--project-legacy"}
	if !reflect.DeepEqual(cfg.Projects[0].Args, wantProject) {
		t.Errorf("Projects[0].Args: got %v, want %v", cfg.Projects[0].Args, wantProject)
	}
	if !reflect.DeepEqual(cfg.Sessions[0].ClaudeFlags, wantProject) {
		t.Errorf("Sessions[0].ClaudeFlags: got %v, want %v", cfg.Sessions[0].ClaudeFlags, wantProject)
	}
}

func TestLoad_AccountArgs(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work", Color: "blue", Args: []string{"--dangerously-skip-permissions"}},
		},
	}
	acc := cfg.Accounts[0]
	if len(acc.Args) != 1 || acc.Args[0] != "--dangerously-skip-permissions" {
		t.Errorf("Account.Args: got %v, want [--dangerously-skip-permissions]", acc.Args)
	}
}

func TestEffectiveArgs_PrefersArgsOverClaudeFlags(t *testing.T) {
	sess := config.Session{
		ClaudeFlags: []string{"--legacy"},
		Args:        []string{"--new"},
	}
	got := sess.EffectiveArgs()
	if len(got) != 1 || got[0] != "--new" {
		t.Errorf("EffectiveArgs: got %v, want [--new]", got)
	}
}

func TestEffectiveArgs_FallsBackToClaudeFlags(t *testing.T) {
	sess := config.Session{
		ClaudeFlags: []string{"--legacy"},
	}
	got := sess.EffectiveArgs()
	if len(got) != 1 || got[0] != "--legacy" {
		t.Errorf("EffectiveArgs fallback: got %v, want [--legacy]", got)
	}
}

func TestDefaultsEffectiveArgs_PrefersArgsOverClaudeFlags(t *testing.T) {
	d := config.Defaults{
		ClaudeFlags: []string{"--legacy"},
		Args:        []string{"--new"},
	}
	got := d.EffectiveArgs()
	if len(got) != 1 || got[0] != "--new" {
		t.Errorf("Defaults.EffectiveArgs: got %v, want [--new]", got)
	}
}

func TestValidate_NegativeIdleThreshold(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{IdleThreshold: -1},
		Accounts: []config.Account{{Name: "work", ConfigDir: "~/.config/claude-work", Color: "blue"}},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for negative idle_threshold, got nil")
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

func TestLoad_NormalizesDefaultHarness(t *testing.T) {
	content := `
[[profiles]]
name = "personal"
home_dir = "~/.claude"
color = "green"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
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
