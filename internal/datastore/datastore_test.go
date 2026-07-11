package datastore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDefaultPathUsesAgentMuxDataDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(home, ".local", "share", ApplicationName, DatabaseFileName)
	if path != want {
		t.Fatalf("DefaultPath = %q, want %q", path, want)
	}
}

func TestOpenPathInitializesPrivateWALDatabaseAndTargetSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", DatabaseFileName)
	store, err := OpenPath(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if got, err := store.SchemaVersion(context.Background()); err != nil || got != CurrentSchemaVersion {
		t.Fatalf("SchemaVersion = %d, %v; want %d, nil", got, err, CurrentSchemaVersion)
	}
	assertMode(t, filepath.Dir(path), dataDirectoryMode)
	assertMode(t, path, databaseFileMode)

	var journalMode string
	if err := store.DB().QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}
	var foreignKeys int
	if err := store.DB().QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}

	for _, table := range []string{
		"schema_migrations", "workspaces", "workspace_aliases", "worktree_contexts",
		"conversations", "profile_memberships", "plans", "plan_files", "plan_workspace_links",
		"search_documents", "search_documents_fts", "handoffs", "activity_items",
		"workspace_read_markers", "live_conversation_mappings", "import_records", "managed_worktrees",
		"workspace_candidates", "workspace_candidate_observations",
	} {
		var found string
		err := store.DB().QueryRow(`SELECT name FROM sqlite_master WHERE name = ?`, table).Scan(&found)
		if err != nil {
			t.Errorf("target schema missing %s: %v", table, err)
		}
	}
}

func TestActivityIdentityMigrationPreservesVersionOneRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(context.Background(), db, migrations[:1]); err != nil {
		_ = db.Close()
		t.Fatalf("create v1 schema: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO activity_items (kind, summary, occurred_at_ns, created_at_ns)
		VALUES ('legacy', 'legacy activity', 1, 1)
	`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenPath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var dedupeKey, harnessName, sessionID sql.NullString
	var windowID sql.NullInt64
	if err := store.DB().QueryRow(`
		SELECT dedupe_key, harness, session_id, window_id
		FROM activity_items WHERE kind = 'legacy'
	`).Scan(&dedupeKey, &harnessName, &sessionID, &windowID); err != nil {
		t.Fatal(err)
	}
	if dedupeKey.Valid || harnessName.Valid || sessionID.Valid || windowID.Valid {
		t.Fatalf("legacy row gained identity data: dedupe=%+v harness=%+v session=%+v window=%+v", dedupeKey, harnessName, sessionID, windowID)
	}
}

func TestSearchDocumentsExternalContentFTSStaysInSync(t *testing.T) {
	store := OpenTest(t)
	ctx := context.Background()

	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO search_documents (entity_type, entity_id, title, normalized_content, updated_at_ns)
		VALUES ('conversation', 'codex:abc', 'Initial title', 'durable sqlite foundation', 1)
	`); err != nil {
		t.Fatalf("insert search document: %v", err)
	}
	assertFTSCount(t, store.DB(), "sqlite", 1)

	if _, err := store.DB().ExecContext(ctx, `
		UPDATE search_documents
		SET normalized_content = 'worktree identity', updated_at_ns = 2
		WHERE entity_type = 'conversation' AND entity_id = 'codex:abc'
	`); err != nil {
		t.Fatalf("update search document: %v", err)
	}
	assertFTSCount(t, store.DB(), "sqlite", 0)
	assertFTSCount(t, store.DB(), "worktree", 1)

	if _, err := store.DB().ExecContext(ctx, `DELETE FROM search_documents WHERE entity_id = 'codex:abc'`); err != nil {
		t.Fatalf("delete search document: %v", err)
	}
	assertFTSCount(t, store.DB(), "worktree", 0)
}

func TestInspectReportsMigrationIntegrityFTSImportsAndPermissions(t *testing.T) {
	store := OpenTest(t)
	ctx := context.Background()
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO import_records (source_name, source_path, fingerprint, completed_at_ns, record_count)
		VALUES ('legacy-test', '/tmp/source.json', 'abc123', 42, 3);
		INSERT INTO search_documents (entity_type, entity_id, title, normalized_content, updated_at_ns)
		VALUES ('conversation', 'codex:abc', 'Title', 'searchable text', 1)
	`); err != nil {
		t.Fatal(err)
	}

	status, err := store.Inspect(ctx)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !status.Healthy || !status.MigrationCurrent || status.Integrity != "ok" || status.ForeignKeyViolations != 0 {
		t.Fatalf("status health = %+v", status)
	}
	if !status.SecurePermissions || len(status.PermissionIssues) != 0 {
		t.Fatalf("permission status = secure %t issues %v", status.SecurePermissions, status.PermissionIssues)
	}
	if !status.Search.Fresh || status.Search.DocumentCount != 1 || status.Search.FTSRowCount != 1 || status.Search.Integrity != "ok" {
		t.Fatalf("search status = %+v", status.Search)
	}
	if len(status.Imports) != 1 || status.Imports[0].SourceName != "legacy-test" || status.Imports[0].RecordCount != 3 {
		t.Fatalf("imports = %+v", status.Imports)
	}
}

func TestSnapshotProtectedStateChangesOnlyForProtectedData(t *testing.T) {
	store := OpenTest(t)
	ctx := context.Background()
	before, err := store.SnapshotProtectedState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO search_documents (entity_type, entity_id, title, normalized_content, updated_at_ns)
		VALUES ('project', 'derived', 'Derived', 'derived', 1)
	`); err != nil {
		t.Fatal(err)
	}
	derived, err := store.SnapshotProtectedState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if derived.SHA256 != before.SHA256 {
		t.Fatalf("derived search row changed protected digest: before %s after %s", before.SHA256, derived.SHA256)
	}

	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO activity_items (kind, summary, metadata_json, occurred_at_ns, created_at_ns)
		VALUES ('complete', 'durable outcome', '{}', 2, 2)
	`); err != nil {
		t.Fatal(err)
	}
	after, err := store.SnapshotProtectedState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.SHA256 == before.SHA256 || after.Counts["activity_items"] != 1 {
		t.Fatalf("protected activity was not reflected: before %+v after %+v", before, after)
	}
}

func TestMigrationsRejectFutureVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	store, err := OpenPath(context.Background(), path)
	if err != nil {
		t.Fatalf("initial OpenPath: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at_ns) VALUES (?, 1)`, CurrentSchemaVersion+1); err != nil {
		_ = db.Close()
		t.Fatalf("insert future migration: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}

	store, err = OpenPath(context.Background(), path)
	if store != nil {
		_ = store.Close()
		t.Fatal("OpenPath unexpectedly returned a store for future schema")
	}
	if !errors.Is(err, ErrFutureSchemaVersion) {
		t.Fatalf("OpenPath future schema error = %v, want ErrFutureSchemaVersion", err)
	}
}

func TestFailedMigrationRollsBackAndLeavesPriorSchemaUsable(t *testing.T) {
	store := OpenTest(t)
	ctx := context.Background()
	failing := append([]migration{}, migrations...)
	failing = append(failing, migration{
		version: CurrentSchemaVersion + 1,
		apply: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `CREATE TABLE should_not_survive (id INTEGER PRIMARY KEY)`); err != nil {
				return err
			}
			return errors.New("injected migration failure")
		},
	})

	err := migrate(ctx, store.DB(), failing)
	if err == nil {
		t.Fatal("migrate unexpectedly succeeded")
	}
	if got, err := store.SchemaVersion(ctx); err != nil || got != CurrentSchemaVersion {
		t.Fatalf("SchemaVersion after failed migration = %d, %v; want %d, nil", got, err, CurrentSchemaVersion)
	}
	var name string
	err = store.DB().QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE name = 'should_not_survive'`).Scan(&name)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed migration table lookup = %v, want sql.ErrNoRows", err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM workspaces`).Scan(new(int)); err != nil {
		t.Fatalf("prior schema is not usable: %v", err)
	}
}

func TestTransactionSerializesConcurrentWriters(t *testing.T) {
	store := OpenTest(t)
	ctx := context.Background()
	if _, err := store.DB().ExecContext(ctx, `CREATE TABLE test_writes (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create test table: %v", err)
	}

	const writers = 12
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- store.Transaction(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO test_writes (value) VALUES (?)`, fmt.Sprintf("writer-%d", i))
				return err
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent transaction: %v", err)
		}
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM test_writes`).Scan(&count); err != nil {
		t.Fatalf("count writes: %v", err)
	}
	if count != writers {
		t.Errorf("wrote %d rows, want %d", count, writers)
	}
	for _, artifact := range []string{store.Path(), store.Path() + "-wal", store.Path() + "-shm"} {
		assertMode(t, artifact, databaseFileMode)
	}
}

func TestEnforcePermissionsRefusesArtifactSymlinkWithoutChangingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("private target"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatalf("chmod target: %v", err)
	}
	artifact := filepath.Join(dir, DatabaseFileName)
	if err := os.Symlink(target, artifact); err != nil {
		t.Fatalf("create artifact symlink: %v", err)
	}

	store := &Store{path: artifact}
	err := store.enforcePermissions()
	if err == nil {
		t.Fatal("enforcePermissions unexpectedly accepted an artifact symlink")
	}
	if !strings.Contains(err.Error(), "refusing symbolic-link database artifact") {
		t.Fatalf("enforcePermissions error = %v, want symbolic-link refusal", err)
	}
	assertMode(t, target, 0o644)
}

func assertFTSCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT count(*) FROM search_documents_fts WHERE search_documents_fts MATCH ?`, query).Scan(&got); err != nil {
		t.Fatalf("FTS query %q: %v", query, err)
	}
	if got != want {
		t.Errorf("FTS query %q returned %d rows, want %d", query, got, want)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("permissions for %s = %04o, want %04o", path, got, want)
	}
}
