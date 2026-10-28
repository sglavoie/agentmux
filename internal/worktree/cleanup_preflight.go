package worktree

// Synchronous revalidation for a checkout that is still present. Everything the
// inventory reported is treated as advisory here and probed again against Git
// itself, because the listing that produced the selection may be arbitrarily
// old by the time the mutation lock is held. Branch deletion adds its own
// merge and provenance proof on top.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

func (i *Inventory) revalidatePresentCleanup(ctx context.Context, root string, selected Entry) (cleanupPreflight, error) {
	if !selected.Actionable || selected.Classification == ClassificationMetadataInvalid {
		return cleanupPreflight{}, ErrMetadataInvalid
	}
	if selected.Classification == ClassificationGitUnavailable || !selected.GitAvailable || !selected.Registered {
		return cleanupPreflight{}, ErrGitUnavailable
	}
	info, err := os.Lstat(selected.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cleanupPreflight{}, ErrWorktreeMissing
		}
		return cleanupPreflight{}, fmt.Errorf("worktree: inspecting checkout: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return cleanupPreflight{}, ErrMetadataInvalid
	}
	path, err := canonicalExistingPath(selected.Path)
	if err != nil || !pathContained(root, path) {
		return cleanupPreflight{}, fmt.Errorf("%w: checkout is outside the managed root", ErrMetadataInvalid)
	}

	git := i.gitRunner()
	top, err := gitLineWith(ctx, git, path, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return cleanupPreflight{}, fmt.Errorf("%w: resolving checkout root: %w", ErrGitUnavailable, err)
	}
	top, err = canonicalExistingPath(top)
	if err != nil || top != path {
		return cleanupPreflight{}, fmt.Errorf("%w: Git root does not match the selected checkout", ErrGitUnavailable)
	}
	commonDir, err := gitLineWith(ctx, git, path, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return cleanupPreflight{}, fmt.Errorf("%w: resolving Git common directory: %w", ErrGitUnavailable, err)
	}
	commonDir, err = canonicalExistingPath(commonDir)
	if err != nil {
		return cleanupPreflight{}, fmt.Errorf("%w: canonicalizing Git common directory: %w", ErrGitUnavailable, err)
	}
	porcelain, err := git(ctx, path, gitProbeTimeout, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return cleanupPreflight{}, fmt.Errorf("%w: listing Git worktrees: %w", ErrGitUnavailable, err)
	}
	records, err := parseWorktreePorcelain(porcelain)
	if err != nil {
		return cleanupPreflight{}, fmt.Errorf("%w: %w", ErrGitUnavailable, err)
	}
	record, ok := matchingWorktree(records, path)
	if !ok {
		return cleanupPreflight{}, fmt.Errorf("%w: checkout is no longer registered", ErrGitUnavailable)
	}
	if record.Locked {
		if record.LockReason != "" {
			return cleanupPreflight{}, fmt.Errorf("%w: %s", ErrWorktreeLocked, record.LockReason)
		}
		return cleanupPreflight{}, ErrWorktreeLocked
	}
	// Keeping the named branch does not preserve a detached HEAD's commits
	// or an in-progress rebase's state. Refuse before removing the checkout,
	// even when branch deletion was not requested.
	if record.Detached || record.Branch == "" {
		return cleanupPreflight{}, ErrWorktreeDetached
	}
	// A clean index does not mean an operation is finished. Removing the
	// checkout also deletes its merge, sequencer, rebase, or bisect state.
	operation, err := checkoutOperation(ctx, git, path)
	if err != nil {
		return cleanupPreflight{}, err
	}
	if operation != "" {
		return cleanupPreflight{}, fmt.Errorf("%w: finish or abort %s in %s first", ErrWorktreeBusy, operation, path)
	}
	status, err := git(ctx, path, gitProbeTimeout, statusArgs()...)
	if err != nil {
		return cleanupPreflight{}, fmt.Errorf("%w: checking checkout status: %w", ErrGitUnavailable, err)
	}
	if len(status) != 0 {
		return cleanupPreflight{}, ErrWorktreeDirty
	}

	entry := selected
	entry.Path = path
	entry.HEAD = record.HEAD
	entry.Branch = record.Branch
	entry.Detached = record.Detached || record.Branch == ""
	entry.Locked = record.Locked
	entry.LockReason = record.LockReason
	entry.Dirty = false
	entry.StatusAvailable = true
	entry.Registered = true
	entry.GitAvailable = true
	entry.GitCommonDir = commonDir

	var metadata *Metadata
	switch selected.Classification {
	case ClassificationValid:
		value, err := ReadMetadata(selected.MetadataPath)
		if err != nil {
			return cleanupPreflight{}, fmt.Errorf("%w: %w", ErrMetadataInvalid, err)
		}
		if canonicalAdjacentPath(value.Path) != path || SidecarPath(path) != selected.MetadataPath ||
			(!record.Detached && value.Branch != record.Branch) {
			return cleanupPreflight{}, fmt.Errorf("%w: sidecar path or branch conflicts with Git", ErrMetadataInvalid)
		}
		metadataCommon, err := canonicalExistingPath(value.GitCommonDir)
		if err != nil || metadataCommon != commonDir {
			return cleanupPreflight{}, fmt.Errorf("%w: sidecar Git common directory conflicts with Git", ErrMetadataInvalid)
		}
		metadata = &value
	case ClassificationLegacy:
		if !strings.HasPrefix(record.Branch, "amx/") {
			return cleanupPreflight{}, ErrMetadataInvalid
		}
		if _, err := os.Lstat(SidecarPath(path)); err == nil || !errors.Is(err, fs.ErrNotExist) {
			return cleanupPreflight{}, ErrMetadataInvalid
		}
	default:
		return cleanupPreflight{}, ErrMetadataInvalid
	}

	controlDir, err := i.controlDirectory(ctx, records, path, commonDir, metadata)
	if err != nil {
		return cleanupPreflight{}, err
	}
	return cleanupPreflight{entry: entry, metadata: metadata, controlDir: controlDir}, nil
}

// controlDirectory picks a registered worktree other than the target to run the
// removal from. Git cannot remove the worktree a command is executing inside,
// so a checkout with no independent sibling is a refusal rather than a force.
func (i *Inventory) controlDirectory(ctx context.Context, records []gitWorktreeRecord, target, commonDir string, metadata *Metadata) (string, error) {
	candidates := make([]string, 0, len(records)+1)
	if metadata != nil {
		candidates = append(candidates, metadata.SourceRoot)
	}
	for _, record := range records {
		candidates = append(candidates, record.Path)
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		path, err := canonicalExistingPath(candidate)
		if err != nil || path == target {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if _, ok := matchingWorktree(records, path); !ok {
			continue
		}
		observed, err := gitLineWith(ctx, i.gitRunner(), path, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			continue
		}
		observed, err = canonicalExistingPath(observed)
		if err == nil && observed == commonDir {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w: no independent registered worktree is available to control cleanup", ErrGitUnavailable)
}

// branchDeletion binds the merge proof to both branch tips, so deletion can
// verify that neither ref changed after the proof.
type branchDeletion struct {
	branch, head, sourceRef, sourceHead string
}

func (i *Inventory) proveBranchDeletion(ctx context.Context, control, branch, head, sourceRef string) (branchDeletion, error) {
	sourceHead, err := gitLineWith(ctx, i.gitRunner(), control, gitProbeTimeout, "rev-parse", "--verify", sourceRef+"^{commit}")
	if err != nil {
		return branchDeletion{}, fmt.Errorf("%w: resolving source branch: %w", ErrGitUnavailable, err)
	}
	if _, err := i.gitRunner()(ctx, control, gitProbeTimeout, "merge-base", "--is-ancestor", head, sourceHead); err != nil {
		return branchDeletion{}, fmt.Errorf("%w: %w", ErrBranchUnmerged, err)
	}
	return branchDeletion{branch: branch, head: head, sourceRef: sourceRef, sourceHead: sourceHead}, nil
}

func (i *Inventory) preflightBranchDeletion(ctx context.Context, control string, entry Entry, metadata Metadata) (branchDeletion, error) {
	if entry.Detached || strings.TrimSpace(entry.Branch) == "" {
		return branchDeletion{}, ErrWorktreeDetached
	}
	branch, branchRef, err := localBranch(metadata.Branch)
	if err != nil || branch != entry.Branch {
		return branchDeletion{}, fmt.Errorf("%w: recorded branch conflicts with the current worktree branch", ErrMetadataInvalid)
	}
	_, sourceRef, err := localBranch(metadata.SourceBranch)
	if err != nil {
		return branchDeletion{}, fmt.Errorf("%w: no usable source branch was recorded", ErrMetadataInvalid)
	}
	branchExists, err := i.gitRefExists(ctx, control, branchRef)
	if err != nil {
		return branchDeletion{}, err
	}
	if !branchExists {
		return branchDeletion{}, ErrBranchMissing
	}
	sourceExists, err := i.gitRefExists(ctx, control, sourceRef)
	if err != nil {
		return branchDeletion{}, err
	}
	if !sourceExists {
		return branchDeletion{}, ErrSourceBranchMissing
	}
	branchHead, err := gitLineWith(ctx, i.gitRunner(), control, gitProbeTimeout, "rev-parse", "--verify", branchRef+"^{commit}")
	if err != nil {
		return branchDeletion{}, fmt.Errorf("%w: resolving checkout branch: %w", ErrGitUnavailable, err)
	}
	if entry.HEAD != "" && branchHead != entry.HEAD {
		return branchDeletion{}, fmt.Errorf("%w: checkout branch moved during cleanup preflight", ErrGitUnavailable)
	}
	return i.proveBranchDeletion(ctx, control, branch, branchHead, sourceRef)
}
