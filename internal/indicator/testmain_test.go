package indicator

import (
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// Most indicator fixtures exercise legacy Kitty hooks. Tests that exercise
// managed hooks set their own runtime ID explicitly; the test runner's session
// must not turn every other fixture into a managed hook.
func TestMain(m *testing.M) {
	_ = os.Unsetenv(runtimesession.EnvVar)
	os.Exit(m.Run())
}
