package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ClearSessionEnv()
	stateRoot, err := os.MkdirTemp("", "agentmux-tui-test-state-")
	if err != nil {
		panic(err)
	}
	previous, hadPrevious := os.LookupEnv("XDG_STATE_HOME")
	if err := os.Setenv("XDG_STATE_HOME", stateRoot); err != nil {
		panic(err)
	}
	previousOverrides, hadPreviousOverrides := os.LookupEnv(overrides.PathEnvVar)
	if err := os.Setenv(overrides.PathEnvVar, filepath.Join(stateRoot, "launch_overrides.json")); err != nil {
		panic(err)
	}
	// Test configurations name project directories that do not exist.
	actions.ProjectDirectoryProblem = func(string) string { return "" }
	code := m.Run()
	if hadPrevious {
		_ = os.Setenv("XDG_STATE_HOME", previous)
	} else {
		_ = os.Unsetenv("XDG_STATE_HOME")
	}
	if hadPreviousOverrides {
		_ = os.Setenv(overrides.PathEnvVar, previousOverrides)
	} else {
		_ = os.Unsetenv(overrides.PathEnvVar)
	}
	_ = os.RemoveAll(stateRoot)
	os.Exit(code)
}
