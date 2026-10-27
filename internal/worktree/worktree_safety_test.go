package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateRefusesUntrackedFilesHiddenByGitConfig(t *testing.T) {
	t.Parallel()
	repo := testRepository(t)
	gitRun(t, repo, "config", "status.showUntrackedFiles", "no")
	if err := os.WriteFile(filepath.Join(repo, "unfinished.txt"), []byte("local work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workflow := testWorkflow(filepath.Join(t.TempDir(), "worktrees"))
	_, err := workflow.Create(context.Background(), Request{SourceCWD: repo, SessionID: "source-1"})
	if !errors.Is(err, ErrDirtySource) {
		t.Fatalf("Create = %v; want ErrDirtySource even when Git hides untracked files", err)
	}
}

func TestLandRefusesUntrackedFilesHiddenByGitConfig(t *testing.T) {
	t.Parallel()
	repo, inventory, entry := reviewFixture(t)
	commitFile(t, entry.Path, "feature.txt", "feature\n", "feature")
	gitRun(t, repo, "config", "status.showUntrackedFiles", "no")
	if err := os.WriteFile(filepath.Join(entry.Path, "unfinished.txt"), []byte("local work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	listed, err := inventory.Resolve(context.Background(), entry.ID)
	if err != nil || !listed.Dirty {
		t.Errorf("Resolve = %+v, %v; want dirty worktree", listed, err)
	}
	before := revision(t, repo, "HEAD")
	result, err := inventory.Land(context.Background(), entry.ID)
	if !errors.Is(err, ErrWorktreeDirty) || result.Landed {
		t.Errorf("Land = %+v, %v; want ErrWorktreeDirty", result, err)
	}
	if after := revision(t, repo, "HEAD"); after != before {
		t.Errorf("dirty landing advanced source from %s to %s", before, after)
	}
}

func TestCreateRefusesSubmoduleChangesHiddenByGitConfig(t *testing.T) {
	t.Parallel()
	repo, child := testRepository(t), testRepository(t)
	gitRun(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", child, "child")
	gitRun(t, repo, "commit", "-am", "add submodule")
	gitRun(t, repo, "config", "submodule.child.ignore", "all")
	if err := os.WriteFile(filepath.Join(repo, "child", "README.md"), []byte("unfinished\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workflow := testWorkflow(filepath.Join(t.TempDir(), "worktrees"))
	_, err := workflow.Create(context.Background(), Request{SourceCWD: repo, SessionID: "source-1"})
	if !errors.Is(err, ErrDirtySource) {
		t.Fatalf("Create = %v; want ErrDirtySource even when Git hides submodule changes", err)
	}
}

func TestRemoveUnusedRetainsMetadataWhenGitCleanupFails(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"checkout", "branch"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			repo := testRepository(t)
			root := filepath.Join(t.TempDir(), "worktrees")
			workflow := testWorkflow(root)
			created, err := workflow.Create(context.Background(), Request{SourceCWD: repo, SessionID: "source-1"})
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(created.MetadataPath)
			if err != nil {
				t.Fatal(err)
			}
			var unblock func()
			if failure == "checkout" {
				gitRun(t, repo, "worktree", "lock", created.Path)
				unblock = func() { gitRun(t, repo, "worktree", "unlock", created.Path) }
			} else {
				lock := filepath.Join(repo, ".git", "refs", "heads", created.Branch+".lock")
				if err := os.WriteFile(lock, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				unblock = func() {
					if err := os.Remove(lock); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := workflow.RemoveUnused(context.Background(), created); err == nil {
				t.Fatal("RemoveUnused succeeded despite a Git lock")
			}
			if after, err := os.ReadFile(created.MetadataPath); err != nil || string(after) != string(before) {
				t.Errorf("failed cleanup lost recovery metadata: %q, %v", after, err)
			}
			unblock()
			// Both a retained checkout and a retained branch must remain
			// recoverable through the ordinary managed-worktree cleanup.
			result, err := testInventory(root).Remove(context.Background(), created.ID, RemoveOptions{DeleteBranch: true})
			if err != nil || !result.BranchDeleted || !result.MetadataForgotten {
				t.Errorf("retry = %+v, %v; want complete cleanup", result, err)
			}
		})
	}
}

func TestCreatePreservesRetainedMetadataOnNameCollision(t *testing.T) {
	t.Parallel()
	repo := testRepository(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	workflow := testWorkflow(root)
	request := Request{ProjectLabel: "api", SourceCWD: repo, SessionID: "source-1"}
	first, err := workflow.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(first.MetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "worktree", "remove", first.Path)
	gitRun(t, repo, "branch", "-D", first.Branch)
	second, err := workflow.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(second.Path, "-2") || !strings.HasSuffix(second.Branch, "-2") {
		t.Errorf("reused a name with retained metadata: %+v", second)
	}
	if after, err := os.ReadFile(first.MetadataPath); err != nil || string(after) != string(before) {
		t.Errorf("creation overwrote retained metadata: %q, %v", after, err)
	}
}
