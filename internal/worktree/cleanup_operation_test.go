package worktree

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRemovePreservesCleanGitOperations(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"cherry-pick", "merge", "bisect"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			repo, inventory, entry := reviewFixture(t)
			gitRun(t, entry.Path, "commit", "--allow-empty", "-m", "work")
			var state string
			switch operation {
			case "cherry-pick":
				if err := execGit(entry.Path, "cherry-pick", "HEAD"); err == nil {
					t.Fatal("expected cherry-pick to pause on an empty commit")
				}
				state = "CHERRY_PICK_HEAD"
			case "merge":
				gitRun(t, repo, "commit", "--allow-empty", "-m", "source work")
				gitRun(t, entry.Path, "merge", "--no-ff", "--no-commit", entry.SourceBranch)
				state = "MERGE_HEAD"
			case "bisect":
				gitRun(t, entry.Path, "bisect", "start", "--no-checkout", "HEAD", "HEAD~1")
				state = "BISECT_START"
			}
			statePath := strings.TrimSpace(gitOutput(t, entry.Path, "rev-parse", "--path-format=absolute", "--git-path", state))
			before, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if status := gitOutput(t, entry.Path, "status", "--porcelain"); status != "" {
				t.Fatalf("expected clean status during %s, got %s", operation, status)
			}
			result, err := inventory.Remove(context.Background(), entry.ID, RemoveOptions{})
			if !errors.Is(err, ErrWorktreeBusy) || result.CheckoutRemoved || result.MetadataForgotten {
				t.Errorf("Remove = %+v, %v; want unfinished %s preserved", result, err, operation)
			}
			if after, err := os.ReadFile(statePath); err != nil || string(after) != string(before) {
				t.Errorf("lost %s state: %q, %v", operation, after, err)
			}
			if _, err := os.Stat(entry.MetadataPath); err != nil {
				t.Errorf("lost metadata: %v", err)
			}
			if t.Failed() {
				return
			}
			if operation == "bisect" {
				gitRun(t, entry.Path, "bisect", "reset")
			} else {
				gitRun(t, entry.Path, operation, "--abort")
			}
			result, err = inventory.Remove(context.Background(), entry.ID, RemoveOptions{})
			if err != nil || !result.CheckoutRemoved || !result.MetadataForgotten {
				t.Errorf("Remove after abort = %+v, %v; want cleanup to succeed", result, err)
			}
		})
	}
}

func TestLandRefusesUnfinishedWorktreeOperation(t *testing.T) {
	t.Parallel()
	repo, inventory, entry := reviewFixture(t)
	commitFile(t, entry.Path, "work.txt", "work\n", "work")
	if err := execGit(entry.Path, "cherry-pick", "HEAD"); err == nil {
		t.Fatal("expected cherry-pick to pause on an already applied commit")
	}
	before := revision(t, repo, "HEAD")
	result, err := inventory.Land(context.Background(), entry.ID)
	if !errors.Is(err, ErrWorktreeBusy) || result.Landed {
		t.Errorf("Land = %+v, %v; want unfinished operation refused", result, err)
	}
	if after := revision(t, repo, "HEAD"); after != before {
		t.Errorf("landing moved source from %s to %s", before, after)
	}
	gitRun(t, entry.Path, "cherry-pick", "--abort")
	result, err = inventory.Land(context.Background(), entry.ID)
	if err != nil || !result.Landed {
		t.Errorf("Land after abort = %+v, %v; want success", result, err)
	}
}

func TestRemoveRefusesUnavailableCheckoutOperationState(t *testing.T) {
	t.Parallel()
	_, inventory, entry := reviewFixture(t)
	git := inventory.git
	probed := false
	inventory.git = func(ctx context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "rev-parse --absolute-git-dir" {
			probed = true
			return nil, errors.New("Git state probe failed")
		}
		return git(ctx, directory, timeout, args...)
	}
	result, err := inventory.Remove(context.Background(), entry.ID, RemoveOptions{DeleteBranch: true})
	if !probed || !errors.Is(err, ErrGitUnavailable) || result.CheckoutRemoved || result.BranchDeleted || result.MetadataForgotten {
		t.Fatalf("Remove = %+v, %v; want unavailable-state refusal (probed=%t)", result, err, probed)
	}
	for _, path := range []string{entry.Path, entry.MetadataPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("lost %s: %v", path, err)
		}
	}
}
