package daemon

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

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
