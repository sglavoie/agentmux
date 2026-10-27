// Package tmuxtest gives tests outside internal/tmux a private tmux server, a
// runtime-session registry, and a stand-in agent, so launch, continuity, and
// attach paths run against real tmux without ever starting a harness.
package tmuxtest

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// Fixture is one isolated tmux server and registry.
type Fixture struct {
	Server   *tmux.Server
	Backend  *tmux.Backend
	Registry *runtimesession.Store
	dir      string
}

// New starts nothing (the first launch starts the server) and skips the test
// when tmux is not installed or is older than tmux.MinVersion. It points SHELL at a stand-in login shell that
// records the runtime session it was launched for and then sleeps, so every
// launch's `$SHELL -lic <harness command>` runs that instead of a harness.
func New(t *testing.T) *Fixture {
	t.Helper()
	path, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed; the private tmux backend needs it")
	}
	out, err := exec.Command(path, "-V").Output()
	if version := strings.TrimPrefix(strings.TrimSpace(string(out)), "tmux "); err != nil || !tmux.SupportedVersion(version) {
		t.Skipf("tmux at %s is not %s or newer (%q, %v); the private tmux backend needs it", path, tmux.MinVersion, version, err)
	}
	// Socket paths are length-limited, so the server lives under a short
	// directory rather than t.TempDir().
	dir, err := os.MkdirTemp("", "amxtt")
	if err != nil {
		t.Fatal(err)
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", filepath.Join(dir, "unused"))
	shell := filepath.Join(dir, "agent-shell")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$%s\" > %q/env-$$\nexec sleep 600\n", runtimesession.EnvVar, dir)
	if err := os.WriteFile(shell, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	server, err := tmux.NewServer(tmux.Options{Dir: filepath.Join(dir, "srv")})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := runtimesession.NewStore(filepath.Join(dir, "runtime-sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Kill(); err != nil {
			t.Errorf("kill tmux server: %v", err)
		}
		_ = os.RemoveAll(dir)
	})
	return &Fixture{Server: server, Backend: tmux.NewBackend(server), Registry: registry, dir: dir}
}

// LaunchedFor reports the runtime session the stand-in agent with pid saw
// in its environment, waiting briefly for the agent to start, or "" when it
// never recorded one.
func (f *Fixture) LaunchedFor(pid int) string {
	path := filepath.Join(f.dir, fmt.Sprintf("env-%d", pid))
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
		if time.Now().After(deadline) {
			return ""
		}
	}
}

// Load reads the registry, failing the test on error.
func (f *Fixture) Load(t *testing.T) *runtimesession.Registry {
	t.Helper()
	registry, err := f.Registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// Refuse ends the server and listens on its socket, hanging up on every
// client: what a server too busy to take a connection looks like to one, which
// tmux reports as no server running. The returned function stops listening
// and leaves the socket behind, as a server that crashed does.
func (f *Fixture) Refuse(t *testing.T) (stop func()) {
	t.Helper()
	if err := f.Server.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.Server.Socket()); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", f.Server.Socket())
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	stop = func() { _ = listener.Close() }
	t.Cleanup(stop)
	return stop
}
