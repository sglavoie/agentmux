package daemon

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// Latency budgets are separated from correctness here on purpose.
//
// The lifecycle tests assert what the daemon *does*: it publishes a serving
// socket, and it joins every goroutine it owns before Run returns. Those are
// properties of the code and hold at any machine load, so those tests
// synchronize on signals the daemon itself publishes and never on elapsed time.
//
// How *quickly* those transitions happen is a property of the machine as much
// as of the code. Asserting it inside the correctness suites is what makes a
// contended `just check` or a parallel stress run report a failure that says
// nothing about the change under test. The budgets still matter — a startup
// that regresses from 100 ms to 5 s is a real defect — so they live here and
// run deliberately, under `just reliability-stress`, on a machine that is not
// simultaneously running the rest of the suite.
//
// Opt in with AMX_PERF_TESTS=1.
const perfTestEnvVar = "AMX_PERF_TESTS"

func requirePerformanceOptIn(t *testing.T) {
	t.Helper()
	if os.Getenv(perfTestEnvVar) != "1" {
		t.Skipf("latency budget test; set %s=1 to measure (see `just reliability-stress`)", perfTestEnvVar)
	}
}

// measureStartupPublication runs a daemon to its startup-publication barrier
// and reports how long that took, then shuts it down cleanly.
func measureStartupPublication(t *testing.T, cfg *config.Config) time.Duration {
	t.Helper()

	published := make(chan time.Duration, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := time.Now()
	runErr := make(chan error, 1)
	go func() {
		runErr <- run(ctx, cfg, runDependencies{
			afterStartupPublication: func() { published <- time.Since(started) },
		})
	}()

	var elapsed time.Duration
	select {
	case elapsed = <-published:
	case err := <-runErr:
		t.Fatalf("Run returned before startup publication: %v", err)
	case <-time.After(testLivenessGrace):
		t.Fatal("Run did not complete startup publication")
	}

	cancel()
	waitForRunReturn(t, runErr)
	return elapsed
}

// startupPublicationBudget is the wall-clock ceiling for reaching startup
// publication with a corrupt megabyte-scale transcript cache in place. The
// daemon must recognize the cache as unusable rather than parse it, so the
// budget is dominated by the bounded work startup is allowed to do, not by the
// size of the file on disk.
const startupPublicationBudget = 500 * time.Millisecond

// TestPerformance_StartupPublicationBudgetWithHugeMalformedIndex is the latency
// half of the huge-index regression. Its correctness half — that publication
// happens at all — is TestRun_BindsSocketWithHugeMalformedTranscriptIndex.
func TestPerformance_StartupPublicationBudgetWithHugeMalformedIndex(t *testing.T) {
	requirePerformanceOptIn(t)

	_, indexPath := seedHugeMalformedTranscriptIndex(t)

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	elapsed := measureStartupPublication(t, cfg)
	t.Logf("startup publication with huge malformed index: %s", elapsed)
	if elapsed > startupPublicationBudget {
		t.Fatalf(
			"startup publication took %s with huge malformed transcripts.json at %s; budget is %s",
			elapsed, indexPath, startupPublicationBudget,
		)
	}
}

// shutdownJoinBudget is the wall-clock ceiling for a full shutdown: from the
// accepted shutdown request to Run returning with every owned loop joined.
// Loops select on ctx.Done at the top of each iteration, so the cost is the
// in-flight tick plus the connection drain — well inside a second on an
// unloaded machine.
const shutdownJoinBudget = time.Second

// TestPerformance_ShutdownJoinBudget measures how long the owned-loop join
// takes. That the join happens at all is asserted, without any time budget, by
// TestServer_GoroutinesDrainAfterShutdownRequest.
func TestPerformance_ShutdownJoinBudget(t *testing.T) {
	requirePerformanceOptIn(t)

	dir, err := os.MkdirTemp("/tmp", "amx-perf-shutdown-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("HOME", dir)

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runErr := startPublishedDaemon(t, ctx, cfg, runDependencies{})

	c := NewClientWithSocket(SocketPath())
	for i := 0; i < 5; i++ {
		if _, err := c.Send(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: i + 1, Action: "active"}); err != nil {
			t.Fatalf("state update %d: %v", i+1, err)
		}
	}

	requested := time.Now()
	if resp, err := c.Send(protocol.Request{Type: protocol.TypeShutdown}); err != nil {
		t.Fatalf("shutdown Send: %v", err)
	} else if !resp.OK {
		t.Fatalf("shutdown response: %+v", resp)
	}
	waitForRunReturn(t, runErr)
	elapsed := time.Since(requested)

	t.Logf("shutdown request to joined Run return: %s", elapsed)
	if elapsed > shutdownJoinBudget {
		t.Fatalf("shutdown join took %s; budget is %s", elapsed, shutdownJoinBudget)
	}
}
