package plans

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/searchstore"
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

// Index is the compatibility facade for discovered plan metadata. After a
// successful migration it serves SQLite while retaining the in-memory JSON
// snapshot for read fallback. The mutex guards in-process races between
// Refresh, Save, Delete, and List on a single instance.
type Index struct {
	mu      sync.Mutex
	path    string
	store   *searchstore.Store
	entries map[string]PlanMeta

	// repo is enabled after the legacy JSON index has been imported. Keeping
	// entries in memory preserves the exact legacy path if opening SQLite or
	// importing the rollback source fails.
	repo           *Repository
	degradedReason error
	ownedStore     *datastore.Store
}

// Close releases the datastore only for facades created by LoadSQLiteIndex.
// Store-injected facades borrow their caller's pool and therefore do not close
// it, even when several such facades share the same store.
func (i *Index) Close() error {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	store := i.ownedStore
	i.ownedStore = nil
	i.mu.Unlock()
	if store == nil {
		return nil
	}
	return store.Close()
}

// indexFile is the on-disk wrapper used for JSON serialization. A
// single-field envelope keeps the file forward-compatible with future
// auxiliary fields without breaking existing readers.
type indexFile struct {
	Entries map[string]PlanMeta `json:"entries"`
}

// LoadIndex reads the index from the default IndexPath.
func LoadIndex() (*Index, error) {
	return LoadSQLiteIndex(IndexPath())
}

// LoadIndexAt reads the index from path. A missing file is not an
// error — it yields an empty, ready-to-populate Index.
func LoadIndexAt(path string) (*Index, error) {
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
		return nil, fmt.Errorf("decoding plan index %s: %w", path, err)
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
	if repo := i.repository(); repo != nil {
		entries, err := repo.List(context.Background())
		if err == nil {
			return len(entries)
		}
		slog.Debug("plans.index: SQLite length failed; using compatibility cache", "err", err)
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.entries)
}

// Get returns a snapshot of one entry by key. The boolean reports
// whether the entry was found.
func (i *Index) Get(key string) (PlanMeta, bool) {
	if repo := i.repository(); repo != nil {
		meta, found, err := repo.Get(context.Background(), key)
		if err == nil {
			return meta, found
		}
		slog.Debug("plans.index: SQLite get failed; using compatibility cache", "key", key, "err", err)
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	m, ok := i.entries[key]
	return m, ok
}

// List returns a copy of every entry, sorted by ModTime descending so
// the most recently touched plan appears first.
func (i *Index) List() []PlanMeta {
	if repo := i.repository(); repo != nil {
		entries, err := repo.List(context.Background())
		if err == nil {
			return entries
		}
		slog.Debug("plans.index: SQLite list failed; using compatibility cache", "err", err)
	}
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

// ListByWorkspace returns plans explicitly linked to workspaceID. Link data
// lives in SQLite, so a degraded compatibility-only index returns no linked
// rows rather than inferring associations from plan names or contents.
func (i *Index) ListByWorkspace(workspaceID string) ([]PlanMeta, error) {
	if workspaceID == "" {
		return nil, nil
	}
	if repo := i.repository(); repo != nil {
		return repo.ListByWorkspace(context.Background(), workspaceID)
	}
	return nil, nil
}

// LinkWorkspace records one explicit plan-to-workspace association. It never
// infers a workspace from plan text or display names.
func (i *Index) LinkWorkspace(planKey, workspaceID string) error {
	repo := i.repository()
	if repo == nil {
		if reason := i.DegradedReason(); reason != nil {
			return reason
		}
		return errors.New("plans: SQLite repository is not enabled")
	}
	return repo.LinkWorkspace(context.Background(), planKey, workspaceID)
}

// UnlinkWorkspace removes one explicit plan-to-workspace association.
func (i *Index) UnlinkWorkspace(planKey, workspaceID string) error {
	repo := i.repository()
	if repo == nil {
		if reason := i.DegradedReason(); reason != nil {
			return reason
		}
		return errors.New("plans: SQLite repository is not enabled")
	}
	return repo.UnlinkWorkspace(context.Background(), planKey, workspaceID)
}

// Save serializes the index to disk atomically (write-to-tmp + rename)
// so a crash mid-write never leaves a half-written JSON file in place.
func (i *Index) Save() error {
	// SQLite commits are handled by Repository methods. The JSON index remains
	// an untouched compatibility/rollback source once the facade is enabled.
	if i.UsesSQLite() {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.saveLocked()
}

func (i *Index) saveLocked() (retErr error) {
	if err := os.MkdirAll(filepath.Dir(i.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(indexFile{Entries: i.entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp := i.path + ".tmp"
	tf, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := tf.Write(data); err != nil {
		_ = tf.Close()
		return err
	}
	if err := tf.Sync(); err != nil {
		_ = tf.Close()
		return err
	}
	if err := tf.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, i.path)
}

// Delete drops the entry with the given key and removes its sidecar.
// File-level deletion of the underlying plan directory is intentionally
// deferred — the TUI may add it in a later session.
func (i *Index) Delete(key string) error {
	if i.UsesSQLite() {
		return i.deleteSQLite(key)
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, ok := i.entries[key]; !ok {
		return fmt.Errorf("plans: no index entry for %q", key)
	}
	delete(i.entries, key)
	if err := i.store.Delete(key); err != nil {
		return fmt.Errorf("removing plan search sidecar: %w", err)
	}
	return i.saveLocked()
}

// Refresh walks the configured plans root, indexes every top-level plan
// directory containing at least one .md file (depth <= 2), and prunes
// entries whose backing directory has disappeared from disk or no longer
// contains eligible markdown. Per-plan failures are logged and skipped —
// a single unreadable plan must not hide the rest.
func (i *Index) Refresh(root string) error {
	if i.UsesSQLite() {
		return i.refreshSQLite(root)
	}
	if root == "" {
		return errors.New("plans: Refresh requires a non-empty root path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolving plans root %s: %w", root, err)
	}
	root = absRoot

	dirEntries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("reading plans root %s: %w", root, err)
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	for _, entry := range dirEntries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "sources" {
			planDir := filepath.Join(root, name)
			key := PlanKey(planDir)
			if _, ok := i.entries[key]; ok {
				delete(i.entries, key)
				if err := i.store.Delete(key); err != nil {
					slog.Debug("plans.index: skipped-plan sidecar delete failed",
						"key", key, "err", err)
				}
			}
			continue
		}
		planDir := filepath.Join(root, name)
		meta, err := i.refreshPlanLocked(planDir)
		if err != nil {
			slog.Debug("plans.index: refresh plan failed",
				"dir", planDir, "err", err)
			continue
		}
		key := PlanKey(planDir)
		if meta == nil {
			if _, ok := i.entries[key]; ok {
				delete(i.entries, key)
				if err := i.store.Delete(key); err != nil {
					slog.Debug("plans.index: empty-plan sidecar delete failed",
						"key", key, "err", err)
				}
			}
			continue
		}
		i.entries[PlanKey(meta.DirPath)] = *meta
	}

	// Drop entries whose DirPath no longer exists on disk and remove
	// their sidecars. Directories that exist but were not candidates
	// this pass (e.g. moved outside the plans root) are kept untouched
	// so the user can still open them from a stale list.
	for key, meta := range i.entries {
		if _, statErr := os.Stat(meta.DirPath); statErr == nil {
			continue
		}
		delete(i.entries, key)
		if err := i.store.Delete(key); err != nil {
			slog.Debug("plans.index: stale sidecar delete failed",
				"key", key, "err", err)
		}
	}
	return i.saveLocked()
}

// refreshPlanLocked rebuilds the PlanMeta for a single plan directory
// or returns (nil, nil) when the directory has no eligible .md files.
// Caller must hold i.mu.
func (i *Index) refreshPlanLocked(planDir string) (*PlanMeta, error) {
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
	if walkErr != nil {
		return nil, walkErr
	}
	if len(files) == 0 {
		return nil, nil
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

	// Cache hit: identical file list, aggregate size, and mtime means
	// the corpus matches the cached snapshot. Reuse the cached snippet
	// and skip the per-file read + sidecar rewrite when the search
	// sidecar still exists; otherwise fall through to rebuild it.
	if cached, ok := i.entries[PlanKey(planDir)]; ok && equalStrings(cached.Files, rels) && cached.Size == size && cached.ModTime.Equal(modTime) && i.sidecarExists(PlanKey(planDir)) {
		cached.Files = rels
		cached.Name = displayName
		cached.DirPath = planDir
		cached.SearchText = ""
		return &cached, nil
	}

	var searchBuilder strings.Builder
	var manifestSnippet, firstSnippet string
	for _, f := range files {
		full := filepath.Join(planDir, f.rel)
		data, readErr := os.ReadFile(full)
		if readErr != nil {
			return nil, readErr
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

	key := PlanKey(planDir)
	if err := i.removeNonRegularSidecar(key); err != nil {
		return nil, err
	}
	if err := i.store.Write(key, searchBuilder.String()); err != nil {
		return nil, fmt.Errorf("writing plan search sidecar: %w", err)
	}
	return &PlanMeta{
		Name:    displayName,
		DirPath: planDir,
		Files:   rels,
		Size:    size,
		ModTime: modTime,
		Snippet: snippet,
	}, nil
}

func (i *Index) sidecarExists(key string) bool {
	ok, err := i.store.Exists(key)
	return err == nil && ok
}

func (i *Index) removeNonRegularSidecar(key string) error {
	path := i.store.Path(key)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("checking plan search sidecar: %w", err)
	}
	if info.Mode().IsRegular() {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing non-regular plan search sidecar: %w", err)
	}
	return nil
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
		return truncateSnippet(line)
	}
	return ""
}
