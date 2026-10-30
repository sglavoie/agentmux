package dashboard

import (
	"errors"
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ClearSessionEnv()
	stateRoot, err := os.MkdirTemp("", "agentmux-dashboard-test-state-")
	if err != nil {
		panic(err)
	}
	previous, hadPrevious := os.LookupEnv("XDG_STATE_HOME")
	if err := os.Setenv("XDG_STATE_HOME", stateRoot); err != nil {
		panic(err)
	}
	// Every test in this package runs as a surface inside Kitty with no tmux
	// server, whatever terminal the suite itself was started from; the tests
	// of terminal selection replace these seams themselves.
	insideKitty = func() bool { return true }
	tmuxServerPresent = func() bool { return false }
	openTmux = func() (backend.TargetLauncher, error) {
		return nil, errors.New("test must install a tmux backend seam")
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
