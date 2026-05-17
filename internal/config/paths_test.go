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
