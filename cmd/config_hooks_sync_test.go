package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
