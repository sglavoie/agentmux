package debug

import (
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// quietBackendStatus is a machine that launches in Kitty and has no tmux
// server: what doctor reads in every test that does not say otherwise, so no
// test looks at the tmux or the server of whoever runs the suite.
func quietBackendStatus() cli.BackendStatus {
	return cli.BackendStatus{
		LaunchBackend: config.LaunchBackendKitty,
		Tmux:          cli.TmuxStatus{Available: true, Path: "/test/bin/tmux", Version: "3.7c", Source: "path", MinVersion: tmux.MinVersion, Candidates: []tmux.Candidate{}},
		Problems:      []cli.BackendProblem{},
	}
}

func TestMain(m *testing.M) {
	stateRoot, err := os.MkdirTemp("", "agentmux-debug-test-state-")
	if err != nil {
		panic(err)
	}
	previous, hadPrevious := os.LookupEnv("XDG_STATE_HOME")
	if err := os.Setenv("XDG_STATE_HOME", stateRoot); err != nil {
		panic(err)
	}
	doctorBackendStatus = quietBackendStatus
	doctorKittyVersion = func() string { return "0.43.1" }
	code := m.Run()
	if hadPrevious {
		_ = os.Setenv("XDG_STATE_HOME", previous)
	} else {
		_ = os.Unsetenv("XDG_STATE_HOME")
	}
	_ = os.RemoveAll(stateRoot)
	os.Exit(code)
}
