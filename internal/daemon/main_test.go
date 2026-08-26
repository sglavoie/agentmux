package daemon

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points the whole package at an empty XDG state home. A Handler with
// no configured ASL state root derives one the way agent-session-loop does, so
// without this every test that reconciles leases would sweep the operator's
// real ~/.local/state/agent-session-loop — reading one state file per plan they
// have ever run and probing live processes. That is both a test-isolation
// violation and a source of wall-clock flakiness in the readiness tests, whose
// assertions are timing budgets. The state home is empty, so the derived ASL
// state root does not exist: the baseline for a machine that has never run ASL,
// which every sweep must read as indeterminate rather than as "no runs
// anywhere". Tests about discovery evidence build the sweep result directly
// with stubDaemonDiscoverPlans; tests that need their own state home still set
// it with t.Setenv.
func TestMain(m *testing.M) {
	stateHome, err := os.MkdirTemp("", "amx-daemon-state-home")
	if err != nil {
		fmt.Fprintln(os.Stderr, "daemon tests: creating isolated state home:", err)
		os.Exit(1)
	}
	if err := os.Setenv("XDG_STATE_HOME", stateHome); err != nil {
		fmt.Fprintln(os.Stderr, "daemon tests: isolating state home:", err)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(stateHome)
	os.Exit(code)
}
