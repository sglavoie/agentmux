package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func reviewFixture(t *testing.T) (repo string, inventory *Inventory, entry Entry) {
	t.Helper()
	repo, root, _, created := cleanupFixture(t)
	inventory = testInventory(root)
	entry, err := inventory.Resolve(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return repo, inventory, entry
}

func commitFile(t *testing.T, directory, name, content, message string) {
	t.Helper()
	gitRun(t, directory, "config", "user.email", "test@example.com")
	gitRun(t, directory, "config", "user.name", "Agent Mux Test")
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, directory, "add", name)
	gitRun(t, directory, "commit", "-m", message)
}

func TestReviewOfAnUntouchedWorktreeHasNothingToLand(t *testing.T) {
	t.Parallel()

	_, inventory, entry := reviewFixture(t)

	review, err := inventory.Review(context.Background(), entry, ReviewOptions{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if review.Lands != LandsNothing || review.Ahead != 0 || review.Behind != 0 || review.Dirty || len(review.Files) != 0 {
		t.Fatalf("review = %+v, want nothing to land", review)
	}
	if review.MergeBase != review.HEAD || review.SourceBranch != entry.SourceBranch {
		t.Fatalf("review = %+v, want merge base at HEAD of %q", review, entry.SourceBranch)
	}
}

func TestReviewRefreshesAStaleEntryByID(t *testing.T) {
	t.Parallel()

	_, inventory, entry := reviewFixture(t)
	wantPath, wantBranch, wantSource := entry.Path, entry.Branch, entry.SourceBranch
	entry.Path = filepath.Join(t.TempDir(), "different-checkout")
	entry.Branch = "stale-branch"
	entry.SourceBranch = "stale-source"
	entry.Exists = false

	review, err := inventory.Review(context.Background(), entry, ReviewOptions{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if review.Path != wantPath || review.Branch != wantBranch || review.SourceBranch != wantSource {
		t.Fatalf("review used stale entry facts: %+v", review)
	}
}

func TestReviewCoversCommittedUncommittedAndUntrackedWork(t *testing.T) {
	t.Parallel()

	_, inventory, entry := reviewFixture(t)
	commitFile(t, entry.Path, "feature.txt", "one\ntwo\n", "feature")
	if err := os.WriteFile(filepath.Join(entry.Path, "README.md"), []byte("# test\nedited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry.Path, "new file.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	review, err := inventory.Review(context.Background(), entry, ReviewOptions{Patch: true})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	wantFiles := []FileChange{{Path: "README.md", Added: 1}, {Path: "feature.txt", Added: 2}}
	if !reflect.DeepEqual(review.Files, wantFiles) || review.Added != 3 || review.Deleted != 0 {
		t.Fatalf("files = %+v (+%d -%d), want %+v", review.Files, review.Added, review.Deleted, wantFiles)
	}
	if !reflect.DeepEqual(review.Untracked, []string{"new file.txt"}) || !review.Dirty {
		t.Fatalf("untracked = %q dirty = %t, want the new file and dirty", review.Untracked, review.Dirty)
	}
	if review.Lands != LandsFastForward || review.Ahead != 1 || review.Behind != 0 {
		t.Fatalf("review = %+v, want a one-commit fast-forward", review)
	}
	if !strings.Contains(review.Patch, "+edited") || !strings.Contains(review.Patch, "+two") {
		t.Fatalf("patch misses committed or uncommitted work:\n%s", review.Patch)
	}

	committed, err := inventory.Review(context.Background(), entry, ReviewOptions{CommittedOnly: true, Patch: true})
	if err != nil {
		t.Fatalf("Review --committed: %v", err)
	}
	if !reflect.DeepEqual(committed.Files, []FileChange{{Path: "feature.txt", Added: 2}}) || len(committed.Untracked) != 0 {
		t.Fatalf("committed review = %+v, want the commit alone", committed)
	}
	if !committed.Dirty || strings.Contains(committed.Patch, "+edited") {
		t.Fatalf("committed review dirty = %t, patch:\n%s", committed.Dirty, committed.Patch)
	}
}

func TestReviewLeavesOutWhatTheSourceGainedMeanwhile(t *testing.T) {
	t.Parallel()

	repo, inventory, entry := reviewFixture(t)
	commitFile(t, entry.Path, "feature.txt", "one\n", "feature")
	commitFile(t, repo, "upstream.txt", "theirs\n", "upstream")

	review, err := inventory.Review(context.Background(), entry, ReviewOptions{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if review.Lands != LandsDivergedClean || review.Ahead != 1 || review.Behind != 1 || len(review.Conflicts) != 0 {
		t.Fatalf("review = %+v, want a clean divergence", review)
	}
	if !reflect.DeepEqual(review.Files, []FileChange{{Path: "feature.txt", Added: 1}}) {
		t.Fatalf("files = %+v, want only the worktree's own change", review.Files)
	}
}

func TestReviewNamesConflictsWithoutTouchingEitherCheckout(t *testing.T) {
	t.Parallel()

	repo, inventory, entry := reviewFixture(t)
	commitFile(t, entry.Path, "README.md", "# ours\n", "ours")
	commitFile(t, repo, "README.md", "# theirs\n", "theirs")
	before := gitOutput(t, repo, "status", "--porcelain") + gitOutput(t, entry.Path, "status", "--porcelain") +
		gitOutput(t, repo, "rev-parse", "HEAD") + gitOutput(t, entry.Path, "rev-parse", "HEAD")

	review, err := inventory.Review(context.Background(), entry, ReviewOptions{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if review.Lands != LandsConflicts || !reflect.DeepEqual(review.Conflicts, []string{"README.md"}) {
		t.Fatalf("review = %+v, want a README.md conflict", review)
	}
	after := gitOutput(t, repo, "status", "--porcelain") + gitOutput(t, entry.Path, "status", "--porcelain") +
		gitOutput(t, repo, "rev-parse", "HEAD") + gitOutput(t, entry.Path, "rev-parse", "HEAD")
	if before != after {
		t.Fatalf("review changed a checkout:\nbefore %q\nafter  %q", before, after)
	}
}

func TestReviewReportsABinaryFileWithoutCounts(t *testing.T) {
	t.Parallel()

	_, inventory, entry := reviewFixture(t)
	commitFile(t, entry.Path, "blob.bin", "a\x00b\x00", "binary")

	review, err := inventory.Review(context.Background(), entry, ReviewOptions{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !reflect.DeepEqual(review.Files, []FileChange{{Path: "blob.bin", Binary: true}}) {
		t.Fatalf("files = %+v, want one binary file", review.Files)
	}
}

func TestReviewNeverRunsTextconv(t *testing.T) {
	t.Parallel()
	_, inventory, entry := reviewFixture(t)
	// The converter leaves a marker if Git runs it. Both the stats and patch
	// must describe the actual content, without invoking configured programs.
	gitRun(t, entry.Path, "config", "diff.review.textconv", "touch converter-ran; cat")
	if err := os.WriteFile(filepath.Join(entry.Path, ".gitattributes"), []byte("README.md diff=review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitFile(t, entry.Path, "README.md", "# changed\n", "change")
	for _, committedOnly := range []bool{false, true} {
		review, err := inventory.Review(context.Background(), entry, ReviewOptions{Patch: true, CommittedOnly: committedOnly})
		if err != nil || !strings.Contains(review.Patch, "+# changed") {
			t.Fatalf("Review committed=%t: %+v, %v", committedOnly, review, err)
		}
		if _, err := os.Stat(filepath.Join(entry.Path, "converter-ran")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Review committed=%t ran textconv: %v", committedOnly, err)
		}
	}
}

func TestReviewShowsCommittedSubmoduleChangesDespiteGitDisplaySettings(t *testing.T) {
	t.Parallel()
	_, inventory, entry := reviewFixture(t)
	// A gitlink can point at any commit object. No checkout or network clone
	// is needed to review its committed addition.
	head := revision(t, entry.Path, "HEAD")
	gitRun(t, entry.Path, "update-index", "--add", "--cacheinfo", "160000,"+head+",child")
	gitRun(t, entry.Path, "commit", "-m", "add child")
	gitRun(t, entry.Path, "config", "diff.ignoreSubmodules", "all")

	review, err := inventory.Review(context.Background(), entry, ReviewOptions{CommittedOnly: true, Patch: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Files) != 1 || review.Files[0].Path != "child" || !strings.Contains(review.Patch, "+Subproject commit "+head) {
		t.Fatalf("review hid committed submodule changes: %+v", review)
	}
}

func TestReviewRefusesWhatItCannotCompare(t *testing.T) {
	t.Parallel()

	t.Run("missing checkout", func(t *testing.T) {
		repo, inventory, entry := reviewFixture(t)
		gitRun(t, repo, "worktree", "remove", entry.Path)
		if _, err := inventory.Review(context.Background(), entry, ReviewOptions{}); !errors.Is(err, ErrWorktreeMissing) {
			t.Fatalf("missing checkout error = %v, want ErrWorktreeMissing", err)
		}
	})
	t.Run("legacy checkout", func(t *testing.T) {
		_, inventory, entry := reviewFixture(t)
		if err := os.Remove(entry.MetadataPath); err != nil {
			t.Fatal(err)
		}
		legacy, err := inventory.Resolve(context.Background(), entry.Path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inventory.Review(context.Background(), legacy, ReviewOptions{}); !errors.Is(err, ErrMetadataInvalid) {
			t.Fatalf("legacy error = %v, want ErrMetadataInvalid", err)
		}
	})
	t.Run("missing source branch", func(t *testing.T) {
		_, inventory, entry := reviewFixture(t)
		metadata, err := ReadMetadata(entry.MetadataPath)
		if err != nil {
			t.Fatal(err)
		}
		metadata.SourceBranch = "no-such-branch"
		if err := WriteMetadata(entry.MetadataPath, metadata); err != nil {
			t.Fatal(err)
		}
		if _, err := inventory.Review(context.Background(), entry, ReviewOptions{}); !errors.Is(err, ErrSourceBranchMissing) {
			t.Fatalf("missing source error = %v, want ErrSourceBranchMissing", err)
		}
	})
}

func TestPredictMergeNeverCallsAnUnpredictedMergeClean(t *testing.T) {
	t.Parallel()

	failing := func(context.Context, string, time.Duration, ...string) ([]byte, error) {
		return nil, errors.New("git merge-tree failed: usage")
	}
	if verdict, _ := predictMerge(context.Background(), failing, ".", "refs/heads/main", "HEAD"); verdict != LandsUnknown {
		t.Fatalf("verdict = %q, want %q", verdict, LandsUnknown)
	}
}
