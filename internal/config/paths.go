package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const configFileName = "config.toml"

// MissingConfigError reports that the app config is missing at the expected
// path and points the user to the bootstrap command.
type MissingConfigError struct {
	Path string
}

func (e *MissingConfigError) Error() string {
	return fmt.Sprintf(
		"config not found at %s; run `amx config init` to create a starter config",
		e.Path,
	)
}

// DefaultConfigDir returns ~/.config/agent-mux (fixed path, no env override).
func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agent-mux")
}

// DefaultConfigPath returns the fixed path to config.toml.
func DefaultConfigPath() string {
	return filepath.Join(DefaultConfigDir(), configFileName)
}

// ReadDefault reads the default config.toml path and returns an actionable
// error when the file is missing.
func ReadDefault() ([]byte, error) {
	path := DefaultConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &MissingConfigError{Path: path}
		}
		return nil, err
	}
	return data, nil
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
