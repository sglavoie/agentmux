package worktree

// Safe cleanup of managed checkouts. Remove and Forget hold the shared mutation
// lock, resolve the selector only after acquiring it, and re-probe every Git
// safety fact immediately before the first mutation — the preflight that does
// that lives in cleanup_preflight.go, and the absent-checkout path in
// cleanup_missing.go. No path here ever forces: a refusal is the correct
// outcome for anything the checks cannot prove safe.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrWorktreeMissing     = errors.New("worktree: checkout is missing")
	ErrWorktreeDirty       = errors.New("worktree: checkout has uncommitted changes")
	ErrWorktreeBusy        = errors.New("worktree: checkout has an unfinished Git operation")
	ErrWorktreeLocked      = errors.New("worktree: checkout is locked")
	ErrWorktreeDetached    = errors.New("worktree: checkout is detached")
	ErrMetadataInvalid     = errors.New("worktree: metadata is invalid or mismatched")
	ErrMetadataMissing     = errors.New("worktree: metadata is missing")
	ErrGitUnavailable      = errors.New("worktree: Git state is unavailable")
	ErrBranchMissing       = errors.New("worktree: checkout branch is missing")
	ErrSourceBranchMissing = errors.New("worktree: recorded source branch is missing")
	ErrBranchUnmerged      = errors.New("worktree: checkout branch is not merged into its recorded source branch")
	ErrForgetRefused       = errors.New("worktree: healthy metadata for a present checkout cannot be forgotten")
)

// RemoveOptions controls safe user cleanup. DeleteBranch never enables force:
// it adds merge and provenance checks before the checkout is removed.
type RemoveOptions struct {
	DeleteBranch bool `json:"delete_branch"`
}

// MutationResult reports only the steps performed by this Remove or Forget
// invocation. Its fields are operation-local, so a retry reports false for
// steps completed by an earlier attempt. A caller must retain this result when
// PartialMutationError is returned.
type MutationResult struct {
	Entry             Entry `json:"entry"`
	CheckoutRemoved   bool  `json:"checkout_removed"`
	BranchDeleted     bool  `json:"branch_deleted"`
	MetadataForgotten bool  `json:"metadata_forgotten"`
}

// PartialMutationError means Git changed successfully but a later requested
// step failed. The adjacent sidecar is deliberately retained until all Git
// work succeeds so the operation can be retried or explicitly forgotten.
type PartialMutationError struct {
	Err error
}

func (e *PartialMutationError) Error() string {
	if e == nil || e.Err == nil {
		return "worktree cleanup completed partially"
	}
	return "worktree cleanup completed partially: " + e.Err.Error()
}

func (e *PartialMutationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type cleanupPreflight struct {
	entry      Entry
	metadata   *Metadata
	controlDir string
}

// Remove safely removes one managed checkout. The selector is resolved only
// after the shared mutation lock is held, then all Git safety facts are probed
// again synchronously immediately before the first mutation.
func (i *Inventory) Remove(ctx context.Context, selector string, options RemoveOptions) (MutationResult, error) {
	manager := i
	if manager == nil {
		manager = NewInventory()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := manager.existingMutationRoot()
	if err != nil {
		return MutationResult{}, err
	}

	var result MutationResult
	err = withMutationLock(ctx, root, func() error {
		entry, err := manager.Resolve(ctx, selector)
		if err != nil {
			return err
		}
		result.Entry = entry
		if !entry.Exists {
			if !options.DeleteBranch || entry.Classification != ClassificationMissing {
				return fmt.Errorf("%w; use explicit metadata forgetting for an absent checkout", ErrWorktreeMissing)
			}
			return manager.finishMissingCleanup(ctx, root, entry, &result)
		}

		preflight, err := manager.revalidatePresentCleanup(ctx, root, entry)
		if err != nil {
			return err
		}
		result.Entry = preflight.entry
		var deletion branchDeletion
		if options.DeleteBranch {
			if preflight.metadata == nil {
				return fmt.Errorf("%w: branch deletion requires schema-v1 sidecar provenance", ErrMetadataInvalid)
			}
			deletion, err = manager.preflightBranchDeletion(ctx, preflight.controlDir, preflight.entry, *preflight.metadata)
			if err != nil {
				return err
			}
		}

		git := manager.gitRunner()
		ignored, err := hasIgnoredFiles(ctx, git, preflight.entry.Path)
		if err != nil {
			return fmt.Errorf("%w: checking ignored files: %w", ErrGitUnavailable, err)
		}
		if ignored {
			return fmt.Errorf("%w: checkout contains ignored files that Git would delete", ErrWorktreeDirty)
		}
		if _, err := git(ctx, preflight.controlDir, gitCommandTimeout, "worktree", "remove", preflight.entry.Path); err != nil {
			return fmt.Errorf("worktree: removing checkout %s: %w", preflight.entry.Path, err)
		}
		result.CheckoutRemoved = true

		if options.DeleteBranch {
			if err := manager.deleteMergedBranch(ctx, preflight.controlDir, deletion); err != nil {
				return &PartialMutationError{Err: err}
			}
			result.BranchDeleted = true
		}
		if preflight.metadata != nil {
			if err := manager.removeSidecar(preflight.entry.MetadataPath); err != nil {
				return &PartialMutationError{Err: err}
			}
			result.MetadataForgotten = true
		}
		return nil
	})
	return result, err
}

// Forget removes only auxiliary metadata. Healthy metadata for a present
// managed checkout is never eligible; missing or invalid/mismatched records
// remain explicitly recoverable without granting any Git mutation authority.
func (i *Inventory) Forget(ctx context.Context, selector string) (MutationResult, error) {
	manager := i
	if manager == nil {
		manager = NewInventory()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := manager.existingMutationRoot()
	if err != nil {
		return MutationResult{}, err
	}

	var result MutationResult
	err = withMutationLock(ctx, root, func() error {
		entry, err := manager.Resolve(ctx, selector)
		if err != nil {
			return err
		}
		result.Entry = entry
		if entry.Exists && entry.Classification != ClassificationMetadataInvalid {
			return ErrForgetRefused
		}
		if err := validateAdjacentMetadataPath(root, entry); err != nil {
			return err
		}
		if _, err := os.Lstat(entry.MetadataPath); errors.Is(err, fs.ErrNotExist) {
			return ErrMetadataMissing
		} else if err != nil {
			return fmt.Errorf("worktree: inspecting metadata %s: %w", entry.MetadataPath, err)
		}
		if err := manager.removeSidecar(entry.MetadataPath); err != nil {
			return err
		}
		result.MetadataForgotten = true
		return nil
	})
	return result, err
}

func (i *Inventory) existingMutationRoot() (string, error) {
	directory := i.directory
	if directory == nil {
		directory = defaultDirectory
	}
	root, err := directory()
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("worktree: resolving managed root: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("worktree: inspecting managed root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("worktree: managed root %s is not a real directory", root)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("worktree: canonicalizing managed root: %w", err)
	}
	return filepath.Clean(root), nil
}

func (i *Inventory) gitRunner() gitExecutor {
	if i != nil && i.git != nil {
		return i.git
	}
	return runGit
}

func (i *Inventory) removeSidecar(path string) error {
	remove := os.Remove
	if i != nil && i.removeMetadata != nil {
		remove = i.removeMetadata
	}
	if err := remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("worktree: removing metadata %s: %w", path, err)
	}
	return nil
}

func validateAdjacentMetadataPath(root string, entry Entry) error {
	path := canonicalAdjacentPath(entry.Path)
	if !pathContained(root, path) || entry.MetadataPath != SidecarPath(path) {
		return fmt.Errorf("%w: metadata is not adjacent to a checkout under the managed root", ErrMetadataInvalid)
	}
	return nil
}

// deleteMergedBranch deletes a branch the caller has already proven merged
// into its recorded source branch, and only while both tips match the proof.
// `git branch -d` is not used: it judges "merged" against the HEAD of the
// checkout it runs in, which is often some other branch (a landing advances a
// source branch nobody has checked out), so it refused branches that were
// merged where it matters. The old values make the delete conditional, so a
// branch that moved since the proof is refused by Git rather than lost, and a
// branch some other worktree has checked out is refused here, as branch -d
// would, because update-ref would delete it from under that checkout.
func (i *Inventory) deleteMergedBranch(ctx context.Context, control string, deletion branchDeletion) error {
	branch := deletion.branch
	git := i.gitRunner()
	porcelain, err := git(ctx, control, gitProbeTimeout, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return fmt.Errorf("worktree: safely deleting branch %s: listing Git worktrees: %w", branch, err)
	}
	records, err := parseWorktreePorcelain(porcelain)
	if err != nil {
		return fmt.Errorf("worktree: safely deleting branch %s: %w", branch, err)
	}
	for _, record := range records {
		if record.Branch == branch {
			return fmt.Errorf("worktree: safely deleting branch %s: it is checked out at %s", branch, record.Path)
		}
		// Rebase and bisect can detach HEAD, hiding the branch from the list.
		operation, err := branchOperation(ctx, git, record.Path, "refs/heads/"+branch)
		if err != nil {
			return err
		}
		if operation != "" {
			return fmt.Errorf("worktree: safely deleting branch %s: it is in use by %s at %s", branch, operation, record.Path)
		}
	}
	// Lock and verify both tips in one transaction. A source reset after the
	// ancestry proof must not allow deletion of the last branch holding work.
	input := fmt.Sprintf("verify %s\x00%s\x00delete refs/heads/%s\x00%s\x00", deletion.sourceRef, deletion.sourceHead, branch, deletion.head)
	run := i.gitInput
	if run == nil {
		run = runGitInput
	}
	if _, err := run(ctx, control, gitCommandTimeout, input, "update-ref", "-m", "amx worktree remove: delete merged branch", "--stdin", "-z"); err != nil {
		return fmt.Errorf("worktree: safely deleting branch %s: %w", branch, err)
	}
	return nil
}

func localBranch(value string) (name, ref string, err error) {
	name = strings.TrimSpace(value)
	if strings.HasPrefix(name, "refs/heads/") {
		name = strings.TrimPrefix(name, "refs/heads/")
	} else if strings.HasPrefix(name, "refs/") {
		return "", "", errors.New("not a local branch")
	}
	if name == "" {
		return "", "", errors.New("branch is empty")
	}
	return name, "refs/heads/" + name, nil
}
