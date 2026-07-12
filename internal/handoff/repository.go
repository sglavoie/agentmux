package handoff

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/datastore"
)

const (
	StatusDraft             = "draft"
	StatusFinal             = "final"
	searchTitleRunes        = 160
	searchContentBytes      = 32 * 1024
	handoffSearchEntityType = "handoff"
)

// Record is the durable metadata for one generated handoff artifact.
type Record struct {
	ID             string
	WorkspaceID    string
	ConversationID *int64
	PlanID         string
	Path           string
	ContentHash    string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Repository owns handoff metadata and its unified-search projection.
type Repository struct {
	store *datastore.Store
	now   func() time.Time
}

func NewRepository(store *datastore.Store) (*Repository, error) {
	if store == nil || store.DB() == nil {
		return nil, errors.New("handoff: repository requires an open datastore")
	}
	return &Repository{store: store, now: time.Now}, nil
}

// Save atomically upserts metadata and its bounded search document. Existing
// creation time is retained when a draft is rewritten or finalized.
func (r *Repository) Save(ctx context.Context, record Record, title string, content []byte) (Record, error) {
	if err := validateRecord(record); err != nil {
		return Record{}, err
	}
	sum := sha256.Sum256(content)
	if record.ContentHash != fmt.Sprintf("%x", sum[:]) {
		return Record{}, errors.New("handoff: record content hash does not match content")
	}
	now := r.now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = now
	}
	title = truncateRunes(strings.TrimSpace(title), searchTitleRunes)
	if title == "" {
		title = "Handoff " + record.ID
	}
	searchContent := truncateUTF8(string(content), searchContentBytes)

	err := r.store.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO handoffs (
				id, workspace_id, conversation_id, plan_id, path, content_hash,
				status, created_at_ns, updated_at_ns
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				workspace_id = excluded.workspace_id,
				conversation_id = excluded.conversation_id,
				plan_id = excluded.plan_id,
				path = excluded.path,
				content_hash = excluded.content_hash,
				status = excluded.status,
				updated_at_ns = excluded.updated_at_ns
		`, record.ID, nullableString(record.WorkspaceID), nullableInt64(record.ConversationID), nullableString(record.PlanID),
			record.Path, record.ContentHash, record.Status, record.CreatedAt.UnixNano(), record.UpdatedAt.UnixNano())
		if err != nil {
			return fmt.Errorf("handoff: saving metadata: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO search_documents (
				entity_type, entity_id, workspace_id, occurred_at_ns, title,
				normalized_content, updated_at_ns
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(entity_type, entity_id) DO UPDATE SET
				workspace_id = excluded.workspace_id,
				occurred_at_ns = excluded.occurred_at_ns,
				title = excluded.title,
				normalized_content = excluded.normalized_content,
				updated_at_ns = excluded.updated_at_ns
		`, handoffSearchEntityType, record.ID, nullableString(record.WorkspaceID), record.UpdatedAt.UnixNano(),
			title, searchContent, record.UpdatedAt.UnixNano())
		if err != nil {
			return fmt.Errorf("handoff: saving search document: %w", err)
		}
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	stored, found, err := r.Get(ctx, record.ID)
	if err != nil {
		return Record{}, err
	}
	if !found {
		return Record{}, fmt.Errorf("handoff: saved metadata %q was not found", record.ID)
	}
	return stored, nil
}

func (r *Repository) Get(ctx context.Context, id string) (Record, bool, error) {
	row := r.store.DB().QueryRowContext(ctx, handoffSelect+` WHERE id = ?`, id)
	record, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("handoff: reading %q: %w", id, err)
	}
	return record, true, nil
}

func (r *Repository) ListByWorkspace(ctx context.Context, workspaceID string) ([]Record, error) {
	rows, err := r.store.DB().QueryContext(ctx, handoffSelect+`
		WHERE workspace_id = ? ORDER BY created_at_ns DESC, id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("handoff: listing workspace %q: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()
	records := make([]Record, 0)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("handoff: scanning workspace %q: %w", workspaceID, err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("handoff: iterating workspace %q: %w", workspaceID, err)
	}
	return records, nil
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = ? AND entity_id = ?`, handoffSearchEntityType, id); err != nil {
			return fmt.Errorf("handoff: deleting search document: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM handoffs WHERE id = ?`, id); err != nil {
			return fmt.Errorf("handoff: deleting metadata: %w", err)
		}
		return nil
	})
}

// RepairMissingSearchDocuments reconstructs derived handoff documents from
// intact artifacts. Missing artifacts remain durable unavailable placeholders.
func (r *Repository) RepairMissingSearchDocuments(ctx context.Context) (int, error) {
	rows, err := r.store.DB().QueryContext(ctx, handoffSelect+`
		WHERE NOT EXISTS (
			SELECT 1 FROM search_documents d
			WHERE d.entity_type = ? AND d.entity_id = handoffs.id
		)
		ORDER BY id
	`, handoffSearchEntityType)
	if err != nil {
		return 0, fmt.Errorf("handoff: listing repairable search documents: %w", err)
	}
	records := make([]Record, 0)
	for rows.Next() {
		record, scanErr := scanRecord(rows)
		if scanErr != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("handoff: scanning repairable search document: %w", scanErr)
		}
		records = append(records, record)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("handoff: closing repairable search documents: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("handoff: iterating repairable search documents: %w", err)
	}

	repaired := 0
	for _, record := range records {
		content, readErr := os.ReadFile(record.Path)
		if readErr != nil {
			if errors.Is(readErr, fs.ErrNotExist) {
				continue
			}
			return repaired, fmt.Errorf("handoff: read repair source %q: %w", record.ID, readErr)
		}
		sum := sha256.Sum256(content)
		if got := fmt.Sprintf("%x", sum[:]); got != record.ContentHash {
			return repaired, fmt.Errorf("handoff: repair source %q does not match durable content hash", record.ID)
		}
		_, err := r.store.DB().ExecContext(ctx, `
			INSERT INTO search_documents (
				entity_type, entity_id, workspace_id, occurred_at_ns, title,
				normalized_content, updated_at_ns
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(entity_type, entity_id) DO UPDATE SET
				workspace_id = excluded.workspace_id,
				occurred_at_ns = excluded.occurred_at_ns,
				title = excluded.title,
				normalized_content = excluded.normalized_content,
				updated_at_ns = excluded.updated_at_ns
		`, handoffSearchEntityType, record.ID, nullableString(record.WorkspaceID), record.UpdatedAt.UnixNano(),
			"Handoff "+record.ID, truncateUTF8(string(content), searchContentBytes), record.UpdatedAt.UnixNano())
		if err != nil {
			return repaired, fmt.Errorf("handoff: repairing search document %q: %w", record.ID, err)
		}
		repaired++
	}
	return repaired, nil
}

func validateRecord(record Record) error {
	if strings.TrimSpace(record.ID) == "" {
		return errors.New("handoff: record ID is empty")
	}
	if strings.TrimSpace(record.Path) == "" || !filepath.IsAbs(record.Path) {
		return errors.New("handoff: record path must be absolute")
	}
	if len(record.ContentHash) != 64 {
		return errors.New("handoff: record content hash must be a SHA-256 hex digest")
	}
	for _, r := range record.ContentHash {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return errors.New("handoff: record content hash must be lowercase hexadecimal")
		}
	}
	if record.Status != StatusDraft && record.Status != StatusFinal {
		return fmt.Errorf("handoff: invalid record status %q", record.Status)
	}
	return nil
}

const handoffSelect = `SELECT id, workspace_id, conversation_id, plan_id, path, content_hash, status, created_at_ns, updated_at_ns FROM handoffs`

type recordScanner interface{ Scan(...any) error }

func scanRecord(scanner recordScanner) (Record, error) {
	var record Record
	var workspaceID, planID sql.NullString
	var conversationID sql.NullInt64
	var createdAtNS, updatedAtNS int64
	if err := scanner.Scan(&record.ID, &workspaceID, &conversationID, &planID, &record.Path, &record.ContentHash,
		&record.Status, &createdAtNS, &updatedAtNS); err != nil {
		return Record{}, err
	}
	if workspaceID.Valid {
		record.WorkspaceID = workspaceID.String
	}
	if planID.Valid {
		record.PlanID = planID.String
	}
	if conversationID.Valid {
		id := conversationID.Int64
		record.ConversationID = &id
	}
	record.CreatedAt = time.Unix(0, createdAtNS).UTC()
	record.UpdatedAt = time.Unix(0, updatedAtNS).UTC()
	return record, nil
}

func nullableString(value string) any {
	if value == "" {
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

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
