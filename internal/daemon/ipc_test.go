package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

func startTestServer(t *testing.T) string {
	t.Helper()
	// Use /tmp to avoid macOS sun_path 104-byte limit on long t.TempDir() paths.
	dir, err := os.MkdirTemp("/tmp", "amx-ipc-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	socketPath := filepath.Join(dir, "d.sock")

	guard := NewStateGuard(time.Hour)
	notifier := NewNotifier(&config.NotificationConfig{})
	notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
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
	return socketPath
}

func TestIPC_StateUpdate(t *testing.T) {
	sock := startTestServer(t)
	c := NewClientWithSocket(sock)

	resp, err := c.Send(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestIPC_StateQuery(t *testing.T) {
	sock := startTestServer(t)
	c := NewClientWithSocket(sock)

	resp, err := c.Send(Request{Type: TypeStateQuery})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !resp.OK {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestIPC_JSONRoundTrip(t *testing.T) {
	sock := startTestServer(t)
	c := NewClientWithSocket(sock)

	// Guard blocks lower-priority update across round-trips.
	if _, err := c.Send(Request{Type: TypeStateUpdate, WindowID: 2, Action: "failure"}); err != nil {
		t.Fatalf("Send failure: %v", err)
	}
	resp, err := c.Send(Request{Type: TypeStateUpdate, WindowID: 2, Action: "permission"})
	if err != nil {
		t.Fatalf("Send permission: %v", err)
	}
	if resp.Allowed {
		t.Fatal("expected permission to be blocked by failure guard")
	}
}

func TestIPC_PromptBypassesStopGuard(t *testing.T) {
	sock := startTestServer(t)
	c := NewClientWithSocket(sock)

	if _, err := c.Send(Request{Type: TypeStateUpdate, WindowID: 3, Action: "stop"}); err != nil {
		t.Fatalf("Send stop: %v", err)
	}
	resp, err := c.Send(Request{Type: TypeStateUpdate, WindowID: 3, Action: "active", IsPrompt: true})
	if err != nil {
		t.Fatalf("Send active+UserPromptSubmit: %v", err)
	}
	if !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("expected prompt to bypass stop guard, got %+v", resp)
	}
}

func TestIPC_ConcurrentUpdates(t *testing.T) {
	sock := startTestServer(t)
	c := NewClientWithSocket(sock)

	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		windowID := i + 1
		go func() {
			_, err := c.Send(Request{Type: TypeStateUpdate, WindowID: windowID, Action: "active"})
			done <- err
		}()
	}
	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent send error: %v", err)
		}
	}
}

func TestIPC_InvalidJSON(t *testing.T) {
	sock := startTestServer(t)

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.Write([]byte("{invalid}\n"))

	buf := make([]byte, 256)
	n, _ := conn.Read(buf)
	var resp Response
	if jsonErr := json.Unmarshal(buf[:n], &resp); jsonErr != nil {
		t.Fatalf("response not valid JSON: %v", jsonErr)
	}
	if resp.OK {
		t.Fatal("expected ok=false for invalid JSON")
	}
}

func TestIPC_IdleShutdown(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-idle2-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	socketPath := filepath.Join(dir, "d.sock")

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	// Override the idle timeout for this test via a tiny guard (we just want
	// the server to come up and respond to queries — idle test is covered at
	// the unit level via daemon_test.go).
	guard := NewStateGuard(time.Hour)
	notifier := NewNotifier(&config.NotificationConfig{})
	notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
	handler := NewHandler(guard, notifier)

	server, err := NewServer(socketPath, handler)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	c := NewClientWithSocket(socketPath)
	resp, err := c.Send(Request{Type: TypeStateQuery})
	if err != nil || !resp.OK {
		t.Fatalf("query failed: err=%v resp=%+v", err, resp)
	}

	// Trigger shutdown via request.
	handler.onShutdown = cancel
	resp, err = c.Send(Request{Type: TypeShutdown})
	if err != nil || !resp.OK {
		t.Fatalf("shutdown request failed: err=%v resp=%+v", err, resp)
	}

	// After shutdown, subsequent connects should fail.
	time.Sleep(50 * time.Millisecond)
	if _, err := c.Send(Request{Type: TypeStateQuery}); err == nil {
		t.Fatal("expected connection error after shutdown")
	}
}
