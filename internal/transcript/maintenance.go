package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/sglavoie/agentmux/internal/config"
)

// StoreStatus summarizes the on-disk transcript metadata file and search
// sidecar store without exposing per-session search text to daemon callers.
type StoreStatus struct {
	Backend                   string
	DatabasePath              string
	DegradedReason            string
	MetadataPath              string
	MetadataSize              int64
	SidecarDir                string
	SidecarCount              int
	SidecarSize               int64
	VisibleEntries            int
	HiddenEntries             int
	VisibleProfileMemberships int
	HiddenProfileMemberships  int
	SharedProfileEntries      int
	MissingSidecars           int
	MissingSidecarKeys        []string
	SearchDocumentCount       int
	MissingSearchDocuments    int
	MissingSearchDocumentKeys []string
}

// Status returns filesystem and entry counts for a transcript index.
func Status(path string) (StoreStatus, error) {
	idx, err := LoadIndexAt(path)
	if err != nil {
		return StoreStatus{}, err
	}
	status := StoreStatus{
		Backend:      "legacy",
		MetadataPath: path,
		SidecarDir:   idx.SearchStoreDir(),
	}
	if info, err := os.Stat(path); err == nil {
		status.MetadataSize = info.Size()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return StoreStatus{}, err
	}
	count, size, err := idx.SearchStoreStats()
	if err != nil {
		return StoreStatus{}, err
	}
	status.SidecarCount = count
	status.SidecarSize = size

	for _, meta := range idx.List(ListFilter{IncludeHidden: true}) {
		key := IndexKey(meta.Harness, meta.ID)
		profileMemberships := len(meta.ProfileNames())
		if meta.HiddenFromIndex {
			status.HiddenEntries++
			status.HiddenProfileMemberships += profileMemberships
			continue
		}
		status.VisibleEntries++
		status.VisibleProfileMemberships += profileMemberships
		if profileMemberships > 1 {
			status.SharedProfileEntries++
		}
		if meta.MessageCount == 0 {
			continue
		}
		exists, err := idx.SearchTextExists(key)
		if err != nil {
			return StoreStatus{}, err
		}
		if !exists {
			status.MissingSidecars++
			status.MissingSidecarKeys = append(status.MissingSidecarKeys, key)
		}
	}
	return status, nil
}

// Status reports the active facade state. SQLite status intentionally does
// not inspect legacy sidecars after cutover; they remain on disk solely as a
// compatibility rollback source.
func (i *Index) Status() (StoreStatus, error) {
	if i == nil {
		return StoreStatus{}, errors.New("transcript: Status requires a non-nil index")
	}
	if repo := i.repository(); repo != nil {
		return repositoryStatus(context.Background(), repo, i.path)
	}
	status, err := Status(i.path)
	if err != nil {
		return StoreStatus{}, err
	}
	if reason := i.DegradedReason(); reason != nil {
		status.DegradedReason = reason.Error()
	}
	return status, nil
}

func repositoryStatus(ctx context.Context, repo *Repository, legacyPath string) (StoreStatus, error) {
	status := StoreStatus{
		Backend:      "sqlite",
		DatabasePath: repo.store.Path(),
		MetadataPath: legacyPath,
	}
	var err error
	if err = repo.store.DB().QueryRowContext(ctx, `SELECT count(*) FROM conversations WHERE hidden = 0`).Scan(&status.VisibleEntries); err != nil {
		return StoreStatus{}, fmt.Errorf("transcript: counting visible SQLite conversations: %w", err)
	}
	if err = repo.store.DB().QueryRowContext(ctx, `SELECT count(*) FROM conversations WHERE hidden = 1`).Scan(&status.HiddenEntries); err != nil {
		return StoreStatus{}, fmt.Errorf("transcript: counting hidden SQLite conversations: %w", err)
	}
	if err = repo.store.DB().QueryRowContext(ctx, `
		SELECT count(*) FROM profile_memberships p
		JOIN conversations c ON c.id = p.conversation_id WHERE c.hidden = 0
	`).Scan(&status.VisibleProfileMemberships); err != nil {
		return StoreStatus{}, fmt.Errorf("transcript: counting visible SQLite profile memberships: %w", err)
	}
	if err = repo.store.DB().QueryRowContext(ctx, `
		SELECT count(*) FROM profile_memberships p
		JOIN conversations c ON c.id = p.conversation_id WHERE c.hidden = 1
	`).Scan(&status.HiddenProfileMemberships); err != nil {
		return StoreStatus{}, fmt.Errorf("transcript: counting hidden SQLite profile memberships: %w", err)
	}
	if err = repo.store.DB().QueryRowContext(ctx, `
		SELECT count(*) FROM (
			SELECT c.id FROM conversations c
			JOIN profile_memberships p ON p.conversation_id = c.id
			WHERE c.hidden = 0 GROUP BY c.id HAVING count(*) > 1
		)
	`).Scan(&status.SharedProfileEntries); err != nil {
		return StoreStatus{}, fmt.Errorf("transcript: counting shared SQLite conversations: %w", err)
	}
	if err = repo.store.DB().QueryRowContext(ctx, `
		SELECT count(*) FROM search_documents WHERE entity_type = ?
	`, transcriptSearchEntityType).Scan(&status.SearchDocumentCount); err != nil {
		return StoreStatus{}, fmt.Errorf("transcript: counting SQLite search documents: %w", err)
	}
	rows, err := repo.store.DB().QueryContext(ctx, `
		SELECT c.harness, c.session_id FROM conversations c
		WHERE c.hidden = 0 AND c.message_count > 0
		AND NOT EXISTS (
			SELECT 1 FROM search_documents d
			WHERE d.entity_type = ? AND d.entity_id = c.harness || ':' || c.session_id
		)
		ORDER BY c.harness, c.session_id
	`, transcriptSearchEntityType)
	if err != nil {
		return StoreStatus{}, fmt.Errorf("transcript: listing missing SQLite search documents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var harness, sessionID string
		if err := rows.Scan(&harness, &sessionID); err != nil {
			return StoreStatus{}, fmt.Errorf("transcript: scanning missing SQLite search document: %w", err)
		}
		status.MissingSearchDocumentKeys = append(status.MissingSearchDocumentKeys, IndexKey(harness, sessionID))
	}
	if err := rows.Err(); err != nil {
		return StoreStatus{}, fmt.Errorf("transcript: iterating missing SQLite search documents: %w", err)
	}
	status.MissingSearchDocuments = len(status.MissingSearchDocumentKeys)
	return status, nil
}

// CompactResult reports what compact observed and rewrote.
type CompactResult struct {
	StatusBefore StoreStatus
	StatusAfter  StoreStatus
	LegacyFields int
	Import       LegacyImportResult
}

// CompactSQLite verifies/imports the untouched legacy index into SQLite. It
// is idempotent and never rewrites transcripts.json or its sidecars.
func CompactSQLite(idx *Index, readers map[string]Reader) (CompactResult, error) {
	if idx == nil {
		return CompactResult{}, errors.New("transcript: CompactSQLite requires a non-nil index")
	}
	before, err := idx.Status()
	if err != nil {
		return CompactResult{}, err
	}
	repo := idx.repository()
	if repo == nil {
		if reason := idx.DegradedReason(); reason != nil {
			return CompactResult{StatusBefore: before}, reason
		}
		return CompactResult{StatusBefore: before}, errors.New("transcript: SQLite repository is not enabled")
	}
	importResult, err := repo.ForceImportLegacyIndex(context.Background(), idx.path, readers)
	if err != nil {
		return CompactResult{StatusBefore: before}, err
	}
	after, err := idx.Status()
	if err != nil {
		return CompactResult{}, err
	}
	return CompactResult{StatusBefore: before, StatusAfter: after, Import: importResult}, nil
}

// Compact migrates legacy inline search_text into sidecars and rewrites the
// metadata file without search text.
func Compact(path string) (CompactResult, error) {
	before, err := Status(path)
	if err != nil {
		return CompactResult{}, err
	}
	legacyFields, err := countLegacySearchTextFields(path)
	if err != nil {
		return CompactResult{}, err
	}
	idx, err := LoadIndexAt(path)
	if err != nil {
		return CompactResult{}, err
	}
	if err := idx.Save(); err != nil {
		return CompactResult{}, err
	}
	after, err := Status(path)
	if err != nil {
		return CompactResult{}, err
	}
	return CompactResult{StatusBefore: before, StatusAfter: after, LegacyFields: legacyFields}, nil
}

// RebuildResult reports the state after a sidecar rebuild.
type RebuildResult struct {
	Status StoreStatus
}

// RebuildSQLite forces source transcript parsing and upserts refreshed SQLite
// search documents. Existing documents remain available if source discovery
// fails partway through; compatibility sidecars are never removed or rewritten.
func RebuildSQLite(idx *Index, cfg *config.Config, readers map[string]Reader, includeAll bool) (RebuildResult, error) {
	if idx == nil {
		return RebuildResult{}, errors.New("transcript: RebuildSQLite requires a non-nil index")
	}
	if cfg == nil {
		return RebuildResult{}, errors.New("transcript: Rebuild requires a non-nil config")
	}
	repo := idx.repository()
	if repo == nil {
		if reason := idx.DegradedReason(); reason != nil {
			return RebuildResult{}, reason
		}
		return RebuildResult{}, errors.New("transcript: SQLite repository is not enabled")
	}
	refreshCfg := *cfg
	if includeAll {
		refreshCfg.Defaults.SessionArchive.ScanMaxAgeDays = -1
	}
	if err := idx.refreshSQLiteSources(&refreshCfg, readers, true); err != nil {
		return RebuildResult{}, err
	}
	status, err := idx.Status()
	if err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{Status: status}, nil
}

// Rebuild refreshes configured profiles and rebuilds search sidecars. When
// includeAll is true, the configured scan age cutoff is disabled for this pass.
func Rebuild(path string, cfg *config.Config, readers map[string]Reader, includeAll bool) (RebuildResult, error) {
	if cfg == nil {
		return RebuildResult{}, errors.New("transcript: Rebuild requires a non-nil config")
	}
	idx, err := LoadIndexAt(path)
	if err != nil {
		return RebuildResult{}, err
	}
	if err := os.RemoveAll(idx.SearchStoreDir()); err != nil {
		return RebuildResult{}, err
	}
	refreshCfg := *cfg
	if includeAll {
		refreshCfg.Defaults.SessionArchive.ScanMaxAgeDays = -1
	}
	if err := idx.Refresh(&refreshCfg, readers); err != nil {
		return RebuildResult{}, err
	}
	status, err := Status(path)
	if err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{Status: status}, nil
}

func countLegacySearchTextFields(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var raw struct {
		Entries map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, fmt.Errorf("decoding transcript index %s: %w", path, err)
	}
	count := 0
	for _, entry := range raw.Entries {
		var probe struct {
			SearchText string `json:"search_text"`
		}
		if err := json.Unmarshal(entry, &probe); err != nil {
			return 0, err
		}
		if probe.SearchText != "" {
			count++
		}
	}
	return count, nil
}
