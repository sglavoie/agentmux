package transcript

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"reflect"

	"github.com/sglavoie/agentmux/internal/cachefile"
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

// removeTranscriptSearchSidecar is a test seam for verifying that compact
// prunes individual orphan sidecars while holding the cache advisory lock.
var removeTranscriptSearchSidecar = os.Remove

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
			idx.superseded = cloneSupersededSegments(loaded.superseded)
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
		i.superseded = cloneSupersededSegments(loaded.superseded)
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

// MigrateSessionClasses classifies legacy cache entries whose persisted class
// is absent. It reads only their source metadata, preserves all cached
// transcript/search fields, and persists unknown for missing, unreadable, or
// unsupported sources so the migration runs at most once per entry.
func (i *Index) MigrateSessionClasses(readers map[string]Reader) error {
	snapshot, loadWarning := i.filesystemSnapshot()
	legacyClasses := classifyLegacySessionEntries(snapshot, readers)
	if len(legacyClasses) == 0 {
		return loadWarning
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
		applyLegacySessionClasses(i, legacyClasses)
		if !i.filesystemDirty {
			return nil
		}
		persistWarning, persistErr := i.persistFilesystemLocked()
		mergeWarning = errors.Join(mergeWarning, persistWarning, persistErr)
		return nil
	})
	return errors.Join(loadWarning, mergeWarning, lockErr)
}

func classifyLegacySessionEntries(snapshot map[string]SessionMeta, readers map[string]Reader) map[string]SessionClass {
	legacyClasses := make(map[string]SessionClass)
	for key, meta := range snapshot {
		if meta.SessionClass != "" {
			continue
		}
		class := SessionClassUnknown
		if classifier, ok := readers[meta.Harness].(sessionClassifier); ok && meta.Path != "" {
			classified, err := classifier.ClassifySession(meta.Path)
			if err != nil {
				slog.Debug("transcript.index: legacy classification failed", "path", meta.Path, "err", err)
			} else {
				class = normalizedSessionClass(classified)
			}
		}
		meta.SessionClass = class
		snapshot[key] = meta
		legacyClasses[key] = class
	}
	return legacyClasses
}

func applyLegacySessionClasses(i *Index, legacyClasses map[string]SessionClass) {
	for key, class := range legacyClasses {
		meta, exists := i.entries[key]
		if !exists || meta.SessionClass != "" {
			continue
		}
		meta.SessionClass = class
		i.entries[key] = meta
		i.filesystemDirty = true
	}
}

type filesystemCompactResult struct {
	LegacyFields             int
	RemovedOrphanSidecars    int
	RemovedOrphanSidecarSize int64
	Warning                  error
}

// compactFilesystem reloads and persists the latest metadata snapshot before
// pruning any orphan sidecars. The advisory lock covers legacy-field counting
// through removal so reporting and reachability use one coherent cache state.
func (i *Index) compactFilesystem() (result filesystemCompactResult, err error) {
	err = cachefile.WithExclusive(i.path, func() error {
		i.mu.Lock()
		defer i.mu.Unlock()

		legacyFields, countErr := countLegacySearchTextFields(i.path)
		if countErr != nil {
			return countErr
		}
		result.LegacyFields = legacyFields

		canPersist, reloadWarning := i.reloadFilesystemLocked()
		if !canPersist {
			return reloadWarning
		}
		persistWarning, persistErr := i.persistFilesystemLocked()
		if persistErr != nil {
			return errors.Join(reloadWarning, persistWarning, persistErr)
		}
		if persistWarning != nil {
			return errors.Join(reloadWarning, persistWarning)
		}
		if reloadWarning != nil {
			var decodeErr *indexDecodeError
			if !errors.As(reloadWarning, &decodeErr) {
				return reloadWarning
			}
			result.Warning = errors.Join(result.Warning, reloadWarning)
		}

		keys := make([]string, 0, len(i.entries))
		for key := range i.entries {
			keys = append(keys, key)
		}
		inventory, inventoryErr := i.store.Inventory(keys)
		if inventoryErr != nil {
			return inventoryErr
		}
		for _, orphan := range inventory.Orphans {
			info, statErr := os.Lstat(orphan.Path)
			if statErr != nil {
				return fmt.Errorf("checking orphan transcript search sidecar %s: %w", orphan.Path, statErr)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("orphan transcript search sidecar %s is no longer a regular file", orphan.Path)
			}
			if removeErr := removeTranscriptSearchSidecar(orphan.Path); removeErr != nil {
				return fmt.Errorf("removing orphan transcript search sidecar %s: %w", orphan.Path, removeErr)
			}
			result.RemovedOrphanSidecars++
			result.RemovedOrphanSidecarSize += info.Size()
		}
		return nil
	})
	return result, err
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
		if err := i.DeleteSearchText(key); err != nil {
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
			if err := i.DeleteSearchText(key); err != nil {
				return errors.Join(warning, fmt.Errorf("removing transcript search sidecar %s: %w", i.SearchTextPath(key), err))
			}
			if entry.SearchText != "" {
				entry.SearchText = ""
				i.filesystemDirty = true
			}
		}
		if entry.HiddenFromIndex != hidden {
			entry.HiddenFromIndex = hidden
			i.filesystemDirty = true
		}
		i.entries[key] = entry
		// Matching in-memory state may still be pending after a failed save.
		if !i.filesystemDirty {
			return warning
		}
		persistWarning, persistErr := i.persistFilesystemLocked()
		return errors.Join(warning, persistWarning, persistErr)
	})
}
