package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/searchstore"
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

// Index is the in-memory + on-disk cache of discovered transcript metadata.
// The mutex guards one instance, while a stable advisory lock coordinates
// independent instances and processes during filesystem mutations.
type Index struct {
	mu      sync.Mutex
	path    string
	store   *searchstore.Store
	entries map[string]SessionMeta

	filesystemBase  map[string]SessionMeta
	filesystemDirty bool
}

// saveLockedHookForTest lets same-package tests count successful persistence
// operations without changing the production save path.
var saveLockedHookForTest func()

// readTranscriptIndexFile is a test seam for distinguishing cache read
// failures from malformed JSON without changing the production read path.
var readTranscriptIndexFile = os.ReadFile

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

// loadIndexAtStrict decodes a complete metadata file without recovery. Public
// callers use LoadIndexAt, which quarantines malformed disposable caches.
func loadIndexAtStrict(path string) (*Index, error) {
	idx := &Index{path: path, entries: make(map[string]SessionMeta)}
	idx.store = searchstore.New(idx.path)
	data, err := readTranscriptIndexFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return idx, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return idx, nil
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	if err := idx.decodeIndexFile(dec); err != nil {
		return nil, &indexDecodeError{path: path, err: err}
	}
	if tok, err := dec.Token(); err != nil {
		if errors.Is(err, io.EOF) {
			return idx, idx.migrateLegacySearchText()
		}
		return nil, &indexDecodeError{path: path, err: err}
	} else {
		return nil, &indexDecodeError{path: path, err: fmt.Errorf("trailing data after top-level object: %v", tok)}
	}
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
	return i.saveFilesystem()
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
// Errors from a single profile do not hide discoveries from other profiles or
// useful partial results. Warnings are returned after the usable discoveries
// have been merged and persisted.
func (i *Index) Refresh(cfg *config.Config, readers map[string]Reader) error {
	return i.refreshFilesystem(cfg, readers)
}

func discoveredMetaStillFresh(meta SessionMeta) bool {
	if meta.Path == "" {
		return true
	}
	info, err := os.Stat(meta.Path)
	if err != nil {
		return false
	}
	return info.Size() == meta.FileSize && info.ModTime().Equal(meta.FileModTime)
}

// SourceDeletedError reports that Delete removed the authoritative transcript
// file but could not persist the follow-up cache mutation. Callers can use
// errors.As to distinguish this partial success from a failure that left the
// source untouched.
type SourceDeletedError struct {
	Path string
	Err  error
}

func (e *SourceDeletedError) Error() string {
	return fmt.Sprintf("transcript file %s was deleted, but updating the transcript cache failed: %v", e.Path, e.Err)
}

func (e *SourceDeletedError) Unwrap() error { return e.Err }

// Delete removes the entry with the given key. When alsoFile is true,
// the underlying transcript file on disk is also removed (best effort:
// a missing file is not an error). Save is called before returning so
// callers do not need a separate Save() to persist the deletion.
func (i *Index) Delete(key string, alsoFile bool) error {
	return i.deleteFilesystem(key, alsoFile)
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
	return i.setHiddenFilesystem(key, hidden)
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
		if !meta.MatchesAnyProfile(filter.Profile) {
			continue
		}
		if !meta.MatchesAnyProject(filter.Project) {
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].LastAt.Equal(out[b].LastAt) {
			return out[a].LastAt.After(out[b].LastAt)
		}
		if !out[a].StartedAt.Equal(out[b].StartedAt) {
			return out[a].StartedAt.After(out[b].StartedAt)
		}
		if out[a].Harness != out[b].Harness {
			return out[a].Harness < out[b].Harness
		}
		return out[a].ID < out[b].ID
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

type transcriptHomeGroup struct {
	harnessName string
	home        string
	profiles    []string
}

type transcriptHomeGroupKey struct {
	harnessName string
	home        string
}

func transcriptHomeGroups(cfg *config.Config) []transcriptHomeGroup {
	if cfg == nil {
		return nil
	}
	groups := make([]transcriptHomeGroup, 0, len(cfg.Profiles))
	groupByKey := make(map[transcriptHomeGroupKey]int, len(cfg.Profiles))
	for _, profile := range cfg.Profiles {
		harnessName := profile.EffectiveHarness()
		home := config.ExpandTilde(profile.EffectiveHomeDir())
		key := transcriptHomeGroupKey{harnessName: harnessName, home: home}
		if idx, ok := groupByKey[key]; ok {
			groups[idx].profiles = append(groups[idx].profiles, profile.Name)
			continue
		}
		groupByKey[key] = len(groups)
		groups = append(groups, transcriptHomeGroup{
			harnessName: harnessName,
			home:        home,
			profiles:    []string{profile.Name},
		})
	}
	for idx := range groups {
		groups[idx].profiles = cleanProfileNames(groups[idx].profiles)
	}
	return groups
}

func displayProfileForGroup(cfg *config.Config, projectTitle, matchedProfile string, profiles []string) string {
	profiles = cleanProfileNames(profiles)
	if len(profiles) == 0 {
		return ""
	}
	for _, profile := range profiles {
		if profile == matchedProfile {
			return profile
		}
	}
	if cfg != nil {
		defaultProfile := cfg.DefaultProfileFor(projectTitle)
		for _, profile := range profiles {
			if profile == defaultProfile {
				return profile
			}
		}
	}
	return profiles[0]
}
