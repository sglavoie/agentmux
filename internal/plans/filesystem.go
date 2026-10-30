package plans

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"

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
	return fmt.Sprintf("decoding plan index %s: %v", e.path, e.err)
}

func (e *indexDecodeError) Unwrap() error { return e.err }

var (
	quarantinePlanIndex              = cachefile.Quarantine
	writePlanIndex                   = cachefile.WriteFileAtomic
	refreshPlansAfterSnapshotForTest func()
	refreshPlansBeforeMergeForTest   func()
	refreshPlanBeforeReadForTest     func(string)
)

// LoadIndexAt loads a JSON plan cache with corruption recovery. A non-nil
// Index accompanied by an error is a usable last-good or empty snapshot; the
// error is a warning for the caller to surface.
func LoadIndexAt(path string) (*Index, error) {
	idx := newFilesystemIndex(path)
	err := cachefile.WithExclusive(path, func() error {
		loaded, loadErr := loadIndexAtStrict(path)
		if loadErr == nil {
			idx.entries = clonePlanEntries(loaded.entries)
			idx.filesystemBase = clonePlanEntries(loaded.entries)
			return nil
		}
		var decodeErr *indexDecodeError
		if !errors.As(loadErr, &decodeErr) {
			return fmt.Errorf("loading plan cache %s: %w", path, loadErr)
		}
		quarantined, quarantineErr := quarantinePlanIndex(path)
		if quarantineErr != nil {
			return errors.Join(
				fmt.Errorf("plan cache is malformed: %w", loadErr),
				fmt.Errorf("quarantining plan cache %s: %w", path, quarantineErr),
			)
		}
		return fmt.Errorf("plan cache quarantined as %s: %w", quarantined, loadErr)
	})
	return idx, err
}

func newFilesystemIndex(path string) *Index {
	return &Index{
		path:           path,
		store:          searchstore.New(path),
		entries:        make(map[string]PlanMeta),
		filesystemBase: make(map[string]PlanMeta),
	}
}

// reloadFilesystemLocked refreshes metadata from disk and reapplies any
// unsaved local diff left by a prior persistence warning. The boolean reports
// whether a following mutation may safely persist: a successfully loaded or
// quarantined cache may be rewritten, while a hard read failure must leave the
// on-disk cache untouched. The caller holds both i.mu and the stable
// <index>.lock advisory lock.
func (i *Index) reloadFilesystemLocked() (bool, error) {
	current := clonePlanEntries(i.entries)
	base := clonePlanEntries(i.filesystemBase)
	dirty := i.filesystemDirty || !reflect.DeepEqual(current, base)

	loaded, err := loadIndexAtStrict(i.path)
	if err == nil {
		disk := clonePlanEntries(loaded.entries)
		i.filesystemBase = clonePlanEntries(disk)
		if dirty {
			applyPlanDiff(disk, current, base)
		}
		i.entries = disk
		return true, nil
	}
	var decodeErr *indexDecodeError
	if errors.As(err, &decodeErr) {
		quarantined, quarantineErr := quarantinePlanIndex(i.path)
		if quarantineErr != nil {
			return true, errors.Join(
				fmt.Errorf("plan cache is malformed: %w", err),
				fmt.Errorf("quarantining plan cache %s: %w", i.path, quarantineErr),
			)
		}
		i.filesystemBase = make(map[string]PlanMeta)
		i.filesystemDirty = len(i.entries) > 0
		return true, fmt.Errorf("plan cache quarantined as %s: %w", quarantined, err)
	}
	return false, fmt.Errorf("loading plan cache %s: %w", i.path, err)
}

func (i *Index) persistFilesystemLocked() error {
	warning, persistErr := i.saveLocked()
	if persistErr != nil {
		i.filesystemDirty = true
		return errors.Join(warning, persistErr)
	}
	i.filesystemBase = clonePlanEntries(i.entries)
	// Inline bodies are durable but still need a sidecar migration retry.
	i.filesystemDirty = warning != nil
	return warning
}

func (i *Index) filesystemSnapshot() (map[string]PlanMeta, error) {
	var snapshot map[string]PlanMeta
	err := cachefile.WithExclusive(i.path, func() error {
		i.mu.Lock()
		defer i.mu.Unlock()
		_, warning := i.reloadFilesystemLocked()
		snapshot = clonePlanEntries(i.entries)
		return warning
	})
	if snapshot == nil {
		snapshot = make(map[string]PlanMeta)
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
		return errors.Join(warning, i.persistFilesystemLocked())
	})
}

func (i *Index) deleteFilesystem(key string) error {
	return cachefile.WithExclusive(i.path, func() error {
		i.mu.Lock()
		defer i.mu.Unlock()
		canPersist, warning := i.reloadFilesystemLocked()
		if !canPersist {
			return warning
		}
		_, ok := i.entries[key]
		if !ok {
			return errors.Join(warning, fmt.Errorf("plans: no index entry for %q", key))
		}
		if err := i.store.Delete(key); err != nil {
			return errors.Join(warning, fmt.Errorf("removing plan search sidecar: %w", err))
		}
		delete(i.entries, key)
		i.filesystemDirty = true
		return errors.Join(warning, i.persistFilesystemLocked())
	})
}

// refreshFilesystem scans without holding the cache lock, then reloads and
// merges under the lock. Per-plan failures preserve cached rows, while useful
// partial metadata from a new plan remains visible with a warning.
func (i *Index) refreshFilesystem(root string, projects []config.Session) error {
	if root == "" {
		return errors.New("plans: Refresh requires a non-empty root path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolving plans root %s: %w", root, err)
	}
	root = absRoot

	snapshot, loadWarning := i.filesystemSnapshot()
	if refreshPlansAfterSnapshotForTest != nil {
		refreshPlansAfterSnapshotForTest()
	}
	dirEntries, readErr := os.ReadDir(root)
	if readErr != nil {
		return errors.Join(loadWarning, fmt.Errorf("reading plans root %s: %w", root, readErr))
	}

	worker := &Index{path: i.path, stateRoot: i.stateRoot, store: i.store, entries: clonePlanEntries(snapshot)}
	resolver := projectresolve.NewWithGit(projects)
	discovered := make(map[string]PlanMeta)
	discoveredSearchText := make(map[string]bool)
	removed := make(map[string]struct{})
	var discoveryWarning error
	for _, entry := range dirEntries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		planDir := filepath.Join(root, name)
		key := PlanKey(planDir)
		if len(name) > 0 && (name[0] == '.' || name == "sources") {
			removed[key] = struct{}{}
			continue
		}
		meta, searchTextReady, planErr := worker.refreshPlanLocked(planDir, resolver)
		if planErr != nil {
			discoveryWarning = errors.Join(discoveryWarning, fmt.Errorf("refreshing plan %s: %w", planDir, planErr))
		}
		if meta == nil {
			if planErr == nil {
				removed[key] = struct{}{}
			}
			continue
		}
		discovered[key] = *meta
		discoveredSearchText[key] = searchTextReady
	}
	if refreshPlansBeforeMergeForTest != nil {
		refreshPlansBeforeMergeForTest()
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
		for key := range removed {
			// A directory that is not a plan is listed here on every refresh;
			// only one the index holds is a change worth writing.
			current, indexed := i.entries[key]
			if !indexed {
				continue
			}
			// An empty-directory observation belongs to the scan's snapshot.
			// Another refresh may have indexed new markdown in the meantime;
			// do not delete that newer entry or its sidecar.
			previous, existed := snapshot[key]
			if !existed || !reflect.DeepEqual(current, previous) {
				continue
			}
			delete(i.entries, key)
			i.filesystemDirty = true
			mergeWarning = errors.Join(mergeWarning, i.store.Delete(key))
		}
		for key, meta := range discovered {
			current, exists := i.entries[key]
			previous, existedAtSnapshot := snapshot[key]
			if existedAtSnapshot && !exists {
				mergeWarning = errors.Join(mergeWarning, i.store.Delete(key))
				continue
			}
			// A concurrent refresh may have added files the scan never saw.
			// Checking only the scanned paths cannot detect that addition, so
			// leave any entry changed since the snapshot to its newer writer.
			if exists && (!existedAtSnapshot || !reflect.DeepEqual(current, previous)) {
				continue
			}
			if !planMetaStillFresh(meta) {
				continue
			}
			// An unchanged cache hit is the common case on every refresh, and
			// marking it dirty is what used to rewrite plans.json each time the
			// Plans view opened even though nothing on disk had moved.
			if current, ok := i.entries[key]; ok && !discoveredSearchText[key] && reflect.DeepEqual(current, meta) {
				continue
			}
			if discoveredSearchText[key] {
				if err := i.store.Write(key, meta.SearchText); err != nil {
					mergeWarning = errors.Join(mergeWarning, fmt.Errorf("writing plan search sidecar: %w", err))
					meta.SearchTextPending = true
				} else {
					meta.SearchText = ""
					meta.SearchTextPending = false
				}
			}
			i.entries[key] = meta
			i.filesystemDirty = true
		}
		// Remove only paths proven absent or no longer real directories.
		// Discovery skips regular files and symlinks, so their old corpus
		// must not survive a directory being replaced at the same path.
		// Permission and transient stat failures preserve the last-good row.
		for key, meta := range i.entries {
			info, statErr := os.Lstat(meta.DirPath)
			if errors.Is(statErr, fs.ErrNotExist) || (statErr == nil && !info.IsDir()) {
				delete(i.entries, key)
				i.filesystemDirty = true
				mergeWarning = errors.Join(mergeWarning, i.store.Delete(key))
			}
		}
		if i.filesystemDirty || !reflect.DeepEqual(i.entries, i.filesystemBase) {
			mergeWarning = errors.Join(mergeWarning, i.persistFilesystemLocked())
		}
		return nil
	})
	return errors.Join(loadWarning, discoveryWarning, mergeWarning, lockErr)
}

func planMetaStillFresh(meta PlanMeta) bool {
	var size int64
	var latest int64
	var files []planFileInfo
	for _, rel := range meta.Files {
		info, err := os.Stat(filepath.Join(meta.DirPath, rel))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		files = append(files, planFileInfo{rel: rel, size: info.Size(), mod: info.ModTime()})
		size += info.Size()
		if nanos := info.ModTime().UnixNano(); nanos > latest {
			latest = nanos
		}
	}
	if meta.FileFingerprint != "" {
		return planFileFingerprint(files) == meta.FileFingerprint
	}
	return size == meta.Size && latest == meta.ModTime.UnixNano()
}

func clonePlanEntries(entries map[string]PlanMeta) map[string]PlanMeta {
	out := make(map[string]PlanMeta, len(entries))
	for key, meta := range entries {
		meta.Files = append([]string(nil), meta.Files...)
		meta.Projects = append([]string(nil), meta.Projects...)
		out[key] = meta
	}
	return out
}

func applyPlanDiff(target, current, base map[string]PlanMeta) {
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
