package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerRemoveRetainsBranchByDefault(t *testing.T) {
	t.Parallel()

	repo, root, workflow, created := cleanupFixture(t)
	manager := testInventory(root)

	result, err := manager.Remove(context.Background(), created.ID, RemoveOptions{})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.CheckoutRemoved || result.BranchDeleted || !result.MetadataForgotten {
		t.Fatalf("result = %+v, want checkout and metadata removed with branch retained", result)
	}
	assertPathMissing(t, created.Path)
	assertPathMissing(t, created.MetadataPath)
	if exists, err := gitBranchExists(context.Background(), repo, created.Branch); err != nil || !exists {
		t.Fatalf("retained branch exists = %v, err = %v", exists, err)
	}
	gitRun(t, repo, "branch", "-D", created.Branch)
	_ = workflow
}

func TestManagerRemoveSafelyDeletesMergedBranch(t *testing.T) {
	t.Parallel()

	repo, root, _, created := cleanupFixture(t)
	manager := testInventory(root)

	result, err := manager.Remove(context.Background(), created.Path, RemoveOptions{DeleteBranch: true})
	if err != nil {
		t.Fatalf("Remove --delete-branch: %v", err)
	}
	if !result.CheckoutRemoved || !result.BranchDeleted || !result.MetadataForgotten {
		t.Fatalf("result = %+v, want complete checkout, branch, and metadata cleanup", result)
	}
	if exists, err := gitBranchExists(context.Background(), repo, created.Branch); err != nil || exists {
		t.Fatalf("deleted branch exists = %v, err = %v", exists, err)
	}
	assertPathMissing(t, created.Path)
	assertPathMissing(t, created.MetadataPath)
}

func TestManagerRemoveLegacyCheckoutRetainsBranchAndRejectsBranchDeletion(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	path := filepath.Join(root, "api", "legacy")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const branch = "amx/api/legacy"
	gitRun(t, repo, "worktree", "add", "-b", branch, path, "HEAD")
	t.Cleanup(func() {
		_ = execGit(repo, "worktree", "remove", "--force", path)
		_ = execGit(repo, "branch", "-D", branch)
	})
	manager := testInventory(root)

	_, err := manager.Remove(context.Background(), path, RemoveOptions{DeleteBranch: true})
	if !errors.Is(err, ErrMetadataInvalid) {
		t.Fatalf("legacy branch-deleting Remove error = %v, want ErrMetadataInvalid", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("refused branch deletion changed legacy checkout: %v", err)
	}

	result, err := manager.Remove(context.Background(), path, RemoveOptions{})
	if err != nil || !result.CheckoutRemoved || result.BranchDeleted || result.MetadataForgotten {
		t.Fatalf("legacy default Remove result = %+v, err = %v", result, err)
	}
	if exists, err := gitBranchExists(context.Background(), repo, branch); err != nil || !exists {
		t.Fatalf("legacy retained branch exists = %v, err = %v", exists, err)
	}
}

func TestManagerRemoveRefusesUnsafeCheckoutStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prepare   func(*testing.T, string, Result)
		want      error
		unlock    bool
		deleteRef bool
	}{
		{
			name: "dirty",
			prepare: func(t *testing.T, _ string, result Result) {
				if err := os.WriteFile(filepath.Join(result.Path, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: ErrWorktreeDirty,
		},
		{
			name: "locked",
			prepare: func(t *testing.T, repo string, result Result) {
				gitRun(t, repo, "worktree", "lock", "--reason", "cleanup test", result.Path)
			},
			want:   ErrWorktreeLocked,
			unlock: true,
		},
		{
			name: "unmerged",
			prepare: func(t *testing.T, _ string, result Result) {
				if err := os.WriteFile(filepath.Join(result.Path, "commit.txt"), []byte("commit\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				gitRun(t, result.Path, "add", "commit.txt")
				gitRun(t, result.Path, "commit", "-m", "unmerged work")
			},
			want:      ErrBranchUnmerged,
			deleteRef: true,
		},
		{
			name: "detached",
			prepare: func(t *testing.T, _ string, result Result) {
				gitRun(t, result.Path, "checkout", "--detach")
			},
			want:      ErrWorktreeDetached,
			deleteRef: true,
		},
		{
			name: "missing source ref",
			prepare: func(t *testing.T, _ string, result Result) {
				metadata, err := ReadMetadata(result.MetadataPath)
				if err != nil {
					t.Fatal(err)
				}
				metadata.SourceBranch = "does-not-exist"
				if err := WriteMetadata(result.MetadataPath, metadata); err != nil {
					t.Fatal(err)
				}
			},
			want:      ErrSourceBranchMissing,
			deleteRef: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo, root, workflow, created := cleanupFixture(t)
			tc.prepare(t, repo, created)
			if tc.unlock {
				t.Cleanup(func() { _ = execGit(repo, "worktree", "unlock", created.Path) })
			}
			_, err := testInventory(root).Remove(context.Background(), created.ID, RemoveOptions{DeleteBranch: tc.deleteRef})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Remove error = %v, want %v", err, tc.want)
			}
			if _, err := os.Stat(created.Path); err != nil {
				t.Fatalf("refused cleanup removed checkout: %v", err)
			}
			_ = workflow
		})
	}
}

func TestManagerRemoveExcludesExternalWorktreeInsideManagedRoot(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	external := filepath.Join(root, "api", "external")
	if err := os.MkdirAll(filepath.Dir(external), 0o700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "worktree", "add", "-b", "external-branch", external, "HEAD")
	t.Cleanup(func() {
		_ = execGit(repo, "worktree", "remove", "--force", external)
		_ = execGit(repo, "branch", "-D", "external-branch")
	})

	_, err := testInventory(root).Remove(context.Background(), external, RemoveOptions{})
	if !errors.Is(err, ErrSelectorNotFound) {
		t.Fatalf("Remove external error = %v, want ErrSelectorNotFound", err)
	}
	if _, err := os.Stat(external); err != nil {
		t.Fatalf("external checkout changed: %v", err)
	}
}

func TestManagerRemoveRefreshesStateUnderMutationLock(t *testing.T) {
	t.Parallel()

	_, root, _, created := cleanupFixture(t)
	manager := testInventory(root)
	entries, err := manager.List(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Dirty {
		t.Fatalf("initial List = %+v, %v", entries, err)
	}
	if err := os.WriteFile(filepath.Join(created.Path, "became-dirty.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = manager.Remove(context.Background(), entries[0].ID, RemoveOptions{})
	if !errors.Is(err, ErrWorktreeDirty) {
		t.Fatalf("Remove stale clean entry error = %v, want ErrWorktreeDirty", err)
	}
}

func TestManagerRemoveRejectsSidecarGitMismatch(t *testing.T) {
	t.Parallel()

	_, root, _, created := cleanupFixture(t)
	metadata, err := ReadMetadata(created.MetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	metadata.Branch = "amx/api/other"
	if err := WriteMetadata(created.MetadataPath, metadata); err != nil {
		t.Fatal(err)
	}

	_, err = testInventory(root).Remove(context.Background(), created.Path, RemoveOptions{})
	if !errors.Is(err, ErrMetadataInvalid) {
		t.Fatalf("Remove mismatched metadata error = %v, want ErrMetadataInvalid", err)
	}
}

func TestManagerRemovePartialBranchFailureIsRetryable(t *testing.T) {
	t.Parallel()

	repo, root, _, created := cleanupFixture(t)
	manager := testInventory(root)
	var failed atomic.Bool
	manager.git = func(ctx context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "branch -d -- "+created.Branch && failed.CompareAndSwap(false, true) {
			return nil, errors.New("injected branch deletion failure")
		}
		return runGit(ctx, directory, timeout, args...)
	}

	result, err := manager.Remove(context.Background(), created.ID, RemoveOptions{DeleteBranch: true})
	var partial *PartialMutationError
	if !errors.As(err, &partial) || !result.CheckoutRemoved || result.BranchDeleted || result.MetadataForgotten {
		t.Fatalf("first Remove result = %+v, err = %v; want typed checkout-only partial", result, err)
	}
	assertPathMissing(t, created.Path)
	if _, err := os.Stat(created.MetadataPath); err != nil {
		t.Fatalf("partial cleanup did not retain sidecar: %v", err)
	}
	if exists, err := gitBranchExists(context.Background(), repo, created.Branch); err != nil || !exists {
		t.Fatalf("branch after injected failure exists = %v, err = %v", exists, err)
	}

	result, err = manager.Remove(context.Background(), created.ID, RemoveOptions{DeleteBranch: true})
	if err != nil || !result.BranchDeleted || !result.MetadataForgotten {
		t.Fatalf("retry result = %+v, err = %v", result, err)
	}
	assertPathMissing(t, created.MetadataPath)
}

func TestManagerRemovePartialMetadataFailureIsRetryable(t *testing.T) {
	t.Parallel()

	_, root, _, created := cleanupFixture(t)
	manager := testInventory(root)
	var failed atomic.Bool
	manager.removeMetadata = func(path string) error {
		if failed.CompareAndSwap(false, true) {
			return errors.New("injected metadata failure")
		}
		return os.Remove(path)
	}

	result, err := manager.Remove(context.Background(), created.ID, RemoveOptions{DeleteBranch: true})
	var partial *PartialMutationError
	if !errors.As(err, &partial) || !result.CheckoutRemoved || !result.BranchDeleted || result.MetadataForgotten {
		t.Fatalf("first Remove result = %+v, err = %v; want typed Git-complete partial", result, err)
	}
	if _, err := os.Stat(created.MetadataPath); err != nil {
		t.Fatalf("metadata partial did not retain sidecar: %v", err)
	}

	result, err = manager.Remove(context.Background(), created.ID, RemoveOptions{DeleteBranch: true})
	if err != nil || result.BranchDeleted || !result.MetadataForgotten {
		t.Fatalf("metadata retry result = %+v, err = %v; want metadata-only completion", result, err)
	}
	assertPathMissing(t, created.MetadataPath)
}

func TestManagerForgetEligibility(t *testing.T) {
	t.Parallel()

	t.Run("healthy present metadata is refused", func(t *testing.T) {
		_, root, _, created := cleanupFixture(t)
		_, err := testInventory(root).Forget(context.Background(), created.ID)
		if !errors.Is(err, ErrForgetRefused) {
			t.Fatalf("Forget error = %v, want ErrForgetRefused", err)
		}
		if _, err := os.Stat(created.MetadataPath); err != nil {
			t.Fatalf("healthy sidecar changed: %v", err)
		}
	})

	t.Run("invalid present metadata is forgotten", func(t *testing.T) {
		_, root, _, created := cleanupFixture(t)
		if err := os.WriteFile(created.MetadataPath, []byte("{invalid"), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := testInventory(root).Forget(context.Background(), created.Path)
		if err != nil || !result.MetadataForgotten {
			t.Fatalf("Forget invalid result = %+v, err = %v", result, err)
		}
		assertPathMissing(t, created.MetadataPath)
		if _, err := os.Stat(created.Path); err != nil {
			t.Fatalf("Forget changed checkout: %v", err)
		}
	})

	t.Run("mismatched present metadata is forgotten", func(t *testing.T) {
		_, root, _, created := cleanupFixture(t)
		metadata, err := ReadMetadata(created.MetadataPath)
		if err != nil {
			t.Fatal(err)
		}
		metadata.Path = filepath.Join(filepath.Dir(created.Path), "different-checkout")
		if err := WriteMetadata(created.MetadataPath, metadata); err != nil {
			t.Fatal(err)
		}
		result, err := testInventory(root).Forget(context.Background(), created.Path)
		if err != nil || !result.MetadataForgotten {
			t.Fatalf("Forget mismatched result = %+v, err = %v", result, err)
		}
		assertPathMissing(t, created.MetadataPath)
		if _, err := os.Stat(created.Path); err != nil {
			t.Fatalf("Forget changed checkout: %v", err)
		}
	})

	t.Run("valid metadata for a missing checkout is forgotten", func(t *testing.T) {
		repo, root, _, created := cleanupFixture(t)
		gitRun(t, repo, "worktree", "remove", created.Path)
		result, err := testInventory(root).Forget(context.Background(), created.ID)
		if err != nil || !result.MetadataForgotten || result.CheckoutRemoved || result.BranchDeleted {
			t.Fatalf("Forget missing result = %+v, err = %v", result, err)
		}
		assertPathMissing(t, created.MetadataPath)
		gitRun(t, repo, "branch", "-D", created.Branch)
	})
}

func TestManagerPlainRemoveOfMissingCheckoutRequiresForget(t *testing.T) {
	t.Parallel()

	repo, root, _, created := cleanupFixture(t)
	gitRun(t, repo, "worktree", "remove", created.Path)

	_, err := testInventory(root).Remove(context.Background(), created.ID, RemoveOptions{})
	if !errors.Is(err, ErrWorktreeMissing) || !strings.Contains(err.Error(), "explicit metadata forgetting") {
		t.Fatalf("plain missing Remove error = %v, want explicit forget guidance", err)
	}
	if _, err := os.Stat(created.MetadataPath); err != nil {
		t.Fatalf("plain missing Remove changed metadata: %v", err)
	}
	gitRun(t, repo, "branch", "-D", created.Branch)
}

func TestManagerDeleteBranchRefusesExternallyMissingRegisteredCheckout(t *testing.T) {
	t.Parallel()

	_, root, _, created := cleanupFixture(t)
	if err := os.RemoveAll(created.Path); err != nil {
		t.Fatal(err)
	}

	_, err := testInventory(root).Remove(context.Background(), created.ID, RemoveOptions{DeleteBranch: true})
	if !errors.Is(err, ErrWorktreeMissing) || !strings.Contains(err.Error(), "remains registered") {
		t.Fatalf("Remove externally missing registered checkout error = %v", err)
	}
	if _, err := os.Stat(created.MetadataPath); err != nil {
		t.Fatalf("refused retry changed metadata: %v", err)
	}
}

func TestCreateRemoveAndForgetShareMutationLock(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	workflow := testWorkflow(root)
	removeTarget, err := workflow.Create(context.Background(), Request{ProjectLabel: "api", SourceCWD: repo, SessionID: "remove-target"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workflow.RemoveUnused(context.Background(), removeTarget) })

	forgetPath := filepath.Join(root, "api", "forget-target")
	if err := os.WriteFile(SidecarPath(forgetPath), []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := testInventory(root)

	entered := make(chan struct{})
	release := make(chan struct{})
	blockedCreate := testWorkflow(root)
	blockedCreate.writeMetadata = func(path string, metadata Metadata) error {
		close(entered)
		<-release
		return WriteMetadata(path, metadata)
	}
	type createOutcome struct {
		result Result
		err    error
	}
	createDone := make(chan createOutcome, 1)
	go func() {
		result, err := blockedCreate.Create(context.Background(), Request{ProjectLabel: "api", SourceCWD: repo, SessionID: "lock-holder"})
		createDone <- createOutcome{result: result, err: err}
	}()
	<-entered

	removeDone := make(chan error, 1)
	forgetDone := make(chan error, 1)
	go func() {
		_, err := manager.Remove(context.Background(), removeTarget.ID, RemoveOptions{})
		removeDone <- err
	}()
	go func() {
		_, err := manager.Forget(context.Background(), forgetPath)
		forgetDone <- err
	}()
	var (
		removeErr     error
		forgetErr     error
		removeArrived bool
		forgetArrived bool
	)
	select {
	case err := <-removeDone:
		removeErr, removeArrived = err, true
	case err := <-forgetDone:
		forgetErr, forgetArrived = err, true
	case <-time.After(50 * time.Millisecond):
	}
	close(release)

	created := <-createDone
	if created.err != nil {
		t.Fatalf("blocked Create: %v", created.err)
	}
	t.Cleanup(func() { _ = blockedCreate.RemoveUnused(context.Background(), created.result) })
	if !removeArrived {
		removeErr = <-removeDone
	}
	if !forgetArrived {
		forgetErr = <-forgetDone
	}
	if removeArrived || forgetArrived {
		t.Fatalf("mutation escaped create lock: remove arrived=%v err=%v; forget arrived=%v err=%v", removeArrived, removeErr, forgetArrived, forgetErr)
	}
	if removeErr != nil {
		t.Fatalf("serialized Remove: %v", removeErr)
	}
	if forgetErr != nil {
		t.Fatalf("serialized Forget: %v", forgetErr)
	}
	gitRun(t, repo, "branch", "-D", removeTarget.Branch)
}

func cleanupFixture(t *testing.T) (repo, root string, workflow *Workflow, result Result) {
	t.Helper()
	repo = testRepository(t)
	root = filepath.Join(t.TempDir(), "worktrees")
	workflow = testWorkflow(root)
	var err error
	result, err = workflow.Create(context.Background(), Request{ProjectLabel: "api", SourceCWD: repo, SessionID: "cleanup-source"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workflow.RemoveUnused(context.Background(), result) })
	return repo, root, workflow, result
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat %s = %v, want not exist", path, err)
	}
}

func TestPartialMutationErrorCarriesCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("cause")
	err := &PartialMutationError{Err: cause}
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "completed partially") {
		t.Fatalf("PartialMutationError = %v", err)
	}
	if got := (&PartialMutationError{}).Error(); got == "" {
		t.Fatal("nil-cause partial error is empty")
	}
}
