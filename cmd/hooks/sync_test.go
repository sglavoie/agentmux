package hooks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// Hook files embed the binary that ran hooks-sync, and the hook indicator fails
// open, so a `go run` build — deleted when the command exits — would silently
// break every hook. The sync is refused before any file is written.
func TestConfigHooksSyncRefusesAGoRunBuild(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "claude-home")
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(validConfigBody(home)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)
	oldBinary := hooksBinary
	t.Cleanup(func() { hooksBinary = oldBinary })
	hooksBinary = func() (string, error) { return "/tmp/go-build1/b001/exe/agentmux", nil }

	err := configHooksSyncCmd.RunE(configHooksSyncCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to write hook commands") {
		t.Fatalf("RunE error = %v; want a go run refusal", err)
	}
	if _, statErr := os.Stat(home); !os.IsNotExist(statErr) {
		t.Fatalf("hook home %s was touched (stat err %v); want nothing written", home, statErr)
	}
}
