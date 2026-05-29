package config_test

import (
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

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
	path := writeTempConfig(t, content)

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
	path := writeTempConfig(t, content)

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
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Deprecations) != 0 {
		t.Errorf("Deprecations: got %+v, want empty", cfg.Deprecations)
	}
}

// TestLoad_ReportsLegacyAliasesEvenWhenCanonicalFieldAlsoSet pins the
// deprecation collector's "key encountered" contract for per-field aliases.
// args/profile win over claude_flags/account at read time, so a config that
// keeps both should still surface the deprecated key instead of making the
// ignored legacy value look active.
func TestLoad_ReportsLegacyAliasesEvenWhenCanonicalFieldAlsoSet(t *testing.T) {
	content := `
[defaults]
args = ["--shared"]
claude_flags = ["--ignored-shared"]

[[profiles]]
name = "work"
home_dir = "~/.config/claude-work"
color = "blue"

[[projects]]
title = "api"
directory = "~/projects/api"
profile = "work"
account = "work"
args = ["--project"]
claude_flags = ["--ignored-project"]
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	keys := make(map[string]bool, len(cfg.Deprecations))
	for _, d := range cfg.Deprecations {
		keys[d.Key] = true
	}
	for _, want := range []string{
		"defaults.claude_flags",
		"[[projects]].claude_flags",
		"[[projects]].account",
	} {
		if !keys[want] {
			t.Errorf("Deprecations missing key %q (got %+v)", want, cfg.Deprecations)
		}
	}

	wantDefaults := []string{"--shared"}
	if !reflect.DeepEqual(cfg.Defaults.Args, wantDefaults) {
		t.Errorf("Defaults.Args: got %v, want %v", cfg.Defaults.Args, wantDefaults)
	}
	if !reflect.DeepEqual(cfg.Defaults.ClaudeFlags, wantDefaults) {
		t.Errorf("Defaults.ClaudeFlags: got %v, want canonical mirror %v", cfg.Defaults.ClaudeFlags, wantDefaults)
	}

	wantProject := []string{"--project"}
	if !reflect.DeepEqual(cfg.Projects[0].Args, wantProject) {
		t.Errorf("Projects[0].Args: got %v, want %v", cfg.Projects[0].Args, wantProject)
	}
	if !reflect.DeepEqual(cfg.Projects[0].ClaudeFlags, wantProject) {
		t.Errorf("Projects[0].ClaudeFlags: got %v, want canonical mirror %v", cfg.Projects[0].ClaudeFlags, wantProject)
	}
}

func TestLoad_ReportsNegativeLegacyStaleActiveThreshold(t *testing.T) {
	content := `
[defaults]
stale_active_threshold = -1

[[profiles]]
name = "work"
home_dir = "~/.config/claude-work"
color = "blue"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, d := range cfg.Deprecations {
		if d.Key == "defaults.stale_active_threshold" {
			return
		}
	}
	t.Fatalf("Deprecations missing defaults.stale_active_threshold (got %+v)", cfg.Deprecations)
}

// TestLoad_ReportsZeroLegacyStaleActiveThreshold pins that deprecation
// detection is based on TOML key presence, not the decoded int value. A user
// who explicitly writes the old key with 0 still needs the migration banner;
// value-based detection used to miss it because 0 is also the Go zero value.
func TestLoad_ReportsZeroLegacyStaleActiveThreshold(t *testing.T) {
	content := `
[defaults]
stale_active_threshold = 0

[[profiles]]
name = "work"
home_dir = "~/.config/claude-work"
color = "blue"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, d := range cfg.Deprecations {
		if d.Key == "defaults.stale_active_threshold" {
			return
		}
	}
	t.Fatalf("Deprecations missing defaults.stale_active_threshold (got %+v)", cfg.Deprecations)
}

// TestLoad_ReportsEmptyLegacyAliases pins the same presence-based contract for
// empty list/string aliases. Empty legacy keys are still legacy keys and should
// be surfaced before normalizeAliases erases the distinction.
func TestLoad_ReportsEmptyLegacyAliases(t *testing.T) {
	content := `
[defaults]
claude_flags = []

[[profiles]]
name = "work"
home_dir = "~/.config/claude-work"
color = "blue"

[[projects]]
title = "api"
directory = "~/projects/api"
profile = "work"
account = ""
claude_flags = []
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	keys := make(map[string]bool, len(cfg.Deprecations))
	for _, d := range cfg.Deprecations {
		keys[d.Key] = true
	}
	for _, want := range []string{
		"defaults.claude_flags",
		"[[projects]].claude_flags",
		"[[projects]].account",
	} {
		if !keys[want] {
			t.Errorf("Deprecations missing key %q (got %+v)", want, cfg.Deprecations)
		}
	}
}
