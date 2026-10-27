package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// branchOperation reports an operation that still owns ref while HEAD may be
// detached. Only a missing state file proves absence: a failed Git probe or
// unreadable file must not grant permission to update or delete the branch.
func branchOperation(ctx context.Context, git gitExecutor, path, ref string) (string, error) {
	for _, state := range []struct {
		path      string
		operation string
	}{
		{"rebase-merge/head-name", "rebase"},
		{"rebase-apply/head-name", "rebase"},
		{"BISECT_START", "bisect"},
	} {
		file, err := gitLineWith(ctx, git, path, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--git-path", state.path)
		if err != nil {
			return "", fmt.Errorf("%w: locating %s state in %s: %w", ErrGitUnavailable, state.operation, path, err)
		}
		data, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("%w: reading %s state in %s: %w", ErrGitUnavailable, state.operation, path, err)
		}
		name := strings.TrimSpace(string(data))
		// Rebase records a full ref; bisect records the short branch name
		// (or a commit when it began with an already detached HEAD).
		if name == ref || (state.operation == "bisect" && name == strings.TrimPrefix(ref, "refs/heads/")) {
			return state.operation, nil
		}
	}
	return "", nil
}
