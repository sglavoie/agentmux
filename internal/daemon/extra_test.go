package daemon

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestHandler_Health verifies that TypeHealth returns the daemon's PID and a
// non-negative uptime. The handler initializes startedAt in NewHandler, so even
// the very first health probe must report uptime >= 0 (never negative).
func TestHandler_Health(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: TypeHealth})
	if !resp.OK {
		t.Fatalf("want ok=true, got %+v", resp)
	}
	if resp.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", resp.PID, os.Getpid())
	}
	if resp.UptimeSeconds < 0 {
		t.Errorf("UptimeSeconds = %d, want >= 0", resp.UptimeSeconds)
	}
}

// TestActionFromIconState exhaustively pins the icon -> action recovery map.
// The handler relies on this when the daemon restarts mid-flow: lastAction is
// empty, so we fall back to the icon currently displayed in the tab title.
// Drift between this map and palette icons would silently break post-restart
// transitions (e.g. idle would no longer clear a stale bolt).
func TestActionFromIconState(t *testing.T) {
	cases := []struct {
		iconState string
		want      string
	}{
		{"bolt", "active"},
		{"compact", "compact"},
		{"subagent", "subagent"},
		{"stop", "stop"},
		{"key", "permission"},
		{"warning", "failure"},
		{"error", "stop_failure"},
		{"question", "elicitation"},
		{"", ""},
		{"unknown", ""},
	}
	for _, tc := range cases {
		if got := actionFromIconState(tc.iconState); got != tc.want {
			t.Errorf("actionFromIconState(%q) = %q, want %q", tc.iconState, got, tc.want)
		}
	}
}

// TestRun_RemovesSocketOnShutdown verifies daemon.Run's defer chain: when a
// shutdown request arrives, the daemon must remove the socket file before
// returning so a follow-up Run() on the same path is not blocked by a leftover
// socket from the prior process. The defer chain is: server.Close() (waits
// for handlers via wg) then os.Remove(socketPath).
func TestRun_RemovesSocketOnShutdown(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-run-cleanup-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// Use a custom socket path inside our temp dir.
	prevHome := os.Getenv("HOME")
	t.Setenv("HOME", dir)
	t.Cleanup(func() { _ = os.Setenv("HOME", prevHome) })

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

	prevHome := os.Getenv("HOME")
	t.Setenv("HOME", dir)
	t.Cleanup(func() { _ = os.Setenv("HOME", prevHome) })

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

// TestHandler_ConcurrentSubagentRefCounting drives many overlapping
// SubagentStart/SubagentStop requests for the same window under -race to lock
// in two properties of the reference-counted subagent gate:
//
//  1. The map mutex (stateMu) covers every read-modify-write of subagentCount.
//     Without coverage, -race would flag concurrent ++/-- on the same key.
//  2. The counter balances out to zero once every Start has a matching Stop,
//     regardless of interleaving. Off-by-one bugs (e.g. forgetting to delete
//     the zero entry, or skipping the decrement when wasTracked=false) would
//     leak the key into subagentCount.
//
// Real-world Claude sessions are serial per window, so a race here would
// indicate a structural locking bug, not a behavioural regression.
func TestHandler_ConcurrentSubagentRefCounting(t *testing.T) {
	h := newTestHandler()

	const goroutines = 16
	const cyclesPerGoroutine = 20

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < cyclesPerGoroutine; j++ {
				h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
				h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
			}
		}()
	}
	wg.Wait()

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if got := h.subagentCount[1]; got != 0 {
		t.Fatalf("subagentCount[1] = %d after balanced Start/Stop cycles, want 0", got)
	}
	if _, ok := h.subagentCount[1]; ok {
		t.Fatalf("subagentCount map should not retain zero-valued entries after final Stop")
	}
}

// TestHandler_ConcurrentStateUpdatesDifferentWindows pins that the handler's
// per-window state maps (activity, lastAction, lastActiveEvent, subagentCount)
// are safe under concurrent updates targeting *different* windows. Each window
// owns its own map slot under a single shared stateMu — the race detector
// catches any unprotected read-modify-write of the shared map header.
func TestHandler_ConcurrentStateUpdatesDifferentWindows(t *testing.T) {
	h := newTestHandler()

	const windows = 32
	const opsPerWindow = 25
	actions := []string{"active", "permission", "stop", "failure", "idle", "subagent"}

	var wg sync.WaitGroup
	for w := 1; w <= windows; w++ {
		wg.Add(1)
		windowID := w
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerWindow; i++ {
				action := actions[i%len(actions)]
				h.Handle(Request{Type: TypeStateUpdate, WindowID: windowID, Action: action})
			}
		}()
	}
	wg.Wait()

	// Verify state_query is consistent and includes (at most) one entry per window.
	resp := h.Handle(Request{Type: TypeStateQuery})
	if !resp.OK {
		t.Fatalf("state_query: %+v", resp)
	}
	if len(resp.Activity) > windows {
		t.Fatalf("Activity contains %d windows; want at most %d", len(resp.Activity), windows)
	}
}

// TestServer_GoroutinesDrainAfterShutdownRequest verifies that a daemon brought
// up via Run and shut down via TypeShutdown leaves no lingering goroutines.
// The combined property under test: handler goroutine, server watchdog,
// notifier timer/dispatch goroutines all exit cleanly when ctx is cancelled
// after the shutdown handler runs.
func TestServer_GoroutinesDrainAfterShutdownRequest(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-drain-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	prevHome := os.Getenv("HOME")
	t.Setenv("HOME", dir)
	t.Cleanup(func() { _ = os.Setenv("HOME", prevHome) })

	socketPath := SocketPath()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	baseline := runtime.NumGoroutine()

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	runErr := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() { runErr <- Run(ctx, cfg) }()

	// Wait for socket.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c := NewClientWithSocket(socketPath)
	for i := 0; i < 5; i++ {
		_, _ = c.Send(Request{Type: TypeStateUpdate, WindowID: i + 1, Action: "active"})
	}
	_, _ = c.Send(Request{Type: TypeShutdown})

	select {
	case <-runErr:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after shutdown")
	}

	// Give any straggler goroutines a moment to fully wind down.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutine count did not return to baseline: baseline=%d, final=%d", baseline, runtime.NumGoroutine())
}
