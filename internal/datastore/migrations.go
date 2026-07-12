package datastore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type migration struct {
	version int
	apply   func(context.Context, *sql.Tx) error
}

var migrations = []migration{
	{
		version: 1,
		apply:   applyInitialSchema,
	},
	{
		version: 2,
		apply:   applyActivityIdentitySchema,
	},
	{
		version: 3,
		apply:   applyWorkspaceCandidateSchema,
	},
	{
		version: 4,
		apply:   applyLegacyImportEntrySchema,
	},
	{
		version: 5,
		apply:   applyWorkspaceSearchMembershipSchema,
	},
}

func migrate(ctx context.Context, db *sql.DB, available []migration) error {
	if err := validateMigrations(available); err != nil {
		return err
	}
	tx, err := beginMigrationTransaction(ctx, db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at_ns INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("datastore: create migration table: %w", err)
	}
	version, err := schemaVersion(ctx, tx)
	if err != nil {
		return err
	}
	latest := available[len(available)-1].version
	if version > latest {
		return fmt.Errorf("%w: database is at %d but this binary supports through %d", ErrFutureSchemaVersion, version, latest)
	}
	for _, m := range available {
		if m.version <= version {
			continue
		}
		if err := m.apply(ctx, tx); err != nil {
			return fmt.Errorf("datastore: apply migration %d: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at_ns) VALUES (?, unixepoch('subsec') * 1000000000)`,
			m.version,
		); err != nil {
			return fmt.Errorf("datastore: record migration %d: %w", m.version, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("datastore: commit migrations: %w", err)
	}
	return nil
}

func validateMigrations(available []migration) error {
	if len(available) == 0 {
		return errors.New("datastore: no migrations registered")
	}
	for i, m := range available {
		if m.version != i+1 {
			return fmt.Errorf("datastore: migration sequence is not contiguous at version %d", m.version)
		}
		if m.apply == nil {
			return fmt.Errorf("datastore: migration %d has no apply function", m.version)
		}
	}
	return nil
}

func beginMigrationTransaction(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	var tx *sql.Tx
	err := retrySQLiteBusy(ctx, func() error {
		var err error
		tx, err = db.BeginTx(ctx, nil)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("datastore: begin migration transaction: %w", err)
	}
	return tx, nil
}

type migrationQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func schemaVersion(ctx context.Context, db migrationQuerier) (int, error) {
	var exists int
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'
		)
	`).Scan(&exists); err != nil {
		return 0, fmt.Errorf("datastore: inspect migration table: %w", err)
	}
	if exists == 0 {
		return 0, nil
	}

	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return 0, fmt.Errorf("datastore: read migration versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	version := 0
	for rows.Next() {
		var recorded int
		if err := rows.Scan(&recorded); err != nil {
			return 0, fmt.Errorf("datastore: scan migration version: %w", err)
		}
		if recorded != version+1 {
			return 0, fmt.Errorf("datastore: malformed migration history: expected version %d, found %d", version+1, recorded)
		}
		version = recorded
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("datastore: iterate migration versions: %w", err)
	}
	return version, nil
}

func applyInitialSchema(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range initialSchemaStatements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// applyActivityIdentitySchema gives activity rows a durable idempotency key
// plus the minimum identity needed to attach an early hook receipt to its
// conversation after transcript indexing catches up. Existing rows intentionally
// keep these columns NULL: they predate the daemon activity writer and remain
// valid historical records.
func applyActivityIdentitySchema(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE activity_items ADD COLUMN dedupe_key TEXT`,
		`ALTER TABLE activity_items ADD COLUMN harness TEXT`,
		`ALTER TABLE activity_items ADD COLUMN session_id TEXT`,
		`ALTER TABLE activity_items ADD COLUMN window_id INTEGER`,
		`CREATE UNIQUE INDEX activity_items_dedupe_key_idx ON activity_items(dedupe_key) WHERE dedupe_key IS NOT NULL`,
		`CREATE INDEX activity_items_conversation_identity_idx ON activity_items(harness, session_id)`,
		`CREATE INDEX activity_items_window_idx ON activity_items(window_id)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// applyWorkspaceCandidateSchema retains discovery identity and evidence even
// after a candidate becomes a configured workspace.
func applyWorkspaceCandidateSchema(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE workspace_candidates (
			id TEXT PRIMARY KEY,
			identity_key TEXT NOT NULL UNIQUE,
			canonical_path TEXT NOT NULL,
			git_common_dir TEXT,
			display_name TEXT NOT NULL,
			workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
			first_seen_at_ns INTEGER NOT NULL,
			last_seen_at_ns INTEGER NOT NULL
		)`,
		`CREATE INDEX workspace_candidates_workspace_idx ON workspace_candidates(workspace_id, last_seen_at_ns DESC)`,
		`CREATE TABLE workspace_candidate_observations (
			candidate_id TEXT NOT NULL REFERENCES workspace_candidates(id) ON DELETE CASCADE,
			source_type TEXT NOT NULL,
			source_id TEXT NOT NULL,
			observed_path TEXT NOT NULL DEFAULT '',
			hint TEXT NOT NULL DEFAULT '',
			confidence TEXT NOT NULL,
			first_seen_at_ns INTEGER NOT NULL,
			last_seen_at_ns INTEGER NOT NULL,
			PRIMARY KEY (candidate_id, source_type, source_id, observed_path, hint)
		)`,
		`CREATE INDEX workspace_candidate_observations_candidate_idx ON workspace_candidate_observations(candidate_id, source_type)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// applyLegacyImportEntrySchema adds a per-entry compatibility ledger. The
// source-level import_records row remains the cheap manifest gate, while this
// table identifies which entries actually need their search bodies reopened.
func applyLegacyImportEntrySchema(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE legacy_import_entries (
			source_name TEXT NOT NULL REFERENCES import_records(source_name) ON DELETE CASCADE,
			entity_key TEXT NOT NULL,
			metadata_hash TEXT NOT NULL,
			sidecar_size INTEGER NOT NULL,
			sidecar_mtime_ns INTEGER NOT NULL,
			content_hash TEXT NOT NULL,
			last_imported_at_ns INTEGER NOT NULL,
			PRIMARY KEY (source_name, entity_key)
		)`,
		`CREATE INDEX legacy_import_entries_source_idx ON legacy_import_entries(source_name, last_imported_at_ns)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// applyWorkspaceSearchMembershipSchema records every configured launch
// profile and harness for a workspace. Existing databases only know the
// scalar defaults on project search documents, so seed those values here;
// the next explicit workspace synchronization replaces them with the full
// current variant set.
func applyWorkspaceSearchMembershipSchema(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE workspace_profile_memberships (
			workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			profile_name TEXT NOT NULL COLLATE NOCASE,
			position INTEGER NOT NULL DEFAULT 0,
			created_at_ns INTEGER NOT NULL,
			PRIMARY KEY (workspace_id, profile_name)
		)`,
		`CREATE INDEX workspace_profile_memberships_lookup_idx ON workspace_profile_memberships(lower(profile_name), workspace_id)`,
		`CREATE TABLE workspace_harness_memberships (
			workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			harness TEXT NOT NULL COLLATE NOCASE,
			position INTEGER NOT NULL DEFAULT 0,
			created_at_ns INTEGER NOT NULL,
			PRIMARY KEY (workspace_id, harness)
		)`,
		`CREATE INDEX workspace_harness_memberships_lookup_idx ON workspace_harness_memberships(lower(harness), workspace_id)`,
		`INSERT OR IGNORE INTO workspace_profile_memberships (workspace_id, profile_name, position, created_at_ns)
		 SELECT workspace_id, profile_name, 0, updated_at_ns
		 FROM search_documents
		 WHERE entity_type = 'project' AND workspace_id IS NOT NULL
			AND profile_name IS NOT NULL AND trim(profile_name) <> ''`,
		`INSERT OR IGNORE INTO workspace_harness_memberships (workspace_id, harness, position, created_at_ns)
		 SELECT workspace_id, harness, 0, updated_at_ns
		 FROM search_documents
		 WHERE entity_type = 'project' AND workspace_id IS NOT NULL
			AND harness IS NOT NULL AND trim(harness) <> ''`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

var initialSchemaStatements = []string{
	`CREATE TABLE workspaces (
		id TEXT PRIMARY KEY,
		canonical_path TEXT NOT NULL UNIQUE,
		display_name TEXT NOT NULL,
		configured_project_id TEXT,
		created_at_ns INTEGER NOT NULL,
		updated_at_ns INTEGER NOT NULL
	)`,
	`CREATE TABLE workspace_aliases (
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		alias TEXT NOT NULL,
		created_at_ns INTEGER NOT NULL,
		PRIMARY KEY (workspace_id, alias)
	)`,
	`CREATE INDEX workspace_aliases_alias_idx ON workspace_aliases(alias)`,
	`CREATE TABLE worktree_contexts (
		id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		canonical_path TEXT NOT NULL UNIQUE,
		git_common_dir TEXT,
		branch TEXT,
		is_primary INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
		created_at_ns INTEGER NOT NULL,
		updated_at_ns INTEGER NOT NULL
	)`,
	`CREATE INDEX worktree_contexts_workspace_idx ON worktree_contexts(workspace_id, canonical_path)`,
	`CREATE TABLE conversations (
		id INTEGER PRIMARY KEY,
		harness TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
		worktree_context_id TEXT REFERENCES worktree_contexts(id) ON DELETE SET NULL,
		display_profile TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL DEFAULT '',
		source_path TEXT NOT NULL,
		source_size INTEGER NOT NULL DEFAULT 0,
		source_mtime_ns INTEGER NOT NULL DEFAULT 0,
		started_at_ns INTEGER NOT NULL DEFAULT 0,
		last_at_ns INTEGER NOT NULL DEFAULT 0,
		message_count INTEGER NOT NULL DEFAULT 0,
		snippet TEXT NOT NULL DEFAULT '',
		hidden INTEGER NOT NULL DEFAULT 0 CHECK (hidden IN (0, 1)),
		created_at_ns INTEGER NOT NULL,
		updated_at_ns INTEGER NOT NULL,
		UNIQUE (harness, session_id)
	)`,
	`CREATE INDEX conversations_workspace_last_idx ON conversations(workspace_id, last_at_ns DESC)`,
	`CREATE INDEX conversations_source_path_idx ON conversations(source_path)`,
	`CREATE TABLE profile_memberships (
		conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		profile_name TEXT NOT NULL,
		is_display_profile INTEGER NOT NULL DEFAULT 0 CHECK (is_display_profile IN (0, 1)),
		position INTEGER NOT NULL DEFAULT 0,
		created_at_ns INTEGER NOT NULL,
		PRIMARY KEY (conversation_id, profile_name)
	)`,
	`CREATE INDEX profile_memberships_profile_idx ON profile_memberships(profile_name, conversation_id)`,
	`CREATE TABLE plans (
		id TEXT PRIMARY KEY,
		path TEXT NOT NULL UNIQUE,
		display_name TEXT NOT NULL,
		snippet TEXT NOT NULL DEFAULT '',
		mod_time_ns INTEGER NOT NULL DEFAULT 0,
		created_at_ns INTEGER NOT NULL,
		updated_at_ns INTEGER NOT NULL
	)`,
	`CREATE INDEX plans_mod_time_idx ON plans(mod_time_ns DESC, id)`,
	`CREATE TABLE plan_files (
		id INTEGER PRIMARY KEY,
		plan_id TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
		relative_path TEXT NOT NULL,
		path TEXT NOT NULL,
		content_hash TEXT,
		file_size INTEGER NOT NULL DEFAULT 0,
		mod_time_ns INTEGER NOT NULL DEFAULT 0,
		UNIQUE (plan_id, relative_path)
	)`,
	`CREATE TABLE plan_workspace_links (
		plan_id TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		created_at_ns INTEGER NOT NULL,
		PRIMARY KEY (plan_id, workspace_id)
	)`,
	`CREATE INDEX plan_workspace_links_workspace_idx ON plan_workspace_links(workspace_id, plan_id)`,
	`CREATE TABLE search_documents (
		id INTEGER PRIMARY KEY,
		entity_type TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
		harness TEXT,
		profile_name TEXT,
		occurred_at_ns INTEGER NOT NULL DEFAULT 0,
		title TEXT NOT NULL DEFAULT '',
		normalized_content TEXT NOT NULL DEFAULT '',
		updated_at_ns INTEGER NOT NULL,
		UNIQUE (entity_type, entity_id)
	)`,
	`CREATE INDEX search_documents_filter_idx ON search_documents(entity_type, workspace_id, occurred_at_ns DESC)`,
	`CREATE VIRTUAL TABLE search_documents_fts USING fts5(
		title,
		normalized_content,
		content = 'search_documents',
		content_rowid = 'id',
		tokenize = 'unicode61 remove_diacritics 2'
	)`,
	`CREATE TRIGGER search_documents_ai AFTER INSERT ON search_documents BEGIN
		INSERT INTO search_documents_fts(rowid, title, normalized_content)
		VALUES (new.id, new.title, new.normalized_content);
	END`,
	`CREATE TRIGGER search_documents_ad AFTER DELETE ON search_documents BEGIN
		INSERT INTO search_documents_fts(search_documents_fts, rowid, title, normalized_content)
		VALUES ('delete', old.id, old.title, old.normalized_content);
	END`,
	`CREATE TRIGGER search_documents_au AFTER UPDATE ON search_documents BEGIN
		INSERT INTO search_documents_fts(search_documents_fts, rowid, title, normalized_content)
		VALUES ('delete', old.id, old.title, old.normalized_content);
		INSERT INTO search_documents_fts(rowid, title, normalized_content)
		VALUES (new.id, new.title, new.normalized_content);
	END`,
	`CREATE TABLE handoffs (
		id TEXT PRIMARY KEY,
		workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
		conversation_id INTEGER REFERENCES conversations(id) ON DELETE SET NULL,
		plan_id TEXT REFERENCES plans(id) ON DELETE SET NULL,
		path TEXT NOT NULL UNIQUE,
		content_hash TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at_ns INTEGER NOT NULL,
		updated_at_ns INTEGER NOT NULL
	)`,
	`CREATE INDEX handoffs_workspace_created_idx ON handoffs(workspace_id, created_at_ns DESC)`,
	`CREATE TABLE activity_items (
		id INTEGER PRIMARY KEY,
		workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
		conversation_id INTEGER REFERENCES conversations(id) ON DELETE SET NULL,
		handoff_id TEXT REFERENCES handoffs(id) ON DELETE SET NULL,
		kind TEXT NOT NULL,
		summary TEXT NOT NULL,
		metadata_json TEXT NOT NULL DEFAULT '{}',
		occurred_at_ns INTEGER NOT NULL,
		created_at_ns INTEGER NOT NULL
	)`,
	`CREATE INDEX activity_items_workspace_time_idx ON activity_items(workspace_id, occurred_at_ns DESC)`,
	`CREATE TABLE workspace_read_markers (
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		reader_id TEXT NOT NULL,
		read_at_ns INTEGER NOT NULL,
		PRIMARY KEY (workspace_id, reader_id)
	)`,
	`CREATE TABLE live_conversation_mappings (
		window_id INTEGER PRIMARY KEY,
		conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		harness TEXT NOT NULL,
		parent_conversation_id INTEGER REFERENCES conversations(id) ON DELETE SET NULL,
		observed_at_ns INTEGER NOT NULL
	)`,
	`CREATE INDEX live_conversation_mappings_conversation_idx ON live_conversation_mappings(conversation_id)`,
	`CREATE TABLE import_records (
		source_name TEXT PRIMARY KEY,
		source_path TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		completed_at_ns INTEGER NOT NULL,
		record_count INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE managed_worktrees (
		id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		worktree_context_id TEXT REFERENCES worktree_contexts(id) ON DELETE SET NULL,
		path TEXT NOT NULL UNIQUE,
		branch TEXT,
		base_ref TEXT,
		status TEXT NOT NULL,
		created_at_ns INTEGER NOT NULL,
		updated_at_ns INTEGER NOT NULL
	)`,
	`CREATE INDEX managed_worktrees_workspace_idx ON managed_worktrees(workspace_id, status)`,
}
