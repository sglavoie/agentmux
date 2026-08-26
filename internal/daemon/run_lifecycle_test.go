package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// testLivenessGrace bounds how long a lifecycle test waits for a signal the
// daemon owns. It is a "this is wedged" detector, not a latency assertion:
// startup, shutdown, and join signals are all published by the daemon itself,
// so a healthy run reaches them immediately regardless of machine load and only
// a genuine hang consumes the grace. Latency budgets live in
// performance_test.go, where they are measured deliberately.
const testLivenessGrace = 30 * time.Second

// startPublishedDaemon runs the daemon and returns once it has completed its
// own startup publication — the health round-trip that proves the socket is
// bound, serving, and answered by this process. Waiting on that barrier instead
// of polling for the socket file removes the window in which the socket exists
// but the daemon is not yet answering, which is exactly the window contention
// widens.
//
// The returned channel carries run's result, so shutdown assertions join an
// owned completion signal rather than settling on a wall clock.
func startPublishedDaemon(
	t *testing.T,
	ctx context.Context,
	cfg *config.Config,
	deps runDependencies,
) chan error {
	t.Helper()

	published := make(chan struct{})
	inner := deps.afterStartupPublication
	deps.afterStartupPublication = func() {
		if inner != nil {
			inner()
		}
		close(published)
	}

	runErr := make(chan error, 1)
	go func() { runErr <- run(ctx, cfg, deps) }()

	select {
	case <-published:
	case err := <-runErr:
		t.Fatalf("Run returned before startup publication: %v", err)
	case <-time.After(testLivenessGrace):
		t.Fatal("Run did not complete startup publication")
	}
	return runErr
}

// waitForRunReturn joins the daemon's run goroutine. Returning from run means
// every loop it owns has been joined, so callers may assert on post-shutdown
// state without any settling delay.
func waitForRunReturn(t *testing.T, runErr <-chan error) {
	t.Helper()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(testLivenessGrace):
		t.Fatal("Run did not return after shutdown")
	}
}

// TestRun_RemovesSocketOnShutdown verifies daemon.Run's defer chain: when a
// shutdown request arrives, the daemon must remove the socket file before
// returning and release its single-instance lock so a follow-up Run() on the
// same path is not blocked by leftovers from the prior process. The defer
// chain is: server.Close() (waits for handlers via wg),
// os.Remove(socketPath), then releaseDaemonLock.
func TestRun_RemovesSocketOnShutdown(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-run-cleanup-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// Use a custom socket path inside our temp dir.
	t.Setenv("HOME", dir)

	socketPath := SocketPath()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Socket creation precedes Serve and the startup health confirmation. Wait
	// for the completed publication boundary before exercising shutdown.
	runErr := startPublishedDaemon(t, ctx, cfg, runDependencies{})
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("published socket missing: %v", err)
	}

	// Send shutdown request via real socket round-trip.
	c := NewClientWithSocket(socketPath)
	resp, err := c.Send(Request{Type: TypeShutdown})
	if err != nil {
		t.Fatalf("shutdown Send: %v", err)
	}
	if !resp.OK {
		t.Fatalf("shutdown response: %+v", resp)
	}

	waitForRunReturn(t, runErr)

	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket file should have been removed; stat err = %v", err)
	}
	if _, err := os.Stat(daemonLockPath(socketPath)); !os.IsNotExist(err) {
		t.Fatalf("lock file should have been removed; stat err = %v", err)
	}
}

func TestRun_ShutdownAcceptedDuringStartupReturnsCleanly(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-run-startup-shutdown-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("HOME", dir)

	startupConfirmationPaused := make(chan struct{})
	runErr := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		runErr <- run(ctx, &config.Config{}, runDependencies{
			beforeStartupConfirmation: func(startupCtx context.Context) {
				close(startupConfirmationPaused)
				<-startupCtx.Done()
			},
		})
	}()

	select {
	case <-startupConfirmationPaused:
	case err := <-runErr:
		t.Fatalf("Run returned before startup confirmation: %v", err)
	case <-time.After(testLivenessGrace):
		t.Fatal("Run did not reach startup confirmation")
	}

	resp, err := NewClientWithSocket(SocketPath()).Send(Request{Type: TypeShutdown})
	if err != nil {
		t.Fatalf("shutdown Send during startup: %v", err)
	}
	if !resp.OK {
		t.Fatalf("shutdown response during startup: %+v", resp)
	}

	waitForRunReturn(t, runErr)
	if _, err := os.Stat(SocketPath()); !os.IsNotExist(err) {
		t.Fatalf("socket file should have been removed; stat err = %v", err)
	}
}

func TestRun_TemporaryHomeCreatesNoDatabase(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "amx-no-database-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runErr := startPublishedDaemon(t, ctx, &config.Config{}, runDependencies{})

	dataDir := filepath.Join(home, ".local", "share", "agent-mux")
	for _, name := range []string{
		"agent-mux.db",
		"agent-mux.db-wal",
		"agent-mux.db-shm",
		"agent-mux.db.recovery.lock",
		"agent-mux.db.rebuild",
		"agent-mux.db.rebuild-startup.lock",
		"recovery",
	} {
		if _, err := os.Lstat(filepath.Join(dataDir, name)); !os.IsNotExist(err) {
			cancel()
			t.Fatalf("daemon startup created retired database artifact %s: %v", name, err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), testLivenessGrace)
	defer shutdownCancel()
	if err := NewClientWithSocket(SocketPath()).ShutdownAndWait(shutdownCtx); err != nil {
		cancel()
		t.Fatalf("ShutdownAndWait: %v", err)
	}
	waitForRunReturn(t, runErr)
}

func TestRun_DuplicateStartExitsWithoutTouchingLiveSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-run-duplicate-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	t.Setenv("HOME", dir)

	socketPath := SocketPath()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// The duplicate start must contend with a daemon that is already answering,
	// not merely one whose socket file exists: the publication barrier is what
	// makes "live" mean live.
	runErr := startPublishedDaemon(t, ctx, cfg, runDependencies{})

	before, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat live socket before duplicate start: %v", err)
	}

	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("duplicate Run returned error: %v", err)
	}

	after, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat live socket after duplicate start: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("duplicate Run replaced the live daemon socket")
	}

	c := NewClientWithSocket(socketPath)
	resp, err := c.Send(Request{Type: TypeHealth})
	if err != nil {
		t.Fatalf("live daemon not reachable after duplicate start: %v", err)
	}
	if !resp.OK || resp.PID == 0 {
		t.Fatalf("health response after duplicate start = %+v, want OK with PID", resp)
	}

	if resp, err := c.Send(Request{Type: TypeShutdown}); err != nil {
		t.Fatalf("shutdown Send: %v", err)
	} else if !resp.OK {
		t.Fatalf("shutdown response: %+v", resp)
	}

	waitForRunReturn(t, runErr)
}

// seedHugeMalformedTranscriptIndex points HOME at a fresh directory holding a
// megabyte-scale malformed transcripts.json, and empties PATH so the unrelated
// bounded Kitty snapshot fails immediately instead of spending its 400 ms
// production budget. Startup behaviour then depends on transcript I/O alone.
func seedHugeMalformedTranscriptIndex(t *testing.T) (home string, indexPath string) {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "amx-run-huge-index-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	t.Setenv("HOME", dir)
	t.Setenv("PATH", t.TempDir())

	indexPath = filepath.Join(dir, ".local", "share", "agent-mux", "transcripts.json")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(index dir): %v", err)
	}
	if err := os.WriteFile(indexPath, []byte(strings.Repeat("{malformed", 1024*1024)), 0o644); err != nil {
		t.Fatalf("WriteFile(huge malformed index): %v", err)
	}
	return dir, indexPath
}

// TestRun_BindsSocketWithHugeMalformedTranscriptIndex pins the correctness half
// of the huge-index regression: a corrupt megabyte-scale transcript cache must
// not stop the daemon from reaching startup publication at all. How *fast* it
// publishes is a latency question, and lives in
// TestPerformance_StartupPublicationBudgetWithHugeMalformedIndex.
func TestRun_BindsSocketWithHugeMalformedTranscriptIndex(t *testing.T) {
	_, indexPath := seedHugeMalformedTranscriptIndex(t)
	socketPath := SocketPath()

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	ctx, cancel := context.WithCancel(context.Background())
	runErr := startPublishedDaemon(t, ctx, cfg, runDependencies{})

	if _, err := os.Stat(socketPath); err != nil {
		cancel()
		t.Fatalf("published daemon socket with malformed index at %s: %v", indexPath, err)
	}
	cancel()
	waitForRunReturn(t, runErr)
}

func TestRun_StaleSocketIsNotRemovedWhenLockHeld(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-run-locked-stale-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	t.Setenv("HOME", dir)

	socketPath := SocketPath()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	const sentinel = "live socket placeholder"
	if err := os.WriteFile(socketPath, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("seed socket placeholder: %v", err)
	}

	lockFile, err := acquireDaemonLock(socketPath)
	if err != nil {
		t.Fatalf("acquire daemon lock: %v", err)
	}
	defer releaseDaemonLock(socketPath, lockFile)

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run with held lock returned error: %v", err)
	}

	got, err := os.ReadFile(socketPath)
	if err != nil {
		t.Fatalf("socket placeholder was removed under held lock: %v", err)
	}
	if string(got) != sentinel {
		t.Fatalf("socket placeholder = %q, want %q", string(got), sentinel)
	}
}

// TestRun_RemovesSocketOnCtxCancel mirrors the shutdown-request test but uses
// external context cancellation as the trigger. Both paths share the same
// cleanup defer, so both must leave the filesystem clean.
func TestRun_RemovesSocketOnCtxCancel(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-run-ctx-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	t.Setenv("HOME", dir)

	socketPath := SocketPath()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	ctx, cancel := context.WithCancel(context.Background())
	runErr := startPublishedDaemon(t, ctx, cfg, runDependencies{})

	cancel()
	waitForRunReturn(t, runErr)

	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket file should have been removed after ctx cancel; stat err = %v", err)
	}
}
