package tmux

import (
	"bytes"
	"fmt"

	"golang.org/x/sys/unix"
)

// processExecutable reads the path the kernel executed for pid. On macOS it
// is the first string after argc in kern.procargs2, which holds the resolved
// executable path whatever argv[0] says (a tmux server's argv is the command
// line of the client that started it, so argv[0] is often a bare "tmux").
func processExecutable(pid int) (string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", fmt.Errorf("reading the executable of pid %d: %w", pid, err)
	}
	if len(raw) < 5 {
		return "", fmt.Errorf("reading the executable of pid %d: short procargs", pid)
	}
	path, _, _ := bytes.Cut(raw[4:] /* after argc */, []byte{0})
	if len(path) == 0 {
		return "", fmt.Errorf("reading the executable of pid %d: no path", pid)
	}
	return string(path), nil
}
