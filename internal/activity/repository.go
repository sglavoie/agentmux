// Package activity owns the durable, compact work journal. It deliberately
// stores only classified outcomes and attribution metadata; raw hook payloads
// (and in particular prompt bodies) never reach this package.
package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/datastore"
)

const (
	searchEntityType = "activity"
	// DefaultReaderID is the local operator's durable read-marker identity.
	// The schema supports additional readers without changing the repository
	// contract, but the desktop TUI intentionally has one predictable marker.
	DefaultReaderID = "local"
	// DefaultFeedLimit bounds each workspace snapshot while still leaving
	// enough context to explain a short sequence of meaningful transitions.
	DefaultFeedLimit = 12
)

// Item is one meaningful state-machine outcome waiting to be journaled.
// WorkspaceID and ConversationID are intentionally optional: hook processing
// can precede transcript indexing and workspace projection by several seconds.
type Item struct {
	DedupeKey      string
	WorkspaceID    string
	ConversationID *int64
	HandoffID      string
	Harness        string
	SessionID      string
	WindowID       int
	Kind           string
	Summary        string
	MetadataJSON   string
	OccurredAt     time.Time
}

// Record is one durable activity row safe for workspace rendering. Summary is
// already the daemon's compact classified outcome; raw hook payloads and prompt
// bodies are never stored or returned here.
type Record struct {
	ID          int64
	WorkspaceID string
	Harness     string
	SessionID   string
	WindowID    int
	Kind        string
	Summary     string
	OccurredAt  time.Time
	Unread      bool
}

// Feed is the bounded activity range loaded for one workspace. When unread
// activity exists, Items contains the oldest unread batch so advancing the
// high-water mark acknowledges only activity the operator could see. When no
// unread activity exists, Items contains the most recent records for context.
type Feed struct {
	Items         []Record
	UnreadCount   int
	ReadThrough   time.Time
	LoadedThrough time.Time
	LatestAt      time.Time
}

// Repository persists activity rows and their unified-search documents.
type Repository struct {
	store *datastore.Store
	now   func() time.Time
}

// NewRepository returns an activity repository backed by store.
func NewRepository(store *datastore.Store) (*Repository, error) {
	if store == nil || store.DB() == nil {
		return nil, errors.New("activity: repository requires an open datastore")
	}
	return &Repository{store: store, now: time.Now}, nil
}

// InsertBatch adds items transactionally. A duplicate dedupe key is a
// successful no-op, which makes writer retries and daemon restarts safe.
func (r *Repository) InsertBatch(ctx context.Context, items []Item) (int, error) {
	if r == nil || r.store == nil {
		return 0, errors.New("activity: insert on nil repository")
	}
	inserted := 0
	err := r.store.Transaction(ctx, func(tx *sql.Tx) error {
		for _, item := range items {
			if err := validateItem(item); err != nil {
				return err
			}
			conversationID, workspaceID, err := r.resolveItemTx(ctx, tx, item)
			if err != nil {
				return err
			}
			if item.ConversationID != nil {
				conversationID = item.ConversationID
				if workspaceID == "" {
					workspaceID, err = workspaceForConversationTx(ctx, tx, *conversationID)
					if err != nil {
						return err
					}
				}
			}
			if item.WorkspaceID != "" {
				workspaceID = item.WorkspaceID
			}
			if conversationID != nil && item.WindowID > 0 && strings.TrimSpace(item.Harness) != "" {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO live_conversation_mappings (
						window_id, conversation_id, harness, parent_conversation_id, observed_at_ns
					) VALUES (?, ?, ?, NULL, ?)
					ON CONFLICT(window_id) DO UPDATE SET
						conversation_id = excluded.conversation_id,
						harness = excluded.harness,
						parent_conversation_id = NULL,
						observed_at_ns = excluded.observed_at_ns
				`, item.WindowID, *conversationID, strings.TrimSpace(item.Harness), r.now().UnixNano()); err != nil {
					return fmt.Errorf("activity: recording live conversation mapping: %w", err)
				}
			}

			occurredAt := item.OccurredAt
			if occurredAt.IsZero() {
				occurredAt = r.now()
			}
			var id int64
			err = tx.QueryRowContext(ctx, `
				INSERT INTO activity_items (
					dedupe_key, workspace_id, conversation_id, handoff_id, harness,
					session_id, window_id, kind, summary, metadata_json,
					occurred_at_ns, created_at_ns
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT DO NOTHING
				RETURNING id
			`, nullableString(item.DedupeKey), nullableString(workspaceID), nullableInt64(conversationID),
				nullableString(item.HandoffID), nullableString(item.Harness), nullableString(item.SessionID),
				nullablePositiveInt(item.WindowID), item.Kind, item.Summary, normalizedMetadata(item.MetadataJSON),
				occurredAt.UnixNano(), r.now().UnixNano()).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("activity: inserting %q: %w", item.Kind, err)
			}
			if err := r.upsertSearchDocumentTx(ctx, tx, id, workspaceID, item, occurredAt); err != nil {
				return err
			}
			inserted++
		}
		return r.backfillTx(ctx, tx)
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

// Backfill attaches previously unlinked activity to conversations and their
// workspaces. It is safe to call repeatedly as transcript import and live
// mapping updates arrive independently from hook delivery.
func (r *Repository) Backfill(ctx context.Context) error {
	if r == nil || r.store == nil {
		return errors.New("activity: backfill on nil repository")
	}
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		return r.backfillTx(ctx, tx)
	})
}

// RepairMissingSearchDocuments recreates derived activity documents entirely
// from durable journal rows; no external source artifact is required.
func (r *Repository) RepairMissingSearchDocuments(ctx context.Context) (int64, error) {
	result, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO search_documents (
			entity_type, entity_id, workspace_id, harness, occurred_at_ns,
			title, normalized_content, updated_at_ns
		)
		SELECT ?, CAST(a.id AS TEXT), a.workspace_id, a.harness, a.occurred_at_ns,
			a.summary, a.summary || char(10) || a.metadata_json, a.created_at_ns
		FROM activity_items a
		WHERE NOT EXISTS (
			SELECT 1 FROM search_documents d
			WHERE d.entity_type = ? AND d.entity_id = CAST(a.id AS TEXT)
		)
		ON CONFLICT(entity_type, entity_id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			harness = excluded.harness,
			occurred_at_ns = excluded.occurred_at_ns,
			title = excluded.title,
			normalized_content = excluded.normalized_content,
			updated_at_ns = excluded.updated_at_ns
	`, searchEntityType, searchEntityType)
	if err != nil {
		return 0, fmt.Errorf("activity: repairing search documents: %w", err)
	}
	return result.RowsAffected()
}

// ListForWorkspaces returns one bounded, deterministic feed for every supplied
// workspace ID. Unknown IDs are represented by an empty feed rather than an
// error so configuration churn cannot make the whole workspace home fail.
func (r *Repository) ListForWorkspaces(ctx context.Context, workspaceIDs []string, readerID string, limit int) (map[string]Feed, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("activity: list on nil repository")
	}
	readerID = normalizedReaderID(readerID)
	if limit <= 0 {
		limit = DefaultFeedLimit
	}
	feeds := make(map[string]Feed, len(workspaceIDs))
	seen := make(map[string]bool, len(workspaceIDs))
	for _, workspaceID := range workspaceIDs {
		workspaceID = strings.TrimSpace(workspaceID)
		if workspaceID == "" || seen[workspaceID] {
			continue
		}
		seen[workspaceID] = true
		feed, err := r.listWorkspace(ctx, workspaceID, readerID, limit)
		if err != nil {
			return nil, err
		}
		feeds[workspaceID] = feed
	}
	return feeds, nil
}

// MarkWorkspaceRead advances one workspace marker monotonically through the
// supplied loaded range. A stale UI command can therefore never move a marker
// backwards or acknowledge activity that arrived after its snapshot.
func (r *Repository) MarkWorkspaceRead(ctx context.Context, workspaceID, readerID string, through time.Time) (bool, error) {
	if r == nil || r.store == nil {
		return false, errors.New("activity: mark read on nil repository")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return false, errors.New("activity: workspace ID is required")
	}
	if through.IsZero() {
		return false, nil
	}
	result, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO workspace_read_markers (workspace_id, reader_id, read_at_ns)
		VALUES (?, ?, ?)
		ON CONFLICT(workspace_id, reader_id) DO UPDATE SET
			read_at_ns = MAX(workspace_read_markers.read_at_ns, excluded.read_at_ns)
		WHERE excluded.read_at_ns > workspace_read_markers.read_at_ns
	`, workspaceID, normalizedReaderID(readerID), through.UnixNano())
	if err != nil {
		return false, fmt.Errorf("activity: marking workspace %q read: %w", workspaceID, err)
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

// MarkAllWorkspacesRead advances every workspace with durable activity to its
// current latest occurrence in one transaction. Later arrivals remain unread.
func (r *Repository) MarkAllWorkspacesRead(ctx context.Context, readerID string) (int64, error) {
	if r == nil || r.store == nil {
		return 0, errors.New("activity: mark all read on nil repository")
	}
	result, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO workspace_read_markers (workspace_id, reader_id, read_at_ns)
		SELECT workspace_id, ?, MAX(occurred_at_ns)
		FROM activity_items
		WHERE workspace_id IS NOT NULL
		GROUP BY workspace_id
		ON CONFLICT(workspace_id, reader_id) DO UPDATE SET
			read_at_ns = MAX(workspace_read_markers.read_at_ns, excluded.read_at_ns)
		WHERE excluded.read_at_ns > workspace_read_markers.read_at_ns
	`, normalizedReaderID(readerID))
	if err != nil {
		return 0, fmt.Errorf("activity: marking all workspaces read: %w", err)
	}
	return result.RowsAffected()
}

// Sweep removes only expired journal/search rows. Conversations, transcripts,
// handoff artifacts, and every other source record are deliberately untouched.
func (r *Repository) Sweep(ctx context.Context, keep time.Duration) (int64, error) {
	if r == nil || r.store == nil {
		return 0, errors.New("activity: sweep on nil repository")
	}
	if keep <= 0 {
		return 0, nil
	}
	cutoff := r.now().Add(-keep).UnixNano()
	var removed int64
	err := r.store.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM search_documents
			WHERE entity_type = ?
				AND entity_id IN (
					SELECT CAST(id AS TEXT) FROM activity_items WHERE occurred_at_ns < ?
				)
		`, searchEntityType, cutoff); err != nil {
			return fmt.Errorf("activity: removing expired search documents: %w", err)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM activity_items WHERE occurred_at_ns < ?`, cutoff)
		if err != nil {
			return fmt.Errorf("activity: removing expired activity: %w", err)
		}
		removed, err = result.RowsAffected()
		return err
	})
	return removed, err
}

func (r *Repository) listWorkspace(ctx context.Context, workspaceID, readerID string, limit int) (Feed, error) {
	var readAt, latestAt int64
	var unreadCount int
	if err := r.store.DB().QueryRowContext(ctx, `
		SELECT
			COALESCE((SELECT read_at_ns FROM workspace_read_markers WHERE workspace_id = ? AND reader_id = ?), 0),
			COALESCE(MAX(occurred_at_ns), 0),
			COUNT(*) FILTER (WHERE occurred_at_ns > COALESCE((SELECT read_at_ns FROM workspace_read_markers WHERE workspace_id = ? AND reader_id = ?), 0))
		FROM activity_items WHERE workspace_id = ?
	`, workspaceID, readerID, workspaceID, readerID, workspaceID).Scan(&readAt, &latestAt, &unreadCount); err != nil {
		return Feed{}, fmt.Errorf("activity: reading workspace %q feed state: %w", workspaceID, err)
	}
	feed := Feed{UnreadCount: unreadCount}
	if readAt > 0 {
		feed.ReadThrough = time.Unix(0, readAt)
	}
	if latestAt > 0 {
		feed.LatestAt = time.Unix(0, latestAt)
	}

	query := `
		SELECT id, COALESCE(harness, ''), COALESCE(session_id, ''), COALESCE(window_id, 0),
			kind, summary, occurred_at_ns
		FROM activity_items
		WHERE workspace_id = ?
		ORDER BY occurred_at_ns DESC, id DESC
		LIMIT ?
	`
	args := []any{workspaceID, limit}
	oldestUnreadFirst := unreadCount > 0
	if oldestUnreadFirst {
		query = `
			SELECT id, COALESCE(harness, ''), COALESCE(session_id, ''), COALESCE(window_id, 0),
				kind, summary, occurred_at_ns
			FROM activity_items
			WHERE workspace_id = ? AND occurred_at_ns > ?
			ORDER BY occurred_at_ns ASC, id ASC
			LIMIT ?
		`
		args = []any{workspaceID, readAt, limit}
	}
	rows, err := r.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return Feed{}, fmt.Errorf("activity: listing workspace %q feed: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var record Record
		var occurredAt int64
		if err := rows.Scan(&record.ID, &record.Harness, &record.SessionID, &record.WindowID, &record.Kind, &record.Summary, &occurredAt); err != nil {
			return Feed{}, fmt.Errorf("activity: scanning workspace %q feed: %w", workspaceID, err)
		}
		record.WorkspaceID = workspaceID
		record.OccurredAt = time.Unix(0, occurredAt)
		record.Unread = occurredAt > readAt
		if record.Unread && record.OccurredAt.After(feed.LoadedThrough) {
			feed.LoadedThrough = record.OccurredAt
		}
		feed.Items = append(feed.Items, record)
	}
	if err := rows.Err(); err != nil {
		return Feed{}, fmt.Errorf("activity: iterating workspace %q feed: %w", workspaceID, err)
	}
	if oldestUnreadFirst {
		for left, right := 0, len(feed.Items)-1; left < right; left, right = left+1, right-1 {
			feed.Items[left], feed.Items[right] = feed.Items[right], feed.Items[left]
		}
	}
	return feed, nil
}

func normalizedReaderID(readerID string) string {
	if readerID = strings.TrimSpace(readerID); readerID != "" {
		return readerID
	}
	return DefaultReaderID
}

func (r *Repository) resolveItemTx(ctx context.Context, tx *sql.Tx, item Item) (*int64, string, error) {
	if harnessName, sessionID := strings.TrimSpace(item.Harness), strings.TrimSpace(item.SessionID); harnessName != "" && sessionID != "" {
		id, workspaceID, found, err := conversationForIdentityTx(ctx, tx, harnessName, sessionID)
		if err != nil {
			return nil, "", err
		}
		if found {
			return &id, workspaceID, nil
		}
	}
	if item.WindowID <= 0 {
		return nil, "", nil
	}
	row := tx.QueryRowContext(ctx, `
		SELECT COALESCE(l.parent_conversation_id, l.conversation_id), COALESCE(c.workspace_id, '')
		FROM live_conversation_mappings l
		JOIN conversations c ON c.id = COALESCE(l.parent_conversation_id, l.conversation_id)
		WHERE l.window_id = ? AND (? = '' OR l.harness = ?)
		ORDER BY l.observed_at_ns DESC
		LIMIT 1
	`, item.WindowID, strings.TrimSpace(item.Harness), strings.TrimSpace(item.Harness))
	var id int64
	var workspaceID string
	err := row.Scan(&id, &workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("activity: resolving live conversation mapping: %w", err)
	}
	return &id, workspaceID, nil
}

func (r *Repository) backfillTx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE activity_items
		SET conversation_id = (
			SELECT c.id FROM conversations c
			WHERE c.harness = activity_items.harness AND c.session_id = activity_items.session_id
		)
		WHERE conversation_id IS NULL
			AND COALESCE(harness, '') <> ''
			AND COALESCE(session_id, '') <> ''
			AND EXISTS (
				SELECT 1 FROM conversations c
				WHERE c.harness = activity_items.harness AND c.session_id = activity_items.session_id
			)
	`); err != nil {
		return fmt.Errorf("activity: backfilling conversation identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE activity_items
		SET conversation_id = (
			SELECT COALESCE(l.parent_conversation_id, l.conversation_id)
			FROM live_conversation_mappings l
			WHERE l.window_id = activity_items.window_id
				AND (COALESCE(activity_items.harness, '') = '' OR l.harness = activity_items.harness)
			ORDER BY l.observed_at_ns DESC
			LIMIT 1
		)
		WHERE conversation_id IS NULL
			AND window_id IS NOT NULL
			AND EXISTS (
				SELECT 1 FROM live_conversation_mappings l
				WHERE l.window_id = activity_items.window_id
					AND (COALESCE(activity_items.harness, '') = '' OR l.harness = activity_items.harness)
			)
	`); err != nil {
		return fmt.Errorf("activity: backfilling live conversation mapping: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE activity_items
		SET workspace_id = (
			SELECT c.workspace_id FROM conversations c WHERE c.id = activity_items.conversation_id
		)
		WHERE workspace_id IS NULL
			AND conversation_id IS NOT NULL
			AND EXISTS (
				SELECT 1 FROM conversations c
				WHERE c.id = activity_items.conversation_id AND c.workspace_id IS NOT NULL
			)
	`); err != nil {
		return fmt.Errorf("activity: backfilling workspace: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE search_documents
		SET workspace_id = (
			SELECT a.workspace_id FROM activity_items a
			WHERE CAST(a.id AS TEXT) = search_documents.entity_id
		)
		WHERE entity_type = ?
			AND workspace_id IS NULL
			AND EXISTS (
				SELECT 1 FROM activity_items a
				WHERE CAST(a.id AS TEXT) = search_documents.entity_id
					AND a.workspace_id IS NOT NULL
			)
	`, searchEntityType); err != nil {
		return fmt.Errorf("activity: backfilling search workspace: %w", err)
	}
	return nil
}

func (r *Repository) upsertSearchDocumentTx(ctx context.Context, tx *sql.Tx, id int64, workspaceID string, item Item, occurredAt time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO search_documents (
			entity_type, entity_id, workspace_id, harness, occurred_at_ns,
			title, normalized_content, updated_at_ns
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(entity_type, entity_id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			harness = excluded.harness,
			occurred_at_ns = excluded.occurred_at_ns,
			title = excluded.title,
			normalized_content = excluded.normalized_content,
			updated_at_ns = excluded.updated_at_ns
	`, searchEntityType, strconv.FormatInt(id, 10), nullableString(workspaceID), nullableString(item.Harness),
		occurredAt.UnixNano(), item.Summary, item.Summary+"\n"+normalizedMetadata(item.MetadataJSON), r.now().UnixNano())
	if err != nil {
		return fmt.Errorf("activity: publishing search document: %w", err)
	}
	return nil
}

func conversationForIdentityTx(ctx context.Context, tx *sql.Tx, harnessName, sessionID string) (int64, string, bool, error) {
	var id int64
	var workspaceID string
	err := tx.QueryRowContext(ctx, `
		SELECT id, COALESCE(workspace_id, '')
		FROM conversations WHERE harness = ? AND session_id = ?
	`, harnessName, sessionID).Scan(&id, &workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, fmt.Errorf("activity: resolving conversation: %w", err)
	}
	return id, workspaceID, true, nil
}

func workspaceForConversationTx(ctx context.Context, tx *sql.Tx, conversationID int64) (string, error) {
	var workspaceID string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(workspace_id, '') FROM conversations WHERE id = ?`, conversationID).Scan(&workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("activity: resolving conversation workspace: %w", err)
	}
	return workspaceID, nil
}

func validateItem(item Item) error {
	if strings.TrimSpace(item.Kind) == "" {
		return errors.New("activity: item kind is required")
	}
	if strings.TrimSpace(item.Summary) == "" {
		return errors.New("activity: item summary is required")
	}
	if metadata := strings.TrimSpace(item.MetadataJSON); metadata != "" && !json.Valid([]byte(metadata)) {
		return errors.New("activity: item metadata must be valid JSON")
	}
	return nil
}

func normalizedMetadata(metadata string) string {
	if strings.TrimSpace(metadata) == "" {
		return "{}"
	}
	return metadata
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullablePositiveInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}
