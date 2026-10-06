package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
)

func TestConfigValidateCommandWritesToCommandOutput(t *testing.T) {
	tempHome := t.TempDir()
	projectDir := filepath.Join(tempHome, "projects", "api")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	t.Setenv("HOME", tempHome)

	path := filepath.Join(tempHome, "config.toml")
	body := validConfigBody(tempHome) + `
[[projects]]
title = "api"
directory = "~/projects/api"
profile = "work"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)

	oldOut := configValidateCmd.OutOrStdout()
	t.Cleanup(func() {
		configValidateCmd.SetOut(oldOut)
	})

	var out bytes.Buffer
	configValidateCmd.SetOut(&out)

	if err := configValidateCmd.RunE(configValidateCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got, want := out.String(), "config is valid\n"; got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
}

// TestConfigValidateCommandWarnsAboutUnknownAndDeprecatedKeys pins that a
// misspelled or legacy key is named without failing validation: the config
// still loads, but the setting the operator meant is not the one in effect.
func TestConfigValidateCommandWarnsAboutUnknownAndDeprecatedKeys(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	path := filepath.Join(tempHome, "config.toml")
	body := "[defaults]\nrefresh_intervall = 3\nclaude_flags = []\n" + validConfigBody(tempHome)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)

	oldOut := configValidateCmd.OutOrStdout()
	t.Cleanup(func() {
		configValidateCmd.SetOut(oldOut)
	})

	var out bytes.Buffer
	configValidateCmd.SetOut(&out)

	if err := configValidateCmd.RunE(configValidateCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	for _, want := range []string{
		"warning: unknown key defaults.refresh_intervall",
		"warning: deprecated defaults.claude_flags in [defaults]; use defaults.args",
		"config is valid\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestConfigValidateCommandListsAllInvalidProjectDirectories(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	notDirectory := filepath.Join(tempHome, "not-a-directory")
	if err := os.WriteFile(notDirectory, []byte("file"), 0o644); err != nil {
		t.Fatalf("write non-directory: %v", err)
	}

	path := filepath.Join(tempHome, "config.toml")
	body := validConfigBody(tempHome) + `
[[projects]]
title = "missing-first"
directory = "~/missing-first"
profile = "work"

[[projects]]
title = "file-project"
directory = "~/not-a-directory"
profile = "work"

[[projects]]
title = "uninspectable-last"
directory = "~/not-a-directory/child"
profile = "work"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)

	oldOut := configValidateCmd.OutOrStdout()
	t.Cleanup(func() {
		configValidateCmd.SetOut(oldOut)
	})

	var out bytes.Buffer
	configValidateCmd.SetOut(&out)

	err := configValidateCmd.RunE(configValidateCmd, nil)
	if err == nil {
		t.Fatal("RunE error = nil; want invalid project directories error")
	}
	if out.Len() != 0 {
		t.Fatalf("output = %q; want empty", out.String())
	}

	msg := err.Error()
	wantEntries := []string{
		`project "work/missing-first": directory "~/missing-first" does not exist`,
		`project "work/file-project": directory "~/not-a-directory" is not a directory`,
		`project "work/uninspectable-last": directory "~/not-a-directory/child" cannot be inspected`,
	}
	prior := -1
	for _, want := range wantEntries {
		index := strings.Index(msg, want)
		if index < 0 {
			t.Fatalf("validation error missing %q:\n%s", want, msg)
		}
		if index <= prior {
			t.Fatalf("validation entries out of config order:\n%s", msg)
		}
		prior = index
	}
	if strings.Contains(msg, tempHome) {
		t.Fatalf("validation error exposed resolved path; want configured paths only:\n%s", msg)
	}
}

func TestLoadRunnableConfigRejectsDuplicateProfiles(t *testing.T) {
	tempHome := t.TempDir()
	path := filepath.Join(tempHome, "config.toml")
	if err := os.WriteFile(path, []byte(duplicateProfileConfigBody(
		filepath.Join(tempHome, "claude"),
		filepath.Join(tempHome, "codex"),
	)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)

	_, err := cli.LoadRunnableConfig()
	if err == nil {
		t.Fatal("cli.LoadRunnableConfig error = nil; want duplicate profile validation error")
	}
	msg := err.Error()
	for _, want := range []string{"duplicate profile name", "unique launch ID", "vta-claude", "vta-codex"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("cli.LoadRunnableConfig error missing %q:\n%s", want, msg)
		}
	}
}

func validConfigBody(homeDir string) string {
	return `
[harness_emojis]
claude = "🦊"

[[profiles]]
name = "work"
harness = "claude"
home_dir = "` + homeDir + `"
color = "blue"
`
}

func duplicateProfileConfigBody(claudeHome, codexHome string) string {
	return `
[harness_emojis]
claude = "🦊"
codex = "🤖"

[[profiles]]
name = "vta"
harness = "claude"
home_dir = "` + claudeHome + `"
color = "blue"

[[profiles]]
name = "vta"
harness = "codex"
home_dir = "` + codexHome + `"
color = "green"
`
}
