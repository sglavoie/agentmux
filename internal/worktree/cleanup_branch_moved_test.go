package worktree

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestRemoveUnusedKeepsCheckoutWhenBranchGainedACommit(t *testing.T) {
	t.Parallel()
	repo, _, workflow, created := cleanupFixture(t)
	commitFile(t, created.Path, "new-work.txt", "preserve this\n", "new work")
	advanced := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "refs/heads/"+created.Branch))
	if advanced == created.BaseCommit {
		t.Fatal("branch did not advance")
	}
	if err := workflow.RemoveUnused(context.Background(), created); !errors.Is(err, ErrUnusedBranchMoved) {
		t.Fatalf("RemoveUnused = %v; want ErrUnusedBranchMoved", err)
	}
	if got := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "refs/heads/"+created.Branch)); got != advanced {
		t.Errorf("branch tip = %s, want %s", got, advanced)
	}
	for _, path := range []string{created.Path, created.MetadataPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("cleanup lost %s: %v", path, err)
		}
	}
}

func TestDeleteUnusedBranchKeepsTipThatMovedAfterPreflight(t *testing.T) {
	t.Parallel()
	repo, _, _, created := cleanupFixture(t)
	branch, ref, err := unusedBranchAtBase(context.Background(), created)
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "worktree", "remove", created.Path)
	commitFile(t, repo, "later.txt", "later\n", "later source commit")
	advanced := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "HEAD"))
	gitRun(t, repo, "update-ref", ref, advanced, created.BaseCommit)

	if err := deleteUnusedBranch(context.Background(), repo, branch, ref, created.BaseCommit); err == nil {
		t.Fatal("conditional deletion removed a branch that moved after preflight")
	}
	if got := strings.TrimSpace(gitOutput(t, repo, "rev-parse", ref)); got != advanced {
		t.Errorf("branch tip = %s, want %s", got, advanced)
	}
	if _, err := os.Stat(created.MetadataPath); err != nil {
		t.Errorf("cleanup lost recovery metadata: %v", err)
	}
}
