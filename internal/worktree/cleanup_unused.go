package worktree

import (
	"context"
	"fmt"
)

// The creation branch can still be at its base while the checkout has detached
// and gained commits of its own. Check Git's registration even when the files
// are missing: unregistering also deletes the checkout's HEAD and reflog.
func revalidateUnusedCheckout(ctx context.Context, result Result, branch string) error {
	porcelain, err := runGit(ctx, result.SourceRoot, gitProbeTimeout, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return fmt.Errorf("%w: listing unused checkout: %w", ErrGitUnavailable, err)
	}
	records, err := parseWorktreePorcelain(porcelain)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrGitUnavailable, err)
	}
	target, err := canonicalPath(result.Path)
	if err != nil {
		return fmt.Errorf("%w: resolving unused checkout: %w", ErrGitUnavailable, err)
	}
	for _, record := range records {
		path, err := canonicalPath(record.Path)
		if err != nil || path != target {
			continue
		}
		if record.Detached || record.Branch == "" {
			return ErrWorktreeDetached
		}
		if branch != "" && record.Branch != branch {
			return fmt.Errorf("%w: unused checkout changed branches", ErrMetadataInvalid)
		}
		if result.BaseCommit != "" && record.HEAD != result.BaseCommit {
			return fmt.Errorf("%w: unused checkout HEAD changed", ErrUnusedBranchMoved)
		}
		if record.Locked {
			return ErrWorktreeLocked
		}
		return nil
	}
	return fmt.Errorf("%w: unused checkout is no longer registered", ErrGitUnavailable)
}
