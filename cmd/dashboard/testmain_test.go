package dashboard

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
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
	code := m.Run()
	if hadPrevious {
		_ = os.Setenv("XDG_STATE_HOME", previous)
	} else {
		_ = os.Unsetenv("XDG_STATE_HOME")
	}
	_ = os.RemoveAll(stateRoot)
	os.Exit(code)
}
