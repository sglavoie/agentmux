package worktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	ErrWorktreeMissing     = errors.New("worktree: checkout is missing")
	ErrWorktreeDirty       = errors.New("worktree: checkout has uncommitted changes")
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
		branch := ""
		if options.DeleteBranch {
			if preflight.metadata == nil {
				return fmt.Errorf("%w: branch deletion requires schema-v1 sidecar provenance", ErrMetadataInvalid)
			}
			branch, err = manager.preflightBranchDeletion(ctx, preflight.controlDir, preflight.entry, *preflight.metadata)
			if err != nil {
				return err
			}
		}

		git := manager.gitRunner()
		if _, err := git(ctx, preflight.controlDir, gitCommandTimeout, "worktree", "remove", preflight.entry.Path); err != nil {
			return fmt.Errorf("worktree: removing checkout %s: %w", preflight.entry.Path, err)
		}
		result.CheckoutRemoved = true

		if options.DeleteBranch {
			if _, err := git(ctx, preflight.controlDir, gitCommandTimeout, "branch", "-d", "--", branch); err != nil {
				return &PartialMutationError{Err: fmt.Errorf("worktree: safely deleting branch %s: %w", branch, err)}
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
		return cleanupPreflight{}, fmt.Errorf("%w: resolving checkout root: %v", ErrGitUnavailable, err)
	}
	top, err = canonicalExistingPath(top)
	if err != nil || top != path {
		return cleanupPreflight{}, fmt.Errorf("%w: Git root does not match the selected checkout", ErrGitUnavailable)
	}
	commonDir, err := gitLineWith(ctx, git, path, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return cleanupPreflight{}, fmt.Errorf("%w: resolving Git common directory: %v", ErrGitUnavailable, err)
	}
	commonDir, err = canonicalExistingPath(commonDir)
	if err != nil {
		return cleanupPreflight{}, fmt.Errorf("%w: canonicalizing Git common directory: %v", ErrGitUnavailable, err)
	}
	porcelain, err := git(ctx, path, gitProbeTimeout, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return cleanupPreflight{}, fmt.Errorf("%w: listing Git worktrees: %v", ErrGitUnavailable, err)
	}
	records, err := parseWorktreePorcelain(porcelain)
	if err != nil {
		return cleanupPreflight{}, fmt.Errorf("%w: %v", ErrGitUnavailable, err)
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
	status, err := git(ctx, path, gitProbeTimeout, "status", "--porcelain=v1", "-z")
	if err != nil {
		return cleanupPreflight{}, fmt.Errorf("%w: checking checkout status: %v", ErrGitUnavailable, err)
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
			return cleanupPreflight{}, fmt.Errorf("%w: %v", ErrMetadataInvalid, err)
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

func (i *Inventory) preflightBranchDeletion(ctx context.Context, control string, entry Entry, metadata Metadata) (string, error) {
	if entry.Detached || strings.TrimSpace(entry.Branch) == "" {
		return "", ErrWorktreeDetached
	}
	branch, branchRef, err := localBranch(metadata.Branch)
	if err != nil || branch != entry.Branch {
		return "", fmt.Errorf("%w: recorded branch conflicts with the current worktree branch", ErrMetadataInvalid)
	}
	_, sourceRef, err := localBranch(metadata.SourceBranch)
	if err != nil {
		return "", fmt.Errorf("%w: no usable source branch was recorded", ErrMetadataInvalid)
	}
	branchExists, err := i.gitRefExists(ctx, control, branchRef)
	if err != nil {
		return "", err
	}
	if !branchExists {
		return "", ErrBranchMissing
	}
	sourceExists, err := i.gitRefExists(ctx, control, sourceRef)
	if err != nil {
		return "", err
	}
	if !sourceExists {
		return "", ErrSourceBranchMissing
	}
	branchHead, err := gitLineWith(ctx, i.gitRunner(), control, gitProbeTimeout, "rev-parse", "--verify", branchRef+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%w: resolving checkout branch: %v", ErrGitUnavailable, err)
	}
	if entry.HEAD != "" && branchHead != entry.HEAD {
		return "", fmt.Errorf("%w: checkout branch moved during cleanup preflight", ErrGitUnavailable)
	}
	if _, err := i.gitRunner()(ctx, control, gitProbeTimeout, "merge-base", "--is-ancestor", branchRef, sourceRef); err != nil {
		return "", fmt.Errorf("%w: %v", ErrBranchUnmerged, err)
	}
	return branch, nil
}

func (i *Inventory) finishMissingCleanup(ctx context.Context, root string, entry Entry, result *MutationResult) error {
	if err := validateAdjacentMetadataPath(root, entry); err != nil {
		return err
	}
	metadata, err := ReadMetadata(entry.MetadataPath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMetadataInvalid, err)
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
		if _, err := i.gitRunner()(ctx, control, gitProbeTimeout, "merge-base", "--is-ancestor", branchRef, sourceRef); err != nil {
			return fmt.Errorf("%w: %v", ErrBranchUnmerged, err)
		}
		if _, err := i.gitRunner()(ctx, control, gitCommandTimeout, "branch", "-d", "--", branch); err != nil {
			return fmt.Errorf("worktree: safely deleting branch %s: %w", branch, err)
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
		return "", nil, "", fmt.Errorf("%w: resolving source Git common directory: %v", ErrGitUnavailable, err)
	}
	commonDir, err = canonicalExistingPath(commonDir)
	if err != nil {
		return "", nil, "", fmt.Errorf("%w: canonicalizing source Git common directory: %v", ErrGitUnavailable, err)
	}
	porcelain, err := git(ctx, control, gitProbeTimeout, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", nil, "", fmt.Errorf("%w: listing Git worktrees: %v", ErrGitUnavailable, err)
	}
	records, err := parseWorktreePorcelain(porcelain)
	if err != nil {
		return "", nil, "", fmt.Errorf("%w: %v", ErrGitUnavailable, err)
	}
	if _, ok := matchingWorktree(records, control); !ok {
		return "", nil, "", fmt.Errorf("%w: recorded source checkout is not registered", ErrGitUnavailable)
	}
	return control, records, commonDir, nil
}

func (i *Inventory) gitRefExists(ctx context.Context, control, ref string) (bool, error) {
	_, err := i.gitRunner()(ctx, control, gitProbeTimeout, "show-ref", "--verify", "--quiet", ref)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("%w: checking ref %s: %v", ErrGitUnavailable, ref, err)
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
