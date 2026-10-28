package config

import (
	"bytes"
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
		name     string
		editor   string
		path     string
		wantName string
		wantArgs []string
		wantErr  bool
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
			name:     "quoted editor argument stays intact",
			editor:   `vim -c "set ft=toml"`,
			path:     "/tmp/config.toml",
			wantName: "vim",
			wantArgs: []string{"-c", "set ft=toml", "/tmp/config.toml"},
		},
		{
			name:     "quoted binary flag value stays intact",
			editor:   `open -a "Visual Studio Code" --args --wait`,
			path:     "/tmp/config.toml",
			wantName: "open",
			wantArgs: []string{"-a", "Visual Studio Code", "--args", "--wait", "/tmp/config.toml"},
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
		{
			name:    "unterminated quote returns an error",
			editor:  `vim -c "set ft=toml`,
			path:    "/tmp/config.toml",
			wantErr: true,
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

func TestConfigPathCommandWritesToCommandOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)

	oldOut := configPathCmd.OutOrStdout()
	t.Cleanup(func() {
		configPathCmd.SetOut(oldOut)
	})

	var out bytes.Buffer
	configPathCmd.SetOut(&out)

	if err := configPathCmd.RunE(configPathCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got, want := out.String(), path+"\n"; got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
}

func TestConfigInitCommandWritesToCommandOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)

	oldOut := configInitCmd.OutOrStdout()
	t.Cleanup(func() {
		configInitCmd.SetOut(oldOut)
	})

	var out bytes.Buffer
	configInitCmd.SetOut(&out)

	if err := configInitCmd.RunE(configInitCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got, want := out.String(), "created "+path+"\n"; got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("created config stat: %v", err)
	}
}

func TestConfigInitRefusesExistingFileWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	const original = "# keep this config\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("amx_CONFIG", path)
	if err := configInitCmd.RunE(configInitCmd, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("init existing config: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("existing config changed: %q, %v", data, err)
	}
}

func TestConfigInitRefusesDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.toml")
	target := filepath.Join(root, "other-file")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("amx_CONFIG", path)
	if err := configInitCmd.RunE(configInitCmd, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("init dangling symlink: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("init followed symlink to target: %v", err)
	}
}

func TestConfigShowCommandWritesToCommandOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "example = true\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)

	oldOut := configShowCmd.OutOrStdout()
	t.Cleanup(func() {
		configShowCmd.SetOut(oldOut)
	})

	var out bytes.Buffer
	configShowCmd.SetOut(&out)

	if err := configShowCmd.RunE(configShowCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got := out.String(); got != body {
		t.Fatalf("output = %q; want %q", got, body)
	}
}

func TestConfigShowCommandAllowsInvalidProfilesForRecovery(t *testing.T) {
	tempHome := t.TempDir()
	path := filepath.Join(tempHome, "config.toml")
	body := duplicateProfileConfigBody(
		filepath.Join(tempHome, "claude"),
		filepath.Join(tempHome, "codex"),
	)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)

	oldOut := configShowCmd.OutOrStdout()
	t.Cleanup(func() {
		configShowCmd.SetOut(oldOut)
	})

	var out bytes.Buffer
	configShowCmd.SetOut(&out)

	if err := configShowCmd.RunE(configShowCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got := out.String(); got != body {
		t.Fatalf("output = %q; want %q", got, body)
	}
}

func TestConfigShowCommandReturnsWriteError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("example = true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)

	oldOut := configShowCmd.OutOrStdout()
	t.Cleanup(func() {
		configShowCmd.SetOut(oldOut)
	})

	configShowCmd.SetOut(failingWriter{})

	if err := configShowCmd.RunE(configShowCmd, nil); err == nil {
		t.Fatal("RunE error = nil; want write error")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, os.ErrPermission
}
