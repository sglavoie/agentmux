package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// IndexPath returns the on-disk JSON path for the searchable session
// transcript index. It mirrors daemon.SocketPath's HOME-fallback so the
// index lands under the same directory tree as the daemon socket on a
// normal install, and under os.TempDir/agent-mux when HOME is empty
// (test environments, broken setups).
func IndexPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "agent-mux", "transcripts.json")
	}
	return filepath.Join(home, ".local", "share", "agent-mux", "transcripts.json")
}

// IndexKey is the stable lookup key for a SessionMeta inside the on-disk
// map: "<harness>:<id>". Exported so the TUI and daemon can address
// entries without re-deriving the format.
func IndexKey(harness, id string) string {
	return harness + ":" + id
}

// Index is the in-memory + on-disk cache of discovered transcript
// metadata. Concurrent callers (the daemon's pruner and the TUI's search
// view) each hold their own *Index value; the mutex guards against
// in-process races between Refresh, Save, Delete, and List on a single
// instance. Cross-process correctness is intentionally out of scope —
// see context.md ("No cross-process locking").
type Index struct {
	mu      sync.Mutex
	path    string
	entries map[string]SessionMeta
}

// saveLockedHookForTest lets same-package tests count successful persistence
// operations without changing the production save path.
var saveLockedHookForTest func()

// indexFile is the on-disk wrapper used for JSON serialization. A struct
// with a single field is used (instead of marshalling the bare map) so
// the file can grow a version or auxiliary fields without breaking
// existing readers.
type indexFile struct {
	Entries map[string]SessionMeta `json:"entries"`
}

// LoadIndex reads the index from the default IndexPath. A missing file
// is not an error — it yields an empty, ready-to-populate Index.
func LoadIndex() (*Index, error) {
	return LoadIndexAt(IndexPath())
}

// LoadIndexAt reads the index from path. Used by tests and any future
// caller that needs to point at a non-default location.
func LoadIndexAt(path string) (*Index, error) {
	idx := &Index{path: path, entries: make(map[string]SessionMeta)}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return idx, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	if err := idx.decodeIndexFile(dec); err != nil {
		if errors.Is(err, io.EOF) {
			return idx, nil
		}
		return nil, fmt.Errorf("decoding transcript index %s: %w", path, err)
	}
	return idx, nil
}

func (i *Index) decodeIndexFile(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("expected object start, got %v", tok)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		field, ok := tok.(string)
		if !ok {
			return fmt.Errorf("expected object field name, got %v", tok)
		}
		if field != "entries" {
			if err := skipJSONValue(dec); err != nil {
				return err
			}
			continue
		}
		if err := i.decodeEntries(dec); err != nil {
			return err
		}
	}
	tok, err = dec.Token()
	if err != nil {
		return err
	}
	delim, ok = tok.(json.Delim)
	if !ok || delim != '}' {
		return fmt.Errorf("expected object end, got %v", tok)
	}
	return nil
}

func (i *Index) decodeEntries(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("expected entries object start, got %v", tok)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("expected entry key, got %v", tok)
		}
		var meta SessionMeta
		if err := dec.Decode(&meta); err != nil {
			return err
		}
		if meta.SearchText != "" {
			if err := i.WriteSearchText(key, meta.SearchText); err != nil {
				return err
			}
			meta.SearchText = ""
		}
		i.entries[key] = meta
	}
	tok, err = dec.Token()
	if err != nil {
		return err
	}
	delim, ok = tok.(json.Delim)
	if !ok || delim != '}' {
		return fmt.Errorf("expected entries object end, got %v", tok)
	}
	return nil
}

func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for dec.More() {
			if _, err := dec.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	_, err = dec.Token()
	return err
}

// Path returns the absolute on-disk path backing this index.
func (i *Index) Path() string { return i.path }

// Len returns the number of entries currently held in memory.
func (i *Index) Len() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.entries)
}

// Save serializes the index to disk atomically (write-to-tmp + rename)
// so a crash mid-write never leaves a half-written JSON file in place.
func (i *Index) Save() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.saveLocked()
}

func (i *Index) saveLocked() (retErr error) {
	if err := os.MkdirAll(filepath.Dir(i.path), 0o755); err != nil {
		return err
	}
	for key, meta := range i.entries {
		if meta.SearchText == "" {
			continue
		}
		if err := i.WriteSearchText(key, meta.SearchText); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(indexFile{Entries: metadataOnlyEntries(i.entries)}, "", "  ")
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
	if err := os.Rename(tmp, i.path); err != nil {
		return err
	}
	if saveLockedHookForTest != nil {
		saveLockedHookForTest()
	}
	return nil
}

func metadataOnlyEntries(entries map[string]SessionMeta) map[string]SessionMeta {
	out := make(map[string]SessionMeta, len(entries))
	for key, meta := range entries {
		meta.SearchText = ""
		out[key] = meta
	}
	return out
}

// Refresh re-walks every configured profile's transcripts directory via
// the provided readers (keyed by canonical harness name) and merges the
// results into the index. Existing entries whose FileSize + FileModTime
// match the on-disk file keep their cached MessageCount, Snippet,
// StartedAt, and LastAt — no JSONL reparse needed. HiddenFromIndex is
// always preserved across rescans so the user's delete / pruner
// decisions do not get undone by the next refresh.
//
// Refresh does not remove entries whose file vanished from disk. Stale
// references are tolerated; the TUI can surface a "missing" indicator
// when opening such an entry, and Delete + Prune handle real removal.
//
// Errors from a single profile are logged and skipped — one broken
// home directory must not hide the rest. The first error encountered
// is returned so callers can surface it if desired, but on-disk state
// is still updated for the profiles that succeeded.
func (i *Index) Refresh(cfg *config.Config, readers map[string]Reader) error {
	if cfg == nil {
		return errors.New("transcript: Refresh requires a non-nil config")
	}
	if readers == nil {
		return errors.New("transcript: Refresh requires at least one reader")
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	// Snapshot the existing entries by Path so each reader's Discover can
	// short-circuit a parse when the on-disk file is byte-identical to the
	// cached metadata. Path keying is used (rather than IndexKey) because
	// the harness id is sometimes only known after parsing the first line
	// of the file (Codex), so it is unavailable at the discovery-walk stage.
	byPath := make(map[string]SessionMeta, len(i.entries))
	for _, meta := range i.entries {
		if meta.Path == "" {
			continue
		}
		byPath[meta.Path] = meta
	}
	lookup := func(path string, size int64, mtime time.Time) (SessionMeta, bool) {
		meta, ok := byPath[path]
		if !ok {
			return SessionMeta{}, false
		}
		if meta.FileSize != size || !meta.FileModTime.Equal(mtime) {
			return SessionMeta{}, false
		}
		if meta.SearchText != "" {
			return meta, true
		}
		if meta.HiddenFromIndex || meta.MessageCount == 0 {
			return meta, true
		}
		key := IndexKey(meta.Harness, meta.ID)
		exists, err := i.SearchTextExists(key)
		if err != nil {
			slog.Debug("transcript.index: search sidecar stat failed",
				"key", key, "err", err)
			return SessionMeta{}, false
		}
		if !exists {
			return SessionMeta{}, false
		}
		return meta, true
	}

	days := cfg.Defaults.SessionArchive.ScanMaxAgeDays
	var cutoff time.Time
	if days > 0 {
		cutoff = time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	}
	// days <= 0 → unlimited; leave cutoff zero so MinTime.IsZero() is true
	// in each reader and no file is filtered by age.

	var firstErr error
	for _, profile := range cfg.Profiles {
		harnessName := profile.EffectiveHarness()
		reader, ok := readers[harnessName]
		if !ok {
			slog.Debug("transcript.index: no reader for harness",
				"harness", harnessName, "profile", profile.Name)
			continue
		}
		home := config.ExpandTilde(profile.EffectiveHomeDir())
		metas, err := reader.Discover(home, DiscoverOptions{Cached: lookup, MinTime: cutoff})
		if err != nil {
			slog.Debug("transcript.index: discover failed",
				"harness", harnessName, "profile", profile.Name, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, meta := range metas {
			meta.Profile = profile.Name
			meta.Project = ResolveProject(meta.CWD, cfg.Projects)
			key := IndexKey(meta.Harness, meta.ID)
			if existing, ok := i.entries[key]; ok {
				meta.HiddenFromIndex = existing.HiddenFromIndex
				if meta.SearchText == "" && existing.FileSize == meta.FileSize && existing.FileModTime.Equal(meta.FileModTime) {
					meta.MessageCount = existing.MessageCount
					meta.Snippet = existing.Snippet
					meta.StartedAt = existing.StartedAt
					meta.LastAt = existing.LastAt
				}
			}
			if meta.SearchText != "" {
				if meta.HiddenFromIndex {
					if err := i.DeleteSearchText(key); err != nil {
						slog.Debug("transcript.index: hidden search sidecar cleanup failed",
							"key", key, "err", err)
						if firstErr == nil {
							firstErr = err
						}
					}
				} else if err := i.WriteSearchText(key, meta.SearchText); err != nil {
					slog.Debug("transcript.index: search sidecar write failed",
						"key", key, "err", err)
					if firstErr == nil {
						firstErr = err
					}
				}
				meta.SearchText = ""
			}
			i.entries[key] = meta
		}
	}
	// Persist the refreshed index so newly-populated SearchText survives a
	// process restart. Save failures are logged and swallowed — a broken
	// disk must not turn a successful in-memory refresh into a Refresh
	// error, and the caller can still operate against the in-memory copy.
	if err := i.saveLocked(); err != nil {
		slog.Debug("transcript.index: save after refresh failed",
			"path", i.path, "err", err)
	}
	return firstErr
}

// Delete removes the entry with the given key. When alsoFile is true,
// the underlying transcript file on disk is also removed (best effort:
// a missing file is not an error). Save is called before returning so
// callers do not need a separate Save() to persist the deletion.
func (i *Index) Delete(key string, alsoFile bool) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	entry, ok := i.entries[key]
	if !ok {
		return fmt.Errorf("transcript: no index entry for %q", key)
	}
	delete(i.entries, key)

	if alsoFile && entry.Path != "" {
		if err := os.Remove(entry.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			// Re-insert so the on-disk state remains consistent with
			// in-memory state when the file removal fails partway.
			i.entries[key] = entry
			return fmt.Errorf("removing transcript file %s: %w", entry.Path, err)
		}
	}
	if err := i.DeleteSearchText(key); err != nil {
		i.entries[key] = entry
		return fmt.Errorf("removing transcript search sidecar %s: %w", i.SearchTextPath(key), err)
	}
	return i.saveLocked()
}

// Get returns a snapshot of one entry by key. The boolean reports
// whether the entry was found.
func (i *Index) Get(key string) (SessionMeta, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	m, ok := i.entries[key]
	return m, ok
}

// SetHidden marks the entry's HiddenFromIndex flag. Used by Prune and by
// the TUI when the user chooses "remove from index only". A non-existent
// key returns an error so callers cannot silently lose state.
func (i *Index) SetHidden(key string, hidden bool) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	entry, ok := i.entries[key]
	if !ok {
		return fmt.Errorf("transcript: no index entry for %q", key)
	}
	if entry.HiddenFromIndex == hidden {
		return nil
	}
	entry.HiddenFromIndex = hidden
	i.entries[key] = entry
	return i.saveLocked()
}

// ListFilter narrows the result of List. An empty slice for a field is
// "no filter on that dimension". IncludeHidden controls whether
// soft-deleted entries are surfaced (the search view sets this to
// false; an admin/debug command could set it to true).
type ListFilter struct {
	Harness       []string
	Profile       []string
	Project       []string
	IncludeHidden bool
}

// List returns a copy of all entries that match the filter, sorted by
// LastAt descending so the most recent session appears first.
func (i *Index) List(filter ListFilter) []SessionMeta {
	i.mu.Lock()
	defer i.mu.Unlock()

	out := make([]SessionMeta, 0, len(i.entries))
	for _, meta := range i.entries {
		if meta.HiddenFromIndex && !filter.IncludeHidden {
			continue
		}
		if !matchAny(meta.Harness, filter.Harness) {
			continue
		}
		if !matchAny(meta.Profile, filter.Profile) {
			continue
		}
		if !matchAny(meta.Project, filter.Project) {
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(a, b int) bool {
		return out[a].LastAt.After(out[b].LastAt)
	})
	return out
}

// matchAny returns true when set is empty (no filter) or when value
// appears in set.
func matchAny(value string, set []string) bool {
	if len(set) == 0 {
		return true
	}
	for _, s := range set {
		if s == value {
			return true
		}
	}
	return false
}
