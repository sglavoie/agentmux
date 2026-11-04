package ipc

// The dashboard state-query round trip and its dedicated deadline.

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// serveOneResponse accepts a single connection on a fresh socket, reads the
// request line, and writes raw as the response. It returns the socket path and
// a channel carrying the request the client actually sent.
func serveOneResponse(t *testing.T, raw []byte) (socketPath string, requests <-chan protocol.Request) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "amx-state-query-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath = filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	seen := make(chan protocol.Request, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		line, readErr := bufio.NewReader(conn).ReadBytes('\n')
		if readErr != nil {
			return
		}
		var req protocol.Request
		_ = json.Unmarshal(line, &req)
		seen <- req
		_, _ = conn.Write(append(raw, '\n'))
	}()
	return socketPath, seen
}

func mustEncodeResponse(t *testing.T, resp protocol.Response) []byte {
	t.Helper()
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("encoding response: %v", err)
	}
	return data
}

// TestStateQueryOwnsADedicatedOneSecondDeadline pins the new budget and, just
// as importantly, that adding it moved none of the existing ones. The hook
// acknowledgement path and the cold-start path are latency contracts with
// harnesses, not internal tuning knobs.
func TestStateQueryOwnsADedicatedOneSecondDeadline(t *testing.T) {
	if stateQueryDeadline != time.Second {
		t.Fatalf("stateQueryDeadline = %v; want one second", stateQueryDeadline)
	}
	if sendDeadline != NormalAckBudget || NormalAckBudget != 250*time.Millisecond {
		t.Fatalf("send deadline = %v (NormalAckBudget %v); want the unchanged 250ms acknowledgement budget",
			sendDeadline, NormalAckBudget)
	}
	if autoStartDeadline != coldStartBudget || coldStartBudget != 2*time.Second {
		t.Fatalf("auto-start deadline = %v; want the unchanged two-second cold-start budget", autoStartDeadline)
	}
	if exportDeadline != 5*time.Second {
		t.Fatalf("export deadline = %v; want the unchanged five-second bulk-export budget", exportDeadline)
	}
	if restartSocketPollCount != 20 || restartSocketPollDelay != 50*time.Millisecond {
		t.Fatalf("shutdown teardown budget = %d x %v; want the unchanged one-second wait",
			restartSocketPollCount, restartSocketPollDelay)
	}
	if maxResponseBytes != 64<<20 {
		t.Fatalf("maxResponseBytes = %d; want the retained 64MiB response ceiling", maxResponseBytes)
	}
}

// TestStateQueryTimesOutOnASilentDaemon proves the budget is enforced rather
// than merely declared: a daemon that accepts the connection and never answers
// must release the caller near one second — long enough that a state query is
// not held to the hook budget, short enough that a dashboard refresh can fall
// back instead of hanging.
func TestStateQueryTimesOutOnASilentDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-state-query-silent-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- conn // held open and never written to
	}()

	start := time.Now()
	_, err = NewClientWithSocket(socketPath).StateQuery()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("StateQuery succeeded against a daemon that never responded")
	}
	select {
	case conn := <-accepted:
		_ = conn.Close()
	default:
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("StateQuery gave up after %v; want it to wait out its one-second budget", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("StateQuery waited %v; want it bounded near one second", elapsed)
	}
}

// TestStateQueryCarriesRuntimeState checks the dashboard's state-query wire
// shape for a runtime session rather than a terminal-specific window tree.
func TestStateQueryCarriesRuntimeState(t *testing.T) {
	raw := mustEncodeResponse(t, protocol.Response{
		OK:           true,
		States:       map[int]protocol.WindowStateSnapshot{42: {IconState: "bolt", LastAction: "active"}},
		RuntimeSlots: map[int]protocol.RuntimeSlot{42: {RuntimeSessionID: "run-42", Backend: "tmux"}},
	})
	socketPath, requests := serveOneResponse(t, raw)
	resp, err := NewClientWithSocket(socketPath).StateQuery()
	if err != nil {
		t.Fatalf("StateQuery: %v", err)
	}
	if req := <-requests; req.Type != protocol.TypeStateQuery {
		t.Fatalf("daemon received request type %q; want %q", req.Type, protocol.TypeStateQuery)
	}
	if resp.States[42].IconState != "bolt" || resp.RuntimeSlots[42].RuntimeSessionID != "run-42" {
		t.Fatalf("runtime state lost on the wire: %+v", resp)
	}
}

// TestStateQuerySurfacesDaemonRefusal pins that a rejected query is an error
// rather than a zero-valued success, so a caller cannot mistake a refusal for a
// daemon with no windows.
func TestStateQuerySurfacesDaemonRefusal(t *testing.T) {
	raw := mustEncodeResponse(t, protocol.Response{OK: false, Error: "unknown request type: state_query"})
	socketPath, _ := serveOneResponse(t, raw)
	if _, err := NewClientWithSocket(socketPath).StateQuery(); err == nil {
		t.Fatal("StateQuery returned no error for a refused request")
	}
}
