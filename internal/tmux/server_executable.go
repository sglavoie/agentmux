package tmux

import (
	"fmt"
	"path/filepath"
	"strconv"
)

// ServerExecutable is the executable the server process with pid is running,
// read from the kernel rather than from the record beside the socket. The
// record names the tmux amx started the server with; any client that reaches
// the socket while no server listens (a plain `tmux` run inside an agent pane
// inherits TMUX, which names this socket) starts its own, and the record then
// describes a server that is gone.
func ServerExecutable(pid string) (string, error) {
	n, err := strconv.Atoi(pid)
	if err != nil || n <= 0 {
		return "", fmt.Errorf("tmux: invalid server pid %q", pid)
	}
	return processExecutable(n)
}

// SameExecutable reports whether a and b name the same file once symlinks
// are resolved, so a Homebrew bin/ link and its Cellar target compare equal.
func SameExecutable(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
