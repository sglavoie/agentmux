package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// checkRemovalIndex refuses index flags that can hide local changes from both
// status and worktree remove. Do not clear these flags in the real index: the
// checkout may belong to an agent, and even a clean flagged file cannot be
// proven safe by status. Absent skip-worktree paths are normal in a sparse
// checkout and have no local content to lose.
func checkRemovalIndex(ctx context.Context, git gitExecutor, directory string) error {
	out, err := git(ctx, directory, gitProbeTimeout, "ls-files", "--cached", "-v", "-z")
	if err != nil {
		return fmt.Errorf("%w: checking checkout index flags: %w", ErrGitUnavailable, err)
	}
	for record := range bytes.SplitSeq(out, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		if len(record) < 3 || record[1] != ' ' {
			return fmt.Errorf("%w: unexpected checkout index flags", ErrGitUnavailable)
		}
		tag, path := record[0], string(record[2:])
		assumed := tag >= 'a' && tag <= 'z'
		skipped := tag == 'S' || tag == 's'
		if !assumed && !skipped {
			continue
		}
		if skipped {
			if _, err := os.Lstat(filepath.Join(directory, path)); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return fmt.Errorf("%w: inspecting skipped path %q: %w", ErrGitUnavailable, path, err)
			}
		}
		flag := "assume-unchanged"
		if skipped {
			flag = "skip-worktree"
		}
		return fmt.Errorf("%w: %q is marked %s, which can hide local changes; clear the index flag and inspect the file before cleanup", ErrWorktreeDirty, path, flag)
	}
	return nil
}
