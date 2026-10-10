package tmuxtest

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// Viewer is a real tmux client on a pseudo-terminal of a chosen size: what an
// embedded terminal, or any terminal running `amx attach`, is to the server.
type Viewer struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{}
}

// StartViewer runs client on a new pseudo-terminal cols wide and rows high.
// It returns once the program is started, not once it is attached; the caller
// waits for whatever it expects of the server. The viewer is closed with the
// test.
func StartViewer(t *testing.T, client *exec.Cmd, cols, rows int) *Viewer {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("uses BSD script(1) for a pseudo-terminal")
	}
	// stty sizes the terminal before the client starts, so the client never
	// reports another size first.
	sized := fmt.Sprintf(`stty cols %d rows %d; exec "$@"`, cols, rows)
	args := append([]string{"-q", "/dev/null", "/bin/sh", "-c", sized, "viewer", client.Path}, client.Args[1:]...)
	script := exec.Command("script", args...)
	script.Env = append(client.Env, "TERM=xterm-256color")
	stdin, err := script.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := script.Start(); err != nil {
		t.Fatal(err)
	}
	v := &Viewer{cmd: script, stdin: stdin, done: make(chan struct{})}
	go func() {
		_ = script.Wait()
		close(v.done)
	}()
	t.Cleanup(v.Close)
	return v
}

// Close shuts the terminal, as closing a window does: its program is hung up.
func (v *Viewer) Close() {
	_ = v.stdin.Close()
	_ = v.cmd.Process.Signal(syscall.SIGKILL)
	<-v.done
}

// Exited reports whether the viewer's program ended within timeout, which is
// what happens to a client the server detached.
func (v *Viewer) Exited(timeout time.Duration) bool {
	select {
	case <-v.done:
		return true
	case <-time.After(timeout):
		return false
	}
}
