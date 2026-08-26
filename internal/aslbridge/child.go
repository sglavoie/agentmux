package aslbridge

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// BridgeArgs are the fixed arguments the daemon starts the bridge with. The
// protocol number is pinned rather than negotiated: a bridge that no longer
// serves it exits before writing a byte of protocol, which is a clearer failure
// than a session that starts and then cannot agree on anything.
var BridgeArgs = []string{"bridge", "serve", "--stdio", "--protocol", "2"}

// Child is one running bridge subprocess, described by the pipes and lifecycle
// hooks the manager needs. Tests substitute a Launcher that returns in-process
// pipes, so every supervision behaviour is exercisable without a real process.
type Child struct {
	// Path is the resolved executable, reported in status so an operator can
	// see which ASL the daemon actually started.
	Path string
	// Source names how Path was found (see the Source* constants). It is
	// reported alongside Path because "found on the daemon's PATH" and
	// "configured absolutely" are the same executable today and a different
	// one after the next reboot, and only the second survives.
	Source string
	Stdin  io.WriteCloser
	Stdout io.Reader
	Stderr io.Reader
	// Wait blocks until the child exits. It is called exactly once.
	Wait func() error
	// Terminate asks the child to stop after stdin close did not suffice.
	Terminate func() error
}

// Launcher starts one bridge child.
type Launcher func(ctx context.Context) (*Child, error)

// execLauncher builds the production launcher for a command that is either a
// bare name or an executable path. Shell syntax never reaches this point:
// config validation rejects it, and the argv here is fixed, so the command is
// executed directly rather than interpreted.
//
// Resolution runs on every launch rather than once at construction, so an `asl`
// installed after the daemon started is found by the next restart attempt
// instead of requiring the daemon to be restarted too.
func execLauncher(command string) Launcher {
	return func(ctx context.Context) (*Child, error) {
		resolution, err := ResolveCommand(command)
		if err != nil {
			return nil, err
		}
		resolved := resolution.Executable
		cmd := exec.Command(resolved, BridgeArgs...)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("asl bridge: opening stdin: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("asl bridge: opening stdout: %w", err)
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, fmt.Errorf("asl bridge: opening stderr: %w", err)
		}
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("asl bridge: starting %s: %w", resolved, err)
		}
		return &Child{
			Path:      resolved,
			Source:    resolution.Source,
			Stdin:     stdin,
			Stdout:    stdout,
			Stderr:    stderr,
			Wait:      cmd.Wait,
			Terminate: func() error { return cmd.Process.Kill() },
		}, nil
	}
}

// stderrTail keeps the last StderrTailBytes of a child's diagnostics. It is
// private daemon evidence: it is never published through IPC, health, or the
// event export, because a bridge is free to print whatever it likes there.
type stderrTail struct {
	mu   sync.Mutex
	buf  []byte
	done chan struct{}
}

func newStderrTail() *stderrTail {
	return &stderrTail{done: make(chan struct{})}
}

func (t *stderrTail) consume(r io.Reader) {
	defer close(t.done)
	if r == nil {
		return
	}
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			t.append(chunk[:n])
		}
		if err != nil {
			return
		}
	}
}

func (t *stderrTail) append(b []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > StderrTailBytes {
		t.buf = t.buf[len(t.buf)-StderrTailBytes:]
	}
}

// String returns the retained tail for a daemon log line.
func (t *stderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// lastLine returns the final non-empty stderr line, which is what a crashing
// bridge normally leaves behind. It is used only for private logging.
func (t *stderrTail) lastLine() string {
	trimmed := strings.TrimRight(t.String(), "\r\n")
	if trimmed == "" {
		return ""
	}
	if idx := strings.LastIndexByte(trimmed, '\n'); idx >= 0 {
		return trimmed[idx+1:]
	}
	return trimmed
}

func (t *stderrTail) wait() { <-t.done }
