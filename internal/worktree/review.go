package worktree

// Review answers what a managed worktree changed since it left its source
// branch, and how that work would land there. It is read-only: every probe is
// a query, and the landing prediction merges trees in Git's object database
// (`git merge-tree --write-tree`) without touching an index or a checkout, so
// reviewing a worktree an agent is working in disturbs nothing.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// LandVerdict predicts what bringing the worktree branch onto its recorded
// source branch would take. It describes committed work only: uncommitted
// changes are reviewed but cannot land.
type LandVerdict string

const (
	// LandsNothing: the branch holds no commit its source lacks.
	LandsNothing LandVerdict = "nothing_to_land"
	// LandsFastForward: the source has not moved since the branch left it.
	LandsFastForward LandVerdict = "fast_forward"
	// LandsDivergedClean: both moved, and the two merge without conflict.
	LandsDivergedClean LandVerdict = "diverged_clean"
	// LandsConflicts: both moved, and Review.Conflicts names what collides.
	LandsConflicts LandVerdict = "conflicts"
	// LandsUnknown: both moved and the merge could not be predicted, which a
	// warning explains. It is never a claim that the merge is clean.
	LandsUnknown LandVerdict = "unknown"
)

// ReviewOptions selects what a review covers.
type ReviewOptions struct {
	// CommittedOnly leaves uncommitted changes out of the file list and patch.
	CommittedOnly bool
	// Patch also returns the unified diff.
	Patch bool
	// Color asks Git to color the patch for a terminal.
	Color bool
}

// FileChange is one path's line counts. A binary file has no counts.
type FileChange struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary"`
}

// Review is one worktree compared with the merge base of its source branch.
type Review struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	Branch       string `json:"branch,omitempty"`
	SourceBranch string `json:"source_branch"`
	HEAD         string `json:"head"`
	MergeBase    string `json:"merge_base"`
	Ahead        int    `json:"ahead"`
	Behind       int    `json:"behind"`
	// Dirty reports uncommitted changes whether or not the review covers them.
	Dirty bool `json:"dirty"`
	// CommittedOnly echoes the option, so a reader knows what Files spans.
	CommittedOnly bool         `json:"committed_only"`
	Files         []FileChange `json:"files"`
	Added         int          `json:"added"`
	Deleted       int          `json:"deleted"`
	// Untracked lists new files Git does not track yet. A diff cannot show
	// them, and work an agent has not staged would otherwise go unseen.
	Untracked []string    `json:"untracked"`
	Lands     LandVerdict `json:"lands"`
	Conflicts []string    `json:"conflicts"`
	Warnings  []string    `json:"warnings"`
	Patch     string      `json:"patch,omitempty"`
}

// Review compares entry with its recorded source branch. The entry is taken as
// a selection only; every fact reported is read from Git now.
func (i *Inventory) Review(ctx context.Context, entry Entry, options ReviewOptions) (Review, error) {
	if i == nil {
		i = NewInventory()
	}
	if !entry.Exists {
		return Review{}, ErrWorktreeMissing
	}
	if entry.Classification != ClassificationValid || strings.TrimSpace(entry.SourceBranch) == "" {
		return Review{}, fmt.Errorf("%w: no source branch was recorded for this worktree", ErrMetadataInvalid)
	}
	if !entry.GitAvailable || !entry.Registered {
		return Review{}, ErrGitUnavailable
	}
	_, sourceRef, err := localBranch(entry.SourceBranch)
	if err != nil {
		return Review{}, fmt.Errorf("%w: no usable source branch was recorded", ErrMetadataInvalid)
	}
	git, path := i.gitRunner(), entry.Path
	if _, err := git(ctx, path, gitProbeTimeout, "rev-parse", "--verify", "--quiet", sourceRef+"^{commit}"); err != nil {
		return Review{}, ErrSourceBranchMissing
	}
	head, err := gitLineWith(ctx, git, path, gitProbeTimeout, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return Review{}, fmt.Errorf("%w: resolving checkout HEAD: %w", ErrGitUnavailable, err)
	}
	mergeBase, err := gitLineWith(ctx, git, path, gitProbeTimeout, "merge-base", sourceRef, head)
	if err != nil {
		return Review{}, fmt.Errorf("%w: the checkout shares no history with %q: %w", ErrGitUnavailable, entry.SourceBranch, err)
	}
	counts, err := git(ctx, path, gitProbeTimeout, "rev-list", "--left-right", "--count", sourceRef+"..."+head)
	if err != nil {
		return Review{}, fmt.Errorf("%w: counting commits: %w", ErrGitUnavailable, err)
	}
	fields := strings.Fields(string(counts))
	if len(fields) != 2 {
		return Review{}, fmt.Errorf("%w: unexpected commit counts", ErrGitUnavailable)
	}
	behind, behindErr := strconv.Atoi(fields[0])
	ahead, aheadErr := strconv.Atoi(fields[1])
	if behindErr != nil || aheadErr != nil {
		return Review{}, fmt.Errorf("%w: unexpected commit counts", ErrGitUnavailable)
	}
	status, err := git(ctx, path, gitProbeTimeout, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return Review{}, fmt.Errorf("%w: checking checkout status: %w", ErrGitUnavailable, err)
	}

	review := Review{
		ID: entry.ID, Path: path, Branch: entry.Branch, SourceBranch: entry.SourceBranch,
		HEAD: head, MergeBase: mergeBase, Ahead: ahead, Behind: behind,
		Dirty: len(status) > 0, CommittedOnly: options.CommittedOnly,
		Files: []FileChange{}, Untracked: []string{}, Conflicts: []string{}, Warnings: []string{},
	}
	if !options.CommittedOnly {
		review.Untracked = untrackedPaths(status)
	}

	// The diff runs from the merge base, not from the source branch's tip:
	// what the source gained meanwhile is not something this worktree did.
	span := []string{mergeBase}
	if options.CommittedOnly {
		span = append(span, head)
	}
	numstat, err := git(ctx, path, gitCommandTimeout, append([]string{"diff", "--no-ext-diff", "--no-renames", "--numstat", "-z"}, span...)...)
	if err != nil {
		return Review{}, fmt.Errorf("%w: summarizing changes: %w", ErrGitUnavailable, err)
	}
	review.Files = parseNumstat(numstat)
	for _, file := range review.Files {
		review.Added += file.Added
		review.Deleted += file.Deleted
	}

	switch {
	case ahead == 0:
		review.Lands = LandsNothing
	case behind == 0:
		review.Lands = LandsFastForward
	default:
		review.Lands, review.Conflicts = predictMerge(ctx, git, path, sourceRef, head)
		if review.Lands == LandsUnknown {
			review.Warnings = append(review.Warnings, "the merge could not be predicted; `git merge-tree --write-tree` needs Git 2.38 or newer")
		}
	}

	if options.Patch {
		color := "--color=never"
		if options.Color {
			color = "--color=always"
		}
		patch, err := git(ctx, path, gitCommandTimeout, append([]string{"diff", "--no-ext-diff", color}, span...)...)
		if err != nil {
			return Review{}, fmt.Errorf("%w: reading the patch: %w", ErrGitUnavailable, err)
		}
		review.Patch = string(patch)
	}
	return review, nil
}

// predictMerge merges the two commits' trees without a checkout. Git exits 0
// for a clean merge and 1 for a conflicted one, listing the conflicted paths
// after the tree it wrote; anything else predicts nothing.
func predictMerge(ctx context.Context, git gitExecutor, path, sourceRef, head string) (LandVerdict, []string) {
	out, err := git(ctx, path, gitCommandTimeout, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", sourceRef, head)
	if err == nil {
		return LandsDivergedClean, []string{}
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return LandsUnknown, []string{}
	}
	fields := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if len(fields) < 2 {
		return LandsUnknown, []string{}
	}
	return LandsConflicts, sortedUnique(fields[1:])
}

// parseNumstat reads `git diff --numstat -z --no-renames`: one
// "added<TAB>deleted<TAB>path" record per NUL, with "-" counts for a binary.
func parseNumstat(data []byte) []FileChange {
	files := []FileChange{}
	for record := range strings.SplitSeq(string(data), "\x00") {
		parts := strings.SplitN(record, "\t", 3)
		if len(parts) != 3 || parts[2] == "" {
			continue
		}
		change := FileChange{Path: parts[2]}
		added, addedErr := strconv.Atoi(parts[0])
		deleted, deletedErr := strconv.Atoi(parts[1])
		if addedErr != nil || deletedErr != nil {
			change.Binary = true
		} else {
			change.Added, change.Deleted = added, deleted
		}
		files = append(files, change)
	}
	sort.Slice(files, func(a, b int) bool { return files[a].Path < files[b].Path })
	return files
}

// untrackedPaths picks the "??" records out of `git status --porcelain=v1 -z`.
// A rename record carries its old path as a second field, which never starts
// with "?? " and is its own NUL-terminated field, so it is skipped with it.
func untrackedPaths(status []byte) []string {
	paths := []string{}
	fields := strings.Split(string(status), "\x00")
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if len(field) < 4 {
			continue
		}
		switch {
		case strings.HasPrefix(field, "?? "):
			paths = append(paths, field[3:])
		case field[0] == 'R' || field[0] == 'C':
			index++
		}
	}
	sort.Strings(paths)
	return paths
}
