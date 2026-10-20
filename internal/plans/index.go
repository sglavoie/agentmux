package plans

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/projectresolve"
	"github.com/sglavoie/agentmux/internal/searchstore"
	"github.com/sglavoie/agentmux/internal/textutil"
)

// IndexPath returns the on-disk JSON path for the plans index. It
// mirrors transcript.IndexPath's HOME fallback so the file lands under
// the same directory tree as the daemon socket on a normal install, and
// under os.TempDir/agent-mux when HOME is empty (test environments,
// broken setups).
func IndexPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "agent-mux", "plans.json")
	}
	return filepath.Join(home, ".local", "share", "agent-mux", "plans.json")
}

// Index is the filesystem cache for discovered plan metadata. The mutex guards
// one instance, while a stable advisory lock coordinates independent instances
// and processes during mutations.
type Index struct {
	mu        sync.Mutex
	path      string
	stateRoot string
	store     *searchstore.Store
	entries   map[string]PlanMeta

	filesystemBase  map[string]PlanMeta
	filesystemDirty bool
}

// indexFile is the on-disk wrapper used for JSON serialization. A
// single-field envelope keeps the file forward-compatible with future
// auxiliary fields without breaking existing readers.
type indexFile struct {
	Entries map[string]PlanMeta `json:"entries"`
}

// LoadIndex reads the index from the default IndexPath.
func LoadIndex() (*Index, error) {
	idx, err := LoadIndexAt(IndexPath())
	if idx != nil {
		idx.stateRoot = agentloop.DefaultStateRoot()
	}
	return idx, err
}

// loadIndexAtStrict decodes a complete metadata file without recovery. Public
// callers use LoadIndexAt, which quarantines malformed disposable caches.
func loadIndexAtStrict(path string) (*Index, error) {
	idx := &Index{path: path, entries: make(map[string]PlanMeta)}
	idx.store = searchstore.New(idx.path)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return idx, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return idx, nil
	}
	var wrapper indexFile
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, &indexDecodeError{path: path, err: err}
	}
	if wrapper.Entries != nil {
		idx.entries = wrapper.Entries
	}
	return idx, nil
}

// Path returns the absolute on-disk path backing this index.
func (i *Index) Path() string { return i.path }

// Len returns the number of entries currently held in memory.
func (i *Index) Len() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.entries)
}

// Get returns a snapshot of one entry by key. The boolean reports
// whether the entry was found.
func (i *Index) Get(key string) (PlanMeta, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	m, ok := i.entries[key]
	return m, ok
}

// List returns a copy of every entry, sorted by ModTime descending so
// the most recently touched plan appears first.
func (i *Index) List() []PlanMeta {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]PlanMeta, 0, len(i.entries))
	for _, meta := range i.entries {
		out = append(out, meta)
	}
	sort.Slice(out, func(a, b int) bool {
		return out[a].ModTime.After(out[b].ModTime)
	})
	return out
}

// Save serializes the index to disk atomically (write-to-tmp + rename)
// so a crash mid-write never leaves a half-written JSON file in place.
func (i *Index) Save() error {
	return i.saveFilesystem()
}

func (i *Index) saveLocked() error {
	data, err := json.MarshalIndent(indexFile{Entries: i.entries}, "", "  ")
	if err != nil {
		return err
	}
	return writePlanIndex(i.path, data, 0o644)
}

// Delete drops the entry with the given key and removes its sidecar.
// File-level deletion of the underlying plan directory is intentionally
// deferred — the TUI may add it in a later session.
func (i *Index) Delete(key string) error {
	return i.deleteFilesystem(key)
}

// Refresh walks the configured plans root, indexes every top-level plan
// directory containing at least one .md file (depth <= 2), and prunes
// entries whose backing directory has disappeared from disk or no longer
// contains eligible markdown. Per-plan failures preserve cached or useful
// partial rows and are returned as warnings without hiding the rest.
func (i *Index) Refresh(root string, projects ...config.Session) error {
	return i.refreshFilesystem(root, projects)
}

// refreshPlanLocked rebuilds the PlanMeta for a single plan directory or
// returns (nil, false, nil) when the directory has no eligible .md files. The
// caller must have exclusive ownership of i.entries; filesystem discovery
// supplies a private worker Index. The boolean reports whether SearchText was
// rebuilt and must be written by the caller after its merge-time freshness
// check.
func (i *Index) refreshPlanLocked(planDir string, resolver projectresolve.Resolver) (*PlanMeta, bool, error) {
	type fileInfo struct {
		rel  string
		size int64
		mod  time.Time
	}
	var files []fileInfo
	walkErr := filepath.WalkDir(planDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == planDir {
			return nil
		}
		rel, relErr := filepath.Rel(planDir, path)
		if relErr != nil {
			return relErr
		}
		depth := strings.Count(rel, string(filepath.Separator)) + 1
		if d.IsDir() {
			if depth >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if depth > 2 {
			return nil
		}
		if filepath.Ext(d.Name()) != ".md" {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files = append(files, fileInfo{rel: rel, size: info.Size(), mod: info.ModTime()})
		return nil
	})
	if len(files) == 0 {
		if cached, ok := i.entries[PlanKey(planDir)]; ok && walkErr != nil {
			return &cached, false, walkErr
		}
		return nil, false, walkErr
	}

	sort.Slice(files, func(a, b int) bool { return files[a].rel < files[b].rel })

	var size int64
	var modTime time.Time
	rels := make([]string, 0, len(files))
	for _, f := range files {
		size += f.size
		if f.mod.After(modTime) {
			modTime = f.mod
		}
		rels = append(rels, f.rel)
	}

	displayName := DisplayName(filepath.Base(planDir))
	cwd, associationErr := discoverPlanCWD(planDir, i.stateRoot)

	// Cache hit: identical file list, aggregate size, and mtime means
	// the corpus matches the cached snapshot. Reuse the cached snippet
	// and skip the per-file read + sidecar rewrite when the search
	// sidecar still exists; otherwise fall through to rebuild it.
	cached, cachedOK := i.entries[PlanKey(planDir)]
	if associationErr != nil && cachedOK {
		return &cached, false, associationErr
	}
	if cachedOK && walkErr == nil && equalStrings(cached.Files, rels) && cached.Size == size && cached.ModTime.Equal(modTime) && i.sidecarExists(PlanKey(planDir)) {
		cached.Files = rels
		cached.Name = displayName
		cached.DirPath = planDir
		cached.SearchText = ""
		cached.CWD = cwd
		cached = classifyPlanProject(cached, resolver)
		return &cached, false, nil
	}

	var searchBuilder strings.Builder
	var manifestSnippet, firstSnippet string
	var readWarning error
	var readableFiles []string
	var readableSize int64
	var readableModTime time.Time
	for _, f := range files {
		full := filepath.Join(planDir, f.rel)
		if refreshPlanBeforeReadForTest != nil {
			refreshPlanBeforeReadForTest(full)
		}
		data, readErr := os.ReadFile(full)
		if readErr != nil {
			readWarning = errors.Join(readWarning, fmt.Errorf("reading %s: %w", full, readErr))
			continue
		}
		readableFiles = append(readableFiles, f.rel)
		readableSize += f.size
		if f.mod.After(readableModTime) {
			readableModTime = f.mod
		}
		if searchBuilder.Len() > 0 {
			searchBuilder.WriteByte('\n')
		}
		searchBuilder.WriteString(strings.ToLower(string(data)))
		if firstSnippet == "" {
			firstSnippet = extractSnippet(string(data))
		}
		if manifestSnippet == "" && filepath.Base(f.rel) == "manifest.md" {
			manifestSnippet = extractSnippet(string(data))
		}
	}
	snippet := manifestSnippet
	if snippet == "" {
		snippet = firstSnippet
	}
	if snippet == "" && cachedOK {
		snippet = cached.Snippet
	}

	meta := &PlanMeta{
		Name:       displayName,
		DirPath:    planDir,
		Files:      rels,
		Size:       size,
		ModTime:    modTime,
		Snippet:    snippet,
		SearchText: searchBuilder.String(),
		CWD:        cwd,
	}
	*meta = classifyPlanProject(*meta, resolver)
	if walkErr != nil || readWarning != nil {
		if cachedOK {
			return &cached, false, errors.Join(walkErr, readWarning)
		}
		meta.Files = readableFiles
		meta.Size = readableSize
		meta.ModTime = readableModTime
		return meta, true, errors.Join(walkErr, readWarning, associationErr)
	}
	return meta, true, associationErr
}

func (i *Index) sidecarExists(key string) bool {
	ok, err := i.store.Readable(key)
	return err == nil && ok
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for idx := range a {
		if a[idx] != b[idx] {
			return false
		}
	}
	return true
}

// extractSnippet returns the first non-empty trimmed line of the given
// file contents, truncated to snippetMaxLen runes.
func extractSnippet(contents string) string {
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return textutil.Snippet(line, snippetMaxLen)
	}
	return ""
}
