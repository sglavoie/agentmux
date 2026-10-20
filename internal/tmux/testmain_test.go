package tmux

import (
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/testenv"
)

// This package reads the session and terminal the process runs in; a test run
// typed inside an Agent Mux session must not hand it the operator's.
func TestMain(m *testing.M) {
	testenv.ClearSessionEnv()
	os.Exit(m.Run())
}
