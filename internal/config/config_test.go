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

func TestValidate_NegativeStaleActiveThreshold(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{StaleActiveThreshold: -1},
		Accounts: []config.Account{{Name: "work", ConfigDir: "~/.config/claude-work", Color: "blue"}},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for negative stale_active_threshold, got nil")
	}
}

func TestLoad_StaleActiveThresholdUserValuePreserved(t *testing.T) {
	content := `
[defaults]
stale_active_threshold = 5

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.StaleActiveThreshold; got != 5 {
		t.Errorf("StaleActiveThreshold: got %d, want 5", got)
	}
}

func TestLoad_WatchdogDefaultsApplied(t *testing.T) {
	content := `
[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.Watchdog.Active; got != 600 {
		t.Errorf("Watchdog.Active default: got %d, want 600", got)
	}
	if got := cfg.Defaults.Watchdog.Subagent; got != 1800 {
		t.Errorf("Watchdog.Subagent default: got %d, want 1800", got)
	}
	if got := cfg.Defaults.Watchdog.Compact; got != 180 {
		t.Errorf("Watchdog.Compact default: got %d, want 180", got)
	}
	if got := cfg.Defaults.Watchdog.HookSilence; got != 14400 {
		t.Errorf("Watchdog.HookSilence default: got %d, want 14400", got)
	}
	if cfg.Defaults.Notifications.HookSilence == nil {
		t.Fatal("Notifications.HookSilence: got nil, want non-nil pointer to true")
	}
	if !*cfg.Defaults.Notifications.HookSilence {
		t.Errorf("Notifications.HookSilence default: got false, want true")
	}
}

func TestLoad_WatchdogUserOverridePreserved(t *testing.T) {
	content := `
[defaults.watchdog]
active = 900
subagent = 3600
compact = 90
hook_silence = 7200

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.Watchdog.Active; got != 900 {
		t.Errorf("Active: got %d, want 900", got)
	}
	if got := cfg.Defaults.Watchdog.Subagent; got != 3600 {
		t.Errorf("Subagent: got %d, want 3600", got)
	}
	if got := cfg.Defaults.Watchdog.Compact; got != 90 {
		t.Errorf("Compact: got %d, want 90", got)
	}
	if got := cfg.Defaults.Watchdog.HookSilence; got != 7200 {
		t.Errorf("HookSilence: got %d, want 7200", got)
	}
}

// TestLoad_WatchdogLegacyStaleActiveMirrored pins the migration contract: a
// config carrying only `stale_active_threshold = N` (no `[defaults.watchdog]`)
// must load N into Watchdog.Active so S2's daemon read switch doesn't drop the
// user's tuned value. A deprecation must also fire so the user sees the
// migration prompt in the TUI banner.
func TestLoad_WatchdogLegacyStaleActiveMirrored(t *testing.T) {
	content := `
[defaults]
stale_active_threshold = 450

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.Watchdog.Active; got != 450 {
		t.Errorf("Watchdog.Active mirror: got %d, want 450", got)
	}
	// Non-legacy fields should still receive their package defaults.
	if got := cfg.Defaults.Watchdog.Subagent; got != 1800 {
		t.Errorf("Watchdog.Subagent default after mirror: got %d, want 1800", got)
	}
	keys := make(map[string]bool, len(cfg.Deprecations))
	for _, d := range cfg.Deprecations {
		keys[d.Key] = true
	}
	if !keys["defaults.stale_active_threshold"] {
		t.Errorf("Deprecations missing stale_active_threshold (got %+v)", cfg.Deprecations)
	}
}

// TestLoad_WatchdogNewBlockWinsOverLegacy pins the precedence rule: when both
// `stale_active_threshold` and `[defaults.watchdog].active` are set, the new
// block wins. The deprecation must still fire so the user notices the legacy
// key is being ignored rather than silently shadowed.
func TestLoad_WatchdogNewBlockWinsOverLegacy(t *testing.T) {
	content := `
[defaults]
stale_active_threshold = 100

[defaults.watchdog]
active = 750

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.Watchdog.Active; got != 750 {
		t.Errorf("Watchdog.Active precedence: got %d, want 750 (new block wins)", got)
	}
	keys := make(map[string]bool, len(cfg.Deprecations))
	for _, d := range cfg.Deprecations {
		keys[d.Key] = true
	}
	if !keys["defaults.stale_active_threshold"] {
		t.Errorf("Deprecations missing stale_active_threshold even though both keys set (got %+v)", cfg.Deprecations)
	}
}

// TestLoad_WatchdogPerProfileOverride pins that per-profile watchdog overrides
// load onto the matching Account entry in cfg.Profiles. S2 reads these as
// per-profile overlays on top of cfg.Defaults.Watchdog.
func TestLoad_WatchdogPerProfileOverride(t *testing.T) {
	content := `
[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
watchdog = { active = 300, subagent = 900 }

[[profiles]]
name = "personal"
home_dir = "~/.claude-personal"
color = "green"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Profiles) != 2 {
		t.Fatalf("profiles: got %d, want 2", len(cfg.Profiles))
	}
	if got := cfg.Profiles[0].Watchdog.Active; got != 300 {
		t.Errorf("Profiles[0].Watchdog.Active: got %d, want 300", got)
	}
	if got := cfg.Profiles[0].Watchdog.Subagent; got != 900 {
		t.Errorf("Profiles[0].Watchdog.Subagent: got %d, want 900", got)
	}
	// Per-profile zero values stay zero (no defaults applied at profile scope).
	if got := cfg.Profiles[0].Watchdog.Compact; got != 0 {
		t.Errorf("Profiles[0].Watchdog.Compact: got %d, want 0 (per-profile zero stays zero)", got)
	}
	if got := cfg.Profiles[1].Watchdog.Active; got != 0 {
		t.Errorf("Profiles[1].Watchdog.Active: got %d, want 0 (no per-profile override)", got)
	}
}

func TestLoad_UserCanDisableHookSilenceNotification(t *testing.T) {
	content := `
[defaults.notifications]
hook_silence = false

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Defaults.Notifications.HookSilence
	if got == nil {
		t.Fatal("HookSilence: got nil, want non-nil pointer to false")
	}
	if *got {
		t.Fatalf("HookSilence user-disabled: got true, want false")
	}
}

func TestLoad_UserCanDisableStaleActiveNotification(t *testing.T) {
	content := `
[defaults.notifications]
stale_active = false

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Defaults.Notifications.StaleActive
	if got == nil {
		t.Fatal("StaleActive: got nil, want non-nil pointer to false")
	}
	if *got {
		t.Fatalf("StaleActive user-disabled: got true, want false (stale_active = false in config should be respected)")
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

func TestLoad_PopulatesDeprecations(t *testing.T) {
	content := `
[defaults]
claude_flags = ["--shared-legacy"]

[[accounts]]
name = "work"
config_dir = "~/.config/claude-work"
color = "blue"

[[sessions]]
title = "api"
directory = "~/projects/api"
account = "work"
claude_flags = ["--project-legacy"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	keys := make(map[string]config.Deprecation, len(cfg.Deprecations))
	for _, d := range cfg.Deprecations {
		keys[d.Key] = d
	}
	for _, want := range []string{
		"accounts",
		"sessions",
		"defaults.claude_flags",
		"[[sessions]].claude_flags",
		"[[sessions]].account",
	} {
		if _, ok := keys[want]; !ok {
			t.Errorf("Deprecations missing key %q (got %+v)", want, cfg.Deprecations)
		}
	}
	if got := keys["accounts"].Replacement; got != "profiles" {
		t.Errorf("Replacement for accounts: got %q, want %q", got, "profiles")
	}
}

// TestLoad_ReportsLegacyEvenWhenCanonicalAlsoSet pins that a config mixing
// [[profiles]] and [[accounts]] (or [[projects]] and [[sessions]]) surfaces the
// legacy keys as deprecations. normalizeAliases silently drops the deprecated
// entries when the canonical form is also populated, so a silent report would
// mean the user's [[accounts]] section is effectively a no-op without any
// signal. Reporting both lets the TUI banner surface the lost data.
func TestLoad_ReportsLegacyEvenWhenCanonicalAlsoSet(t *testing.T) {
	content := `
[[profiles]]
name = "primary"
home_dir = "~/.config/claude-primary"
color = "blue"

[[accounts]]
name = "legacy"
config_dir = "~/.config/claude-legacy"
color = "green"

[[projects]]
title = "api"
directory = "~/projects/api"
profile = "primary"

[[sessions]]
title = "old"
directory = "~/projects/old"
account = "legacy"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	keys := make(map[string]bool, len(cfg.Deprecations))
	for _, d := range cfg.Deprecations {
		keys[d.Key] = true
	}
	for _, want := range []string{"accounts", "sessions"} {
		if !keys[want] {
			t.Errorf("Deprecations missing key %q (got %+v)", want, cfg.Deprecations)
		}
	}
}

func TestLoad_CanonicalKeysHaveNoDeprecations(t *testing.T) {
	content := `
[defaults]
args = ["--shared"]

[[profiles]]
name = "work"
home_dir = "~/.config/claude-work"
color = "blue"

[[projects]]
title = "api"
directory = "~/projects/api"
profile = "work"
args = ["--project"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Deprecations) != 0 {
		t.Errorf("Deprecations: got %+v, want empty", cfg.Deprecations)
	}
}

func TestLoad_StopFailureDefaultsTrueWhenAbsent(t *testing.T) {
	content := `
[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Defaults.Notifications.StopFailure
	if got == nil {
		t.Fatal("StopFailure: got nil, want non-nil pointer to true")
	}
	if !*got {
		t.Fatalf("StopFailure default: got %v, want true", *got)
	}
}

func TestLoad_UserCanDisableStopFailureNotification(t *testing.T) {
	content := `
[defaults.notifications]
stop_failure = false

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Defaults.Notifications.StopFailure
	if got == nil {
		t.Fatal("StopFailure: got nil, want non-nil pointer to false")
	}
	if *got {
		t.Fatalf("StopFailure user-disabled: got true, want false (stop_failure = false in config should be respected)")
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
