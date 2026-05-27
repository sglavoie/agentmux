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

	t.Setenv("HOME", dir)

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
