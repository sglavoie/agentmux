package daemon

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestHandler_ConcurrentSubagentRefCounting drives many overlapping
// SubagentStart/SubagentStop requests for the same window under -race to lock
// in two properties of the reference-counted subagent gate:
//
//  1. The state mutex (stateMu) covers every read-modify-write of SubagentCount.
//     Without coverage, -race would flag concurrent ++/-- on the same key.
//  2. The counter balances out to zero once every Start has a matching Stop,
//     regardless of interleaving. Off-by-one bugs (e.g. skipping the decrement
//     when wasTracked=false) would leak a positive SubagentCount.
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
				h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
				h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
			}
		}()
	}
	wg.Wait()

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if got := h.store[1].SubagentCount; got != 0 {
		t.Fatalf("subagentCount[1] = %d after balanced Start/Stop cycles, want 0", got)
	}
}

// TestHandler_ConcurrentStateUpdatesDifferentWindows pins that the handler's
// per-window store is safe under concurrent updates targeting *different*
// windows. Each window owns its own state entry under a single shared stateMu —
// the race detector catches any unprotected read-modify-write of the shared map
// header.
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
				h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: windowID, Action: action})
			}
		}()
	}
	wg.Wait()

	// Verify state_query is consistent and includes (at most) one entry per window.
	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	if !resp.OK {
		t.Fatalf("state_query: %+v", resp)
	}
	if len(resp.Activity) > windows {
		t.Fatalf("Activity contains %d windows; want at most %d", len(resp.Activity), windows)
	}
}

// daemonOwnedGoroutines lists the goroutine entry points run owns for the
// lifetime of a daemon: its five loops, the serve goroutine and the connection
// handlers it spawns, and the diagnostic journal maintenance loop. Naming them
// is what makes the drain assertion diagnostic — a failure says which loop
// outlived Run instead of reporting an arithmetic drift in a global count.
var daemonOwnedGoroutines = []string{
	"daemon.(*Handler).runMaintenance",
	"daemon.(*Handler).runWatchdog",
	"daemon.(*Handler).runHarnessProcessLifecycle",
	"daemon.(*Handler).runRenderWorker",
	"daemon.(*Handler).runScreenProbes",
	"daemon.(*Server).Serve",
	"daemon.(*Server).handleConn",
	"daemon.(*lifecycleRecorder).runJournalMaintenance",
}

// assertNoDaemonGoroutines fails with the offending stacks if any goroutine
// owned by run is still alive. It is called only after run has returned, which
// — because run joins every loop it starts before returning — makes the check
// a statement about ownership rather than a race against the scheduler.
func assertNoDaemonGoroutines(t *testing.T) {
	t.Helper()

	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}

	var leaked []string
	for _, stack := range strings.Split(string(buf), "\n\n") {
		for _, owned := range daemonOwnedGoroutines {
			if strings.Contains(stack, owned) {
				leaked = append(leaked, stack)
				break
			}
		}
	}
	if len(leaked) > 0 {
		t.Fatalf("daemon-owned goroutines outlived Run:\n\n%s", strings.Join(leaked, "\n\n"))
	}
}

// TestServer_GoroutinesDrainAfterShutdownRequest verifies that a daemon brought
// up via Run and shut down via TypeShutdown leaves no lingering goroutines. The
// combined property under test: the five handler loops, the serve goroutine and
// its connection handlers, and the journal maintenance loop all exit before Run
// returns.
//
// Both boundaries are signals the daemon owns. Startup waits on the publication
// barrier rather than on the socket file appearing, and the drain assertion runs
// the instant Run returns — Run joins its loop group, so there is nothing left
// to settle for and no wall-clock window for contention to widen.
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

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runErr := startPublishedDaemon(t, ctx, cfg, runDependencies{})

	c := NewClientWithSocket(socketPath)
	for i := 0; i < 5; i++ {
		if _, err := c.Send(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: i + 1, Action: "active"}); err != nil {
			t.Fatalf("state update %d: %v", i+1, err)
		}
	}
	if resp, err := c.Send(protocol.Request{Type: protocol.TypeShutdown}); err != nil {
		t.Fatalf("shutdown Send: %v", err)
	} else if !resp.OK {
		t.Fatalf("shutdown response: %+v", resp)
	}

	waitForRunReturn(t, runErr)
	assertNoDaemonGoroutines(t)
}
