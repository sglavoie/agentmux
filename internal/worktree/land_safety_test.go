package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLandPreservesIgnoredFilesInSourceCheckout(t *testing.T) {
	t.Parallel()
	repo, inventory, entry := reviewFixture(t)
	commitFile(t, entry.Path, "local.txt", "committed content\n", "feature")
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte("local.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(repo, "local.txt")
	const content = "local content that must survive\n"
	if err := os.WriteFile(local, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	before := revision(t, repo, "HEAD")
	result, err := inventory.Land(context.Background(), entry.ID)
	if err == nil || result.Landed {
		t.Errorf("Land succeeded despite an ignored file collision: %+v, %v", result, err)
	}
	if got, err := os.ReadFile(local); err != nil || string(got) != content {
		t.Errorf("local content lost: %q, %v", got, err)
	}
	if got := revision(t, repo, "HEAD"); got != before {
		t.Errorf("source moved from %s to %s", before, got)
	}
}

func TestLandOverridesSourceBranchSquashPreference(t *testing.T) {
	t.Parallel()
	repo, inventory, entry := reviewFixture(t)
	commitFile(t, entry.Path, "feature.txt", "feature\n", "feature")
	gitRun(t, repo, "config", "branch."+entry.SourceBranch+".mergeOptions", "--squash")
	tip := revision(t, entry.Path, "HEAD")
	result, err := inventory.Land(context.Background(), entry.ID)
	if err != nil || !result.Landed {
		t.Fatalf("Land = %+v, %v", result, err)
	}
	if got := revision(t, repo, "HEAD"); got != tip {
		t.Errorf("Land reported success but source HEAD = %s, want %s", got, tip)
	}
	if status := gitOutput(t, repo, "status", "--porcelain"); status != "" {
		t.Errorf("Land left uncommitted changes: %s", status)
	}
}

func TestLandUsesTheCommitValidatedBeforeTheBranchMoves(t *testing.T) {
	t.Parallel()
	repo, inventory, entry := reviewFixture(t)
	commitFile(t, entry.Path, "feature.txt", "reviewed\n", "feature")
	tip := revision(t, entry.Path, "HEAD")
	git := inventory.git
	moved := false
	inventory.git = func(ctx context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error) {
		if gitSubcommand(args) == "merge" && !moved {
			moved = true
			commitFile(t, entry.Path, "later.txt", "not reviewed\n", "later")
		}
		return git(ctx, directory, timeout, args...)
	}
	result, err := inventory.Land(context.Background(), entry.ID)
	if err != nil || !result.Landed || !moved {
		t.Fatalf("Land = %+v, %v; branch moved = %t", result, err, moved)
	}
	if got := revision(t, repo, "HEAD"); got != tip || got != result.To {
		t.Errorf("landed %s, want validated %s and reported %s", got, tip, result.To)
	}
}
