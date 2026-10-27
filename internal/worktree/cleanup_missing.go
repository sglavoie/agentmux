package worktree

// Cleanup for a checkout that is already gone from disk. There is no worktree
// to probe, so the recorded source checkout becomes the control directory and
// every fact is re-derived from it: that the checkout really is absent, that
// Git no longer registers it, and that the sidecar describes the same
// repository. A branch is deleted only when it is still merged.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
)

func (i *Inventory) finishMissingCleanup(ctx context.Context, root string, entry Entry, result *MutationResult) error {
	if err := validateAdjacentMetadataPath(root, entry); err != nil {
		return err
	}
	metadata, err := ReadMetadata(entry.MetadataPath)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMetadataInvalid, err)
	}
	if canonicalAdjacentPath(metadata.Path) != entry.Path {
		return ErrMetadataInvalid
	}
	if _, err := os.Lstat(entry.Path); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("%w: checkout reappeared during cleanup", ErrGitUnavailable)
		}
		return fmt.Errorf("worktree: inspecting missing checkout: %w", err)
	}

	control, records, commonDir, err := i.missingControlState(ctx, metadata)
	if err != nil {
		return err
	}
	if canonicalAdjacentPath(metadata.Path) != entry.Path || !pathContained(root, entry.Path) {
		return ErrMetadataInvalid
	}
	for _, record := range records {
		if canonicalAdjacentPath(record.Path) == entry.Path {
			return fmt.Errorf("%w: absent checkout remains registered by Git; use explicit metadata forgetting", ErrWorktreeMissing)
		}
	}
	metadataCommon, err := canonicalExistingPath(metadata.GitCommonDir)
	if err != nil || metadataCommon != commonDir {
		return fmt.Errorf("%w: sidecar Git common directory conflicts with Git", ErrMetadataInvalid)
	}

	branch, branchRef, err := localBranch(metadata.Branch)
	if err != nil {
		return ErrMetadataInvalid
	}
	branchExists, err := i.gitRefExists(ctx, control, branchRef)
	if err != nil {
		return err
	}
	if branchExists {
		_, sourceRef, err := localBranch(metadata.SourceBranch)
		if err != nil {
			return fmt.Errorf("%w: no usable source branch was recorded", ErrMetadataInvalid)
		}
		sourceExists, err := i.gitRefExists(ctx, control, sourceRef)
		if err != nil {
			return err
		}
		if !sourceExists {
			return ErrSourceBranchMissing
		}
		branchHead, err := gitLineWith(ctx, i.gitRunner(), control, gitProbeTimeout, "rev-parse", "--verify", branchRef+"^{commit}")
		if err != nil {
			return fmt.Errorf("%w: resolving checkout branch: %w", ErrGitUnavailable, err)
		}
		if _, err := i.gitRunner()(ctx, control, gitProbeTimeout, "merge-base", "--is-ancestor", branchHead, sourceRef); err != nil {
			return fmt.Errorf("%w: %w", ErrBranchUnmerged, err)
		}
		if err := i.deleteMergedBranch(ctx, control, branch, branchHead); err != nil {
			return err
		}
		result.BranchDeleted = true
	}
	if err := i.removeSidecar(entry.MetadataPath); err != nil {
		return &PartialMutationError{Err: err}
	}
	result.MetadataForgotten = true
	return nil
}

func (i *Inventory) missingControlState(ctx context.Context, metadata Metadata) (string, []gitWorktreeRecord, string, error) {
	control, err := canonicalExistingPath(metadata.SourceRoot)
	if err != nil {
		return "", nil, "", fmt.Errorf("%w: recorded source checkout is unavailable", ErrGitUnavailable)
	}
	git := i.gitRunner()
	commonDir, err := gitLineWith(ctx, git, control, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", nil, "", fmt.Errorf("%w: resolving source Git common directory: %w", ErrGitUnavailable, err)
	}
	commonDir, err = canonicalExistingPath(commonDir)
	if err != nil {
		return "", nil, "", fmt.Errorf("%w: canonicalizing source Git common directory: %w", ErrGitUnavailable, err)
	}
	porcelain, err := git(ctx, control, gitProbeTimeout, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", nil, "", fmt.Errorf("%w: listing Git worktrees: %w", ErrGitUnavailable, err)
	}
	records, err := parseWorktreePorcelain(porcelain)
	if err != nil {
		return "", nil, "", fmt.Errorf("%w: %w", ErrGitUnavailable, err)
	}
	if _, ok := matchingWorktree(records, control); !ok {
		return "", nil, "", fmt.Errorf("%w: recorded source checkout is not registered", ErrGitUnavailable)
	}
	return control, records, commonDir, nil
}

// gitRefExists distinguishes "the ref is absent" (show-ref exit 1) from a real
// Git failure, so a broken repository is never mistaken for a deleted branch.
func (i *Inventory) gitRefExists(ctx context.Context, control, ref string) (bool, error) {
	_, err := i.gitRunner()(ctx, control, gitProbeTimeout, "show-ref", "--verify", "--quiet", ref)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("%w: checking ref %s: %w", ErrGitUnavailable, ref, err)
}
