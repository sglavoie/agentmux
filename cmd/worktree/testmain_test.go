package worktree

import (
	"errors"
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ClearSessionEnv()
	// Default launches must never reach the operator's private server.
	// Tests exercising tmux install their own isolated backend.
	cli.NewTmuxBackend = func() (backend.TargetLauncher, error) {
		return nil, errors.New("test must install a tmux backend seam")
	}
	stateRoot, err := os.MkdirTemp("", "agentmux-worktree-test-state-")
	if err != nil {
		panic(err)
	}
	previous, hadPrevious := os.LookupEnv("XDG_STATE_HOME")
	if err := os.Setenv("XDG_STATE_HOME", stateRoot); err != nil {
		panic(err)
	}
	// No test reads a real tmux server unless it supplies its own.
	worktreeCommandsObservedTmux = func() (backend.TargetLauncher, error) {
		return nil, errors.New("no tmux server in worktree tests")
	}
	code := m.Run()
	if hadPrevious {
		_ = os.Setenv("XDG_STATE_HOME", previous)
	} else {
		_ = os.Unsetenv("XDG_STATE_HOME")
	}
	_ = os.RemoveAll(stateRoot)
	os.Exit(code)
}
