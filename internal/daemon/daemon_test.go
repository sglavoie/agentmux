package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// runWithSocket starts a daemon on an isolated socket path for testing.
// Returns the socket path and a cancel function.
func runWithSocket(t *testing.T, idle time.Duration) (socketPath string, cancel context.CancelFunc) {
	t.Helper()
	// Use /tmp directly to avoid macOS sun_path 104-byte limit on long t.TempDir() paths.
	dir, err := os.MkdirTemp("/tmp", "amx-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath = filepath.Join(dir, "d.sock")

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	guard := NewStateGuard(time.Duration(cfg.Defaults.GuardDuration) * time.Second)
	notifier := NewNotifier(&cfg.Defaults.Notifications)
	notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
	handler := NewHandler(guard, notifier)

	ctx, cancelFn := context.WithCancel(context.Background())

	idleTimer := time.NewTimer(idle)
	activityCh := make(chan struct{}, 1)
	handler.onActivity = func() {
		select {
		case activityCh <- struct{}{}:
		default:
		}
	}
	handler.onShutdown = cancelFn

	server, err := NewServer(socketPath, handler)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ctx) }()

	go func() {
		for {
			select {
			case <-activityCh:
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(idle)
			case <-idleTimer.C:
				cancelFn()
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	t.Cleanup(func() {
		cancelFn()
		server.Close()
		_ = os.Remove(socketPath)
	})

	return socketPath, cancelFn
}

func TestDaemon_IdleShutdown(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-idle-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	socketPath := filepath.Join(dir, "d.sock")

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	guard := NewStateGuard(time.Second)
	notifier := NewNotifier(&cfg.Defaults.Notifications)
	notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
	handler := NewHandler(guard, notifier)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idleTimer := time.NewTimer(150 * time.Millisecond)
	handler.onShutdown = cancel

	server, err := NewServer(socketPath, handler)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ctx) }()

	idleDone := make(chan struct{})
	go func() {
		select {
		case <-idleTimer.C:
			cancel()
		case <-ctx.Done():
		}
		close(idleDone)
	}()

	select {
	case <-idleDone:
		// Idle shutdown fired as expected.
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not reach idle timeout")
	}
}

func TestDaemon_ActivityResetsIdleTimer(t *testing.T) {
	socketPath, cancel := runWithSocket(t, 200*time.Millisecond)
	defer cancel()

	// Wait for socket to be ready.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c := NewClientWithSocket(socketPath)

	// Verify daemon is up.
	if _, err := c.Send(Request{Type: TypeStateQuery}); err != nil {
		t.Fatalf("daemon not reachable: %v", err)
	}

	// Send activity at 50ms and 150ms — each resets the 200ms idle clock.
	time.Sleep(50 * time.Millisecond)
	if _, err := c.Send(Request{Type: TypeStateQuery}); err != nil {
		t.Logf("activity send failed (may have just shut down): %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := c.Send(Request{Type: TypeStateQuery}); err != nil {
		t.Logf("activity send failed (may have just shut down): %v", err)
	}

	// After the last activity at 150ms from test start, the daemon should stay alive
	// for another 200ms, then shut down. Cancel it cleanly.
	time.Sleep(50 * time.Millisecond)

	// Daemon should still respond around t=200ms (200ms after last reset is at t=350ms).
	if _, err := c.Send(Request{Type: TypeStateQuery}); err != nil {
		t.Errorf("daemon should still be alive after activity resets: %v", err)
	}
}
