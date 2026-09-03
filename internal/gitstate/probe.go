package gitstate

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"time"
)

const (
	// ProbeTimeout bounds one git invocation. It matches worktree's
	// gitProbeTimeout (internal/worktree/worktree.go) on purpose: both are the
	// "answer now or not at all" budget for a probe whose result is decoration,
	// and a networked or hung filesystem must cost a bounded wait rather than a
	// wedged sweep.
	ProbeTimeout = 2 * time.Second

	// maxProbeOutput bounds how much of git's stdout a probe keeps. Both
	// answers are small: a branch is one short line, and the dirty bit is
	// settled by the FIRST BYTE of status output. A repository with ten
	// thousand modified files must not cost ten thousand lines of memory to
	// learn "dirty", so stdout is capped rather than parsed.
	maxProbeOutput = 4096

	// detachedHEAD is what rev-parse --abbrev-ref prints when HEAD points at a
	// commit rather than a branch. There is no branch to report, so it reads as
	// an empty branch like every other unavailable answer.
	detachedHEAD = "HEAD"
)

// Executor runs one git command in directory under timeout and returns its
// standard output. It is the injectable runner seam, and it deliberately wears
// the same shape as worktree's gitExecutor
// (internal/worktree/inventory.go) — including the timeout as an explicit
// parameter rather than baked into the context — so both git callers in this
// repository look alike to a reader and to a test.
type Executor func(ctx context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error)

// State is one directory's advisory Git facts. The zero value is what every
// unavailable answer looks like: no branch, not dirty.
type State struct {
	// Branch is the checked-out branch name, or empty when there is none to
	// report — a non-repository, an absent git, a timeout, and a detached HEAD
	// are all spelled the same way on purpose.
	Branch string
	// Dirty reports tracked-file modifications only. Untracked files are
	// excluded because a build artifact nobody has gitignored yet is not a
	// reason to mark a session's row.
	Dirty bool
}

// Probe reads directory's branch and dirty bit through git. It never returns an
// error: every failure resolves to the zero value for the fact it could not
// establish, which is the package's fail-open-and-silent contract.
//
// The two facts are probed INDEPENDENTLY and neither gates the other. A
// detached HEAD still reports its dirty bit, because this package reports what
// it saw and lets the caller decide what is worth showing.
func Probe(ctx context.Context, git Executor, directory string, timeout time.Duration) State {
	if git == nil {
		return State{}
	}
	var state State
	if out, err := git(ctx, directory, timeout, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		state.Branch = branchFromRevParse(out)
	}
	// The first byte of `status --porcelain` output is the whole answer, so the
	// result is measured rather than parsed. runGit already bounds how much of
	// it was kept.
	if out, err := git(ctx, directory, timeout, "status", "--porcelain", "--untracked-files=no"); err == nil {
		state.Dirty = len(bytes.TrimSpace(out)) > 0
	}
	return state
}

// branchFromRevParse reduces rev-parse output to a branch name. Anything that
// is not exactly one non-HEAD line is treated as "no branch", so unexpected
// output degrades the same way a failed command does.
func branchFromRevParse(out []byte) string {
	line := strings.TrimSpace(string(out))
	if line == "" || line == detachedHEAD || strings.ContainsAny(line, "\n\r") {
		return ""
	}
	return line
}

// runGit is the production Executor. It shells out to git in directory with a
// per-command deadline, discards stderr (nothing here reports a git error to a
// user), and keeps at most maxProbeOutput bytes of stdout.
//
// --no-optional-locks is not cosmetic: `git status` would otherwise take the
// index lock and write a refreshed index, which is exactly the cost this probe
// must not impose on a slow or networked filesystem — the case the
// [defaults].git_probe toggle exists for.
func runGit(ctx context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{"--no-optional-locks", "-C", directory}, args...)
	cmd := exec.CommandContext(commandCtx, "git", full...)
	stdout := &boundedBuffer{limit: maxProbeOutput}
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// boundedBuffer accepts every write so the child process never blocks or sees a
// broken pipe, but retains only the first limit bytes.
type boundedBuffer struct {
	limit int
	buf   bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if remaining := b.limit - b.buf.Len(); remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		b.buf.Write(p[:remaining])
	}
	return len(p), nil
}

func (b *boundedBuffer) Bytes() []byte { return b.buf.Bytes() }
