package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
)

func waitForTestSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket not created: %v", os.ErrNotExist)
}

// TestRun_RemovesSocketOnShutdown verifies daemon.Run's defer chain: when a
// shutdown request arrives, the daemon must remove the socket file before
// returning and release its single-instance lock so a follow-up Run() on the
// same path is not blocked by leftovers from the prior process. The defer
// chain is: server.Close() (waits for handlers via wg), activity Writer.Close()
// (flushes and closes its datastore), os.Remove(socketPath), then
// releaseDaemonLock.
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

	runErr := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	published := make(chan struct{})

	go func() {
		runErr <- run(ctx, cfg, runDependencies{
			afterStartupPublication: func() { close(published) },
			openActivityStore:       openDaemonActivityStore,
		})
	}()

	// Socket creation precedes Serve and the startup health confirmation. Wait
	// for the completed publication boundary before exercising shutdown.
	select {
	case <-published:
	case err := <-runErr:
		t.Fatalf("Run returned before startup publication: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not complete startup publication")
	}
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

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after shutdown")
	}

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
			openActivityStore: openDaemonActivityStore,
		})
	}()

	select {
	case <-startupConfirmationPaused:
	case err := <-runErr:
		t.Fatalf("Run returned before startup confirmation: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not reach startup confirmation")
	}

	resp, err := NewClientWithSocket(SocketPath()).Send(Request{Type: TypeShutdown})
	if err != nil {
		t.Fatalf("shutdown Send during startup: %v", err)
	}
	if !resp.OK {
		t.Fatalf("shutdown response during startup: %+v", resp)
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error after accepting startup shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after accepting startup shutdown")
	}
	if _, err := os.Stat(SocketPath()); !os.IsNotExist(err) {
		t.Fatalf("socket file should have been removed; stat err = %v", err)
	}
}

func TestRun_ShutdownAcceptedDuringStartupWithActivityRetentionReturnsPromptly(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-run-startup-activity-shutdown-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("HOME", dir)

	startupConfirmationPaused := make(chan struct{})
	activityOpenStarted := make(chan struct{})
	activityOpenReturned := make(chan struct{})
	runErr := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := &config.Config{}
	cfg.Defaults.Activity.KeepDays = 30
	go func() {
		runErr <- run(ctx, cfg, runDependencies{
			beforeStartupConfirmation: func(startupCtx context.Context) {
				close(startupConfirmationPaused)
				<-startupCtx.Done()
			},
			openActivityStore: func(openCtx context.Context) (*datastore.Store, error) {
				close(activityOpenStarted)
				store, openErr := openDaemonActivityStore(openCtx)
				close(activityOpenReturned)
				return store, openErr
			},
		})
	}()

	select {
	case <-startupConfirmationPaused:
	case err := <-runErr:
		t.Fatalf("Run returned before startup confirmation: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not reach startup confirmation")
	}
	select {
	case <-activityOpenStarted:
	case err := <-runErr:
		t.Fatalf("Run returned before the activity retention sweep opened its store: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("activity retention sweep did not attempt to open its store")
	}
	select {
	case <-activityOpenReturned:
		t.Fatal("activity retention sweep crossed the startup lease before publication")
	default:
	}

	resp, err := NewClientWithSocket(SocketPath()).Send(Request{Type: TypeShutdown})
	if err != nil {
		t.Fatalf("shutdown Send during startup: %v", err)
	}
	if !resp.OK {
		t.Fatalf("shutdown response during startup: %+v", resp)
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error after accepting startup shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run waited for the activity writer datastore-open timeout after shutdown")
	}
	select {
	case <-activityOpenReturned:
	default:
		t.Fatal("Run returned before the activity opener completed")
	}
	if _, err := os.Stat(SocketPath()); !os.IsNotExist(err) {
		t.Fatalf("socket file should have been removed; stat err = %v", err)
	}
}

func TestCloseDaemonResourcesReleasesStartupBeforeClosingActivityStore(t *testing.T) {
	steps := make([]string, 0, 4)
	if err := closeDaemonResources(
		func() { steps = append(steps, "server-drained") },
		func() error {
			steps = append(steps, "startup-released")
			return nil
		},
		// activity.Writer.Close does not return until its worker defer has
		// closed the owned datastore; this callback represents that boundary.
		func() { steps = append(steps, "activity-store-closed") },
		func() error {
			steps = append(steps, "socket-removed")
			return nil
		},
	); err != nil {
		t.Fatalf("closeDaemonResources: %v", err)
	}
	if got, want := strings.Join(steps, ","), "server-drained,startup-released,activity-store-closed,socket-removed"; got != want {
		t.Fatalf("teardown order = %s, want %s", got, want)
	}
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

	runErr := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { runErr <- Run(ctx, cfg) }()

	waitForTestSocket(t, socketPath)
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

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("original Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("original Run did not return after shutdown")
	}
}

func TestRunRefusesHookAutoStartDuringDatastoreRebuild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	gate, err := datastore.BeginDefaultRebuildGate()
	if err != nil {
		t.Fatalf("begin rebuild gate: %v", err)
	}
	t.Cleanup(func() { _ = gate.Release() })

	err = Run(t.Context(), &config.Config{})
	if !errors.Is(err, datastore.ErrRebuildInProgress) {
		t.Fatalf("Run error = %v, want ErrRebuildInProgress", err)
	}
	if _, statErr := os.Stat(SocketPath()); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("blocked daemon published socket: %v", statErr)
	}
}

func TestRunDoesNotCrossRebuildGatePublishedDuringStartup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	startupPaused := make(chan struct{})
	resumeStartup := make(chan struct{})
	var datastoreOpens atomic.Int32
	runErr := make(chan error, 1)
	go func() {
		runErr <- run(t.Context(), &config.Config{}, runDependencies{
			beforeDatastoreStartup: func() {
				close(startupPaused)
				<-resumeStartup
			},
			openActivityStore: func(ctx context.Context) (*datastore.Store, error) {
				datastoreOpens.Add(1)
				return datastore.Open(ctx)
			},
		})
	}()

	select {
	case <-startupPaused:
	case <-time.After(3 * time.Second):
		close(resumeStartup)
		t.Fatal("daemon did not reach the pre-gate startup pause")
	}

	gate, err := datastore.BeginDefaultRebuildGate()
	if err != nil {
		close(resumeStartup)
		t.Fatalf("publish rebuild gate while daemon startup is paused: %v", err)
	}
	t.Cleanup(func() { _ = gate.Release() })
	close(resumeStartup)

	select {
	case err := <-runErr:
		if !errors.Is(err, datastore.ErrRebuildInProgress) {
			t.Fatalf("Run error = %v, want ErrRebuildInProgress", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon startup did not stop at the published rebuild gate")
	}

	if _, err := os.Lstat(SocketPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blocked daemon published socket: %v", err)
	}
	if got := datastoreOpens.Load(); got != 0 {
		t.Fatalf("blocked daemon opened SQLite %d times, want 0", got)
	}
}

func TestOpenDaemonActivityStoreRefusesActiveRebuildGate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	gate, err := datastore.BeginDefaultRebuildGate()
	if err != nil {
		t.Fatalf("begin rebuild gate: %v", err)
	}
	t.Cleanup(func() { _ = gate.Release() })

	store, err := openDaemonActivityStore(t.Context())
	if store != nil {
		_ = store.Close()
		t.Fatal("activity writer opened SQLite while rebuild gate was active")
	}
	if !errors.Is(err, datastore.ErrRebuildInProgress) {
		t.Fatalf("activity store open error = %v, want ErrRebuildInProgress", err)
	}
	databasePath, err := datastore.DefaultPath()
	if err != nil {
		t.Fatalf("resolve datastore path: %v", err)
	}
	if _, err := os.Lstat(databasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blocked activity writer created datastore: %v", err)
	}
}

func TestRun_BindsSocketWithHugeMalformedTranscriptIndex(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-run-huge-index-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	t.Setenv("HOME", dir)

	socketPath := SocketPath()
	indexPath := filepath.Join(dir, ".local", "share", "agent-mux", "transcripts.json")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(index dir): %v", err)
	}
	if err := os.WriteFile(indexPath, []byte(strings.Repeat("{malformed", 1024*1024)), 0o644); err != nil {
		t.Fatalf("WriteFile(huge malformed index): %v", err)
	}

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	published := make(chan struct{})
	go func() {
		runErr <- run(ctx, cfg, runDependencies{
			afterStartupPublication: func() { close(published) },
			openActivityStore:       openDaemonActivityStore,
		})
	}()

	select {
	case <-published:
		if _, err := os.Stat(socketPath); err != nil {
			cancel()
			t.Fatalf("published daemon socket: %v", err)
		}
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Run did not return after cancel")
		}
	case <-time.After(500 * time.Millisecond):
		cancel()
		t.Fatalf("daemon not published promptly with huge malformed transcripts.json at %s", indexPath)
	}
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

	runErr := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())

	go func() { runErr <- Run(ctx, cfg) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()

	select {
	case <-runErr:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}

	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket file should have been removed after ctx cancel; stat err = %v", err)
	}
}
