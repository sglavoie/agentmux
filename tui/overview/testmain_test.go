package overview

import (
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/testenv"
	"github.com/sglavoie/agentmux/tui/commands"
)

func TestMain(m *testing.M) {
	testenv.ClearSessionEnv()
	commands.Portable.Launch = runtimesession.BackendTmux
	// Test configurations name project directories that do not exist, and the
	// close-recovery relaunch goes through actions.Launch.
	actions.ProjectDirectoryProblem = func(string) string { return "" }
	os.Exit(m.Run())
}
