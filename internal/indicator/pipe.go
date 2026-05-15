package indicator

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// IsPipeMode reports whether the current process tree contains a non-interactive
// Claude or Copilot invocation (e.g. `claude -p`, `claude --print`,
// `copilot -p`, or `copilot --prompt`)
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

		if looksLikePipeMode(cmdline(pid)) {
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

func looksLikePipeMode(cmdline string) bool {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return false
	}

	containsClaude := strings.Contains(cmdline, "claude")
	containsCopilot := strings.Contains(cmdline, "copilot")
	if !containsClaude && !containsCopilot {
		return false
	}

	for _, field := range fields {
		if field == "-p" {
			return true
		}
		if containsClaude && field == "--print" {
			return true
		}
		if containsCopilot && field == "--prompt" {
			return true
		}
	}
	return false
}

// looksLikeClaudePipe is kept as a compatibility alias for older tests while
// the implementation now supports both Claude and Copilot non-interactive runs.
func looksLikeClaudePipe(cmdline string) bool {
	return looksLikePipeMode(cmdline)
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
