package agentloop

import (
	"errors"
	"syscall"
)

// IsPIDRunning mirrors agent-session-loop's os.kill(pid, 0) check.
//
// This is a liveness check, not an identity check: it reports that *some*
// process holds the PID. Establishing that it is the same process ASL
// published requires the start token as well — see authenticateProcess.
func IsPIDRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err == nil {
		return true
	} else if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}
