package indicator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
)

// psCommandTimeout bounds each `ps` invocation used during process-tree
// walking. The indicator runs synchronously inside every hook invocation, so
// a wedged or paused `ps` (e.g. swapped out under heavy load, audit-locked
// on macOS) must surface as a missing parent rather than hang the hook
// script. Exposed as a var so tests can shrink it without sleeping.
var psCommandTimeout = 2 * time.Second

// IsPipeMode reports whether the current process tree contains a non-interactive
// harness invocation such as `claude -p`, `copilot --prompt`, or `codex exec`.
// When true, desktop notifications should be suppressed.
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

	// Require an argv field whose basename is exactly a supported harness
	// executable (so claude-lint and /opt/codex-tools/foo do NOT match) and a
	// pipe-mode token in the fields *after* it (so an unrelated token earlier
	// in the cmdline doesn't trigger). Scanning stops at the POSIX `--` option
	// terminator so `claude -- -p` (where `-p` is a positional argument) and
	// `codex -- review` do not falsely trigger pipe mode.
	for i, field := range fields {
		base := commandBase(field)
		if !harness.Valid(base) {
			continue
		}
		tokens := harness.Lookup(base).PipeModeTokens()
		if len(tokens) == 0 {
			continue
		}
		tokenSet := make(map[string]bool, len(tokens))
		for _, token := range tokens {
			tokenSet[token] = true
		}
		for _, after := range fields[i+1:] {
			if after == "--" {
				break
			}
			if tokenSet[after] {
				return true
			}
		}
	}
	return false
}

func commandBase(field string) string {
	field = strings.Trim(strings.TrimSpace(field), `"'`)
	base := filepath.Base(field)
	if i := strings.LastIndex(base, `\`); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".exe")
}

// looksLikeClaudePipe is kept as a compatibility alias for older tests while
// the implementation now supports Claude, Copilot, and Codex non-interactive runs.
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
