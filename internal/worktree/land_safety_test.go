package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLandRefusesASourceCheckoutThatChangedBranches(t *testing.T) {
	t.Parallel()
	for _, detached := range []bool{false, true} {
		t.Run("detached="+strconv.FormatBool(detached), func(t *testing.T) {
			t.Parallel()
			repo, inventory, entry := reviewFixture(t)
			commitFile(t, entry.Path, "feature.txt", "feature\n", "feature")
			before := revision(t, repo, "HEAD")
			git := inventory.git
			armed, switched := false, false
			inventory.git = func(ctx context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error) {
				out, err := git(ctx, directory, timeout, args...)
				if strings.Join(args, " ") == "rev-parse --verify refs/heads/"+entry.SourceBranch+"^{commit}" {
					armed = true
				}
				if armed && !switched && gitSubcommand(args) == "worktree" && err == nil {
					switched = true
					if detached {
						gitRun(t, repo, "checkout", "--detach")
					} else {
						gitRun(t, repo, "checkout", "-b", "elsewhere")
					}
				}
				return out, err
			}
			result, err := inventory.Land(context.Background(), entry.ID)
			if !switched || !errors.Is(err, ErrGitUnavailable) || result.Landed {
				t.Errorf("Land = %+v, %v; switched = %t; want refusal", result, err, switched)
			}
			for _, ref := range []string{"HEAD", "refs/heads/" + entry.SourceBranch} {
				if got := revision(t, repo, ref); got != before {
					t.Errorf("%s moved from %s to %s", ref, before, got)
				}
			}
		})
	}
}

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
