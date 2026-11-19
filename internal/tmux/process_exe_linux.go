package tmux

import (
	"os"
	"strconv"
)

// processExecutable reads the path the kernel executed for pid.
func processExecutable(pid int) (string, error) {
	return os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
}
