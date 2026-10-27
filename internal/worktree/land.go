package worktree

// Landing brings a worktree's committed work onto the source branch it was
// forked from, and only by fast-forward: the source branch ends on the very
// commit the worktree branch is on, so nothing is merged, rewritten, or
// resolved here. A source branch that has moved on is a refusal, and the way
// forward (rebase the worktree branch, then land) stays with the operator.
//
// Like Remove, Land holds the shared mutation lock, resolves its selector only
// then, and re-probes every fact it depends on immediately before the one
// mutation. It never forces. It leaves the worktree, its branch, and its
// sidecar exactly as they were; removal is a separate, later step.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

var (
	ErrNothingToLand = errors.New("worktree: the checkout branch has no commit its source branch lacks")
	ErrLandDiverged  = errors.New("worktree: the source branch has moved on; rebase the worktree branch onto it before landing")
	ErrSourceDirty   = errors.New("worktree: the source branch's checkout has uncommitted changes")
	ErrSourceBusy    = errors.New("worktree: the source branch is being rebased")
)

// LandResult reports one landing. Landed is false whenever the source branch
// was left where it was.
type LandResult struct {
	Entry        Entry  `json:"entry"`
	SourceBranch string `json:"source_branch"`
	// SourceCheckout is the worktree the source branch is checked out in, and
	// so the checkout that moved; empty when no worktree has it checked out
	// and only the branch itself was advanced.
	SourceCheckout string `json:"source_checkout,omitempty"`
	From           string `json:"from,omitempty"`
	To             string `json:"to,omitempty"`
	Commits        int    `json:"commits"`
	Landed         bool   `json:"landed"`
}

// Land fast-forwards the recorded source branch to the worktree branch.
func (i *Inventory) Land(ctx context.Context, selector string) (LandResult, error) {
	manager := i
	if manager == nil {
		manager = NewInventory()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := manager.existingMutationRoot()
	if err != nil {
		return LandResult{}, err
	}

	var result LandResult
	err = withMutationLock(ctx, root, func() error {
		entry, err := manager.Resolve(ctx, selector)
		if err != nil {
			return err
		}
		result.Entry = entry
		if !entry.Exists {
			return ErrWorktreeMissing
		}
		// The cleanup preflight is the same proof a landing needs of the
		// worktree: present, registered, unlocked, clean, and described by a
		// sidecar that agrees with Git.
		preflight, err := manager.revalidatePresentCleanup(ctx, root, entry)
		if err != nil {
			return err
		}
		result.Entry = preflight.entry
		if preflight.metadata == nil {
			return fmt.Errorf("%w: landing requires schema-v1 sidecar provenance", ErrMetadataInvalid)
		}
		if preflight.entry.Detached || strings.TrimSpace(preflight.entry.Branch) == "" {
			return ErrWorktreeDetached
		}
		_, branchRef, err := localBranch(preflight.metadata.Branch)
		if err != nil {
			return fmt.Errorf("%w: no usable worktree branch was recorded", ErrMetadataInvalid)
		}
		source, sourceRef, err := localBranch(preflight.metadata.SourceBranch)
		if err != nil {
			return fmt.Errorf("%w: no usable source branch was recorded", ErrMetadataInvalid)
		}
		result.SourceBranch = source

		git, path := manager.gitRunner(), preflight.entry.Path
		sourceExists, err := manager.gitRefExists(ctx, path, sourceRef)
		if err != nil {
			return err
		}
		if !sourceExists {
			return ErrSourceBranchMissing
		}
		to, err := gitLineWith(ctx, git, path, gitProbeTimeout, "rev-parse", "--verify", branchRef+"^{commit}")
		if err != nil {
			return fmt.Errorf("%w: resolving checkout branch: %w", ErrGitUnavailable, err)
		}
		if preflight.entry.HEAD != "" && to != preflight.entry.HEAD {
			return fmt.Errorf("%w: checkout branch moved during landing preflight", ErrGitUnavailable)
		}
		from, err := gitLineWith(ctx, git, path, gitProbeTimeout, "rev-parse", "--verify", sourceRef+"^{commit}")
		if err != nil {
			return fmt.Errorf("%w: resolving source branch: %w", ErrGitUnavailable, err)
		}
		result.From, result.To = from, to
		counts, err := git(ctx, path, gitProbeTimeout, "rev-list", "--left-right", "--count", from+"..."+to)
		if err != nil {
			return fmt.Errorf("%w: counting commits: %w", ErrGitUnavailable, err)
		}
		fields := strings.Fields(string(counts))
		if len(fields) != 2 {
			return fmt.Errorf("%w: unexpected commit counts", ErrGitUnavailable)
		}
		behind, behindErr := strconv.Atoi(fields[0])
		ahead, aheadErr := strconv.Atoi(fields[1])
		if behindErr != nil || aheadErr != nil {
			return fmt.Errorf("%w: unexpected commit counts", ErrGitUnavailable)
		}
		result.Commits = ahead
		if ahead == 0 {
			return ErrNothingToLand
		}
		if behind != 0 {
			return fmt.Errorf("%w (%q is %d ahead of the worktree branch)", ErrLandDiverged, source, behind)
		}

		porcelain, err := git(ctx, path, gitProbeTimeout, "worktree", "list", "--porcelain", "-z")
		if err != nil {
			return fmt.Errorf("%w: listing Git worktrees: %w", ErrGitUnavailable, err)
		}
		records, err := parseWorktreePorcelain(porcelain)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrGitUnavailable, err)
		}
		for _, record := range records {
			if record.Branch == source && !record.Detached {
				return manager.landInCheckout(ctx, record.Path, from, to, &result)
			}
		}
		// No worktree has the source branch checked out. One may still be in
		// the middle of rebasing it, with HEAD detached until the rebase ends
		// and rewrites the branch; advancing it now would be undone then.
		for _, record := range records {
			if rebasing(ctx, git, record.Path, sourceRef) {
				return fmt.Errorf("%w in %s", ErrSourceBusy, record.Path)
			}
		}
		// The old value makes the update conditional: if the branch moved
		// since it was read, Git refuses rather than discarding that move.
		if _, err := git(ctx, path, gitCommandTimeout, "update-ref", "-m", "amx worktree land: fast-forward", sourceRef, to, from); err != nil {
			return fmt.Errorf("worktree: advancing %s: %w", source, err)
		}
		result.Landed = true
		return nil
	})
	return result, err
}

// landInCheckout fast-forwards the source branch where it is checked out, so
// that checkout's files and index move with it. Tracked changes there are a
// refusal; untracked files, including ignored files, are left to Git with an
// explicit instruction to refuse overwriting them.
func (i *Inventory) landInCheckout(ctx context.Context, checkout, from, to string, result *LandResult) error {
	git := i.gitRunner()
	canonical, err := canonicalExistingPath(checkout)
	if err != nil {
		return fmt.Errorf("%w: the source branch's checkout %s is unavailable: %w", ErrGitUnavailable, checkout, err)
	}
	result.SourceCheckout = canonical
	head, err := gitLineWith(ctx, git, canonical, gitProbeTimeout, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != from {
		return fmt.Errorf("%w: the source branch's checkout moved during landing preflight", ErrGitUnavailable)
	}
	status, err := git(ctx, canonical, gitProbeTimeout, statusArgs("--untracked-files=no")...)
	if err != nil {
		return fmt.Errorf("%w: checking the source branch's checkout: %w", ErrGitUnavailable, err)
	}
	if len(status) != 0 {
		return fmt.Errorf("%w: %s", ErrSourceDirty, canonical)
	}
	// Merge the exact commit whose ancestry and count were validated. The
	// worktree's branch can move while another process commits there. Explicit
	// options also keep branch merge preferences from turning a landing into
	// staged squash changes, or Git's default from overwriting ignored files.
	if _, err := git(ctx, canonical, gitCommandTimeout, "merge", "--ff-only", "--no-squash", "--no-overwrite-ignore", "--no-edit", to); err != nil {
		return fmt.Errorf("worktree: fast-forwarding %s in %s: %w", result.SourceBranch, canonical, err)
	}
	result.Landed = true
	return nil
}

// rebasing reports whether the worktree at path is in a rebase of ref. Git
// records the branch a rebase will rewrite in a head-name file of its state
// directory.
func rebasing(ctx context.Context, git gitExecutor, path, ref string) bool {
	for _, state := range []string{"rebase-merge/head-name", "rebase-apply/head-name"} {
		file, err := gitLineWith(ctx, git, path, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--git-path", state)
		if err != nil {
			continue
		}
		if name, err := os.ReadFile(file); err == nil && strings.TrimSpace(string(name)) == ref {
			return true
		}
	}
	return false
}
