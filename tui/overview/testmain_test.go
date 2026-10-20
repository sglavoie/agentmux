package overview

import (
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ClearSessionEnv()
	// Test configurations name project directories that do not exist, and the
	// close-recovery relaunch goes through actions.Launch.
	actions.ProjectDirectoryProblem = func(string) string { return "" }
	os.Exit(m.Run())
}
