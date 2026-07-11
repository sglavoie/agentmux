package transcript

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/datastore"
)

const transcriptSearchEntityType = "conversation"

// Repository stores transcript metadata and searchable content in SQLite.
// The file-backed Index remains the active compatibility facade until the
// cutover; this type deliberately has no side effects on legacy artifacts.
type Repository struct {
	store *datastore.Store
	now   func() time.Time
}

// ImportRecord describes the last completed import for a legacy source.
type ImportRecord struct {
	SourceName  string
	SourcePath  string
	Fingerprint string
	CompletedAt time.Time
	RecordCount int
}

// NewRepository constructs a transcript repository over an opened datastore.
func NewRepository(store *datastore.Store) (*Repository, error) {
	if store == nil || store.DB() == nil {
		return nil, errors.New("transcript: repository requires an open datastore")
	}
	return &Repository{store: store, now: time.Now}, nil
}

// Upsert replaces metadata, profile memberships, and searchable content for a
// conversation. Hidden conversations retain their metadata but have no search
// document, matching the legacy index's sidecar behavior.
func (r *Repository) Upsert(ctx context.Context, meta SessionMeta, searchText string) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		return r.upsertTx(ctx, tx, meta, &searchText)
	})
}

// UpsertMetadata replaces metadata and profile memberships while preserving
// existing searchable content. Hiding a row still removes its search document.
func (r *Repository) UpsertMetadata(ctx context.Context, meta SessionMeta) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		return r.upsertTx(ctx, tx, meta, nil)
	})
}

// SetSearchText replaces the search document for an existing visible row.
func (r *Repository) SetSearchText(ctx context.Context, harness, sessionID, searchText string) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		var id int64
		var hidden int
		var profile, snippet string
		var lastAt int64
		err := tx.QueryRowContext(ctx, `
			SELECT id, hidden, display_profile, snippet, last_at_ns
			FROM conversations WHERE harness = ? AND session_id = ?
		`, harness, sessionID).Scan(&id, &hidden, &profile, &snippet, &lastAt)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("transcript: no repository entry for %q", IndexKey(harness, sessionID))
		}
		if err != nil {
			return fmt.Errorf("transcript: reading search-document owner: %w", err)
		}
		if hidden != 0 {
			_, err = tx.ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = ? AND entity_id = ?`,
				transcriptSearchEntityType, IndexKey(harness, sessionID))
			return err
		}
		return r.upsertSearchDocumentTx(ctx, tx, id, harness, sessionID, profile, snippet, lastAt, searchText)
	})
}

// DeleteSearchText removes a conversation's searchable content while leaving
// metadata intact. It is primarily the compatibility equivalent of deleting a
// legacy sidecar; normal user-facing hiding uses SetHidden instead.
func (r *Repository) DeleteSearchText(ctx context.Context, harness, sessionID string) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			DELETE FROM search_documents WHERE entity_type = ? AND entity_id = ?
		`, transcriptSearchEntityType, IndexKey(harness, sessionID))
		if err != nil {
			return fmt.Errorf("transcript: deleting search document: %w", err)
		}
		return nil
	})
}

// ClearSearchDocuments removes all transcript search documents. Rebuild uses
// it to force fresh source parsing while retaining conversation metadata and
// hidden state.
func (r *Repository) ClearSearchDocuments(ctx context.Context) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = ?`, transcriptSearchEntityType)
		if err != nil {
			return fmt.Errorf("transcript: clearing search documents: %w", err)
		}
		return nil
	})
}

// SearchText returns the normalized content associated with one conversation.
func (r *Repository) SearchText(ctx context.Context, harness, sessionID string) (string, bool, error) {
	var content string
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT normalized_content FROM search_documents
		WHERE entity_type = ? AND entity_id = ?
	`, transcriptSearchEntityType, IndexKey(harness, sessionID)).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("transcript: reading search document: %w", err)
	}
	return content, true, nil
}

// Get returns one transcript with its stable display profile and ordered
// profile memberships reconstructed from relational rows.
func (r *Repository) Get(ctx context.Context, harness, sessionID string) (SessionMeta, bool, error) {
	row := r.store.DB().QueryRowContext(ctx, conversationSelect+`
		WHERE harness = ? AND session_id = ?
	`, harness, sessionID)
	id, meta, err := scanConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionMeta{}, false, nil
	}
	if err != nil {
		return SessionMeta{}, false, fmt.Errorf("transcript: reading repository entry: %w", err)
	}
	profiles, err := r.profileNames(ctx, []int64{id})
	if err != nil {
		return SessionMeta{}, false, err
	}
	meta.Profiles = profiles[id]
	return meta, true, nil
}

// FreshByPath finds metadata whose recorded source size and mtime still match.
// It is the repository equivalent of the legacy Refresh cache lookup.
func (r *Repository) FreshByPath(ctx context.Context, path string, size int64, mtime time.Time) (SessionMeta, bool, error) {
	row := r.store.DB().QueryRowContext(ctx, conversationSelect+`
		WHERE source_path = ? AND source_size = ? AND source_mtime_ns = ?
		ORDER BY id LIMIT 1
	`, path, size, timeToNS(mtime))
	id, meta, err := scanConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionMeta{}, false, nil
	}
	if err != nil {
		return SessionMeta{}, false, fmt.Errorf("transcript: reading fresh repository entry: %w", err)
	}
	profiles, err := r.profileNames(ctx, []int64{id})
	if err != nil {
		return SessionMeta{}, false, err
	}
	meta.Profiles = profiles[id]
	return meta, true, nil
}

// List returns repository entries using the same filtering and ordering as
// Index.List. Profile filters apply to every membership, not just the display
// profile, so shared transcript homes remain discoverable from each profile.
func (r *Repository) List(ctx context.Context, filter ListFilter) ([]SessionMeta, error) {
	rows, err := r.store.DB().QueryContext(ctx, conversationSelect+`
		ORDER BY last_at_ns DESC, started_at_ns DESC, harness, session_id
	`)
	if err != nil {
		return nil, fmt.Errorf("transcript: listing repository entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type stored struct {
		id   int64
		meta SessionMeta
	}
	storedRows := make([]stored, 0)
	ids := make([]int64, 0)
	for rows.Next() {
		id, meta, err := scanConversation(rows)
		if err != nil {
			return nil, fmt.Errorf("transcript: scanning repository entry: %w", err)
		}
		storedRows = append(storedRows, stored{id: id, meta: meta})
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("transcript: iterating repository entries: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("transcript: closing repository listing: %w", err)
	}

	profiles, err := r.profileNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(storedRows))
	for _, stored := range storedRows {
		meta := stored.meta
		meta.Profiles = profiles[stored.id]
		if meta.HiddenFromIndex && !filter.IncludeHidden {
			continue
		}
		if !matchAny(meta.Harness, filter.Harness) || !meta.MatchesAnyProfile(filter.Profile) || !matchAny(meta.Project, filter.Project) {
			continue
		}
		out = append(out, meta)
	}
	return out, nil
}

// SetHidden changes soft-deletion state. Hiding also removes searchable
// content; unhiding leaves content absent until a refresh supplies it again.
func (r *Repository) SetHidden(ctx context.Context, harness, sessionID string, hidden bool) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE conversations SET hidden = ?, updated_at_ns = ?
			WHERE harness = ? AND session_id = ?
		`, boolInt(hidden), r.now().UnixNano(), harness, sessionID)
		if err != nil {
			return fmt.Errorf("transcript: updating hidden state: %w", err)
		}
		if err := requireAffected(result, IndexKey(harness, sessionID)); err != nil {
			return err
		}
		if hidden {
			if _, err := tx.ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = ? AND entity_id = ?`,
				transcriptSearchEntityType, IndexKey(harness, sessionID)); err != nil {
				return fmt.Errorf("transcript: removing hidden search document: %w", err)
			}
		}
		return nil
	})
}

// Delete removes repository metadata, memberships, and searchable content.
// It never removes the source transcript file.
func (r *Repository) Delete(ctx context.Context, harness, sessionID string) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		key := IndexKey(harness, sessionID)
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = ? AND entity_id = ?`,
			transcriptSearchEntityType, key); err != nil {
			return fmt.Errorf("transcript: deleting search document: %w", err)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM conversations WHERE harness = ? AND session_id = ?`, harness, sessionID)
		if err != nil {
			return fmt.Errorf("transcript: deleting repository entry: %w", err)
		}
		return requireAffected(result, key)
	})
}

// HideBefore applies retention without touching source transcript files.
func (r *Repository) HideBefore(ctx context.Context, cutoff time.Time) (int, error) {
	hidden := 0
	err := r.store.Transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT harness, session_id FROM conversations
			WHERE hidden = 0 AND last_at_ns > 0 AND last_at_ns < ?
		`, timeToNS(cutoff))
		if err != nil {
			return fmt.Errorf("transcript: selecting retention candidates: %w", err)
		}
		var keys []string
		for rows.Next() {
			var harness, sessionID string
			if err := rows.Scan(&harness, &sessionID); err != nil {
				_ = rows.Close()
				return fmt.Errorf("transcript: scanning retention candidate: %w", err)
			}
			keys = append(keys, IndexKey(harness, sessionID))
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("transcript: iterating retention candidates: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("transcript: closing retention candidates: %w", err)
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE conversations SET hidden = 1, updated_at_ns = ?
			WHERE hidden = 0 AND last_at_ns > 0 AND last_at_ns < ?
		`, r.now().UnixNano(), timeToNS(cutoff))
		if err != nil {
			return fmt.Errorf("transcript: applying retention: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("transcript: counting retention updates: %w", err)
		}
		hidden = int(count)
		for _, key := range keys {
			if _, err := tx.ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = ? AND entity_id = ?`,
				transcriptSearchEntityType, key); err != nil {
				return fmt.Errorf("transcript: removing retained search document: %w", err)
			}
		}
		return nil
	})
	return hidden, err
}

// LastImport returns the completion record for a named legacy source.
func (r *Repository) LastImport(ctx context.Context, sourceName string) (ImportRecord, bool, error) {
	var record ImportRecord
	var completedAt int64
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT source_name, source_path, fingerprint, completed_at_ns, record_count
		FROM import_records WHERE source_name = ?
	`, sourceName).Scan(&record.SourceName, &record.SourcePath, &record.Fingerprint, &completedAt, &record.RecordCount)
	if errors.Is(err, sql.ErrNoRows) {
		return ImportRecord{}, false, nil
	}
	if err != nil {
		return ImportRecord{}, false, fmt.Errorf("transcript: reading import record: %w", err)
	}
	record.CompletedAt = timeFromNS(completedAt)
	return record, true, nil
}

func (r *Repository) upsertTx(ctx context.Context, tx *sql.Tx, meta SessionMeta, searchText *string) error {
	if meta.Harness == "" || meta.ID == "" {
		return errors.New("transcript: repository entry requires harness and session id")
	}
	nowNS := r.now().UnixNano()
	var conversationID int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO conversations (
			harness, session_id, display_profile, project, cwd, source_path,
			source_size, source_mtime_ns, started_at_ns, last_at_ns,
			message_count, snippet, hidden, created_at_ns, updated_at_ns
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(harness, session_id) DO UPDATE SET
			display_profile = excluded.display_profile,
			project = excluded.project,
			cwd = excluded.cwd,
			source_path = excluded.source_path,
			source_size = excluded.source_size,
			source_mtime_ns = excluded.source_mtime_ns,
			started_at_ns = excluded.started_at_ns,
			last_at_ns = excluded.last_at_ns,
			message_count = excluded.message_count,
			snippet = excluded.snippet,
			hidden = excluded.hidden,
			updated_at_ns = excluded.updated_at_ns
		RETURNING id
	`, meta.Harness, meta.ID, meta.DisplayProfile(), meta.Project, meta.CWD, meta.Path,
		meta.FileSize, timeToNS(meta.FileModTime), timeToNS(meta.StartedAt), timeToNS(meta.LastAt),
		meta.MessageCount, meta.Snippet, boolInt(meta.HiddenFromIndex), nowNS, nowNS).Scan(&conversationID)
	if err != nil {
		return fmt.Errorf("transcript: upserting repository entry %q: %w", IndexKey(meta.Harness, meta.ID), err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM profile_memberships WHERE conversation_id = ?`, conversationID); err != nil {
		return fmt.Errorf("transcript: replacing profile memberships: %w", err)
	}
	for position, profile := range meta.ProfileNames() {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO profile_memberships (
				conversation_id, profile_name, is_display_profile, position, created_at_ns
			) VALUES (?, ?, ?, ?, ?)
		`, conversationID, profile, boolInt(profile == meta.DisplayProfile()), position, nowNS)
		if err != nil {
			return fmt.Errorf("transcript: inserting profile membership %q: %w", profile, err)
		}
	}
	key := IndexKey(meta.Harness, meta.ID)
	if meta.HiddenFromIndex {
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = ? AND entity_id = ?`,
			transcriptSearchEntityType, key); err != nil {
			return fmt.Errorf("transcript: removing hidden search document: %w", err)
		}
		return nil
	}
	if searchText == nil {
		return nil
	}
	return r.upsertSearchDocumentTx(ctx, tx, conversationID, meta.Harness, meta.ID,
		meta.DisplayProfile(), meta.Snippet, timeToNS(meta.LastAt), *searchText)
}

func (r *Repository) upsertSearchDocumentTx(
	ctx context.Context,
	tx *sql.Tx,
	conversationID int64,
	harness, sessionID, profile, snippet string,
	lastAt int64,
	searchText string,
) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO search_documents (
			entity_type, entity_id, harness, profile_name, occurred_at_ns,
			title, normalized_content, updated_at_ns
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(entity_type, entity_id) DO UPDATE SET
			harness = excluded.harness,
			profile_name = excluded.profile_name,
			occurred_at_ns = excluded.occurred_at_ns,
			title = excluded.title,
			normalized_content = excluded.normalized_content,
			updated_at_ns = excluded.updated_at_ns
	`, transcriptSearchEntityType, IndexKey(harness, sessionID), harness, profile,
		lastAt, snippet, searchText, r.now().UnixNano())
	if err != nil {
		return fmt.Errorf("transcript: upserting search document for conversation %d: %w", conversationID, err)
	}
	return nil
}

const conversationSelect = `
	SELECT id, harness, session_id, display_profile, project, cwd, source_path,
		source_size, source_mtime_ns, started_at_ns, last_at_ns,
		message_count, snippet, hidden
	FROM conversations
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanConversation(scanner rowScanner) (int64, SessionMeta, error) {
	var id int64
	var meta SessionMeta
	var sourceMtime, startedAt, lastAt int64
	var hidden int
	err := scanner.Scan(&id, &meta.Harness, &meta.ID, &meta.Profile, &meta.Project, &meta.CWD, &meta.Path,
		&meta.FileSize, &sourceMtime, &startedAt, &lastAt, &meta.MessageCount, &meta.Snippet, &hidden)
	if err != nil {
		return 0, SessionMeta{}, err
	}
	meta.FileModTime = timeFromNS(sourceMtime)
	meta.StartedAt = timeFromNS(startedAt)
	meta.LastAt = timeFromNS(lastAt)
	meta.HiddenFromIndex = hidden != 0
	return id, meta, nil
}

func (r *Repository) profileNames(ctx context.Context, ids []int64) (map[int64][]string, error) {
	profiles := make(map[int64][]string, len(ids))
	if len(ids) == 0 {
		return profiles, nil
	}
	// Stay below SQLite's variable limit for indexes with many transcripts.
	const chunkSize = 500
	for start := 0; start < len(ids); start += chunkSize {
		end := min(start+chunkSize, len(ids))
		chunk := ids[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := r.store.DB().QueryContext(ctx, `
			SELECT conversation_id, profile_name FROM profile_memberships
			WHERE conversation_id IN (`+placeholders+`)
			ORDER BY conversation_id, position, profile_name
		`, args...)
		if err != nil {
			return nil, fmt.Errorf("transcript: reading profile memberships: %w", err)
		}
		for rows.Next() {
			var id int64
			var profile string
			if err := rows.Scan(&id, &profile); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("transcript: scanning profile membership: %w", err)
			}
			profiles[id] = append(profiles[id], profile)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("transcript: iterating profile memberships: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("transcript: closing profile memberships: %w", err)
		}
	}
	return profiles, nil
}

func requireAffected(result sql.Result, key string) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("transcript: checking repository mutation for %q: %w", key, err)
	}
	if count == 0 {
		return fmt.Errorf("transcript: no repository entry for %q", key)
	}
	return nil
}

func timeToNS(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixNano()
}

func timeFromNS(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
