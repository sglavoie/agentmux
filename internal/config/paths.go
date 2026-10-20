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

// DefaultConfigDir returns ~/.config/agent-mux.
func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agent-mux")
}

// DefaultConfigPath returns the path to config.toml. When amx_CONFIG or
// AMX_CONFIG is set to a non-empty value, that path (with leading ~ expanded)
// is returned; otherwise the default <DefaultConfigDir>/config.toml is used.
// Both casings are accepted to match the amx_DEBUG/AMX_DEBUG convention; the
// lowercase form is checked first so existing configs that rely on it keep
// working.
func DefaultConfigPath() string {
	for _, key := range []string{"amx_CONFIG", "AMX_CONFIG"} {
		if v := os.Getenv(key); v != "" {
			return ExpandTilde(v)
		}
	}
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

// ExpandTilde replaces a leading "~" or "~/" with the user's home directory.
// It is the one tilde expansion in the tree (internal/agentloop's
// CanonicalizePath uses it too, to mirror Python's expanduser).
//
// Only the current user's home is expanded: "~user" and a "~" anywhere but
// the start are left as they are. When the home directory cannot be
// determined the path is returned unchanged rather than turned into "" or a
// path relative to the working directory, so an error that follows still
// names the path the operator wrote.
func ExpandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
