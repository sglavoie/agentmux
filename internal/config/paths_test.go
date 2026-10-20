package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestExpandTilde_Home(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := config.ExpandTilde("~")
	if got != home {
		t.Errorf("ExpandTilde(~): got %q, want %q", got, home)
	}
}

func TestExpandTilde_SubPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := config.ExpandTilde("~/.config/claude")
	want := filepath.Join(home, ".config", "claude")
	if got != want {
		t.Errorf("ExpandTilde(~/.config/claude): got %q, want %q", got, want)
	}
}

func TestExpandTilde_NoTilde(t *testing.T) {
	got := config.ExpandTilde("/absolute/path")
	if got != "/absolute/path" {
		t.Errorf("ExpandTilde(/absolute/path): got %q, want %q", got, "/absolute/path")
	}
}

// Only a leading "~" or "~/" of the current user expands; everything else is
// a literal the shell would not have expanded either.
func TestExpandTilde_LeavesOtherTildesAlone(t *testing.T) {
	for _, path := range []string{"~user", "~user/x", "a/~/b", "~x/y", "", "relative/path", "/~"} {
		if got := config.ExpandTilde(path); got != path {
			t.Errorf("ExpandTilde(%q) = %q, want it unchanged", path, got)
		}
	}
}

// Without a home directory the path comes back as written, never as "" or a
// path relative to the working directory.
func TestExpandTilde_NoHomeKeepsThePath(t *testing.T) {
	t.Setenv("HOME", "")
	for _, path := range []string{"~", "~/.codex"} {
		if got := config.ExpandTilde(path); got != path {
			t.Errorf("ExpandTilde(%q) with no HOME = %q, want it unchanged", path, got)
		}
	}
}

func TestExpandTilde_UsesHOME(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := config.ExpandTilde("~"); got != home {
		t.Errorf("ExpandTilde(~) = %q, want %q", got, home)
	}
	if got, want := config.ExpandTilde("~/a/../b"), filepath.Join(home, "b"); got != want {
		t.Errorf("ExpandTilde(~/a/../b) = %q, want %q", got, want)
	}
}

func TestDefaultConfigDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := config.DefaultConfigDir()
	want := filepath.Join(home, ".config", "agent-mux")
	if got != want {
		t.Errorf("DefaultConfigDir: got %q, want %q", got, want)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	t.Setenv("amx_CONFIG", "")
	got := config.DefaultConfigPath()
	if !strings.HasSuffix(got, "config.toml") {
		t.Errorf("DefaultConfigPath: got %q, expected suffix config.toml", got)
	}
}

func TestDefaultConfigPath_EnvUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Unsetenv("amx_CONFIG"); err != nil {
		t.Fatalf("unsetting amx_CONFIG: %v", err)
	}

	got := config.DefaultConfigPath()
	want := filepath.Join(home, ".config", "agent-mux", "config.toml")
	if got != want {
		t.Errorf("DefaultConfigPath (env unset): got %q, want %q", got, want)
	}
}

func TestDefaultConfigPath_EnvOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "custom.toml")
	t.Setenv("amx_CONFIG", override)

	got := config.DefaultConfigPath()
	if got != override {
		t.Errorf("DefaultConfigPath (env override): got %q, want %q", got, override)
	}
}

func TestDefaultConfigPath_EnvOverrideTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("amx_CONFIG", "~/bar/baz.toml")

	got := config.DefaultConfigPath()
	want := filepath.Join(home, "bar", "baz.toml")
	if got != want {
		t.Errorf("DefaultConfigPath (~ expansion): got %q, want %q", got, want)
	}
}

func TestDefaultConfigPath_EnvEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("amx_CONFIG", "")

	got := config.DefaultConfigPath()
	want := filepath.Join(home, ".config", "agent-mux", "config.toml")
	if got != want {
		t.Errorf("DefaultConfigPath (env empty): got %q, want %q", got, want)
	}
}

// TestDefaultConfigPath_UppercaseEnvOverride pins the contract that AMX_CONFIG
// (uppercase) is honored as a fallback when the lowercase amx_CONFIG is
// unset. The amx_DEBUG/AMX_DEBUG pair already accepts both casings, and a
// user setting one but not the other previously caused the config override to
// be silently ignored.
func TestDefaultConfigPath_UppercaseEnvOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "custom.toml")
	t.Setenv("amx_CONFIG", "")
	t.Setenv("AMX_CONFIG", override)

	got := config.DefaultConfigPath()
	if got != override {
		t.Errorf("DefaultConfigPath (AMX_CONFIG override): got %q, want %q", got, override)
	}
}

// TestDefaultConfigPath_LowercaseTakesPrecedence pins that when both casings
// are set, the lowercase form wins. This matches the lookup order documented
// on DefaultConfigPath and preserves backwards compatibility with configs
// that already rely on amx_CONFIG.
func TestDefaultConfigPath_LowercaseTakesPrecedence(t *testing.T) {
	lower := filepath.Join(t.TempDir(), "lower.toml")
	upper := filepath.Join(t.TempDir(), "upper.toml")
	t.Setenv("amx_CONFIG", lower)
	t.Setenv("AMX_CONFIG", upper)

	got := config.DefaultConfigPath()
	if got != lower {
		t.Errorf("DefaultConfigPath (both env set): got %q, want %q (lowercase precedence)", got, lower)
	}
}

func TestReadDefault_MissingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("amx_CONFIG", "")

	_, err := config.ReadDefault()
	if err == nil {
		t.Fatal("expected error for missing default config, got nil")
	}

	var missing *config.MissingConfigError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingConfigError, got %T", err)
	}

	want := filepath.Join(home, ".config", "agent-mux", "config.toml")
	if missing.Path != want {
		t.Errorf("missing config path: got %q, want %q", missing.Path, want)
	}
	if !strings.Contains(err.Error(), "amx config init") {
		t.Errorf("missing config error: got %q, expected amx config init hint", err.Error())
	}
}

func TestLoadDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("amx_CONFIG", "")

	path := filepath.Join(home, ".config", "agent-mux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("[defaults]\nsort = \"alpha\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if cfg.Defaults.Sort != "alpha" {
		t.Errorf("sort: got %q, want %q", cfg.Defaults.Sort, "alpha")
	}
	if cfg.Defaults.RefreshInterval != 2 {
		t.Errorf("refresh_interval: got %d, want 2", cfg.Defaults.RefreshInterval)
	}
}
