package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func newShutdownTestSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "amx-shutdown-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "daemon.sock")
}

func TestClientShutdownAndWaitWaitsPastAcknowledgement(t *testing.T) {
	socketPath := newShutdownTestSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	disableListenerAutoUnlink(listener)
	acknowledged := make(chan struct{})
	allowTeardown := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		_ = json.NewEncoder(conn).Encode(protocol.Response{OK: true})
		close(acknowledged)
		<-allowTeardown
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-allowTeardown:
		default:
			close(allowTeardown)
		}
		<-serverDone
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- NewClientWithSocket(socketPath).ShutdownAndWait(ctx) }()

	select {
	case <-acknowledged:
	case <-time.After(time.Second):
		t.Fatal("shutdown request was not acknowledged")
	}
	select {
	case err := <-result:
		t.Fatalf("ShutdownAndWait returned at acknowledgement boundary: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	close(allowTeardown)
	if err := <-result; err != nil {
		t.Fatalf("ShutdownAndWait: %v", err)
	}
}

func TestClientShutdownAndWaitAlreadyStopped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := NewClientWithSocket(newShutdownTestSocketPath(t)).ShutdownAndWait(ctx); err != nil {
		t.Fatalf("ShutdownAndWait on stopped daemon: %v", err)
	}
}

func TestClientShutdownAndWaitRejectsFailedAcknowledgement(t *testing.T) {
	socketPath := newShutdownTestSocketPath(t)
	stop := startTestDaemonServer(t, socketPath, func(protocol.Request) (protocol.Response, bool) {
		return protocol.Response{OK: false, Error: "shutdown disabled"}, true
	})
	t.Cleanup(stop)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := NewClientWithSocket(socketPath).ShutdownAndWait(ctx)
	if err == nil || !strings.Contains(err.Error(), "rejected shutdown") || !strings.Contains(err.Error(), "shutdown disabled") {
		t.Fatalf("ShutdownAndWait error = %v, want explicit rejection", err)
	}
}

func TestClientShutdownAndWaitHonorsTimeoutAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name       string
		cancel     bool
		want       error
		wantPhrase string
	}{
		{name: "deadline", want: context.DeadlineExceeded, wantPhrase: "timed out"},
		{name: "cancellation", cancel: true, want: context.Canceled, wantPhrase: "waiting for daemon teardown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			socketPath := newShutdownTestSocketPath(t)
			acknowledged := make(chan struct{}, 1)
			stop := startTestDaemonServer(t, socketPath, func(protocol.Request) (protocol.Response, bool) {
				acknowledged <- struct{}{}
				return protocol.Response{OK: true}, true
			})
			t.Cleanup(stop)

			ctx, cancel := context.WithTimeout(context.Background(), 125*time.Millisecond)
			if test.cancel {
				go func() {
					<-acknowledged
					time.Sleep(25 * time.Millisecond)
					cancel()
				}()
			} else {
				defer cancel()
			}
			err := NewClientWithSocket(socketPath).ShutdownAndWait(ctx)
			if !errors.Is(err, test.want) || !strings.Contains(err.Error(), test.wantPhrase) {
				t.Fatalf("ShutdownAndWait error = %v, want %v containing %q", err, test.want, test.wantPhrase)
			}
		})
	}
}

func TestClientHealthIncludesDaemonDiagnostics(t *testing.T) {
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
		_ = json.NewEncoder(conn).Encode(protocol.Response{
			OK: true, PID: 42, UptimeSeconds: 7, Ready: false,
			DegradedReason: "initial snapshot timed out",
			Metrics:        &protocol.MetricsSnapshot{Accepted: 6, QueueFull: 1},
		})
	}()
	health := NewClientWithSocket(sockPath).Health()
	if !health.Alive || health.PID != 42 || health.Uptime != 7*time.Second || health.Ready ||
		health.DegradedReason != "initial snapshot timed out" || health.Metrics == nil ||
		health.Metrics.Accepted != 6 || health.Metrics.QueueFull != 1 {
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
	_, err = c.Send(protocol.Request{Type: protocol.TypeStateQuery})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected Send to fail when daemon never responds")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Send blocked beyond deadline (elapsed=%v); deadline=%v", elapsed, sendDeadline)
	}
}

func TestClient_SendWithAutoStartHonorsFullDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		oldSendDeadline := sendDeadline
		sendDeadline = 100 * time.Millisecond
		defer func() { sendDeadline = oldSendDeadline }()
		oldAutoStartDeadline := autoStartDeadline
		autoStartDeadline = 220 * time.Millisecond
		defer func() { autoStartDeadline = oldAutoStartDeadline }()
		oldRetryDelay := autoStartRetryDelay
		autoStartRetryDelay = 10 * time.Millisecond
		defer func() { autoStartRetryDelay = oldRetryDelay }()
		oldSpawn := spawnDetached
		spawnDetached = func(string) error { return nil }
		defer func() { spawnDetached = oldSpawn }()

		client := NewClientWithSocket(filepath.Join(t.TempDir(), "d.sock"))
		client.dialContext = func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			go func() {
				defer func() { _ = serverConn.Close() }()
				_, _ = io.Copy(io.Discard, serverConn)
			}()
			return clientConn, nil
		}

		started := time.Now()
		_, err := client.SendWithAutoStart(protocol.Request{Type: protocol.TypeHealth}, "/tmp/amx")
		if err == nil {
			t.Fatal("SendWithAutoStart() error = nil, want deadline failure")
		}
		if elapsed := time.Since(started); elapsed != autoStartDeadline {
			t.Fatalf("SendWithAutoStart() took %v, want exact full deadline %v", elapsed, autoStartDeadline)
		}
	})
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
	_, sendErr := c.Send(protocol.Request{Type: protocol.TypeStateQuery})
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
	events := make([]protocol.SessionEvent, 4096)
	for i := range events {
		events[i] = protocol.SessionEvent{At: int64(i), Event: "PostToolUse", Action: "active", Reason: filler}
	}
	resp := protocol.Response{OK: true, EventsByWindow: map[int][]protocol.SessionEvent{7: events}}
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
