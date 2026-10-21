package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func revision(t *testing.T, directory, rev string) string {
	t.Helper()
	return strings.TrimSpace(gitOutput(t, directory, "rev-parse", rev))
}

func TestLandFastForwardsTheSourceCheckoutAndLeavesTheWorktreeAlone(t *testing.T) {
	t.Parallel()

	repo, inventory, entry := reviewFixture(t)
	commitFile(t, entry.Path, "feature.txt", "one\n", "feature")
	commitFile(t, entry.Path, "more.txt", "two\n", "more")
	before := revision(t, repo, "HEAD")
	tip := revision(t, entry.Path, "HEAD")

	result, err := inventory.Land(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	canonicalRepo, _ := canonicalExistingPath(repo)
	if !result.Landed || result.Commits != 2 || result.From != before || result.To != tip || result.SourceCheckout != canonicalRepo {
		t.Fatalf("result = %+v, want two commits landed in %s", result, canonicalRepo)
	}
	if got := revision(t, repo, "HEAD"); got != tip {
		t.Fatalf("source HEAD = %s, want %s", got, tip)
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Fatalf("source checkout did not receive the landed file: %v", err)
	}
	if status := gitOutput(t, repo, "status", "--porcelain"); status != "" {
		t.Fatalf("source checkout is dirty after landing: %q", status)
	}
	if _, err := os.Stat(entry.MetadataPath); err != nil {
		t.Fatalf("landing touched the sidecar: %v", err)
	}
	if got := revision(t, entry.Path, "HEAD"); got != tip {
		t.Fatalf("worktree HEAD moved to %s", got)
	}

	// What landed is provably merged, so the cleanup that follows may now
	// delete the branch.
	removed, err := inventory.Remove(context.Background(), entry.ID, RemoveOptions{DeleteBranch: true})
	if err != nil || !removed.CheckoutRemoved || !removed.BranchDeleted {
		t.Fatalf("Remove after Land = %+v, err = %v", removed, err)
	}

}

func TestLandAdvancesASourceBranchNoWorktreeHasCheckedOut(t *testing.T) {
	t.Parallel()

	repo, inventory, entry := reviewFixture(t)
	source := entry.SourceBranch
	gitRun(t, repo, "checkout", "-b", "elsewhere")
	commitFile(t, entry.Path, "feature.txt", "one\n", "feature")
	tip := revision(t, entry.Path, "HEAD")

	result, err := inventory.Land(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	if !result.Landed || result.SourceCheckout != "" || result.Commits != 1 {
		t.Fatalf("result = %+v, want the branch alone advanced", result)
	}
	if got := revision(t, repo, "refs/heads/"+source); got != tip {
		t.Fatalf("%s = %s, want %s", source, got, tip)
	}
	if got := revision(t, repo, "HEAD"); got == tip {
		t.Fatal("the checkout of another branch moved")
	}
}

func TestLandRefusesWithoutMovingAnything(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		arrange func(t *testing.T, repo string, entry Entry)
		want    error
	}{
		"nothing to land": {func(*testing.T, string, Entry) {}, ErrNothingToLand},
		"source moved on": {func(t *testing.T, repo string, entry Entry) {
			commitFile(t, entry.Path, "feature.txt", "one\n", "feature")
			commitFile(t, repo, "upstream.txt", "theirs\n", "upstream")
		}, ErrLandDiverged},
		"dirty worktree": {func(t *testing.T, _ string, entry Entry) {
			commitFile(t, entry.Path, "feature.txt", "one\n", "feature")
			if err := os.WriteFile(filepath.Join(entry.Path, "feature.txt"), []byte("edited\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, ErrWorktreeDirty},
		"dirty source checkout": {func(t *testing.T, repo string, entry Entry) {
			commitFile(t, entry.Path, "feature.txt", "one\n", "feature")
			if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# edited\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, ErrSourceDirty},
		"source being rebased": {func(t *testing.T, repo string, entry Entry) {
			commitFile(t, entry.Path, "feature.txt", "one\n", "feature")
			// A rebase stopped at a failing exec leaves the source branch
			// where it was, HEAD detached, and its name in the rebase state.
			_ = execGit(repo, "rebase", "--exec", "false", "HEAD~1")
		}, ErrSourceBusy},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo := testRepository(t)
			commitFile(t, repo, "second.txt", "2\n", "second")
			root := filepath.Join(t.TempDir(), "worktrees")
			workflow := testWorkflow(root)
			created, err := workflow.Create(context.Background(), Request{ProjectLabel: "api", SourceCWD: repo, SessionID: "land-source"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = execGit(repo, "rebase", "--abort")
				_ = workflow.RemoveUnused(context.Background(), created)
			})
			inventory := testInventory(root)
			entry, err := inventory.Resolve(context.Background(), created.ID)
			if err != nil {
				t.Fatal(err)
			}
			tc.arrange(t, repo, entry)
			sourceBefore := revision(t, repo, "refs/heads/"+entry.SourceBranch)
			branchBefore := revision(t, entry.Path, "HEAD")

			result, err := inventory.Land(context.Background(), entry.ID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Land error = %v, want %v", err, tc.want)
			}
			if result.Landed {
				t.Fatalf("result = %+v, want nothing landed", result)
			}
			if got := revision(t, repo, "refs/heads/"+entry.SourceBranch); got != sourceBefore {
				t.Fatalf("source branch moved from %s to %s", sourceBefore, got)
			}
			if got := revision(t, entry.Path, "HEAD"); got != branchBefore {
				t.Fatalf("worktree branch moved from %s to %s", branchBefore, got)
			}
		})
	}
}
