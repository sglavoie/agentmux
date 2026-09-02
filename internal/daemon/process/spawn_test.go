package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSpawnDetachedPersistsBootstrapFailure(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv(DetachedEnv, "0")
	t.Setenv(LaunchIDEnv, "stale-parent-launch")
	t.Setenv(BootstrapEnv, "/stale/parent.log")
	script := filepath.Join(t.TempDir(), "fake-amx")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"detached=$AMX_DAEMON_DETACHED launch=$AMX_DAEMON_LAUNCH_ID bootstrap=$AMX_DAEMON_BOOTSTRAP_LOG\" >&2\necho 'fatal: config could not be loaded' >&2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SpawnDetached(script); err != nil {
		t.Fatal(err)
	}
	daemonDir := filepath.Join(stateHome, "agent-mux", "daemon")
	// Same host-load tolerance as TestSpawnDetachedFallbackWithoutDiagnostics
	// below: the detached child may not be scheduled promptly under the full
	// parallel suite, so bound a genuinely missing log rather than a slow host.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(daemonDir, "bootstrap-*.log"))
		if len(matches) == 1 {
			data, err := os.ReadFile(matches[0])
			if err == nil && strings.Contains(string(data), "fatal: config could not be loaded") {
				if !strings.Contains(string(data), "detached=1") || strings.Contains(string(data), "stale-parent") || !strings.Contains(string(data), "bootstrap="+matches[0]) {
					t.Fatalf("detached environment was not overridden: %s", data)
				}
				if !strings.Contains(string(data), "daemon bootstrap started") {
					t.Fatalf("bootstrap header missing: %s", data)
				}
				info, err := os.Stat(matches[0])
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("bootstrap mode = %v, err = %v", info.Mode().Perm(), err)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("detached daemon failure did not reach bootstrap log")
}

func TestSpawnDetachedFallbackWithoutDiagnostics(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateHome, 0o700); err != nil {
		t.Fatal(err)
	}
	// A non-directory at the diagnostics root makes OpenBootstrapLog fail while
	// leaving process creation itself available.
	if err := os.WriteFile(filepath.Join(stateHome, "agent-mux"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", stateHome)

	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("AMX_SPAWN_FALLBACK_MARKER", marker)
	script := filepath.Join(t.TempDir(), "fake-amx")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf started > \"$AMX_SPAWN_FALLBACK_MARKER\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := SpawnDetached(script); err != nil {
		t.Fatalf("SpawnDetached() error = %v, want diagnostics failure to be fail-open", err)
	}
	// The full acceptance suite runs package tests in parallel, so the detached
	// child may not be scheduled promptly even after cmd.Start succeeds. Keep
	// this fail-open regression tolerant of host load while still bounding a
	// genuinely missing marker.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "started" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("detached daemon command did not start after bootstrap-log failure")
}
