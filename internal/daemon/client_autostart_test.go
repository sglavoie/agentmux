package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestClient_SendWithAutoStart_RestartsDaemonWhenBinaryIsNewer(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-client-restart-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "daemon.sock")
	amxBin := filepath.Join(dir, "amx")
	if err := os.WriteFile(amxBin, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile(binary): %v", err)
	}

	oldRequests := make(chan Request, 4)
	newRequests := make(chan Request, 2)

	oldStop := startTestDaemonServer(t, socketPath, func(req Request) (Response, bool) {
		oldRequests <- req
		switch req.Type {
		case TypeHealth:
			return Response{OK: true, PID: 1234, UptimeSeconds: 3600}, true
		case TypeShutdown:
			return Response{OK: true}, false
		default:
			return Response{OK: true, Allowed: true, IconState: "old"}, true
		}
	})
	t.Cleanup(oldStop)

	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(socketPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(socket): %v", err)
	}
	if err := os.Chtimes(amxBin, newTime, newTime); err != nil {
		t.Fatalf("Chtimes(binary): %v", err)
	}

	prevRetryDelay := autoStartRetryDelay
	autoStartRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { autoStartRetryDelay = prevRetryDelay })

	prevPollDelay := restartSocketPollDelay
	restartSocketPollDelay = 10 * time.Millisecond
	t.Cleanup(func() { restartSocketPollDelay = prevPollDelay })

	spawnCalls := 0
	var newStop func()
	prevSpawn := spawnDetached
	spawnDetached = func(path string) error {
		spawnCalls++
		if path != amxBin {
			t.Fatalf("spawn path = %q, want %q", path, amxBin)
		}
		newStop = startTestDaemonServer(t, socketPath, func(req Request) (Response, bool) {
			newRequests <- req
			return Response{OK: true, Allowed: true, IconState: "bolt"}, true
		})
		return nil
	}
	t.Cleanup(func() {
		spawnDetached = prevSpawn
		if newStop != nil {
			newStop()
		}
	})

	c := NewClientWithSocket(socketPath)
	resp, err := c.SendWithAutoStart(Request{
		Type:     TypeStateUpdate,
		WindowID: 7,
		Action:   "active",
	}, amxBin)
	if err != nil {
		t.Fatalf("SendWithAutoStart: %v", err)
	}
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("response = %+v, want OK/Allowed/bolt from restarted daemon", resp)
	}
	if spawnCalls != 1 {
		t.Fatalf("spawn calls = %d, want 1", spawnCalls)
	}

	var seen []MessageType
	for i := 0; i < 2; i++ {
		select {
		case req := <-oldRequests:
			seen = append(seen, req.Type)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for old daemon requests; got %v", seen)
		}
	}
	if len(seen) != 2 || seen[0] != TypeHealth || seen[1] != TypeShutdown {
		t.Fatalf("old daemon requests = %v, want [health shutdown]", seen)
	}

	select {
	case req := <-newRequests:
		if req.Type != TypeStateUpdate || req.Action != "active" || req.WindowID != 7 {
			t.Fatalf("new daemon request = %+v, want state update", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for restarted daemon state update")
	}
}

// TestClient_SendWithAutoStart_NoDoubleSpawnAfterRestart pins that after the
// stale-binary restart path spawns a replacement daemon, a transient connect
// failure during the brief window before the new daemon binds the socket does
// NOT cause SendWithAutoStart to spawn a second daemon process. Two concurrent
// daemons race in NewServer (each removes the socket file and rebinds),
// leaving the loser running but unreachable as a ghost process. The test
// drives that exact race: the spawn callback delays the new server's bind
// until after the first post-restart Send attempt would have completed, so
// the only way the test daemon comes up is if the retry loop (not a fresh
// spawnDetached) reaches it.
func TestClient_SendWithAutoStart_NoDoubleSpawnAfterRestart(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-client-no-double-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "daemon.sock")
	amxBin := filepath.Join(dir, "amx")
	if err := os.WriteFile(amxBin, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile(binary): %v", err)
	}

	oldStop := startTestDaemonServer(t, socketPath, func(req Request) (Response, bool) {
		switch req.Type {
		case TypeHealth:
			return Response{OK: true, PID: 1234, UptimeSeconds: 3600}, true
		case TypeShutdown:
			return Response{OK: true}, false
		default:
			return Response{OK: true}, true
		}
	})
	t.Cleanup(oldStop)

	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(socketPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(socket): %v", err)
	}
	if err := os.Chtimes(amxBin, newTime, newTime); err != nil {
		t.Fatalf("Chtimes(binary): %v", err)
	}

	prevRetryDelay := autoStartRetryDelay
	autoStartRetryDelay = 20 * time.Millisecond
	t.Cleanup(func() { autoStartRetryDelay = prevRetryDelay })

	prevPollDelay := restartSocketPollDelay
	restartSocketPollDelay = 10 * time.Millisecond
	t.Cleanup(func() { restartSocketPollDelay = prevPollDelay })

	var spawnMu sync.Mutex
	spawnCalls := 0
	var newStop func()
	prevSpawn := spawnDetached
	spawnDetached = func(path string) error {
		spawnMu.Lock()
		spawnCalls++
		spawnMu.Unlock()
		if path != amxBin {
			t.Errorf("spawn path = %q, want %q", path, amxBin)
		}
		// Bring up the replacement daemon asynchronously so the first
		// post-restart Send observes "no such file or directory" — the
		// real-world race the fix targets. Without the fix, the caller
		// would spawnDetached again here and the counter would tick to 2.
		go func() {
			time.Sleep(50 * time.Millisecond)
			stop := startTestDaemonServer(t, socketPath, func(req Request) (Response, bool) {
				return Response{OK: true, Allowed: true, IconState: "bolt"}, true
			})
			spawnMu.Lock()
			newStop = stop
			spawnMu.Unlock()
		}()
		return nil
	}
	t.Cleanup(func() {
		spawnDetached = prevSpawn
		spawnMu.Lock()
		stop := newStop
		spawnMu.Unlock()
		if stop != nil {
			stop()
		}
	})

	c := NewClientWithSocket(socketPath)
	resp, err := c.SendWithAutoStart(Request{
		Type:     TypeStateUpdate,
		WindowID: 9,
		Action:   "active",
	}, amxBin)
	if err != nil {
		t.Fatalf("SendWithAutoStart: %v", err)
	}
	if !resp.OK || resp.IconState != "bolt" {
		t.Fatalf("response = %+v, want OK/bolt from restarted daemon", resp)
	}

	spawnMu.Lock()
	got := spawnCalls
	spawnMu.Unlock()
	if got != 1 {
		t.Fatalf("spawn calls = %d, want 1 (restart-then-spawn-again would double-up daemons)", got)
	}
}

func TestClient_SendWithAutoStart_BurstSpawnsOnce(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-client-burst-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "daemon.sock")
	amxBin := filepath.Join(dir, "amx")
	if err := os.WriteFile(amxBin, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile(binary): %v", err)
	}

	prevRetryDelay := autoStartRetryDelay
	autoStartRetryDelay = 20 * time.Millisecond
	t.Cleanup(func() { autoStartRetryDelay = prevRetryDelay })

	var spawnMu sync.Mutex
	spawnCalls := 0
	var stop func()
	prevSpawn := spawnDetached
	spawnDetached = func(path string) error {
		spawnMu.Lock()
		spawnCalls++
		spawnMu.Unlock()
		if path != amxBin {
			t.Errorf("spawn path = %q, want %q", path, amxBin)
		}
		time.Sleep(30 * time.Millisecond)
		stop = startTestDaemonServer(t, socketPath, func(req Request) (Response, bool) {
			return Response{OK: true, Allowed: true, IconState: "bolt"}, true
		})
		return nil
	}
	t.Cleanup(func() {
		spawnDetached = prevSpawn
		if stop != nil {
			stop()
		}
	})

	const callers = 8
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(windowID int) {
			defer wg.Done()
			c := NewClientWithSocket(socketPath)
			resp, err := c.SendWithAutoStart(Request{
				Type:     TypeStateUpdate,
				WindowID: windowID,
				Action:   "active",
			}, amxBin)
			if err != nil {
				errs <- err
				return
			}
			if !resp.OK || resp.IconState != "bolt" {
				errs <- fmt.Errorf("response = %+v, want OK/bolt", resp)
			}
		}(i + 1)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	spawnMu.Lock()
	got := spawnCalls
	spawnMu.Unlock()
	if got != 1 {
		t.Fatalf("spawn calls = %d, want 1 for burst auto-start", got)
	}
}

// TestClient_SendWithAutoStart_RetryReusesRequestID pins that the request ID
// generated once by the caller survives the auto-start + retry path unchanged:
// the daemon that finally answers sees the same ID the first (failed) attempt
// would have carried, which is what lets it deduplicate an ambiguous resend.
func TestClient_SendWithAutoStart_RetryReusesRequestID(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-client-reqid-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "daemon.sock")
	amxBin := filepath.Join(dir, "amx")
	if err := os.WriteFile(amxBin, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile(binary): %v", err)
	}

	prevRetryDelay := autoStartRetryDelay
	autoStartRetryDelay = 20 * time.Millisecond
	t.Cleanup(func() { autoStartRetryDelay = prevRetryDelay })

	seen := make(chan Request, 4)
	var stageMu sync.Mutex
	var stages []DeliveryStageEvent
	var stop func()
	prevSpawn := spawnDetached
	spawnDetached = func(path string) error {
		// The socket does not exist yet, so the first Send fails with connect
		// refused; only after this spawn brings up the recorder does the retry
		// succeed. The recorder captures the request ID it receives.
		time.Sleep(30 * time.Millisecond)
		stop = startTestDaemonServer(t, socketPath, func(req Request) (Response, bool) {
			seen <- req
			return Response{OK: true, Allowed: true, IconState: "bolt"}, true
		})
		return nil
	}
	t.Cleanup(func() {
		spawnDetached = prevSpawn
		if stop != nil {
			stop()
		}
	})

	c := NewClientWithSocket(socketPath)
	resp, err := c.SendWithAutoStartObserved(Request{
		Type:      TypeStateUpdate,
		RequestID: "stable-id-123",
		WindowID:  4,
		Action:    "active",
	}, amxBin, func(stage DeliveryStageEvent) {
		stageMu.Lock()
		stages = append(stages, stage)
		stageMu.Unlock()
	})
	if err != nil {
		t.Fatalf("SendWithAutoStart: %v", err)
	}
	if !resp.OK || resp.IconState != "bolt" {
		t.Fatalf("response = %+v, want OK/bolt", resp)
	}
	select {
	case got := <-seen:
		if got.RequestID != "stable-id-123" {
			t.Fatalf("daemon saw request ID %q, want stable-id-123 (must survive retry)", got.RequestID)
		}
		if got.Attempt <= 1 {
			t.Fatalf("daemon saw attempt %d, want a retry attempt greater than 1", got.Attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for retried request")
	}
	stageMu.Lock()
	defer stageMu.Unlock()
	var sawAutostart, sawFailedAttempt, sawSuccessfulAttempt bool
	for _, stage := range stages {
		switch {
		case stage.Stage == DeliveryStageDaemonAutostart && stage.Outcome == "success":
			sawAutostart = true
		case stage.Stage == DeliveryStageIPCAttempt && stage.Outcome == "error":
			sawFailedAttempt = true
		case stage.Stage == DeliveryStageIPCAttempt && stage.Outcome == "success" && stage.Attempt > 1:
			sawSuccessfulAttempt = true
		}
	}
	if !sawAutostart || !sawFailedAttempt || !sawSuccessfulAttempt {
		t.Fatalf("delivery stages = %+v", stages)
	}
}

func startTestDaemonServer(t *testing.T, socketPath string, handler func(Request) (Response, bool)) func() {
	t.Helper()

	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(%s): %v", socketPath, err)
	}
	disableListenerAutoUnlink(ln)

	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = ln.Close()
			_ = os.Remove(socketPath)
		})
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				scanner := bufio.NewScanner(conn)
				if !scanner.Scan() {
					return
				}
				var req Request
				if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
					t.Errorf("unmarshal request: %v", err)
					return
				}
				resp, keep := handler(req)
				data, err := json.Marshal(resp)
				if err != nil {
					t.Errorf("marshal response: %v", err)
					return
				}
				data = append(data, '\n')
				if _, err := conn.Write(data); err != nil {
					t.Errorf("write response: %v", err)
					return
				}
				if !keep {
					stop()
				}
			}(conn)
		}
	}()

	return stop
}
