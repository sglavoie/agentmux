package overview

import (
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
)

func TestMain(m *testing.M) {
	// Test configurations name project directories that do not exist, and the
	// close-recovery relaunch goes through actions.Launch.
	actions.ProjectDirectoryProblem = func(string) string { return "" }
	os.Exit(m.Run())
}
