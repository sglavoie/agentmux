package worktree

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestRemovePreservesBranchWhenSourceMovesAfterMergeProof(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                   string
		missing, atTransaction bool
	}{
		{"present/after-proof", false, false},
		{"missing/after-proof", true, false},
		{"present/at-transaction", false, true},
		{"missing/at-transaction", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, inventory, entry := reviewFixture(t)
			base := revision(t, repo, "HEAD")
			commitFile(t, entry.Path, "feature.txt", "work to preserve\n", "feature")
			tip := revision(t, entry.Path, "HEAD")
			if _, err := inventory.Land(context.Background(), entry.ID); err != nil {
				t.Fatal(err)
			}
			if tc.missing {
				gitRun(t, repo, "worktree", "remove", entry.Path)
			}
			git := inventory.git
			proved, moved := false, false
			inventory.git = func(ctx context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error) {
				out, err := git(ctx, directory, timeout, args...)
				if gitSubcommand(args) == "merge-base" && err == nil {
					proved = true
				}
				if !tc.atTransaction && proved && !moved && gitSubcommand(args) == "worktree" {
					gitRun(t, repo, "reset", "--hard", base)
					moved = true
				}
				return out, err
			}
			if tc.atTransaction {
				inventory.gitInput = func(ctx context.Context, directory string, timeout time.Duration, input string, args ...string) ([]byte, error) {
					if !moved {
						gitRun(t, repo, "reset", "--hard", base)
						moved = true
					}
					return runGitInput(ctx, directory, timeout, input, args...)
				}
			}
			result, err := inventory.Remove(context.Background(), entry.ID, RemoveOptions{DeleteBranch: true})
			if !moved || err == nil || result.BranchDeleted || result.MetadataForgotten {
				t.Errorf("Remove = %+v, %v; source moved = %t; want branch and metadata preserved", result, err, moved)
			}
			if !tc.missing {
				var partial *PartialMutationError
				if !result.CheckoutRemoved || !errors.As(err, &partial) {
					t.Errorf("Remove = %+v, %v; want checkout-only partial cleanup", result, err)
				}
			}
			if exists, err := inventory.gitRefExists(context.Background(), repo, "refs/heads/"+entry.Branch); err != nil || !exists {
				t.Errorf("branch lost: exists=%t, %v", exists, err)
			} else if got := revision(t, repo, "refs/heads/"+entry.Branch); got != tip {
				t.Errorf("branch = %s, want %s", got, tip)
			}
			if _, err := os.Stat(entry.MetadataPath); err != nil {
				t.Errorf("recovery metadata lost: %v", err)
			}
			// Once the work is on the source again, the retained record must
			// let the operator finish cleanup without recreating a checkout.
			gitRun(t, repo, "reset", "--hard", tip)
			result, err = inventory.Remove(context.Background(), entry.ID, RemoveOptions{DeleteBranch: true})
			if err != nil || result.CheckoutRemoved || !result.BranchDeleted || !result.MetadataForgotten {
				t.Errorf("retry = %+v, %v; want branch and metadata cleanup", result, err)
			}
		})
	}
}
