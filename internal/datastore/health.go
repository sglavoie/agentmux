package datastore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ImportStatus describes one completed compatibility-source import.
type ImportStatus struct {
	SourceName  string    `json:"source_name"`
	SourcePath  string    `json:"source_path"`
	Fingerprint string    `json:"fingerprint"`
	CompletedAt time.Time `json:"completed_at"`
	RecordCount int       `json:"record_count"`
}

// SearchStatus summarizes the external-content FTS projection.
type SearchStatus struct {
	DocumentCount    int            `json:"document_count"`
	FTSRowCount      int            `json:"fts_row_count"`
	MissingDocuments map[string]int `json:"missing_documents"`
	Integrity        string         `json:"integrity"`
	Fresh            bool           `json:"fresh"`
}

// Status is the operational state of the durable Agent Mux datastore.
type Status struct {
	Path                 string           `json:"path"`
	SchemaVersion        int              `json:"schema_version"`
	CurrentSchemaVersion int              `json:"current_schema_version"`
	MigrationCurrent     bool             `json:"migration_current"`
	Integrity            string           `json:"integrity"`
	ForeignKeyViolations int              `json:"foreign_key_violations"`
	SecurePermissions    bool             `json:"secure_permissions"`
	PermissionIssues     []string         `json:"permission_issues,omitempty"`
	Imports              []ImportStatus   `json:"imports"`
	EntityCounts         map[string]int64 `json:"entity_counts"`
	Search               SearchStatus     `json:"search"`
	Healthy              bool             `json:"healthy"`
}

// Inspect performs bounded integrity, migration, import, FTS, and permission
// checks. It does not rewrite source artifacts or derived rows.
func (s *Store) Inspect(ctx context.Context) (Status, error) {
	if s == nil || s.db == nil {
		return Status{}, fmt.Errorf("datastore: inspect on nil store")
	}
	status := Status{
		Path:                 s.path,
		CurrentSchemaVersion: CurrentSchemaVersion,
		Imports:              make([]ImportStatus, 0),
		EntityCounts:         make(map[string]int64),
	}
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return Status{}, err
	}
	status.SchemaVersion = version
	status.MigrationCurrent = version == CurrentSchemaVersion

	status.Integrity, err = pragmaCheck(ctx, s.db, `PRAGMA quick_check`)
	if err != nil {
		return Status{}, fmt.Errorf("datastore: quick integrity check: %w", err)
	}
	status.ForeignKeyViolations, err = foreignKeyViolationCount(ctx, s.db)
	if err != nil {
		return Status{}, err
	}
	status.PermissionIssues, err = permissionIssues(s.path)
	if err != nil {
		return Status{}, err
	}
	status.SecurePermissions = len(status.PermissionIssues) == 0

	rows, err := s.db.QueryContext(ctx, `
		SELECT source_name, source_path, fingerprint, completed_at_ns, record_count
		FROM import_records ORDER BY source_name`)
	if err != nil {
		return Status{}, fmt.Errorf("datastore: list import state: %w", err)
	}
	for rows.Next() {
		var item ImportStatus
		var completedAt int64
		if err := rows.Scan(&item.SourceName, &item.SourcePath, &item.Fingerprint, &completedAt, &item.RecordCount); err != nil {
			_ = rows.Close()
			return Status{}, fmt.Errorf("datastore: scan import state: %w", err)
		}
		item.CompletedAt = time.Unix(0, completedAt).UTC()
		status.Imports = append(status.Imports, item)
	}
	if err := rows.Close(); err != nil {
		return Status{}, fmt.Errorf("datastore: close import state: %w", err)
	}
	if err := rows.Err(); err != nil {
		return Status{}, fmt.Errorf("datastore: iterate import state: %w", err)
	}

	for _, table := range []string{
		"workspaces", "conversations", "plans", "handoffs", "activity_items",
		"workspace_candidates", "managed_worktrees", "search_documents",
	} {
		var count int64
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			return Status{}, fmt.Errorf("datastore: count %s: %w", table, err)
		}
		status.EntityCounts[table] = count
	}
	status.Search.DocumentCount = int(status.EntityCounts["search_documents"])
	status.Search.MissingDocuments = make(map[string]int)
	missingQueries := map[string]string{
		"conversation": `SELECT count(*) FROM conversations c WHERE c.hidden = 0 AND c.message_count > 0 AND NOT EXISTS (SELECT 1 FROM search_documents d WHERE d.entity_type = 'conversation' AND d.entity_id = c.harness || ':' || c.session_id)`,
		"plan":         `SELECT count(*) FROM plans p WHERE NOT EXISTS (SELECT 1 FROM search_documents d WHERE d.entity_type = 'plan' AND d.entity_id = p.id)`,
		"handoff":      `SELECT count(*) FROM handoffs h WHERE NOT EXISTS (SELECT 1 FROM search_documents d WHERE d.entity_type = 'handoff' AND d.entity_id = h.id)`,
		"activity":     `SELECT count(*) FROM activity_items a WHERE NOT EXISTS (SELECT 1 FROM search_documents d WHERE d.entity_type = 'activity' AND d.entity_id = CAST(a.id AS TEXT))`,
	}
	missingTotal := 0
	for entityType, query := range missingQueries {
		var missing int
		if err := s.db.QueryRowContext(ctx, query).Scan(&missing); err != nil {
			return Status{}, fmt.Errorf("datastore: count missing %s search documents: %w", entityType, err)
		}
		status.Search.MissingDocuments[entityType] = missing
		missingTotal += missing
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM search_documents_fts`).Scan(&status.Search.FTSRowCount); err != nil {
		return Status{}, fmt.Errorf("datastore: count FTS rows: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO search_documents_fts(search_documents_fts) VALUES ('integrity-check')`); err != nil {
		status.Search.Integrity = err.Error()
	} else {
		status.Search.Integrity = "ok"
	}
	status.Search.Fresh = status.Search.Integrity == "ok" && status.Search.DocumentCount == status.Search.FTSRowCount && missingTotal == 0
	status.Healthy = status.MigrationCurrent && status.Integrity == "ok" &&
		status.ForeignKeyViolations == 0 && status.SecurePermissions && status.Search.Fresh
	return status, nil
}

// RebuildSearchIndex reconstructs the external-content FTS index from the
// current search_documents table. Source rows and every non-search table are
// untouched.
func (s *Store) RebuildSearchIndex(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("datastore: rebuild search index on nil store")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO search_documents_fts(search_documents_fts) VALUES ('rebuild')`); err != nil {
		return fmt.Errorf("datastore: rebuild FTS index: %w", err)
	}
	return s.enforcePermissions()
}

func pragmaCheck(ctx context.Context, db *sql.DB, query string) (string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var results []string
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return "", err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return strings.Join(results, "; "), nil
}

func foreignKeyViolationCount(ctx context.Context, db *sql.DB) (int, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return 0, fmt.Errorf("datastore: foreign-key check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent string
		var key int
		if err := rows.Scan(&table, &rowID, &parent, &key); err != nil {
			return 0, fmt.Errorf("datastore: scan foreign-key violation: %w", err)
		}
		count++
	}
	return count, rows.Err()
}

func permissionIssues(databasePath string) ([]string, error) {
	expected := []struct {
		path string
		mode fs.FileMode
		dir  bool
	}{
		{filepath.Dir(databasePath), dataDirectoryMode, true},
		{databasePath, databaseFileMode, false},
		{databasePath + "-wal", databaseFileMode, false},
		{databasePath + "-shm", databaseFileMode, false},
	}
	issues := make([]string, 0)
	for _, item := range expected {
		info, err := os.Lstat(item.path)
		if err != nil {
			if os.IsNotExist(err) && !item.dir && item.path != databasePath {
				continue
			}
			return nil, fmt.Errorf("datastore: inspect permissions for %s: %w", item.path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			issues = append(issues, fmt.Sprintf("%s is a symbolic link", item.path))
			continue
		}
		if item.dir != info.IsDir() {
			issues = append(issues, fmt.Sprintf("%s has the wrong file type", item.path))
			continue
		}
		if got := info.Mode().Perm(); got != item.mode {
			issues = append(issues, fmt.Sprintf("%s mode is %04o; want %04o", item.path, got, item.mode))
		}
	}
	sort.Strings(issues)
	return issues, nil
}

// ProtectedState fingerprints durable user decisions and non-derived history
// that an index rebuild must not modify.
type ProtectedState struct {
	Counts map[string]int64 `json:"counts"`
	SHA256 string           `json:"sha256"`
}

// SnapshotProtectedState returns a stable digest over explicit links, handoff
// metadata, activity history/read markers, and managed-worktree relationships.
func (s *Store) SnapshotProtectedState(ctx context.Context) (ProtectedState, error) {
	if s == nil || s.db == nil {
		return ProtectedState{}, fmt.Errorf("datastore: protected-state snapshot on nil store")
	}
	queries := []struct {
		name  string
		query string
	}{
		{"plan_workspace_links", `SELECT plan_id, workspace_id, created_at_ns FROM plan_workspace_links ORDER BY plan_id, workspace_id`},
		{"handoffs", `SELECT id, workspace_id, conversation_id, plan_id, path, content_hash, status, created_at_ns, updated_at_ns FROM handoffs ORDER BY id`},
		{"activity_items", `SELECT id, handoff_id, dedupe_key, harness, session_id, window_id, kind, summary, metadata_json, occurred_at_ns, created_at_ns FROM activity_items ORDER BY id`},
		{"workspace_read_markers", `SELECT workspace_id, reader_id, read_at_ns FROM workspace_read_markers ORDER BY workspace_id, reader_id`},
		{"managed_worktrees", `SELECT id, workspace_id, worktree_context_id, path, branch, base_ref, status, created_at_ns, updated_at_ns FROM managed_worktrees ORDER BY id`},
		{"managed_worktree_contexts", `SELECT c.id, c.workspace_id, c.canonical_path FROM worktree_contexts c WHERE EXISTS (SELECT 1 FROM managed_worktrees m WHERE m.worktree_context_id = c.id) ORDER BY c.id`},
	}
	hash := sha256.New()
	state := ProtectedState{Counts: make(map[string]int64, len(queries))}
	for _, item := range queries {
		_, _ = hash.Write([]byte(item.name + "\x00"))
		count, err := hashRows(ctx, s.db, hash, item.query)
		if err != nil {
			return ProtectedState{}, fmt.Errorf("datastore: snapshot %s: %w", item.name, err)
		}
		state.Counts[item.name] = count
	}
	state.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return state, nil
}

type byteWriter interface{ Write([]byte) (int, error) }

func hashRows(ctx context.Context, db *sql.DB, hash byteWriter, query string) (int64, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	var count int64
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return 0, err
		}
		for _, value := range values {
			switch typed := value.(type) {
			case nil:
				_, _ = hash.Write([]byte("N\x00"))
			case []byte:
				_, _ = hash.Write([]byte("B"))
				_, _ = hash.Write(typed)
				_, _ = hash.Write([]byte{0})
			default:
				_, _ = hash.Write([]byte("V" + fmt.Sprint(typed) + "\x00"))
			}
		}
		_, _ = hash.Write([]byte{0xff})
		count++
	}
	return count, rows.Err()
}
