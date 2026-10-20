package indicator

import (
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/testenv"
)

// Most indicator fixtures exercise legacy Kitty hooks. Tests that exercise
// managed hooks set their own runtime ID explicitly; the test runner's session
// must not turn every other fixture into a managed hook, so testenv clears it
// with the rest of the session environment.
func TestMain(m *testing.M) {
	testenv.ClearSessionEnv()
	os.Exit(m.Run())
}
