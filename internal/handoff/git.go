package handoff

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

type commandGitInspector struct{}

func (commandGitInspector) Inspect(ctx context.Context, cwd string, timeout time.Duration) GitState {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(probeCtx, "git", append([]string{"-C", cwd}, args...)...)
		cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0")
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(out), "\r\n"), nil
	}

	inside, err := run("rev-parse", "--is-inside-work-tree")
	if err != nil || inside != "true" {
		return unavailableGit(probeCtx, err, "non-git", "working directory is not a Git worktree")
	}
	branch, err := run("branch", "--show-current")
	if err != nil {
		return unavailableGit(probeCtx, err, "error", "Git branch inspection failed")
	}
	if branch == "" {
		branch = "(detached HEAD)"
	}
	head, err := run("rev-parse", "HEAD")
	if err != nil {
		return unavailableGit(probeCtx, err, "error", "Git HEAD inspection failed")
	}
	status, err := run("status", "--short")
	if err != nil {
		return unavailableGit(probeCtx, err, "error", "Git status inspection failed")
	}
	diffStat, err := run("diff", "--stat", "--no-ext-diff", "HEAD")
	if err != nil {
		return unavailableGit(probeCtx, err, "error", "Git diff-stat inspection failed")
	}
	return GitState{Available: true, Kind: "git", Branch: branch, Head: head, Status: status, DiffStat: diffStat}
}

func unavailableGit(ctx context.Context, err error, kind, detail string) GitState {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return GitState{Kind: "timeout", Detail: "Git inspection timed out"}
	}
	if errors.Is(err, exec.ErrNotFound) {
		return GitState{Kind: "unavailable", Detail: "git executable is unavailable"}
	}
	return GitState{Kind: kind, Detail: detail}
}
