package ipc

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/process"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestRestartDetachedExcludesAutoStartUntilReplacementAnswers(t *testing.T) {
	socket := newShutdownTestSocketPath(t)
	stop := startTestDaemonServer(t, socket, func(req protocol.Request) (protocol.Response, bool) {
		if req.Type == protocol.TypeShutdown {
			lock, err := process.AcquireAutoStartLock(socket)
			if err == nil {
				process.ReleaseAutoStartLock(lock)
			}
			if !errors.Is(err, process.ErrAutoStartInProgress) {
				t.Errorf("shutdown was not protected against auto-start: %v", err)
			}
			return protocol.Response{OK: true}, false
		}
		return protocol.Response{OK: true, PID: 1}, true
	})
	t.Cleanup(stop)

	spawned := make(chan struct{})
	var competingSpawns atomic.Int32
	previous := spawnDetached
	spawnDetached = func(binary string) error {
		if binary != "/installed/amx" {
			competingSpawns.Add(1)
			return errors.New("bundled dashboard tried to take over the restart")
		}
		close(spawned)
		// The child has been spawned but has not published its socket yet.
		return nil
	}
	t.Cleanup(func() { spawnDetached = previous })

	restarted := make(chan error, 1)
	go func() { restarted <- NewClientWithSocket(socket).RestartDetached("/installed/amx") }()
	select {
	case <-spawned:
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not spawn its replacement")
	}

	// An embedded dashboard asks for state in the gap before the replacement
	// binds. It must retry the installed daemon, never spawn its bundled amx.
	retrying := make(chan struct{})
	var retryOnce sync.Once
	autoDone := make(chan error, 1)
	go func() {
		_, err := NewClientWithSocket(socket).SendWithAutoStartObserved(
			protocol.Request{Type: protocol.TypeStateQuery}, "/bundled/amx", func(event DeliveryStageEvent) {
				if event.Stage == DeliveryStageIPCAttempt && event.Attempt >= 2 {
					retryOnce.Do(func() { close(retrying) })
				}
			})
		autoDone <- err
	}()
	var earlyAutoErr error
	autoFinished := false
	select {
	case <-retrying:
	case earlyAutoErr = <-autoDone:
		autoFinished = true
	case <-time.After(3 * time.Second):
		t.Fatal("dashboard neither retried nor finished")
	}

	stopReplacement := startTestDaemonServer(t, socket, func(protocol.Request) (protocol.Response, bool) {
		return protocol.Response{OK: true, PID: 2}, true
	})
	t.Cleanup(stopReplacement)
	if err := <-restarted; err != nil {
		t.Fatalf("restart: %v", err)
	}
	if !autoFinished {
		earlyAutoErr = <-autoDone
	}
	if earlyAutoErr != nil || competingSpawns.Load() != 0 {
		t.Fatalf("dashboard spawned %d competing daemons; request error: %v", competingSpawns.Load(), earlyAutoErr)
	}
	lock, err := process.AcquireAutoStartLock(socket)
	if err != nil {
		t.Fatalf("restart left the startup lock held: %v", err)
	}
	process.ReleaseAutoStartLock(lock)
}

func TestRestartDetachedFailuresReleaseStartupLock(t *testing.T) {
	for _, tc := range []struct {
		name      string
		spawnErr  error
		wantError string
	}{
		{"spawn failed", errors.New("executable unavailable"), "executable unavailable"},
		{"replacement never answered", nil, "replacement did not answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := newShutdownTestSocketPath(t)
			previousSpawn, previousTimeout := spawnDetached, explicitRestartTimeout
			spawnDetached = func(string) error { return tc.spawnErr }
			explicitRestartTimeout = 100 * time.Millisecond
			t.Cleanup(func() {
				spawnDetached, explicitRestartTimeout = previousSpawn, previousTimeout
			})
			if err := NewClientWithSocket(socket).RestartDetached("/installed/amx"); err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("restart error = %v; want %q", err, tc.wantError)
			}
			lock, err := process.AcquireAutoStartLock(socket)
			if err != nil {
				t.Fatalf("failed restart left the startup lock held: %v", err)
			}
			process.ReleaseAutoStartLock(lock)
		})
	}
}

func TestRestartDetachedWaitsForBusyStartupLockWithoutStoppingDaemon(t *testing.T) {
	socket := newShutdownTestSocketPath(t)
	lock, err := process.AcquireAutoStartLock(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer process.ReleaseAutoStartLock(lock)
	var shutdowns atomic.Int32
	stop := startTestDaemonServer(t, socket, func(req protocol.Request) (protocol.Response, bool) {
		if req.Type == protocol.TypeShutdown {
			shutdowns.Add(1)
		}
		return protocol.Response{OK: true, PID: 1}, true
	})
	t.Cleanup(stop)
	previousSpawn, previousTimeout := spawnDetached, explicitRestartTimeout
	spawnCalls := 0
	spawnDetached = func(string) error { spawnCalls++; return nil }
	explicitRestartTimeout = 100 * time.Millisecond
	t.Cleanup(func() {
		spawnDetached, explicitRestartTimeout = previousSpawn, previousTimeout
	})
	started := time.Now()
	err = NewClientWithSocket(socket).RestartDetached("/installed/amx")
	if err == nil || !strings.Contains(err.Error(), "waiting for another daemon starter") {
		t.Fatalf("restart error = %v; want lock timeout", err)
	}
	if time.Since(started) < explicitRestartTimeout {
		t.Fatal("restart did not wait for the competing starter")
	}
	if shutdowns.Load() != 0 || spawnCalls != 0 {
		t.Fatalf("restart interfered with another starter: shutdowns=%d spawns=%d", shutdowns.Load(), spawnCalls)
	}
}
