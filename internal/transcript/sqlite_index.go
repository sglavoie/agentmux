package transcript

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/projectresolve"
)

// LoadSQLiteIndex loads the compatibility cache first, then enables the
// SQLite facade when the datastore can be opened and the legacy import is
// valid. A database problem deliberately does not make cache loading fail:
// callers keep the prior file-backed behavior and can surface
// DegradedReason to operators.
func LoadSQLiteIndex(path string, readers map[string]Reader) (*Index, error) {
	idx, err := LoadIndexAt(path)
	if err != nil {
		return nil, err
	}
	store, err := datastore.Open(context.Background())
	if err != nil {
		idx.setDegradedReason(err)
		return idx, nil
	}
	idx.mu.Lock()
	idx.ownedStore = store
	idx.mu.Unlock()
	return enableSQLiteIndex(context.Background(), idx, store, readers, compatimport.SyncIfChanged), nil
}

// LoadSQLiteIndexWithStore loads the compatibility metadata and attaches a
// caller-owned store using the requested synchronization policy. The returned
// facade borrows store; closing it never closes the shared pool.
func LoadSQLiteIndexWithStore(
	ctx context.Context,
	path string,
	store *datastore.Store,
	readers map[string]Reader,
	mode compatimport.SyncMode,
) (*Index, error) {
	idx, err := LoadIndexAt(path)
	if err != nil {
		return nil, err
	}
	return enableSQLiteIndex(ctx, idx, store, readers, mode), nil
}

// EnableSQLiteIndex activates SQLite for an already-loaded compatibility
// index. It is exported so commands and tests can provide an isolated store.
// Import failures leave idx on the legacy path and never modify legacy files.
func EnableSQLiteIndex(ctx context.Context, idx *Index, store *datastore.Store, readers map[string]Reader) *Index {
	return enableSQLiteIndex(ctx, idx, store, readers, compatimport.SyncIfChanged)
}

// EnableSQLiteIndexWithMode attaches a borrowed store to an already-decoded
// compatibility facade with an explicit synchronization policy.
func EnableSQLiteIndexWithMode(ctx context.Context, idx *Index, store *datastore.Store, readers map[string]Reader, mode compatimport.SyncMode) *Index {
	return enableSQLiteIndex(ctx, idx, store, readers, mode)
}

func enableSQLiteIndex(ctx context.Context, idx *Index, store *datastore.Store, readers map[string]Reader, mode compatimport.SyncMode) *Index {
	if idx == nil {
		return nil
	}
	if !mode.Valid() {
		idx.setDegradedReason(fmt.Errorf("invalid compatibility sync mode %d", mode))
		return idx
	}
	if store == nil {
		idx.setDegradedReason(errors.New("SQLite datastore is unavailable"))
		return idx
	}
	repo, err := NewRepository(store)
	if err != nil {
		idx.setDegradedReason(err)
		return idx
	}
	var syncErr error
	switch mode {
	case compatimport.SyncSkip:
	case compatimport.SyncIfChanged:
		_, syncErr = repo.ImportLegacyIndex(ctx, idx.path, readers)
	case compatimport.SyncForce:
		_, syncErr = repo.ForceImportLegacyIndex(ctx, idx.path, readers)
	}
	if syncErr != nil {
		idx.setDegradedReason(syncErr)
		return idx
	}
	idx.mu.Lock()
	idx.repo = repo
	idx.degradedReason = nil
	idx.mu.Unlock()
	return idx
}

// UsesSQLite reports whether this facade is serving transcript state from the
// migrated SQLite repository rather than the compatibility JSON cache.
func (i *Index) UsesSQLite() bool {
	if i == nil {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.repo != nil
}

// DegradedReason explains why the compatibility index remains active. It is
// nil once SQLite has been initialized and imported successfully.
func (i *Index) DegradedReason() error {
	if i == nil {
		return errors.New("transcript: nil index")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.degradedReason
}

func (i *Index) setDegradedReason(err error) {
	if err == nil {
		return
	}
	i.mu.Lock()
	i.degradedReason = fmt.Errorf("SQLite unavailable; using legacy transcript index: %w", err)
	i.mu.Unlock()
}

func (i *Index) repository() *Repository {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.repo
}

func defaultTranscriptReaders() map[string]Reader {
	return map[string]Reader{
		"claude":  NewClaudeReader(),
		"copilot": NewCopilotReader(),
		"codex":   NewCodexReader(),
	}
}

func splitRepositoryKey(key string) (string, string, error) {
	harness, sessionID, found := strings.Cut(key, ":")
	if !found || harness == "" || sessionID == "" {
		return "", "", fmt.Errorf("transcript: invalid index key %q", key)
	}
	return harness, sessionID, nil
}

func (i *Index) repositoryList(filter ListFilter) ([]SessionMeta, error) {
	repo := i.repository()
	if repo == nil {
		return nil, errors.New("transcript: SQLite repository is not enabled")
	}
	return repo.List(context.Background(), filter)
}

func (i *Index) repositoryGet(key string) (SessionMeta, bool, error) {
	harness, sessionID, err := splitRepositoryKey(key)
	if err != nil {
		return SessionMeta{}, false, err
	}
	repo := i.repository()
	if repo == nil {
		return SessionMeta{}, false, errors.New("transcript: SQLite repository is not enabled")
	}
	return repo.Get(context.Background(), harness, sessionID)
}

func (i *Index) refreshSQLite(cfg *config.Config, readers map[string]Reader) error {
	return i.refreshSQLiteSources(cfg, readers, false)
}

func (i *Index) refreshSQLiteSources(cfg *config.Config, readers map[string]Reader, forceSearchRefresh bool) error {
	if cfg == nil {
		return errors.New("transcript: Refresh requires a non-nil config")
	}
	if readers == nil {
		return errors.New("transcript: Refresh requires at least one reader")
	}
	repo := i.repository()
	if repo == nil {
		return errors.New("transcript: SQLite repository is not enabled")
	}
	ctx := context.Background()
	lookup := func(path string, size int64, mtime time.Time) (SessionMeta, bool) {
		meta, ok, err := repo.FreshByPath(ctx, path, size, mtime)
		if err != nil || !ok {
			if err != nil {
				slog.Debug("transcript.index: SQLite freshness lookup failed", "path", path, "err", err)
			}
			return SessionMeta{}, false
		}
		if meta.HiddenFromIndex || meta.MessageCount == 0 {
			return meta, true
		}
		if forceSearchRefresh {
			return SessionMeta{}, false
		}
		_, exists, err := repo.SearchText(ctx, meta.Harness, meta.ID)
		if err != nil {
			slog.Debug("transcript.index: SQLite search document lookup failed", "key", IndexKey(meta.Harness, meta.ID), "err", err)
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
	projectResolver := projectresolve.NewWithGit(cfg.Projects)

	var firstErr error
	for _, group := range transcriptHomeGroups(cfg) {
		reader, ok := readers[group.harnessName]
		if !ok {
			slog.Debug("transcript.index: no reader for harness", "harness", group.harnessName, "profiles", group.profiles)
			continue
		}
		metas, err := reader.Discover(group.home, DiscoverOptions{Cached: lookup, MinTime: cutoff})
		if err != nil {
			slog.Debug("transcript.index: discover failed", "harness", group.harnessName, "home", group.home, "profiles", group.profiles, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, meta := range metas {
			project := projectResolver.ResolveOrFallback(meta.CWD)
			meta.Project = project.Title
			meta.Profiles = append([]string(nil), group.profiles...)
			meta.Profile = displayProfileForGroup(cfg, meta.Project, project.Profile, group.profiles)
			key := IndexKey(meta.Harness, meta.ID)
			existing, exists, err := repo.Get(ctx, meta.Harness, meta.ID)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
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
			if !discoveredMetaStillFresh(meta) {
				continue
			}
			searchText := meta.SearchText
			meta.SearchText = ""
			if searchText != "" {
				err = repo.Upsert(ctx, meta, searchText)
			} else {
				err = repo.UpsertMetadata(ctx, meta)
			}
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("transcript: refreshing %q in SQLite: %w", key, err)
				}
				continue
			}
			// Keep the in-memory compatibility snapshot current for a later
			// read fallback, without persisting or touching sidecars.
			i.mu.Lock()
			i.entries[key] = meta
			i.mu.Unlock()
		}
	}
	return firstErr
}

func (i *Index) deleteSQLite(key string, alsoFile bool) error {
	harness, sessionID, err := splitRepositoryKey(key)
	if err != nil {
		return err
	}
	repo := i.repository()
	if repo == nil {
		return errors.New("transcript: SQLite repository is not enabled")
	}
	ctx := context.Background()
	meta, ok, err := repo.Get(ctx, harness, sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("transcript: no index entry for %q", key)
	}
	if alsoFile && meta.Path != "" {
		if err := os.Remove(meta.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing transcript file %s: %w", meta.Path, err)
		}
	}
	if err := repo.Delete(ctx, harness, sessionID); err != nil {
		return err
	}
	i.mu.Lock()
	delete(i.entries, key)
	i.mu.Unlock()
	return nil
}

func (i *Index) setHiddenSQLite(key string, hidden bool) error {
	harness, sessionID, err := splitRepositoryKey(key)
	if err != nil {
		return err
	}
	repo := i.repository()
	if repo == nil {
		return errors.New("transcript: SQLite repository is not enabled")
	}
	if err := repo.SetHidden(context.Background(), harness, sessionID, hidden); err != nil {
		return err
	}
	i.mu.Lock()
	if meta, ok := i.entries[key]; ok {
		meta.HiddenFromIndex = hidden
		meta.SearchText = ""
		i.entries[key] = meta
	}
	i.mu.Unlock()
	return nil
}

func (i *Index) pruneSQLite(cfg *config.Config, now time.Time) (int, error) {
	if cfg == nil {
		return 0, errors.New("transcript: Prune requires a non-nil config")
	}
	keepDays := cfg.Defaults.SessionArchive.KeepDays
	if keepDays <= 0 {
		return 0, nil
	}
	repo := i.repository()
	if repo == nil {
		return 0, errors.New("transcript: SQLite repository is not enabled")
	}
	hidden, err := repo.HideBefore(context.Background(), now.Add(-time.Duration(keepDays)*24*time.Hour))
	if err != nil {
		return 0, err
	}
	if hidden == 0 {
		return 0, nil
	}
	entries, err := repo.List(context.Background(), ListFilter{IncludeHidden: true})
	if err != nil {
		return hidden, err
	}
	i.mu.Lock()
	for _, meta := range entries {
		i.entries[IndexKey(meta.Harness, meta.ID)] = meta
	}
	i.mu.Unlock()
	return hidden, nil
}
