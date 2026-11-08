//go:build tuitest

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sglavoie/agentmux/internal/overrides"
)

// uitestConfigEnv captures the throwaway locations a config-backed harness run
// redirects HOME and the AMX/XDG data paths to. Staging these before loading a
// config guarantees a run can never read or write the operator's real config,
// overrides, plan index, transcript index, or daemon socket.
type uitestConfigEnv struct {
	Root       string            // temp root containing every staged location
	Home       string            // staged $HOME
	ConfigPath string            // staged config.toml the run loads
	Vars       map[string]string // env var → value applied via the setenv callback
}

// IsolateUITestEnv points every user-path helper at an empty environment
// under root before a harness run builds a model. A fixture needs it as much
// as a staged config does: constructing a model loads the launch overrides
// and prunes the entries its projects do not name, and with the operator's
// own HOME that is the operator's file, emptied by a fixture's projects.
func IsolateUITestEnv(root string, setenv func(string, string) error) error {
	_, err := stageUITestConfigEnv(root, "", setenv)
	return err
}

// stageUITestConfigEnv stages an isolated environment under root and, when
// srcConfigPath is non-empty, copies that config into it. It applies HOME,
// AMX_CONFIG, AMX_OVERRIDES_PATH, XDG_DATA_HOME, and XDG_STATE_HOME through
// setenv so every user-path helper (config, overrides, plans, transcript,
// daemon) resolves under root. Pass t.Setenv in tests and os.Setenv in the CLI.
func stageUITestConfigEnv(root, srcConfigPath string, setenv func(string, string) error) (uitestConfigEnv, error) {
	home := filepath.Join(root, "home")
	dataHome := filepath.Join(root, "xdg-data")
	stateHome := filepath.Join(root, "xdg-state")
	cfgDir := filepath.Join(home, ".config", "agent-mux")
	for _, dir := range []string{home, dataHome, stateHome, cfgDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return uitestConfigEnv{}, fmt.Errorf("uitest: staging %s: %w", dir, err)
		}
	}

	configPath := filepath.Join(cfgDir, "config.toml")
	if srcConfigPath != "" {
		data, err := os.ReadFile(srcConfigPath)
		if err != nil {
			return uitestConfigEnv{}, fmt.Errorf("uitest: reading config %s: %w", srcConfigPath, err)
		}
		if err := os.WriteFile(configPath, data, 0o644); err != nil {
			return uitestConfigEnv{}, fmt.Errorf("uitest: staging config: %w", err)
		}
	}

	vars := map[string]string{
		"HOME":               home,
		"AMX_CONFIG":         configPath,
		overrides.PathEnvVar: filepath.Join(home, ".local", "share", "agent-mux", "launch_overrides.json"),
		"XDG_DATA_HOME":      dataHome,
		"XDG_STATE_HOME":     stateHome,
	}
	// Apply in a stable order so a setenv failure is attributed deterministically.
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := setenv(k, vars[k]); err != nil {
			return uitestConfigEnv{}, fmt.Errorf("uitest: setting %s: %w", k, err)
		}
	}

	return uitestConfigEnv{Root: root, Home: home, ConfigPath: configPath, Vars: vars}, nil
}
