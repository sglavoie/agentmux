package daemon

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/activity"
)

func TestClientHealthIncludesActivityWriterMetrics(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-health-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sockPath := filepath.Join(dir, "health.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		_ = json.NewEncoder(conn).Encode(Response{
			OK: true, Ready: true, PID: 42,
			ActivityWriter: &activity.Metrics{Queued: 7, Persisted: 6, Dropped: 1, Degraded: true},
		})
	}()
	health := NewClientWithSocket(sockPath).Health()
	if !health.Alive || health.ActivityWriter == nil || health.ActivityWriter.Queued != 7 || !health.ActivityWriter.Degraded {
		t.Fatalf("health = %+v", health)
	}
}

// TestSocketPath_FallsBackWhenHomeMissing pins that SocketPath returns an
// absolute path even when the user's home directory cannot be resolved (e.g.
// HOME is unset). The previous implementation discarded the error and
// produced a relative path that net.Listen would resolve against the current
// working directory — silently creating the socket in unpredictable
// locations and breaking client/daemon agreement on the address.
func TestSocketPath_FallsBackWhenHomeMissing(t *testing.T) {
	// Force os.UserHomeDir to fail by clearing the relevant env vars.
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "") // Windows; harmless on darwin/linux

	got := SocketPath()
	if !filepath.IsAbs(got) {
		t.Fatalf("SocketPath() returned a relative path with HOME unset: %q", got)
	}
	if !strings.HasSuffix(got, "daemon.sock") {
		t.Fatalf("SocketPath() = %q; expected suffix daemon.sock", got)
	}
}

// TestClient_SendHonorsDeadline asserts that Send returns within sendDeadline
// when a server accepts the connection but never replies. Without an explicit
// SetDeadline on the conn, a wedged daemon would hang the hook script
// indefinitely.
func TestClient_SendHonorsDeadline(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-client-deadline-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// Accept loop that holds connections open without ever writing back.
	var mu sync.Mutex
	var accepted []net.Conn
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				close(done)
				return
			}
			mu.Lock()
			accepted = append(accepted, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		mu.Lock()
		for _, c := range accepted {
			_ = c.Close()
		}
		mu.Unlock()
	})

	// Shrink the deadline so the test does not wait the production 5s.
	prev := sendDeadline
	sendDeadline = 150 * time.Millisecond
	t.Cleanup(func() { sendDeadline = prev })

	c := NewClientWithSocket(sockPath)

	start := time.Now()
	_, err = c.Send(Request{Type: TypeStateQuery})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected Send to fail when daemon never responds")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Send blocked beyond deadline (elapsed=%v); deadline=%v", elapsed, sendDeadline)
	}
}

func TestClient_SendWithAutoStartHonorsFullDeadline(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-client-autostart-deadline-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				buf := make([]byte, 1024)
				_, _ = conn.Read(buf)
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()

	oldSendDeadline := sendDeadline
	sendDeadline = 100 * time.Millisecond
	t.Cleanup(func() { sendDeadline = oldSendDeadline })
	oldAutoStartDeadline := autoStartDeadline
	autoStartDeadline = 220 * time.Millisecond
	t.Cleanup(func() { autoStartDeadline = oldAutoStartDeadline })
	oldRetryDelay := autoStartRetryDelay
	autoStartRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { autoStartRetryDelay = oldRetryDelay })
	oldSpawn := spawnDetached
	spawnDetached = func(string) error { return nil }
	t.Cleanup(func() { spawnDetached = oldSpawn })

	started := time.Now()
	_, err = NewClientWithSocket(sockPath).SendWithAutoStart(Request{Type: TypeHealth}, "/tmp/amx")
	if err == nil {
		t.Fatal("SendWithAutoStart() error = nil, want deadline failure")
	}
	if elapsed := time.Since(started); elapsed > 350*time.Millisecond {
		t.Fatalf("SendWithAutoStart() took %v, want bounded near %v", elapsed, autoStartDeadline)
	}
}

// TestClient_RejectsOversizeResponse pins the client-side ceiling: a daemon
// response that exceeds maxResponseBytes must surface as an explicit error
// (not be silently truncated by bufio.Scanner's default behavior). The bug
// path is a server that writes a payload longer than the client's scanner
// buffer ceiling but never newline-terminates within the limit — the
// scanner returns bufio.ErrTooLong which we must translate into a
// human-readable error rather than a generic "reading response" wrap.
func TestClient_RejectsOversizeResponse(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-client-oversize-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// Lower the ceiling locally so the oversize path is exercised without
	// shuttling the production 64 MiB bound through a socketpair. Must stay
	// above the scanner's 64 KiB initial buffer: Scanner.Buffer takes the
	// larger of max and cap(buf) as the effective ceiling.
	oldMax := maxResponseBytes
	maxResponseBytes = 128 << 10
	t.Cleanup(func() { maxResponseBytes = oldMax })

	// Server: emit a payload larger than maxResponseBytes with no newline so the
	// client-side scanner exhausts its buffer.
	huge := strings.Repeat("A", maxResponseBytes+128)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte(huge))
	}()

	c := NewClientWithSocket(sockPath)
	_, sendErr := c.Send(Request{Type: TypeStateQuery})
	if sendErr == nil {
		t.Fatal("expected Send to fail on oversize daemon response")
	}
	if !strings.Contains(sendErr.Error(), "exceeds") {
		t.Fatalf("expected explicit oversize error, got: %v", sendErr)
	}
}

// TestClient_ExportEventsAboveLegacyCapRoundTrips pins the raised response
// ceiling: a full events export larger than the previous 1 MiB client cap
// must decode cleanly. Against the long-running production daemon the full
// export (5000-event ring buffers per window) exceeded 1 MiB, so doctor and
// audit degraded to a daemon_export failure finding instead of inspecting
// the daemon — the regression this test locks out.
func TestClient_ExportEventsAboveLegacyCapRoundTrips(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-client-bigexport-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// A valid export response comfortably above the legacy 1 MiB ceiling:
	// 4096 events carrying ~512B payload fields each (~2.5 MiB marshaled).
	filler := strings.Repeat("x", 512)
	events := make([]SessionEvent, 4096)
	for i := range events {
		events[i] = SessionEvent{At: int64(i), Event: "PostToolUse", Action: "active", Reason: filler}
	}
	resp := Response{OK: true, EventsByWindow: map[int][]SessionEvent{7: events}}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) <= 1<<20 {
		t.Fatalf("test response is %d bytes; must exceed the legacy 1 MiB cap to pin the regression", len(data))
	}
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)
		_, _ = conn.Write(append(data, '\n'))
	}()

	c := NewClientWithSocket(sockPath)
	export, exportErr := c.ExportEvents(0)
	if exportErr != nil {
		t.Fatalf("ExportEvents: %v", exportErr)
	}
	if got := len(export.EventsByWindow[7]); got != len(events) {
		t.Fatalf("EventsByWindow[7] length = %d, want %d", got, len(events))
	}
}

func TestDaemonSocketOlderThanBinary(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-client-socket-age-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "daemon.sock")
	if err := os.WriteFile(socketPath, []byte("socket"), 0o644); err != nil {
		t.Fatalf("WriteFile(socket): %v", err)
	}
	binPath := filepath.Join(dir, "amx")
	if err := os.WriteFile(binPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile(binary): %v", err)
	}

	old := time.Now().Add(-2 * time.Hour)
	newer := old.Add(time.Hour)
	if err := os.Chtimes(socketPath, old, old); err != nil {
		t.Fatalf("Chtimes(socket): %v", err)
	}
	if err := os.Chtimes(binPath, newer, newer); err != nil {
		t.Fatalf("Chtimes(binary): %v", err)
	}

	if !daemonSocketOlderThanBinary(socketPath, binPath) {
		t.Fatal("expected newer binary to trigger daemon restart check")
	}

	if err := os.Chtimes(binPath, old.Add(-time.Hour), old.Add(-time.Hour)); err != nil {
		t.Fatalf("Chtimes(binary older): %v", err)
	}
	if daemonSocketOlderThanBinary(socketPath, binPath) {
		t.Fatal("expected older binary to skip daemon restart check")
	}
}
