package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLandPreservesPausedAMInSourceCheckout(t *testing.T) {
	t.Parallel()
	repo, inventory, entry := reviewFixture(t)
	gitRun(t, entry.Path, "commit", "--allow-empty", "-m", "empty patch")
	patch := filepath.Join(t.TempDir(), "empty.patch")
	if err := os.WriteFile(patch, []byte(gitOutput(t, entry.Path, "format-patch", "--always", "-1", "HEAD", "--stdout")), 0o600); err != nil {
		t.Fatal(err)
	}
	commitFile(t, entry.Path, "feature.txt", "feature\n", "feature")
	if err := execGit(repo, "am", patch); err == nil {
		t.Fatal("expected git am to pause on an empty patch")
	}
	if status := gitOutput(t, repo, "status", "--porcelain"); status != "" {
		t.Fatalf("expected clean source checkout during am, got %s", status)
	}
	statePath := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "--path-format=absolute", "--git-path", "rebase-apply/patch"))
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	before := revision(t, repo, "HEAD")
	result, err := inventory.Land(context.Background(), entry.ID)
	if !errors.Is(err, ErrSourceBusy) || result.Landed {
		t.Errorf("Land = %+v, %v; want unfinished source am refused", result, err)
	}
	if after := revision(t, repo, "HEAD"); after != before {
		t.Errorf("landing moved source from %s to %s", before, after)
	}
	if after, err := os.ReadFile(statePath); err != nil || string(after) != string(state) {
		t.Errorf("changed am state: %q, %v", after, err)
	}
	gitRun(t, repo, "am", "--abort")
	result, err = inventory.Land(context.Background(), entry.ID)
	if err != nil || !result.Landed {
		t.Errorf("Land after abort = %+v, %v; want success", result, err)
	}
}

func TestLandRefusesUnavailableSourceOperationState(t *testing.T) {
	t.Parallel()
	repo, inventory, entry := reviewFixture(t)
	commitFile(t, entry.Path, "feature.txt", "feature\n", "feature")
	before := revision(t, repo, "HEAD")
	canonicalRepo, err := canonicalExistingPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	git := inventory.git
	probed := false
	inventory.git = func(ctx context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error) {
		if directory == canonicalRepo && strings.Join(args, " ") == "rev-parse --absolute-git-dir" {
			probed = true
			return nil, errors.New("source operation state unavailable")
		}
		return git(ctx, directory, timeout, args...)
	}
	result, err := inventory.Land(context.Background(), entry.ID)
	if !probed || !errors.Is(err, ErrGitUnavailable) || result.Landed {
		t.Errorf("Land = %+v, %v; want unavailable-state refusal (probed=%t)", result, err, probed)
	}
	if after := revision(t, repo, "HEAD"); after != before {
		t.Errorf("landing moved source from %s to %s", before, after)
	}
}
