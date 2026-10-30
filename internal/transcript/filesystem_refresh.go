package transcript

// Refreshing and rebuilding the filesystem-backed transcript cache: source
// discovery outside the advisory lock, then a merge under it that preserves
// whatever a concurrent delete or hide decided while the scan was running.

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"github.com/sglavoie/agentmux/internal/cachefile"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/projectresolve"
	"github.com/sglavoie/agentmux/internal/timeutil"
)

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
	// A missing class identifies a legacy cache entry. Classify those sources
	// directly, independent of the normal discovery cutoff, and retain all
	// cached transcript/search metadata. Explicit unknown is persisted so an
	// unreadable or ambiguous source is not retried on every refresh.
	legacyClasses := classifyLegacySessionEntries(snapshot, readers)
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
		if cachePredatesRules(meta) {
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

	// Files set aside for a later segment of their conversation are looked
	// up by size and mtime as well, and the ones set aside this pass replace
	// the remembered set under the lock below.
	i.mu.Lock()
	supersededBefore := cloneSupersededSegments(i.superseded)
	i.mu.Unlock()
	supersededNow := make(map[string]supersededSegment)
	supersededLookup := func(path string, size int64, mtime time.Time) (SessionMeta, bool) {
		if rebuildSidecars {
			return SessionMeta{}, false
		}
		segment, ok := supersededBefore[path]
		if !ok || segment.FileSize != size || !segment.FileModTime.Equal(mtime) {
			return SessionMeta{}, false
		}
		return segment.meta(path), true
	}
	supersede := func(meta SessionMeta) {
		if meta.Path != "" {
			supersededNow[meta.Path] = supersededSegmentOf(meta)
		}
	}

	var cutoff time.Time
	if days := cfg.Defaults.SessionArchive.ScanMaxAgeDays; days > 0 {
		cutoff = timeutil.DaysAgo(time.Now(), days)
	}
	projectResolver := projectresolve.NewWithGit(cfg.Projects)
	var discoveryWarning error
	var discovered []SessionMeta
	for _, group := range transcriptHomeGroups(cfg) {
		reader, ok := readers[group.harnessName]
		if !ok {
			continue
		}
		metas, err := reader.Discover(group.home, DiscoverOptions{
			Cached:     lookup,
			MinTime:    cutoff,
			Superseded: supersededLookup,
			Supersede:  supersede,
		})
		if err != nil {
			slog.Debug("transcript.index: partial discover failed", "harness", group.harnessName, "home", group.home, "err", err)
			discoveryWarning = errors.Join(discoveryWarning, err)
		}
		// Readers may return useful discoveries with an error. Keep them.
		for _, meta := range metas {
			meta.SessionClass = normalizedSessionClass(meta.SessionClass)
			meta.Profiles = append([]string(nil), group.profiles...)
			meta = classifySessionProject(meta, cfg, projectResolver)
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
		applyLegacySessionClasses(i, legacyClasses)
		if !supersededSegmentsEqual(i.superseded, supersededNow) {
			i.superseded = cloneSupersededSegments(supersededNow)
			i.filesystemDirty = true
		}
		// A rebuild that scanned everything resets the whole sidecar store. One
		// bounded by the scan age resets only what it reparsed (below): the
		// sessions it did not reach keep the sidecars they still match.
		if rebuildSidecars && cutoff.IsZero() {
			if err := removeTranscriptSearchStore(i.SearchStoreDir()); err != nil {
				return fmt.Errorf("removing transcript search sidecars: %w", err)
			}
		}
		for key, meta := range i.entries {
			classified := classifySessionProject(meta, cfg, projectResolver)
			if reflect.DeepEqual(classified, meta) {
				continue
			}
			i.entries[key] = classified
			i.filesystemDirty = true
		}
		for _, meta := range discovered {
			key := IndexKey(meta.Harness, meta.ID)
			existing, exists := i.entries[key]
			if _, existedAtSnapshot := snapshot[key]; existedAtSnapshot && !exists {
				// A concurrent delete won after discovery began. Discovery may
				// have recreated its sidecar, so remove that disposable artifact.
				mergeWarning = errors.Join(mergeWarning, i.DeleteSearchText(key))
				continue
			}
			if !discoveredMetaStillFresh(meta) {
				continue
			}
			if exists {
				meta.HiddenFromIndex = existing.HiddenFromIndex
				if meta.SearchText == "" && existing.FileSize == meta.FileSize && existing.FileModTime.Equal(meta.FileModTime) {
					// Preserve the cached representation as well as the instant.
					// JSON round trips can replace time.Local with time.UTC (or a
					// fixed zone), which is Equal but not reflect.DeepEqual.
					meta.FileModTime = existing.FileModTime
					meta.MessageCount = existing.MessageCount
					meta.Snippet = existing.Snippet
					meta.StartedAt = existing.StartedAt
					meta.LastAt = existing.LastAt
				}
			}
			if meta.SearchText != "" {
				if meta.HiddenFromIndex {
					mergeWarning = errors.Join(mergeWarning, i.DeleteSearchText(key))
					meta.SearchText = ""
				} else if err := i.WriteSearchText(key, meta.SearchText); err != nil {
					mergeWarning = errors.Join(mergeWarning, fmt.Errorf("writing transcript search sidecar %s: %w", key, err))
					// Keep the new body with its metadata until saveLocked can
					// migrate it. An older readable sidecar may still exist.
				} else {
					meta.SearchText = ""
				}
			} else if rebuildSidecars && !cutoff.IsZero() {
				// A reparse with no searchable text leaves no sidecar behind.
				mergeWarning = errors.Join(mergeWarning, i.DeleteSearchText(key))
			} else if exists && (existing.FileSize != meta.FileSize || !existing.FileModTime.Equal(meta.FileModTime)) {
				// A changed transcript with no searchable text must not retain
				// either a legacy or source-aware sidecar from its old contents.
				mergeWarning = errors.Join(mergeWarning, i.DeleteSearchText(key))
			}
			if !exists || !reflect.DeepEqual(existing, meta) {
				i.entries[key] = meta
				i.filesystemDirty = true
			}
		}
		if !i.filesystemDirty {
			return nil
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
		meta.Projects = append([]string(nil), meta.Projects...)
		out[key] = meta
	}
	return out
}

func classifySessionProject(meta SessionMeta, cfg *config.Config, resolver projectresolve.Resolver) SessionMeta {
	if meta.CWD == "" {
		return meta
	}

	matches := resolver.Matches(meta.CWD)
	if len(matches) == 0 {
		fallback := resolver.ResolveOrFallback(meta.CWD)
		meta.Project = fallback.Title
		meta.Projects = nil
		meta.Profile = displayProfileForGroup(cfg, meta.Project, "", meta.ProfileNames())
		return meta
	}

	projects := make([]string, 0, len(matches))
	for _, match := range matches {
		projects = append(projects, match.Title)
	}
	meta.Project = matches[0].Title
	meta.Projects = cleanProjectNames(projects)
	meta.Profile = displayProfileForGroup(cfg, meta.Project, matches[0].Profile, meta.ProfileNames())
	return meta
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
