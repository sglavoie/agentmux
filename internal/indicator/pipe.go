package indicator

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// IsPipeMode reports whether the current process tree contains a `claude -p`
// or `claude --print` invocation (i.e., Claude is running in pipe/non-interactive
// mode). When true, desktop notifications should be suppressed.
func IsPipeMode() bool {
	return walkTree(os.Getpid(), psCommandLine, psParentPID)
}

// walkTree walks up the process tree from pid, checking each ancestor's
// command line via cmdline. parentPID is injectable for tests.
func walkTree(pid int, cmdline func(int) string, ppid func(int) int) bool {
	visited := make(map[int]bool)
	for pid > 1 {
		if visited[pid] {
			break
		}
		visited[pid] = true

		if looksLikeClaudePipe(cmdline(pid)) {
			return true
		}

		parent := ppid(pid)
		if parent <= 0 || parent == pid {
			break
		}
		pid = parent
	}
	return false
}

func looksLikeClaudePipe(cmdline string) bool {
	if !strings.Contains(cmdline, "claude") {
		return false
	}
	for _, field := range strings.Fields(cmdline) {
		if field == "-p" || field == "--print" {
			return true
		}
	}
	return false
}

// psCommandLine returns the full command line for pid using `ps`.
func psCommandLine(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// psParentPID returns the parent PID of pid using `ps`.
func psParentPID(pid int) int {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
	if err != nil {
		return -1
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return -1
	}
	return p
}
