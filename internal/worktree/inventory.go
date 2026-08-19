package worktree

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxInventoryWorkers = 4

// Classification describes how an owned worktree was discovered. Git state is
// authoritative; metadata only proves ownership and retains provenance.
type Classification string

const (
	ClassificationValid           Classification = "valid"
	ClassificationLegacy          Classification = "legacy"
	ClassificationMissing         Classification = "missing"
	ClassificationMetadataInvalid Classification = "metadata_invalid"
	ClassificationGitUnavailable  Classification = "git_unavailable"
)

// Usage is populated by higher layers once session and loop state is known.
// Inventory itself performs no session discovery.
type Usage struct {
	Live    bool `json:"live"`
	Looping bool `json:"looping"`
}

// Entry is one owned worktree or retained metadata record. Nullable merge
// fields distinguish an unavailable fact from a real zero or false result.
type Entry struct {
	ID                   string         `json:"id"`
	Classification       Classification `json:"classification"`
	ProjectLabel         string         `json:"project_label,omitempty"`
	Path                 string         `json:"path"`
	MetadataPath         string         `json:"metadata_path,omitempty"`
	Branch               string         `json:"branch,omitempty"`
	HEAD                 string         `json:"head,omitempty"`
	GitCommonDir         string         `json:"git_common_dir,omitempty"`
	SourceRoot           string         `json:"source_root,omitempty"`
	SourceBranch         string         `json:"source_branch,omitempty"`
	BaseCommit           string         `json:"base_commit,omitempty"`
	Profile              string         `json:"profile,omitempty"`
	Harness              string         `json:"harness,omitempty"`
	SourceConversationID string         `json:"source_conversation_id,omitempty"`
	CreatedAt            time.Time      `json:"created_at,omitempty"`
	Exists               bool           `json:"exists"`
	GitAvailable         bool           `json:"git_available"`
	StatusAvailable      bool           `json:"status_available"`
	Registered           bool           `json:"registered"`
	Locked               bool           `json:"locked"`
	LockReason           string         `json:"lock_reason,omitempty"`
	Detached             bool           `json:"detached"`
	Dirty                bool           `json:"dirty"`
	Ahead                *int           `json:"ahead,omitempty"`
	Behind               *int           `json:"behind,omitempty"`
	ReachableFromSource  *bool          `json:"reachable_from_source,omitempty"`
	Actionable           bool           `json:"actionable"`
	Usage                Usage          `json:"usage"`
	Warnings             []string       `json:"warnings,omitempty"`
}

type gitExecutor func(context.Context, string, time.Duration, ...string) ([]byte, error)

// Inventory owns Git-authoritative discovery. Its Manager alias also exposes
// safe locked cleanup, while creation remains on the narrow action-facing
// Runner contract.
type Inventory struct {
	directory      func() (string, error)
	git            gitExecutor
	workers        int
	removeMetadata func(string) error
}

// Manager is retained as a descriptive alias for callers that prefer the
// lifecycle-manager terminology.
type Manager = Inventory

// NewInventory constructs the production read-only inventory.
func NewInventory() *Inventory {
	return &Inventory{directory: defaultDirectory, git: runGit, workers: maxInventoryWorkers}
}

// NewManager constructs the production read-only lifecycle manager.
func NewManager() *Manager { return NewInventory() }

type inventoryCandidate struct {
	entry           Entry
	hasSidecar      bool
	metadataValid   bool
	checkoutSymlink bool
	checkoutInvalid bool
	canonicalRoot   string
}

// List returns all owned entries in deterministic attention order. Per-entry
// Git failures are warnings on that entry; only root scanning and cancellation
// are returned as aggregate errors.
func (i *Inventory) List(ctx context.Context) ([]Entry, error) {
	if i == nil {
		i = NewInventory()
	}
	candidates, scanErr := i.scan()
	if len(candidates) == 0 {
		return []Entry{}, scanErr
	}

	workers := i.workers
	if workers <= 0 || workers > maxInventoryWorkers {
		workers = maxInventoryWorkers
	}
	if workers > len(candidates) {
		workers = len(candidates)
	}
	git := i.git
	if git == nil {
		git = runGit
	}

	type result struct {
		entry   Entry
		include bool
	}
	jobs := make(chan inventoryCandidate)
	results := make(chan result, len(candidates))
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for candidate := range jobs {
				entry, include := probeInventoryEntry(ctx, git, candidate)
				results <- result{entry: entry, include: include}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, candidate := range candidates {
			select {
			case jobs <- candidate:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		group.Wait()
		close(results)
	}()

	entries := make([]Entry, 0, len(candidates))
	for result := range results {
		if result.include {
			result.entry.Warnings = sortedUnique(result.entry.Warnings)
			entries = append(entries, result.entry)
		}
	}
	SortEntries(entries)
	return entries, errors.Join(scanErr, ctx.Err())
}

// Resolve refreshes inventory and resolves a stable ID, unique ID prefix,
// branch, or canonical path.
func (i *Inventory) Resolve(ctx context.Context, selector string) (Entry, error) {
	entries, err := i.List(ctx)
	if err != nil {
		return Entry{}, err
	}
	return ResolveSelector(entries, selector)
}

func (i *Inventory) scan() ([]inventoryCandidate, error) {
	directory := i.directory
	if directory == nil {
		directory = defaultDirectory
	}
	root, err := directory()
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("worktree: resolving inventory root: %w", err)
	}
	root = filepath.Clean(root)
	info, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return []inventoryCandidate{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("worktree: inspecting inventory root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("worktree: inventory root %s is not a real directory", root)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("worktree: canonicalizing inventory root: %w", err)
	}
	canonicalRoot = filepath.Clean(canonicalRoot)
	root = canonicalRoot

	projects, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("worktree: reading inventory root: %w", err)
	}
	var candidates []inventoryCandidate
	var scanErrors []error
	for _, project := range projects {
		projectPath := filepath.Join(root, project.Name())
		projectInfo, lstatErr := os.Lstat(projectPath)
		if lstatErr != nil {
			scanErrors = append(scanErrors, fmt.Errorf("worktree: inspecting %s: %w", projectPath, lstatErr))
			continue
		}
		if projectInfo.Mode()&os.ModeSymlink != 0 || !projectInfo.IsDir() {
			continue
		}
		if !pathContained(canonicalRoot, projectPath) {
			continue
		}
		children, readErr := os.ReadDir(projectPath)
		if readErr != nil {
			scanErrors = append(scanErrors, fmt.Errorf("worktree: reading %s: %w", projectPath, readErr))
			continue
		}
		type pair struct {
			checkout fs.DirEntry
			sidecar  fs.DirEntry
		}
		pairs := make(map[string]*pair)
		for _, child := range children {
			name := child.Name()
			if strings.HasSuffix(name, MetadataSuffix) {
				base := strings.TrimSuffix(name, MetadataSuffix)
				if base != "" {
					if pairs[base] == nil {
						pairs[base] = &pair{}
					}
					pairs[base].sidecar = child
				}
				continue
			}
			if child.IsDir() || child.Type()&os.ModeSymlink != 0 {
				if pairs[name] == nil {
					pairs[name] = &pair{}
				}
				pairs[name].checkout = child
			}
		}
		bases := make([]string, 0, len(pairs))
		for base := range pairs {
			bases = append(bases, base)
		}
		sort.Strings(bases)
		for _, base := range bases {
			pair := pairs[base]
			if pair.checkout == nil && pair.sidecar == nil {
				continue
			}
			candidate := buildInventoryCandidate(canonicalRoot, projectPath, base, pair.checkout != nil, pair.sidecar != nil)
			if !candidate.hasSidecar && candidate.checkoutSymlink {
				continue
			}
			candidates = append(candidates, candidate)
		}
	}
	return candidates, errors.Join(scanErrors...)
}

func buildInventoryCandidate(root, parent, base string, hasCheckout, hasSidecar bool) inventoryCandidate {
	path := filepath.Clean(filepath.Join(parent, base))
	if !hasCheckout && hasSidecar {
		_, err := os.Lstat(path)
		hasCheckout = err == nil
	}
	entry := Entry{
		ID:             derivedID("entry", "", path),
		Classification: ClassificationLegacy,
		Path:           path,
		MetadataPath:   SidecarPath(path),
		Exists:         hasCheckout,
	}
	candidate := inventoryCandidate{entry: entry, hasSidecar: hasSidecar, canonicalRoot: root}
	if hasCheckout {
		if info, err := os.Lstat(path); err != nil {
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("checkout: %v", err))
			entry.Exists = false
		} else {
			candidate.checkoutSymlink = info.Mode()&os.ModeSymlink != 0
			if !info.IsDir() && !candidate.checkoutSymlink {
				candidate.checkoutInvalid = true
				entry.Warnings = append(entry.Warnings, "checkout is not a directory")
			}
			entry.CreatedAt = info.ModTime().UTC()
		}
	}
	if !hasSidecar {
		candidate.entry = entry
		return candidate
	}

	sidecarInfo, err := os.Lstat(entry.MetadataPath)
	if err != nil {
		entry.Classification = ClassificationMetadataInvalid
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("metadata: %v", err))
		candidate.entry = entry
		return candidate
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = sidecarInfo.ModTime().UTC()
	}
	if sidecarInfo.Mode()&os.ModeSymlink != 0 || !sidecarInfo.Mode().IsRegular() {
		entry.Classification = ClassificationMetadataInvalid
		entry.Warnings = append(entry.Warnings, "metadata is not a regular file")
		candidate.entry = entry
		return candidate
	}
	metadata, err := ReadMetadata(entry.MetadataPath)
	if err != nil {
		entry.Classification = ClassificationMetadataInvalid
		entry.Warnings = append(entry.Warnings, err.Error())
		candidate.entry = entry
		return candidate
	}
	candidate.metadataValid = true
	entry.ID = metadata.ID
	entry.Classification = ClassificationValid
	entry.ProjectLabel = metadata.ProjectLabel
	entry.Branch = metadata.Branch
	entry.GitCommonDir = metadata.GitCommonDir
	entry.SourceRoot = metadata.SourceRoot
	entry.SourceBranch = metadata.SourceBranch
	entry.BaseCommit = metadata.BaseCommit
	entry.Profile = metadata.Profile
	entry.Harness = metadata.Harness
	entry.SourceConversationID = metadata.SourceConversationID
	entry.CreatedAt = metadata.CreatedAt.UTC()
	metadataPath := canonicalAdjacentPath(metadata.Path)
	if metadataPath != path {
		entry.Classification = ClassificationMetadataInvalid
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("metadata path %q does not match adjacent checkout %q", metadata.Path, path))
		candidate.metadataValid = false
	}
	if !entry.Exists && entry.Classification == ClassificationValid {
		entry.Classification = ClassificationMissing
	}
	candidate.entry = entry
	return candidate
}

func canonicalAdjacentPath(path string) string {
	path = filepath.Clean(path)
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return path
	}
	return filepath.Join(filepath.Clean(parent), filepath.Base(path))
}

// SortEntries applies attention order, newest creation time, stable ID, then
// path. Higher layers can set Usage before calling it again.
func SortEntries(entries []Entry) {
	sort.SliceStable(entries, func(a, b int) bool {
		left, right := entries[a], entries[b]
		if lc, rc := attentionClass(left), attentionClass(right); lc != rc {
			return lc < rc
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.Path < right.Path
	})
}

func attentionClass(entry Entry) int {
	if entry.Classification == ClassificationMissing || entry.Classification == ClassificationMetadataInvalid ||
		entry.Classification == ClassificationGitUnavailable || !entry.GitAvailable {
		return 0
	}
	if entry.Usage.Live || entry.Usage.Looping {
		return 1
	}
	if entry.Dirty {
		return 2
	}
	return 3
}

func pathContained(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func derivedID(prefix, commonDir, path string) string {
	digest := sha256.Sum256([]byte(filepath.Clean(commonDir) + "\x00" + filepath.Clean(path)))
	return fmt.Sprintf("%s-%x", prefix, digest[:16])
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}
