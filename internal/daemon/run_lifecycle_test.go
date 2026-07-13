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

	go func() { runErr <- Run(ctx, cfg) }()

	// Wait for the socket to appear.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("socket not created: %v", err)
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

func TestCloseDaemonResourcesClosesActivityStoreBeforeSocketRemoval(t *testing.T) {
	steps := make([]string, 0, 3)
	closeDaemonResources(
		func() { steps = append(steps, "server-drained") },
		// activity.Writer.Close does not return until its worker defer has
		// closed the owned datastore; this callback represents that boundary.
		func() { steps = append(steps, "activity-store-closed") },
		func() error {
			steps = append(steps, "socket-removed")
			return nil
		},
	)
	if got, want := strings.Join(steps, ","), "server-drained,activity-store-closed,socket-removed"; got != want {
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
	go func() { runErr <- Run(ctx, cfg) }()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			cancel()
			select {
			case err := <-runErr:
				if err != nil {
					t.Fatalf("Run returned error: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Run did not return after cancel")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatalf("socket not created promptly with huge malformed transcripts.json at %s", indexPath)
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
