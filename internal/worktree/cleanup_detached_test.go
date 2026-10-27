package worktree

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestRemovePreservesDetachedWork(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"unreferenced commit", "paused rebase"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			_, inventory, entry := reviewFixture(t)
			if state == "unreferenced commit" {
				gitRun(t, entry.Path, "checkout", "--detach")
				commitFile(t, entry.Path, "work.txt", "keep this work\n", "detached work")
				if refs := gitOutput(t, entry.Path, "for-each-ref", "--contains=HEAD", "--format=%(refname)"); refs != "" {
					t.Fatalf("expected commit reachable only through detached HEAD, found %s", refs)
				}
			} else {
				commitFile(t, entry.Path, "work.txt", "keep this work\n", "work")
				if err := execGit(entry.Path, "rebase", "--exec", "false", "HEAD~1"); err == nil {
					t.Fatal("expected rebase to stop at the failing exec")
				}
				t.Cleanup(func() { _ = execGit(entry.Path, "rebase", "--abort") })
			}
			if status := gitOutput(t, entry.Path, "status", "--porcelain"); status != "" {
				t.Fatalf("expected a clean checkout, got %s", status)
			}
			before := revision(t, entry.Path, "HEAD")
			result, err := inventory.Remove(context.Background(), entry.ID, RemoveOptions{})
			if !errors.Is(err, ErrWorktreeDetached) || result.CheckoutRemoved || result.MetadataForgotten {
				t.Errorf("Remove = %+v, %v; want detached checkout preserved", result, err)
			}
			for _, path := range []string{entry.Path, entry.MetadataPath} {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("lost %s: %v", path, err)
				}
			}
			if got := revision(t, entry.Path, "HEAD"); got != before {
				t.Errorf("HEAD = %s, want %s", got, before)
			}
			if state == "paused rebase" {
				gitRun(t, entry.Path, "rebase", "--continue")
			}
		})
	}
}

func TestCleanupRechecksDetachedHead(t *testing.T) {
	t.Parallel()
	_, root, _, created := cleanupFixture(t)
	inventory := testInventory(root)
	selected, err := inventory.Resolve(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Detached {
		t.Fatal("expected the inventory snapshot to have an attached HEAD")
	}
	gitRun(t, selected.Path, "checkout", "--detach")
	root, err = inventory.existingMutationRoot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inventory.revalidatePresentCleanup(context.Background(), root, selected); !errors.Is(err, ErrWorktreeDetached) {
		t.Fatalf("preflight with stale attached snapshot = %v; want ErrWorktreeDetached", err)
	}
}
