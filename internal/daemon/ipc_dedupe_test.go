package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// newTestServerWithRef starts a real Server over a Unix socket and returns both
// the socket path and the *Server so a test can inspect Metrics after a round
// trip. Mirrors startTestServer but exposes the server handle.
func newTestServerWithRef(t *testing.T) (string, *Server) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "amx-dedupe-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	socketPath := filepath.Join(dir, "d.sock")

	guard := NewStateGuard(time.Hour)
	notifier := notification.NewNotifier(&config.NotificationConfig{})
	notifier.SetBackend(func(_ context.Context, _ int, _, _, _ string) (bool, error) {
		return false, nil
	})
	handler := NewHandler(guard, notifier)

	server, err := NewServer(socketPath, handler)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = os.RemoveAll(dir)
	})
	return socketPath, server
}

// windowEventCount returns how many events the daemon recorded for windowID.
func windowEventCount(t *testing.T, c *Client, windowID int) int {
	t.Helper()
	resp, err := c.Send(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: windowID})
	if err != nil {
		t.Fatalf("session detail: %v", err)
	}
	return len(resp.Events)
}

// TestIPC_DedupeAppliesEventOnce is the session completion contract: a logical
// event carried by one request ID is applied exactly once across concurrent
// duplicates and a later ambiguous-timeout retry.
func TestIPC_DedupeAppliesEventOnce(t *testing.T) {
	sock, server := newTestServerWithRef(t)
	c := NewClientWithSocket(sock)

	const windowID = 5
	req := protocol.Request{
		Type:      protocol.TypeStateUpdate,
		RequestID: "logical-event-1",
		WindowID:  windowID,
		Action:    "active",
		Event:     "PreToolUse",
	}

	// Concurrent duplicates: the daemon must apply the state update once.
	const callers = 12
	var wg sync.WaitGroup
	resps := make([]protocol.Response, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resps[idx], errs[idx] = c.Send(req)
		}(i)
	}
	wg.Wait()

	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d send error: %v", i, errs[i])
		}
		if !resps[i].OK || resps[i].IconState != "bolt" {
			t.Fatalf("caller %d response = %+v; want OK/bolt", i, resps[i])
		}
	}

	// A later retry with the same request ID (the ambiguous-timeout resend)
	// must replay the response, not re-apply the event.
	retry, err := c.Send(req)
	if err != nil {
		t.Fatalf("retry send: %v", err)
	}
	if !retry.OK || retry.IconState != "bolt" {
		t.Fatalf("retry response = %+v; want OK/bolt", retry)
	}

	// Snapshot metrics before the event-count query below, which is itself a
	// legacy (no-ID) request that would bump Accepted.
	snap := server.Metrics()
	if snap.Accepted != 1 {
		t.Errorf("Accepted = %d; want 1", snap.Accepted)
	}
	if snap.Duplicate != callers { // callers-1 concurrent duplicates + 1 retry
		t.Errorf("Duplicate = %d; want %d", snap.Duplicate, callers)
	}

	if got := windowEventCount(t, c, windowID); got != 1 {
		t.Fatalf("window recorded %d events; want exactly 1 (applied-once)", got)
	}
}

// TestIPC_DistinctRequestIDsEachApply confirms deduplication is scoped to the
// request ID: a new logical event with a fresh ID applies again, and a legacy
// (no-ID) request always applies.
func TestIPC_DistinctRequestIDsEachApply(t *testing.T) {
	sock, server := newTestServerWithRef(t)
	c := NewClientWithSocket(sock)

	const windowID = 6
	base := protocol.Request{Type: protocol.TypeStateUpdate, WindowID: windowID, Action: "active", Event: "PreToolUse"}

	first := base
	first.RequestID = "evt-1"
	if _, err := c.Send(first); err != nil {
		t.Fatalf("first: %v", err)
	}

	second := base
	second.RequestID = "evt-2"
	if _, err := c.Send(second); err != nil {
		t.Fatalf("second: %v", err)
	}

	// Legacy request: no ID, never deduplicated.
	if _, err := c.Send(base); err != nil {
		t.Fatalf("legacy: %v", err)
	}

	// Snapshot before the event-count query, which is a legacy request.
	snap := server.Metrics()
	if snap.Accepted != 3 {
		t.Errorf("Accepted = %d; want 3", snap.Accepted)
	}
	if snap.Duplicate != 0 {
		t.Errorf("Duplicate = %d; want 0", snap.Duplicate)
	}

	if got := windowEventCount(t, c, windowID); got != 3 {
		t.Fatalf("window recorded %d events; want 3 (two distinct IDs + one legacy)", got)
	}
}

// TestIPC_RejectedMetricCountsBadPayload confirms malformed input increments the
// rejected metric rather than any dedupe bucket.
func TestIPC_RejectedMetricCountsBadPayload(t *testing.T) {
	sock, server := newTestServerWithRef(t)

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("{not json}\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 256)
	_, _ = conn.Read(buf)

	// Give the handler goroutine a beat to record the metric after writing.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if server.Metrics().Rejected >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if snap := server.Metrics(); snap.Rejected != 1 {
		t.Fatalf("Rejected = %d; want 1", snap.Rejected)
	}
}
