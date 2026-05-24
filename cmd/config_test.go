package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestResolveEditorCommand_SplitsEditorArguments locks in the fix for the
// `amx config edit` bug where exec.Command treats the entire $EDITOR string as
// a single program name. Real-world EDITOR values like "code --wait" or
// "emacs -nw" must be split so the flags are passed as arguments, not as part
// of the binary name.
func TestResolveEditorCommand_SplitsEditorArguments(t *testing.T) {
	cases := []struct {
		name       string
		editor     string
		path       string
		wantName   string
		wantArgs   []string
		wantErr    bool
	}{
		{
			name:     "single binary uses it directly",
			editor:   "vim",
			path:     "/tmp/config.toml",
			wantName: "vim",
			wantArgs: []string{"/tmp/config.toml"},
		},
		{
			name:     "editor with flags is split",
			editor:   "code --wait",
			path:     "/tmp/config.toml",
			wantName: "code",
			wantArgs: []string{"--wait", "/tmp/config.toml"},
		},
		{
			name:     "editor with multiple flags is split",
			editor:   "emacs -nw --no-splash",
			path:     "/tmp/config.toml",
			wantName: "emacs",
			wantArgs: []string{"-nw", "--no-splash", "/tmp/config.toml"},
		},
		{
			name:     "empty EDITOR falls back to vi",
			editor:   "",
			path:     "/tmp/config.toml",
			wantName: "vi",
			wantArgs: []string{"/tmp/config.toml"},
		},
		{
			name:     "whitespace-only EDITOR falls back to vi",
			editor:   "   ",
			path:     "/tmp/config.toml",
			wantName: "vi",
			wantArgs: []string{"/tmp/config.toml"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, args, err := resolveEditorCommand(tc.editor, tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != tc.wantName {
				t.Errorf("name: got %q, want %q", name, tc.wantName)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args: got %#v, want %#v", args, tc.wantArgs)
			}
		})
	}
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

[harness_colors]
claude = "blue"

[[profiles]]
name = "good"
harness = "claude"
home_dir = "` + goodHome + `"

[[profiles]]
name = "bad"
harness = "claude"
home_dir = "` + badHome + `"
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

	// The hooks-sync RunE writes per-account errors directly to os.Stderr
	// (not the cobra command's stderr), so we intercept the real os.Stderr
	// to capture them.
	origStderr := os.Stderr
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("pipe: %v", pipeErr)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	err := rootCmd.Execute()

	_ = w.Close()
	os.Stderr = origStderr
	capturedStderr := <-done

	if err == nil {
		t.Fatalf("expected non-nil error from hooks-sync with one failing profile, got nil (stdout=%q stderr=%q)", stdoutBuf.String(), capturedStderr)
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("expected error to name the failing profile %q, got %q", "bad", err.Error())
	}
	// stderr must still carry the per-account error detail.
	if !strings.Contains(capturedStderr, "error (bad)") {
		t.Errorf("expected stderr to contain per-account error line, got %q", capturedStderr)
	}
}
