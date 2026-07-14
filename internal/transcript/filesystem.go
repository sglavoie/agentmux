package transcript

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"reflect"
	"time"

	"github.com/sglavoie/agentmux/internal/cachefile"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/projectresolve"
	"github.com/sglavoie/agentmux/internal/searchstore"
)

type indexDecodeError struct {
	path string
	err  error
}

func (e *indexDecodeError) Error() string {
	return fmt.Sprintf("decoding transcript index %s: %v", e.path, e.err)
}

func (e *indexDecodeError) Unwrap() error { return e.err }

var quarantineTranscriptIndex = cachefile.Quarantine

// removeTranscriptSearchStore is a test seam for verifying that rebuilds
// reset disposable sidecars while holding the cache advisory lock.
var removeTranscriptSearchStore = os.RemoveAll

// LoadIndexAt loads a JSON cache with corruption recovery and advisory-locked
// filesystem mutations. A non-nil index may be returned with a warning;
// callers should keep that usable snapshot and surface the warning separately.
func LoadIndexAt(path string) (*Index, error) {
	idx := newFilesystemIndex(path)
	err := cachefile.WithExclusive(path, func() error {
		loaded, loadErr := loadIndexAtStrict(path)
		if loaded != nil {
			idx.entries = cloneSessionEntries(loaded.entries)
			idx.filesystemBase = cloneSessionEntries(loaded.entries)
			idx.filesystemDirty = sessionEntriesHaveInlineSearch(idx.entries)
			return loadErr
		}
		var decodeErr *indexDecodeError
		if !errors.As(loadErr, &decodeErr) {
			return fmt.Errorf("loading transcript cache %s: %w", path, loadErr)
		}
		quarantined, quarantineErr := quarantineTranscriptIndex(path)
		if quarantineErr != nil {
			return errors.Join(
				fmt.Errorf("transcript cache is malformed: %w", loadErr),
				fmt.Errorf("quarantining transcript cache %s: %w", path, quarantineErr),
			)
		}
		return fmt.Errorf("transcript cache quarantined as %s: %w", quarantined, loadErr)
	})
	return idx, err
}

func newFilesystemIndex(path string) *Index {
	entries := make(map[string]SessionMeta)
	return &Index{
		path:           path,
		store:          searchstore.New(path),
		entries:        entries,
		filesystemBase: make(map[string]SessionMeta),
	}
}

// reloadFilesystemLocked replaces the in-memory snapshot with the latest
// complete on-disk snapshot. Unsaved local changes from a prior persistence
// warning are replayed as a diff so a later operation can repair the cache
// without discarding results already returned to the caller. The boolean
// reports whether a following mutation may safely persist: a successfully
// loaded or quarantined cache may be rewritten, while a hard read failure must
// leave the on-disk cache untouched. The caller holds i.mu and the cachefile
// advisory lock.
func (i *Index) reloadFilesystemLocked() (bool, error) {
	current := cloneSessionEntries(i.entries)
	base := cloneSessionEntries(i.filesystemBase)
	dirty := i.filesystemDirty || !reflect.DeepEqual(current, base)

	loaded, err := loadIndexAtStrict(i.path)
	if loaded != nil {
		disk := cloneSessionEntries(loaded.entries)
		i.filesystemBase = cloneSessionEntries(disk)
		if dirty {
			applySessionDiff(disk, current, base)
		}
		i.entries = disk
		i.filesystemDirty = dirty || sessionEntriesHaveInlineSearch(disk)
		return true, err
	}

	var decodeErr *indexDecodeError
	if errors.As(err, &decodeErr) {
		quarantined, quarantineErr := quarantineTranscriptIndex(i.path)
		if quarantineErr != nil {
			return true, errors.Join(
				fmt.Errorf("transcript cache is malformed: %w", err),
				fmt.Errorf("quarantining transcript cache %s: %w", i.path, quarantineErr),
			)
		}
		// The last in-memory snapshot is still useful. Treat it as dirty
		// against the now-empty disk so the current operation rewrites it.
		i.filesystemBase = make(map[string]SessionMeta)
		i.filesystemDirty = len(i.entries) > 0
		return true, fmt.Errorf("transcript cache quarantined as %s: %w", quarantined, err)
	}
	return false, fmt.Errorf("loading transcript cache %s: %w", i.path, err)
}

func (i *Index) persistFilesystemLocked() (warning, persistErr error) {
	warning, persistErr = i.saveLocked()
	if persistErr != nil {
		i.filesystemDirty = true
		return warning, persistErr
	}
	i.filesystemBase = cloneSessionEntries(i.entries)
	// Inline search bodies remain dirty after a successful metadata write so a
	// later mutation retries their sidecar migration. They are nevertheless
	// present in filesystemBase because the metadata write retained them.
	i.filesystemDirty = sessionEntriesHaveInlineSearch(i.entries)
	return warning, nil
}

func (i *Index) filesystemSnapshot() (map[string]SessionMeta, error) {
	var snapshot map[string]SessionMeta
	err := cachefile.WithExclusive(i.path, func() error {
		i.mu.Lock()
		defer i.mu.Unlock()
		_, warning := i.reloadFilesystemLocked()
		snapshot = cloneSessionEntries(i.entries)
		return warning
	})
	if snapshot == nil {
		snapshot = make(map[string]SessionMeta)
	}
	return snapshot, err
}

func (i *Index) saveFilesystem() error {
	return cachefile.WithExclusive(i.path, func() error {
		i.mu.Lock()
		defer i.mu.Unlock()
		canPersist, warning := i.reloadFilesystemLocked()
		if !canPersist {
			return warning
		}
		persistWarning, persistErr := i.persistFilesystemLocked()
		return errors.Join(warning, persistWarning, persistErr)
	})
}

func (i *Index) deleteFilesystem(key string, alsoFile bool) error {
	return cachefile.WithExclusive(i.path, func() error {
		i.mu.Lock()
		defer i.mu.Unlock()
		canPersist, warning := i.reloadFilesystemLocked()
		if !canPersist {
			return warning
		}
		entry, ok := i.entries[key]
		if !ok {
			return errors.Join(warning, fmt.Errorf("transcript: no index entry for %q", key))
		}
		// Delete the disposable sidecar before the source file. If sidecar
		// removal fails, the operation must leave both the authoritative source
		// and the in-memory cache state unchanged.
		if err := i.store.Delete(key); err != nil {
			return errors.Join(warning, fmt.Errorf("removing transcript search sidecar %s: %w", i.SearchTextPath(key), err))
		}
		sourceDeleted := false
		if alsoFile && entry.Path != "" {
			if err := os.Remove(entry.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return errors.Join(warning, fmt.Errorf("removing transcript file %s: %w", entry.Path, err))
			} else if err == nil {
				sourceDeleted = true
			}
		}
		delete(i.entries, key)
		i.filesystemDirty = true
		persistWarning, persistErr := i.persistFilesystemLocked()
		if sourceDeleted && persistErr != nil {
			persistErr = &SourceDeletedError{Path: entry.Path, Err: persistErr}
		}
		return errors.Join(warning, persistWarning, persistErr)
	})
}

func (i *Index) setHiddenFilesystem(key string, hidden bool) error {
	return cachefile.WithExclusive(i.path, func() error {
		i.mu.Lock()
		defer i.mu.Unlock()
		canPersist, warning := i.reloadFilesystemLocked()
		if !canPersist {
			return warning
		}
		entry, ok := i.entries[key]
		if !ok {
			return errors.Join(warning, fmt.Errorf("transcript: no index entry for %q", key))
		}
		if hidden {
			if err := i.store.Delete(key); err != nil {
				return errors.Join(warning, fmt.Errorf("removing transcript search sidecar %s: %w", i.SearchTextPath(key), err))
			}
			entry.SearchText = ""
		}
		if entry.HiddenFromIndex == hidden {
			i.entries[key] = entry
			return warning
		}
		entry.HiddenFromIndex = hidden
		i.entries[key] = entry
		i.filesystemDirty = true
		persistWarning, persistErr := i.persistFilesystemLocked()
		return errors.Join(warning, persistWarning, persistErr)
	})
}

// refreshFilesystem discovers without holding the advisory lock, then reloads
// and merges under the lock. This keeps long source scans away from render/read
// paths while still preserving a delete or hide that wins during discovery.
func (i *Index) refreshFilesystem(cfg *config.Config, readers map[string]Reader) error {
	return i.refreshFilesystemWithOptions(cfg, readers, false)
}

func (i *Index) rebuildFilesystem(cfg *config.Config, readers map[string]Reader) error {
	return i.refreshFilesystemWithOptions(cfg, readers, true)
}

func (i *Index) refreshFilesystemWithOptions(cfg *config.Config, readers map[string]Reader, rebuildSidecars bool) error {
	if cfg == nil {
		return errors.New("transcript: Refresh requires a non-nil config")
	}
	if readers == nil {
		return errors.New("transcript: Refresh requires at least one reader")
	}

	snapshot, loadWarning := i.filesystemSnapshot()
	byPath := make(map[string]SessionMeta, len(snapshot))
	for _, meta := range snapshot {
		if meta.Path != "" {
			byPath[meta.Path] = meta
		}
	}
	lookup := func(path string, size int64, mtime time.Time) (SessionMeta, bool) {
		if rebuildSidecars {
			return SessionMeta{}, false
		}
		meta, ok := byPath[path]
		if !ok || meta.FileSize != size || !meta.FileModTime.Equal(mtime) {
			return SessionMeta{}, false
		}
		if meta.SearchText != "" || meta.HiddenFromIndex || meta.MessageCount == 0 {
			return meta, true
		}
		readable, err := i.store.Readable(IndexKey(meta.Harness, meta.ID))
		if err != nil {
			slog.Debug("transcript.index: search sidecar open failed", "path", meta.Path, "err", err)
			return SessionMeta{}, false
		}
		return meta, readable
	}

	var cutoff time.Time
	if days := cfg.Defaults.SessionArchive.ScanMaxAgeDays; days > 0 {
		cutoff = time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	}
	projectResolver := projectresolve.NewWithGit(cfg.Projects)
	var discoveryWarning error
	var discovered []SessionMeta
	for _, group := range transcriptHomeGroups(cfg) {
		reader, ok := readers[group.harnessName]
		if !ok {
			continue
		}
		metas, err := reader.Discover(group.home, DiscoverOptions{Cached: lookup, MinTime: cutoff})
		if err != nil {
			slog.Debug("transcript.index: partial discover failed", "harness", group.harnessName, "home", group.home, "err", err)
			discoveryWarning = errors.Join(discoveryWarning, err)
		}
		// Readers may return useful discoveries with an error. Keep them.
		for _, meta := range metas {
			project := projectResolver.ResolveOrFallback(meta.CWD)
			meta.Project = project.Title
			meta.Profiles = append([]string(nil), group.profiles...)
			meta.Profile = displayProfileForGroup(cfg, meta.Project, project.Profile, group.profiles)
			discovered = append(discovered, meta)
		}
	}

	var mergeWarning error
	lockErr := cachefile.WithExclusive(i.path, func() error {
		i.mu.Lock()
		defer i.mu.Unlock()
		canPersist, reloadWarning := i.reloadFilesystemLocked()
		if !canPersist {
			return reloadWarning
		}
		mergeWarning = errors.Join(mergeWarning, reloadWarning)
		if rebuildSidecars {
			if err := removeTranscriptSearchStore(i.SearchStoreDir()); err != nil {
				return fmt.Errorf("removing transcript search sidecars: %w", err)
			}
		}
		for _, meta := range discovered {
			key := IndexKey(meta.Harness, meta.ID)
			existing, exists := i.entries[key]
			if _, existedAtSnapshot := snapshot[key]; existedAtSnapshot && !exists {
				// A concurrent delete won after discovery began. Discovery may
				// have recreated its sidecar, so remove that disposable artifact.
				mergeWarning = errors.Join(mergeWarning, i.store.Delete(key))
				continue
			}
			if !discoveredMetaStillFresh(meta) {
				continue
			}
			if exists {
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
					mergeWarning = errors.Join(mergeWarning, i.store.Delete(key))
				} else if err := i.store.Write(key, meta.SearchText); err != nil {
					mergeWarning = errors.Join(mergeWarning, fmt.Errorf("writing transcript search sidecar %s: %w", key, err))
				}
				meta.SearchText = ""
			}
			i.entries[key] = meta
			i.filesystemDirty = true
		}
		persistWarning, persistErr := i.persistFilesystemLocked()
		mergeWarning = errors.Join(mergeWarning, persistWarning, persistErr)
		return nil
	})
	return errors.Join(loadWarning, discoveryWarning, mergeWarning, lockErr)
}

func cloneSessionEntries(entries map[string]SessionMeta) map[string]SessionMeta {
	out := make(map[string]SessionMeta, len(entries))
	for key, meta := range entries {
		meta.Profiles = append([]string(nil), meta.Profiles...)
		out[key] = meta
	}
	return out
}

func applySessionDiff(target, current, base map[string]SessionMeta) {
	for key, meta := range current {
		baseMeta, existed := base[key]
		if !existed || !reflect.DeepEqual(meta, baseMeta) {
			target[key] = meta
		}
	}
	for key := range base {
		if _, exists := current[key]; !exists {
			delete(target, key)
		}
	}
}

func sessionEntriesHaveInlineSearch(entries map[string]SessionMeta) bool {
	for _, meta := range entries {
		if meta.SearchText != "" {
			return true
		}
	}
	return false
}
