package companion

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/testenv"
)

// TestMain fails the package if any test leaves a goroutine behind. The
// projection is pure and starts none, so the only acceptable count after the
// run is the one before it.
//
// It follows the runtime.NumGoroutine baseline pattern used in
// internal/daemon/ipc rather than go.uber.org/goleak, which is not a module
// dependency: a third-party test dependency is a decision for the operator,
// and a pure package needs no more than a count to prove it starts nothing.
func TestMain(m *testing.M) {
	testenv.ClearSessionEnv()
	baseline := runtime.NumGoroutine()
	code := m.Run()
	if code == 0 {
		if leaked := settleGoroutines(baseline); leaked > 0 {
			fmt.Fprintf(os.Stderr, "companion: %d goroutine(s) leaked by the tests\n", leaked)
			code = 1
		}
	}
	os.Exit(code)
}

func settleGoroutines(baseline int) int {
	deadline := time.Now().Add(2 * time.Second)
	for {
		runtime.GC()
		extra := runtime.NumGoroutine() - baseline
		if extra <= 0 || time.Now().After(deadline) {
			return max(extra, 0)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
