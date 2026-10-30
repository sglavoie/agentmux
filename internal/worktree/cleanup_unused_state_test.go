package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveUnusedPreservesUntrackedFilesHiddenByGitConfig(t *testing.T) {
	t.Parallel()
	repo, _, workflow, created := cleanupFixture(t)
	gitRun(t, repo, "config", "status.showUntrackedFiles", "no")
	path := filepath.Join(created.Path, "unfinished.txt")
	if err := os.WriteFile(path, []byte("keep this work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := workflow.RemoveUnused(context.Background(), created); !errors.Is(err, ErrWorktreeDirty) {
		t.Errorf("RemoveUnused = %v; want ErrWorktreeDirty", err)
	}
	if contents, err := os.ReadFile(path); err != nil || string(contents) != "keep this work\n" {
		t.Errorf("cleanup lost untracked work: %q, %v", contents, err)
	}
}

func TestRemoveUnusedPreservesDetachedWork(t *testing.T) {
	t.Parallel()
	for _, missing := range []bool{false, true} {
		name := "present"
		if missing {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo, _, workflow, created := cleanupFixture(t)
			gitRun(t, created.Path, "checkout", "--detach")
			commitFile(t, created.Path, "detached.txt", "keep this work\n", "detached work")
			head := revision(t, created.Path, "HEAD")
			if missing {
				if err := os.RemoveAll(created.Path); err != nil {
					t.Fatal(err)
				}
			}
			if err := workflow.RemoveUnused(context.Background(), created); !errors.Is(err, ErrWorktreeDetached) {
				t.Errorf("RemoveUnused = %v; want ErrWorktreeDetached", err)
			}
			listing := gitOutput(t, repo, "worktree", "list", "--porcelain")
			if !strings.Contains(listing, "HEAD "+head) {
				t.Errorf("cleanup lost detached HEAD %s: %s", head, listing)
			}
			if _, err := os.Stat(created.MetadataPath); err != nil {
				t.Errorf("cleanup lost recovery metadata: %v", err)
			}
		})
	}
}

func TestRemoveUnusedPreservesCleanGitOperation(t *testing.T) {
	t.Parallel()
	_, _, workflow, created := cleanupFixture(t)
	// Cherry-picking the creation commit pauses because it is already applied,
	// without moving the branch or dirtying the checkout.
	if err := execGit(created.Path, "cherry-pick", "HEAD"); err == nil {
		t.Fatal("expected cherry-pick to pause")
	}
	state := strings.TrimSpace(gitOutput(t, created.Path, "rev-parse", "--path-format=absolute", "--git-path", "CHERRY_PICK_HEAD"))
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := workflow.RemoveUnused(context.Background(), created); !errors.Is(err, ErrWorktreeBusy) {
		t.Errorf("RemoveUnused = %v; want ErrWorktreeBusy", err)
	}
	if after, err := os.ReadFile(state); err != nil || string(after) != string(before) {
		t.Errorf("cleanup lost cherry-pick state: %q, %v", after, err)
	}
	if _, err := os.Stat(created.MetadataPath); err != nil {
		t.Errorf("cleanup lost recovery metadata: %v", err)
	}
	if t.Failed() {
		return
	}
	gitRun(t, created.Path, "cherry-pick", "--abort")
	if err := workflow.RemoveUnused(context.Background(), created); err != nil {
		t.Errorf("RemoveUnused after abort: %v", err)
	}
}
