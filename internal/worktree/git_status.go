package worktree

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type gitWorktreeRecord struct {
	Path       string
	HEAD       string
	Branch     string
	Detached   bool
	Locked     bool
	LockReason string
}

func probeInventoryEntry(ctx context.Context, git gitExecutor, candidate inventoryCandidate) (Entry, bool) {
	entry := candidate.entry
	if !entry.Exists {
		return entry, candidate.hasSidecar
	}
	if candidate.checkoutSymlink {
		entry.Classification = ClassificationMetadataInvalid
		entry.Warnings = append(entry.Warnings, "checkout is a symbolic link; Git probes were refused")
		return entry, candidate.hasSidecar
	}
	if candidate.checkoutInvalid {
		entry.Classification = ClassificationMetadataInvalid
		entry.Actionable = false
		return entry, candidate.hasSidecar
	}
	canonicalPath, err := filepath.EvalSymlinks(entry.Path)
	if err != nil {
		return gitFailure(entry, candidate.hasSidecar, fmt.Errorf("canonicalizing checkout: %w", err))
	}
	canonicalPath = filepath.Clean(canonicalPath)
	if !pathContained(candidate.canonicalRoot, canonicalPath) {
		entry.Classification = ClassificationMetadataInvalid
		entry.Warnings = append(entry.Warnings, "checkout resolves outside the managed root")
		entry.Actionable = false
		return entry, candidate.hasSidecar
	}
	entry.Path = canonicalPath

	top, err := gitLineWith(ctx, git, entry.Path, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return gitFailure(entry, candidate.hasSidecar, fmt.Errorf("resolving Git worktree root: %w", err))
	}
	top, err = filepath.EvalSymlinks(top)
	if err != nil || filepath.Clean(top) != entry.Path {
		if err == nil {
			err = fmt.Errorf("git root %q does not match checkout", filepath.Clean(top))
		}
		return gitFailure(entry, candidate.hasSidecar, err)
	}

	commonDir, err := gitLineWith(ctx, git, entry.Path, gitProbeTimeout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return gitFailure(entry, candidate.hasSidecar, fmt.Errorf("resolving Git common directory: %w", err))
	}
	commonDir, err = canonicalExistingPath(commonDir)
	if err != nil {
		return gitFailure(entry, candidate.hasSidecar, fmt.Errorf("canonicalizing Git common directory: %w", err))
	}

	porcelain, err := git(ctx, entry.Path, gitProbeTimeout, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return gitFailure(entry, candidate.hasSidecar, fmt.Errorf("listing Git worktrees: %w", err))
	}
	records, err := parseWorktreePorcelain(porcelain)
	if err != nil {
		return gitFailure(entry, candidate.hasSidecar, err)
	}
	record, ok := matchingWorktree(records, entry.Path)
	if !ok {
		return gitFailure(entry, candidate.hasSidecar, fmt.Errorf("checkout is not registered by Git"))
	}
	entry.GitAvailable = true
	entry.Registered = true
	entry.GitCommonDir = commonDir
	entry.HEAD = record.HEAD
	entry.Branch = record.Branch
	entry.Detached = record.Detached || record.Branch == ""
	entry.Locked = record.Locked
	entry.LockReason = record.LockReason

	if !candidate.hasSidecar {
		if !strings.HasPrefix(entry.Branch, "amx/") {
			return Entry{}, false
		}
		entry.Classification = ClassificationLegacy
		entry.ID = derivedID("legacy", commonDir, entry.Path)
		entry.ProjectLabel = legacyProjectLabel(candidate.canonicalRoot, entry.Path)
	} else if candidate.metadataValid {
		metadata := candidate.entry
		if metadata.GitCommonDir != commonDir {
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("metadata Git common directory %q conflicts with Git value %q", metadata.GitCommonDir, commonDir))
		}
		if metadata.Branch != entry.Branch {
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("metadata branch %q conflicts with Git value %q", metadata.Branch, entry.Branch))
		}
	}

	status, err := git(ctx, entry.Path, gitProbeTimeout, "status", "--porcelain=v1", "-z")
	if err != nil {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("Git status unavailable: %v", err))
		entry.Actionable = false
		return entry, true
	}
	entry.StatusAvailable = true
	entry.Dirty = len(status) > 0
	entry.Actionable = candidate.metadataValid || !candidate.hasSidecar
	if candidate.hasSidecar && !candidate.metadataValid {
		entry.Actionable = false
	}

	if candidate.metadataValid && strings.TrimSpace(entry.SourceBranch) != "" {
		probeMergeFacts(ctx, git, &entry)
	}
	return entry, true
}

func gitFailure(entry Entry, hasSidecar bool, err error) (Entry, bool) {
	if entry.Classification != ClassificationMetadataInvalid {
		entry.Classification = ClassificationGitUnavailable
	}
	entry.GitAvailable = false
	entry.Actionable = false
	if err != nil {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("Git unavailable: %v", err))
	}
	return entry, hasSidecar
}

func probeMergeFacts(ctx context.Context, git gitExecutor, entry *Entry) {
	ref := strings.TrimSpace(entry.SourceBranch)
	if !strings.HasPrefix(ref, "refs/") {
		ref = "refs/heads/" + ref
	}
	if _, err := git(ctx, entry.Path, gitProbeTimeout, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("source branch %q is unavailable: %v", entry.SourceBranch, err))
		return
	}
	counts, err := git(ctx, entry.Path, gitProbeTimeout, "rev-list", "--left-right", "--count", ref+"...HEAD")
	if err != nil {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("ahead/behind unavailable for %q: %v", entry.SourceBranch, err))
		return
	}
	fields := strings.Fields(string(counts))
	if len(fields) != 2 {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("ahead/behind returned unexpected output for %q", entry.SourceBranch))
		return
	}
	behind, behindErr := strconv.Atoi(fields[0])
	ahead, aheadErr := strconv.Atoi(fields[1])
	if behindErr != nil || aheadErr != nil {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("ahead/behind returned invalid counts for %q", entry.SourceBranch))
		return
	}
	entry.Ahead, entry.Behind = intPointer(ahead), intPointer(behind)
	unique, err := git(ctx, entry.Path, gitProbeTimeout, "rev-list", "-n", "1", "HEAD", "--not", ref)
	if err != nil {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("reachability unavailable for %q: %v", entry.SourceBranch, err))
		return
	}
	reachable := strings.TrimSpace(string(unique)) == ""
	entry.ReachableFromSource = &reachable
}

func parseWorktreePorcelain(data []byte) ([]gitWorktreeRecord, error) {
	separator := "\n"
	text := string(data)
	if strings.ContainsRune(text, '\x00') {
		separator = "\x00"
	}
	fields := strings.Split(text, separator)
	var records []gitWorktreeRecord
	var current *gitWorktreeRecord
	flush := func() {
		if current != nil {
			records = append(records, *current)
			current = nil
		}
	}
	for _, raw := range fields {
		line := strings.TrimSuffix(raw, "\r")
		if line == "" {
			flush()
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		if key == "worktree" {
			flush()
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("worktree: malformed Git worktree record without a path")
			}
			current = &gitWorktreeRecord{Path: value}
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("worktree: malformed Git worktree porcelain")
		}
		switch key {
		case "HEAD":
			current.HEAD = strings.TrimSpace(value)
		case "branch":
			current.Branch = strings.TrimPrefix(strings.TrimSpace(value), "refs/heads/")
		case "detached":
			current.Detached = true
		case "locked":
			current.Locked = true
			current.LockReason = strings.TrimSpace(value)
		}
	}
	flush()
	return records, nil
}

func matchingWorktree(records []gitWorktreeRecord, path string) (gitWorktreeRecord, bool) {
	for _, record := range records {
		canonical, err := canonicalExistingPath(record.Path)
		if err == nil && canonical == path {
			return record, true
		}
	}
	return gitWorktreeRecord{}, false
}

func canonicalExistingPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func gitLineWith(ctx context.Context, git gitExecutor, directory string, timeout time.Duration, args ...string) (string, error) {
	out, err := git(ctx, directory, timeout, args...)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(out))
	if line == "" || strings.Contains(line, "\n") {
		return "", fmt.Errorf("unexpected Git output")
	}
	return line, nil
}

func legacyProjectLabel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err == nil {
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) > 1 && strings.TrimSpace(parts[0]) != "" {
			return parts[0]
		}
	}
	return filepath.Base(filepath.Dir(path))
}

func intPointer(value int) *int { return &value }
