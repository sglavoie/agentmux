package indicator

import (
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/testenv"
)

// Hook fixtures run inside a managed tmux session unless a test clears it.
func TestMain(m *testing.M) {
	testenv.ClearSessionEnv()
	_ = os.Setenv(runtimesession.EnvVar, testRuntimeSession)
	os.Exit(m.Run())
}
