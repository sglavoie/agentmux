package hooks

import (
	"os"
	"os/exec"
	"testing"

	"github.com/sglavoie/agentmux/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ClearSessionEnv()
	stateRoot, err := os.MkdirTemp("", "agentmux-hooks-test-state-")
	if err != nil {
		panic(err)
	}
	previous, hadPrevious := os.LookupEnv("XDG_STATE_HOME")
	if err := os.Setenv("XDG_STATE_HOME", stateRoot); err != nil {
		panic(err)
	}
	// The operator's installed amx must not make every test binary foreign.
	hooksLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	code := m.Run()
	if hadPrevious {
		_ = os.Setenv("XDG_STATE_HOME", previous)
	} else {
		_ = os.Unsetenv("XDG_STATE_HOME")
	}
	_ = os.RemoveAll(stateRoot)
	os.Exit(code)
}
