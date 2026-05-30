package config_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

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
	if cfg.Defaults.StaleActiveThreshold != 600 {
		t.Errorf("default stale_active_threshold: got %d, want 600", cfg.Defaults.StaleActiveThreshold)
	}
	if cfg.Defaults.Notifications.StaleActive == nil {
		t.Fatal("default Notifications.StaleActive: got nil, want non-nil pointer to true")
	}
	if !*cfg.Defaults.Notifications.StaleActive {
		t.Errorf("default Notifications.StaleActive: got false, want true")
	}
	if cfg.Defaults.Notifications.PermissionDebounceMs != 500 {
		t.Errorf("default permission_debounce_ms: got %d, want 500", cfg.Defaults.Notifications.PermissionDebounceMs)
	}
	if cfg.Defaults.PlansDirectory != "~/.local/share/agent-plans" {
		t.Errorf("default plans_directory: got %q, want %q", cfg.Defaults.PlansDirectory, "~/.local/share/agent-plans")
	}
}

func TestLoad_PreservesExplicitPlansDirectory(t *testing.T) {
	path := writeTempConfig(t, `[defaults]
plans_directory = "/elsewhere"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Defaults.PlansDirectory != "/elsewhere" {
		t.Errorf("explicit plans_directory: got %q, want %q", cfg.Defaults.PlansDirectory, "/elsewhere")
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

func TestLoad_ClonesProfileWatchdogProbeAlias(t *testing.T) {
	path := writeTempConfig(t, `
[[profiles]]
name = "work"
home_dir = "~/.config/claude-work"
color = "blue"
watchdog = { claude_context_limit_probe = false }
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Profiles) != 1 || len(cfg.Accounts) != 1 {
		t.Fatalf("profiles/accounts: got %d/%d, want 1/1", len(cfg.Profiles), len(cfg.Accounts))
	}
	if cfg.Profiles[0].Watchdog.ClaudeContextLimitProbe == nil {
		t.Fatal("Profiles[0].Watchdog.ClaudeContextLimitProbe: got nil, want explicit false")
	}
	if cfg.Accounts[0].Watchdog.ClaudeContextLimitProbe == nil {
		t.Fatal("Accounts[0].Watchdog.ClaudeContextLimitProbe: got nil, want explicit false")
	}

	*cfg.Accounts[0].Watchdog.ClaudeContextLimitProbe = true
	if got := *cfg.Profiles[0].Watchdog.ClaudeContextLimitProbe; got {
		t.Errorf("Profiles[0].Watchdog.ClaudeContextLimitProbe shared pointer with Accounts alias: got %v, want false", got)
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
			{Name: "work", ConfigDir: "~/.config/claude-work", Args: []string{"--dangerously-skip-permissions"}},
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
		Defaults:      config.Defaults{IdleThreshold: -1},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", ConfigDir: "~/.config/claude-work"}},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for negative idle_threshold, got nil")
	}
}

func TestValidate_NegativeStaleActiveThreshold(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{StaleActiveThreshold: -1},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", ConfigDir: "~/.config/claude-work"}},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for negative stale_active_threshold, got nil")
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
	if !strings.Contains(err.Error(), `invalid color "magenta"`) {
		t.Errorf("expected error to mention invalid color, got %q", err.Error())
	}
}

func TestValidate_MissingProfileColor(t *testing.T) {
	path := filepath.Join(testdataDir, "invalid_config_missing_profile_color.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for missing profile color, got nil")
	}
	if !strings.Contains(err.Error(), "'color' is required") {
		t.Errorf("expected error to mention 'color' is required, got %q", err.Error())
	}
}

func TestValidate_MissingTitle(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", ConfigDir: "~/.config/claude-work"}},
		Sessions:      []config.Session{{Title: "", Directory: "~/projects", Account: "work"}},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for missing title, got nil")
	}
}

func TestValidate_DuplicateAccountName(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work"},
			{Name: "work", ConfigDir: "~/.config/claude-work2"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for duplicate account name, got nil")
	}
}

func TestValidate_InvalidSort(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "random"},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", ConfigDir: "~/.config/claude-work"}},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for invalid sort, got nil")
	}
}
