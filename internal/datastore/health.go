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
	DocumentCount              int            `json:"document_count"`
	FTSRowCount                int            `json:"fts_row_count"`
	MissingDocuments           map[string]int `json:"missing_documents"`
	RepairableMissingDocuments map[string]int `json:"repairable_missing_documents"`
	UnavailablePlaceholders    map[string]int `json:"unavailable_placeholders"`
	InconsistentDocuments      map[string]int `json:"inconsistent_documents"`
	Integrity                  string         `json:"integrity"`
	IntegrityVerified          bool           `json:"integrity_verified"`
	Warnings                   []string       `json:"warnings,omitempty"`
	Fresh                      bool           `json:"fresh"`
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
	status.Search, err = s.inspectSearch(ctx, int(status.EntityCounts["search_documents"]))
	if err != nil {
		return Status{}, err
	}
	status.Healthy = status.MigrationCurrent && status.Integrity == "ok" &&
		status.ForeignKeyViolations == 0 && status.SecurePermissions && status.Search.Fresh
	return status, nil
}

type missingSearchSource struct {
	entityType string
	query      string
	wantDir    bool
}

func (s *Store) inspectSearch(ctx context.Context, documentCount int) (SearchStatus, error) {
	status := SearchStatus{
		DocumentCount:              documentCount,
		MissingDocuments:           make(map[string]int),
		RepairableMissingDocuments: make(map[string]int),
		UnavailablePlaceholders:    make(map[string]int),
		InconsistentDocuments:      make(map[string]int),
	}
	for _, entityType := range []string{"conversation", "plan", "handoff", "activity"} {
		status.MissingDocuments[entityType] = 0
		status.RepairableMissingDocuments[entityType] = 0
		status.UnavailablePlaceholders[entityType] = 0
	}
	sources := []missingSearchSource{
		{
			entityType: "conversation",
			query: `SELECT c.source_path FROM conversations c
				WHERE c.hidden = 0 AND c.message_count > 0
				AND NOT EXISTS (SELECT 1 FROM search_documents d
					WHERE d.entity_type = 'conversation' AND d.entity_id = c.harness || ':' || c.session_id)`,
		},
		{
			entityType: "plan",
			query: `SELECT p.path FROM plans p
				WHERE NOT EXISTS (SELECT 1 FROM search_documents d
					WHERE d.entity_type = 'plan' AND d.entity_id = p.id)`,
			wantDir: true,
		},
		{
			entityType: "handoff",
			query: `SELECT h.path FROM handoffs h
				WHERE NOT EXISTS (SELECT 1 FROM search_documents d
					WHERE d.entity_type = 'handoff' AND d.entity_id = h.id)`,
		},
	}
	repairableTotal := 0
	for _, source := range sources {
		rows, err := s.db.QueryContext(ctx, source.query)
		if err != nil {
			return SearchStatus{}, fmt.Errorf("datastore: list missing %s search documents: %w", source.entityType, err)
		}
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				_ = rows.Close()
				return SearchStatus{}, fmt.Errorf("datastore: scan missing %s search document: %w", source.entityType, err)
			}
			status.MissingDocuments[source.entityType]++
			if searchSourceAvailable(path, source.wantDir) {
				status.RepairableMissingDocuments[source.entityType]++
				repairableTotal++
			} else {
				status.UnavailablePlaceholders[source.entityType]++
			}
		}
		if err := rows.Close(); err != nil {
			return SearchStatus{}, fmt.Errorf("datastore: close missing %s search documents: %w", source.entityType, err)
		}
		if err := rows.Err(); err != nil {
			return SearchStatus{}, fmt.Errorf("datastore: iterate missing %s search documents: %w", source.entityType, err)
		}
	}

	var missingActivity int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM activity_items a
		WHERE NOT EXISTS (SELECT 1 FROM search_documents d
			WHERE d.entity_type = 'activity' AND d.entity_id = CAST(a.id AS TEXT))
	`).Scan(&missingActivity); err != nil {
		return SearchStatus{}, fmt.Errorf("datastore: count missing activity search documents: %w", err)
	}
	status.MissingDocuments["activity"] = missingActivity
	status.RepairableMissingDocuments["activity"] = missingActivity
	repairableTotal += missingActivity

	// External-content FTS tables return content rows for ordinary unfiltered
	// SELECTs even when the index itself is empty. The docsize shadow table is
	// the read-only source of truth for actual indexed row relationships.
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM search_documents_fts_docsize`).Scan(&status.FTSRowCount); err != nil {
		return SearchStatus{}, fmt.Errorf("datastore: count indexed FTS rows: %w", err)
	}
	consistencyQueries := map[string]string{
		"fts_missing_rows": `SELECT count(*) FROM search_documents d
			WHERE NOT EXISTS (SELECT 1 FROM search_documents_fts_docsize f WHERE f.id = d.id)`,
		"fts_orphan_rows": `SELECT count(*) FROM search_documents_fts_docsize f
			WHERE NOT EXISTS (SELECT 1 FROM search_documents d WHERE d.id = f.id)`,
	}
	inconsistentTotal := 0
	for name, query := range consistencyQueries {
		var count int
		if err := s.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return SearchStatus{}, fmt.Errorf("datastore: inspect %s: %w", name, err)
		}
		status.InconsistentDocuments[name] = count
		inconsistentTotal += count
	}
	if inconsistentTotal == 0 && status.DocumentCount == status.FTSRowCount {
		status.Integrity = "ok"
	} else {
		status.Integrity = fmt.Sprintf("FTS/content relationships disagree (%d inconsistent rows)", inconsistentTotal)
	}
	for _, entityType := range []string{"conversation", "plan", "handoff", "activity"} {
		if unavailable := status.UnavailablePlaceholders[entityType]; unavailable > 0 {
			status.Warnings = append(status.Warnings,
				fmt.Sprintf("%d %s search placeholder(s) unavailable because source data disappeared", unavailable, entityType))
		}
	}
	status.Fresh = status.Integrity == "ok" && repairableTotal == 0
	return status, nil
}

func searchSourceAvailable(path string, wantDir bool) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		// Permission and transient I/O failures are repairable failures. Only a
		// definitely absent source is an intentionally retained placeholder.
		return !os.IsNotExist(err)
	}
	if wantDir {
		return info.IsDir()
	}
	return info.Mode().IsRegular()
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

// VerifySearchIndex runs FTS5's mutating external-content integrity command.
// Ordinary status deliberately does not call this method; explicit rebuilds
// and strict verification are the only production callers.
func (s *Store) VerifySearchIndex(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("datastore: verify search index on nil store")
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO search_documents_fts(search_documents_fts, rank)
		VALUES ('integrity-check', 1)
	`); err != nil {
		return fmt.Errorf("datastore: verify FTS index: %w", err)
	}
	return nil
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
		{"workspaces", `SELECT id, canonical_path, display_name, configured_project_id, created_at_ns FROM workspaces ORDER BY id`},
		{"workspace_aliases", `SELECT workspace_id, alias FROM workspace_aliases ORDER BY workspace_id, alias`},
		{"conversations", `SELECT id, harness, session_id, workspace_id, worktree_context_id, hidden, created_at_ns FROM conversations ORDER BY id`},
		{"profile_memberships", `SELECT conversation_id, profile_name, is_display_profile, position FROM profile_memberships ORDER BY conversation_id, position, profile_name`},
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
