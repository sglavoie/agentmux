package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
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
	want := filepath.Join(home, ".config", "cc-manager")
	if got != want {
		t.Errorf("DefaultConfigDir: got %q, want %q", got, want)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	got := config.DefaultConfigPath()
	if !strings.HasSuffix(got, "config.toml") {
		t.Errorf("DefaultConfigPath: got %q, expected suffix config.toml", got)
	}
}
