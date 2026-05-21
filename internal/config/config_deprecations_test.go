package config_test

import (
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
