package project

import (
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
)

func TestMain(m *testing.M) {
	stateRoot, err := os.MkdirTemp("", "agentmux-project-test-state-")
	if err != nil {
		panic(err)
	}
	previous, hadPrevious := os.LookupEnv("XDG_STATE_HOME")
	if err := os.Setenv("XDG_STATE_HOME", stateRoot); err != nil {
		panic(err)
	}
	// Test configurations name directories that do not exist on purpose; the
	// directory check is off unless a test installs its own.
	projectDirectoryProblem = func(string) string { return "" }
	actions.ProjectDirectoryProblem = projectDirectoryProblem
	code := m.Run()
	if hadPrevious {
		_ = os.Setenv("XDG_STATE_HOME", previous)
	} else {
		_ = os.Unsetenv("XDG_STATE_HOME")
	}
	_ = os.RemoveAll(stateRoot)
	os.Exit(code)
}
