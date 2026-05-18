package indicator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// psCommandTimeout bounds each `ps` invocation used during process-tree
// walking. The indicator runs synchronously inside every hook invocation, so
// a wedged or paused `ps` (e.g. swapped out under heavy load, audit-locked
// on macOS) must surface as a missing parent rather than hang the hook
// script. Exposed as a var so tests can shrink it without sleeping.
var psCommandTimeout = 2 * time.Second

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

// pipeFlagsByCommand maps the supported command basenames to the set of flags
// that imply non-interactive (pipe) mode for that command.
var pipeFlagsByCommand = map[string]map[string]bool{
	"claude":  {"-p": true, "--print": true},
	"copilot": {"-p": true, "--prompt": true},
}

func looksLikePipeMode(cmdline string) bool {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return false
	}

	// Require an argv field whose basename is exactly "claude" or "copilot"
	// (so claude-lint and /opt/claude-tools/foo do NOT match) and a pipe flag
	// in the fields *after* it (so an unrelated -p earlier in the cmdline
	// doesn't trigger).
	for i, field := range fields {
		flags, ok := pipeFlagsByCommand[filepath.Base(field)]
		if !ok {
			continue
		}
		for _, after := range fields[i+1:] {
			if flags[after] {
				return true
			}
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
	ctx, cancel := context.WithTimeout(context.Background(), psCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// psParentPID returns the parent PID of pid using `ps`.
func psParentPID(pid int) int {
	ctx, cancel := context.WithTimeout(context.Background(), psCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
	if err != nil {
		return -1
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return -1
	}
	return p
}
