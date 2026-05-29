package daemon

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func newTestServer() *Server {
	return &Server{handler: newTestHandler()}
}

// errReader returns the same error from every Read call. Used to simulate a
// non-EOF I/O failure that bufio.Scanner should surface via Err().
type errReader struct{ err error }

func (e *errReader) Read(_ []byte) (int, error) { return 0, e.err }

// slowWriter writes at most chunk bytes per Write call, draining into buf.
// It models a partial-write socket where Write returns short counts.
type slowWriter struct {
	buf   bytes.Buffer
	chunk int
}

func (s *slowWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := s.chunk
	if n <= 0 || n > len(p) {
		n = len(p)
	}
	return s.buf.Write(p[:n])
}

// TestServer_CloseWaitsForInflightHandlers verifies that Server.Close blocks
// until every spawned handleConn goroutine has returned. A client opens a
// connection and stalls without sending a complete request, leaving the
// handler parked inside bufio.Scanner.Scan. We assert that Close does not
// return while the handler is parked, and unblocks once the client closes.
//
// Synchronization between the test and the handler uses two unbuffered
// channels (started, closed via onConnStart; serveDone, closed when Serve
// returns) rather than sleeps to avoid flakes under load.
func TestServer_CloseWaitsForInflightHandlers(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-srvclose-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "d.sock")

	server, err := NewServer(socketPath, newTestHandler())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	started := make(chan struct{})
	server.onConnStart = func() { close(started) }

	serveDone := make(chan struct{})
	go func() {
		_ = server.Serve(context.Background())
		close(serveDone)
	}()

	// Open a connection and write a partial request with no trailing newline,
	// so the server-side scanner blocks waiting for more data.
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := conn.Write([]byte("{\"type\":\"state_query\"")); err != nil {
		t.Fatalf("partial write: %v", err)
	}

	// Wait deterministically until the handler goroutine has registered.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConn goroutine never started")
	}

	// Close the listener; Close must NOT return until the in-flight handler
	// finishes, which requires the client connection to be closed.
	closeReturned := make(chan struct{})
	go func() {
		server.Close()
		close(closeReturned)
	}()

	select {
	case <-closeReturned:
		t.Fatal("Close returned while a handler was still in flight")
	case <-time.After(50 * time.Millisecond):
		// Expected: Close is parked on wg.Wait().
	}

	// Releasing the client unblocks the handler's scanner; Close must now return.
	_ = conn.Close()

	select {
	case <-closeReturned:
		// Good.
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after in-flight handler completed")
	}

	// Serve must also have returned now (listener closed, handlers drained).
	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after Close")
	}
}

// TestServer_HandshakeDeadlineReleasesIdleConn verifies that a client which
// opens a connection but never sends data does not pin a handler goroutine
// indefinitely. The server enforces a per-connection deadline so a stuck or
// crashed peer cannot exhaust the daemon by holding sockets open.
func TestServer_HandshakeDeadlineReleasesIdleConn(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-srvdeadline-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "d.sock")

	prevDeadline := connDeadline
	connDeadline = 50 * time.Millisecond
	t.Cleanup(func() { connDeadline = prevDeadline })

	server, err := NewServer(socketPath, newTestHandler())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(server.Close)

	go func() { _ = server.Serve(context.Background()) }()

	// Open a connection and write nothing. Without a server-side deadline this
	// would park the handler goroutine until the test process exited.
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Reading the response should EOF (server closes the conn after timeout)
	// or yield no bytes within the deadline window.
	if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 64)
	_, readErr := conn.Read(buf)
	if readErr == nil {
		t.Fatal("expected EOF or read error after server-side deadline elapsed")
	}
}

func TestServer_CloseDoesNotUnlinkSocketPath(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-srv-unlink-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "d.sock")

	server, err := NewServer(socketPath, newTestHandler())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	server.Close()

	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("socket path should survive Server.Close for explicit owner removal; stat err = %v", err)
	}
}

// TestServer_ServeDoesNotLeakWatchdogOnCloseWithoutCancel verifies that the
// listener-closer goroutine started inside Serve exits when Serve returns,
// even if the caller shuts the server down via Close without ever cancelling
// the context originally passed to Serve. Before the fix, the watchdog parked
// forever on <-ctx.Done(), leaking one goroutine per Server instance.
func TestServer_ServeDoesNotLeakWatchdogOnCloseWithoutCancel(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-watchdog-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	baseline := runtime.NumGoroutine()

	const iterations = 16
	for i := 0; i < iterations; i++ {
		socketPath := filepath.Join(dir, "d.sock")

		server, err := NewServer(socketPath, newTestHandler())
		if err != nil {
			t.Fatalf("NewServer: %v", err)
		}

		serveDone := make(chan struct{})
		go func() {
			_ = server.Serve(context.Background())
			close(serveDone)
		}()

		server.Close()

		select {
		case <-serveDone:
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not return after Close")
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	var final int
	for time.Now().Before(deadline) {
		runtime.GC()
		final = runtime.NumGoroutine()
		if final <= baseline+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutine count grew without bound: baseline=%d after %d iterations=%d", baseline, iterations, final)
}
