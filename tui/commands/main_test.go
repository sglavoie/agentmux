package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain gives the package a home of its own. Tests here launch stand-in
// agents on a real private tmux server, and a launch writes its native log
// directory, the diagnostics key, and journal rows under the state home, and
// could read or write anything else a path helper resolves under HOME. None
// of that may be the home of whoever runs the suite.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "amx-tui-commands-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui/commands tests: creating an isolated home:", err)
		os.Exit(1)
	}
	for key, value := range map[string]string{
		"HOME":               root,
		"XDG_STATE_HOME":     filepath.Join(root, "state"),
		"XDG_DATA_HOME":      filepath.Join(root, "data"),
		"XDG_CONFIG_HOME":    filepath.Join(root, "config"),
		"AMX_CONFIG":         filepath.Join(root, "config", "agent-mux", "config.toml"),
		"AMX_OVERRIDES_PATH": filepath.Join(root, "launch_overrides.json"),
	} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintln(os.Stderr, "tui/commands tests: isolating", key+":", err)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
