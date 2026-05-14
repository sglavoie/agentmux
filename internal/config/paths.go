package config

import (
	"os"
	"path/filepath"
	"strings"
)

const configFileName = "config.toml"

// DefaultConfigDir returns ~/.config/cc-manager (fixed path, no env override).
func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cc-manager")
}

// DefaultConfigPath returns the fixed path to config.toml.
func DefaultConfigPath() string {
	return filepath.Join(DefaultConfigDir(), configFileName)
}

// ExpandTilde replaces a leading ~ with the user's home directory.
func ExpandTilde(path string) string {
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
