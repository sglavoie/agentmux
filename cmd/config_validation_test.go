package cmd

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
		`project "missing-first": directory "~/missing-first" does not exist`,
		`project "file-project": directory "~/not-a-directory" is not a directory`,
		`project "uninspectable-last": directory "~/not-a-directory/child" cannot be inspected`,
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

func TestConfigHooksStatusCommandWritesToCommandOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	home := filepath.Join(t.TempDir(), "missing-home")
	if err := os.WriteFile(path, []byte(validConfigBody(home)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)

	oldOut := configHooksStatusCmd.OutOrStdout()
	oldErr := configHooksStatusCmd.ErrOrStderr()
	t.Cleanup(func() {
		configHooksStatusCmd.SetOut(oldOut)
		configHooksStatusCmd.SetErr(oldErr)
	})

	var out, stderr bytes.Buffer
	configHooksStatusCmd.SetOut(&out)
	configHooksStatusCmd.SetErr(&stderr)

	if err := configHooksStatusCmd.RunE(configHooksStatusCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"ACCOUNT",
		"work",
		"missing",
		"hint: run `amx config hooks-sync`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("hooks-status output missing %q:\n%s", want, got)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q; want empty", stderr.String())
	}
}

func TestConfigHooksStatusCommandWritesErrorsToCommandErrorOutput(t *testing.T) {
	tempHome := t.TempDir()
	badHomeParent := filepath.Join(tempHome, "bad-as-file")
	if err := os.WriteFile(badHomeParent, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write badHomeParent: %v", err)
	}

	path := filepath.Join(tempHome, "config.toml")
	if err := os.WriteFile(path, []byte(validConfigBody(filepath.Join(badHomeParent, "nested"))), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)

	oldOut := configHooksStatusCmd.OutOrStdout()
	oldErr := configHooksStatusCmd.ErrOrStderr()
	t.Cleanup(func() {
		configHooksStatusCmd.SetOut(oldOut)
		configHooksStatusCmd.SetErr(oldErr)
	})

	var out, stderr bytes.Buffer
	configHooksStatusCmd.SetOut(&out)
	configHooksStatusCmd.SetErr(&stderr)

	err := configHooksStatusCmd.RunE(configHooksStatusCmd, nil)
	if err == nil {
		t.Fatal("RunE error = nil; want status error")
	}
	if got := out.String(); !strings.Contains(got, "error") || !strings.Contains(got, "work") {
		t.Fatalf("stdout should include account error row, got:\n%s", got)
	}
	if got := stderr.String(); !strings.Contains(got, "error:") || !strings.Contains(got, "work") {
		t.Fatalf("stderr should include status error, got:\n%s", got)
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

// TestConfigHooksSync_ReturnsErrorOnPartialFailure pins the contract that the
// hooks-sync command exits non-zero when any per-profile merge fails. Earlier
// behavior printed per-account errors to stderr but returned nil, so a CI run
// or a human script could not notice that one profile had not been updated.
func TestConfigHooksSync_ReturnsErrorOnPartialFailure(t *testing.T) {
	tempHome := t.TempDir()
	// goodHome is a real directory; merge for this profile must succeed.
	goodHome := filepath.Join(tempHome, "good")
	if err := os.MkdirAll(goodHome, 0o755); err != nil {
		t.Fatalf("mkdir goodHome: %v", err)
	}
	// badHomeParent is a regular file standing where a directory must live —
	// this makes mergeViaContract's MkdirAll fail when it tries to create the
	// parent of <badHome>/settings.json, modelling a real-world "permission
	// denied" or "not a directory" failure.
	badHomeParent := filepath.Join(tempHome, "bad-as-file")
	if err := os.WriteFile(badHomeParent, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write badHomeParent: %v", err)
	}
	badHome := filepath.Join(badHomeParent, "nested")

	cfgPath := filepath.Join(tempHome, "config.toml")
	cfgBody := `
[defaults]
sort = "alpha"

[harness_emojis]
claude = "🦊"

[[profiles]]
name = "good"
harness = "claude"
home_dir = "` + goodHome + `"
color = "blue"

[[profiles]]
name = "bad"
harness = "claude"
home_dir = "` + badHome + `"
color = "red"
`
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("amx_CONFIG", cfgPath)

	// Drive the cobra command via rootCmd so the subcommand resolution path is
	// the same as the real binary's. We explicitly pass "config hooks-sync"
	// so the test does not accidentally execute the root TUI command.
	rootCmd.SetArgs([]string{"config", "hooks-sync"})
	var stdoutBuf, stderrBuf bytes.Buffer
	rootCmd.SetOut(&stdoutBuf)
	rootCmd.SetErr(&stderrBuf)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	err := rootCmd.Execute()

	if err == nil {
		t.Fatalf("expected non-nil error from hooks-sync with one failing profile, got nil (stdout=%q stderr=%q)", stdoutBuf.String(), stderrBuf.String())
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("expected error to name the failing profile %q, got %q", "bad", err.Error())
	}
	// stderr must carry the per-account error detail through Cobra's configured
	// stream, not directly through process-global os.Stderr.
	if !strings.Contains(stderrBuf.String(), "error (bad)") {
		t.Errorf("expected stderr to contain per-account error line, got %q", stderrBuf.String())
	}
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
