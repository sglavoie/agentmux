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
	"time"
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
		"workspace_candidates", "workspace_candidate_observations", "legacy_import_entries",
		"workspace_profile_memberships", "workspace_harness_memberships",
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

func TestConcurrentOpenPathInitializesAndUpgradesIndependentPools(t *testing.T) {
	for _, initialVersion := range []int{0, 1, 2, 3, 4} {
		initialVersion := initialVersion
		t.Run(fmt.Sprintf("from-v%d", initialVersion), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data", DatabaseFileName)
			if initialVersion > 0 {
				if err := os.MkdirAll(filepath.Dir(path), dataDirectoryMode); err != nil {
					t.Fatalf("create data directory: %v", err)
				}
				db, err := sql.Open("sqlite", sqliteDSN(path))
				if err != nil {
					t.Fatalf("open v%d database: %v", initialVersion, err)
				}
				if err := migrate(context.Background(), db, migrations[:initialVersion]); err != nil {
					_ = db.Close()
					t.Fatalf("create v%d database: %v", initialVersion, err)
				}
				if err := db.Close(); err != nil {
					t.Fatalf("close v%d database: %v", initialVersion, err)
				}
			}

			const openers = 16
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			start := make(chan struct{})
			stores := make([]*Store, openers)
			errs := make(chan error, openers)
			var wg sync.WaitGroup
			for i := 0; i < openers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					store, err := OpenPath(ctx, path)
					if err == nil {
						stores[i] = store
					}
					errs <- err
				}(i)
			}
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Errorf("concurrent OpenPath from v%d: %v", initialVersion, err)
				}
			}
			for _, store := range stores {
				if store != nil {
					defer func(store *Store) { _ = store.Close() }(store)
				}
			}
			if t.Failed() {
				t.FailNow()
			}

			seenHandles := make(map[*sql.DB]struct{}, openers)
			for i, store := range stores {
				if store == nil {
					t.Fatalf("store %d is nil", i)
				}
				if _, exists := seenHandles[store.DB()]; exists {
					t.Fatalf("store %d reused another opener's *sql.DB handle", i)
				}
				seenHandles[store.DB()] = struct{}{}
				if got, err := store.SchemaVersion(ctx); err != nil || got != CurrentSchemaVersion {
					t.Fatalf("store %d schema version = %d, %v; want %d, nil", i, got, err, CurrentSchemaVersion)
				}
			}

			var historyCount, maxVersion int
			if err := stores[0].DB().QueryRowContext(ctx,
				`SELECT count(*), max(version) FROM schema_migrations`,
			).Scan(&historyCount, &maxVersion); err != nil {
				t.Fatalf("read migration history: %v", err)
			}
			if historyCount != CurrentSchemaVersion || maxVersion != CurrentSchemaVersion {
				t.Fatalf("migration history = count %d max %d, want %d and %d", historyCount, maxVersion, CurrentSchemaVersion, CurrentSchemaVersion)
			}

			if err := stores[0].Transaction(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `CREATE TABLE concurrent_open_probe (id INTEGER PRIMARY KEY)`)
				return err
			}); err != nil {
				t.Fatalf("create WAL artifact probe: %v", err)
			}
			assertMode(t, filepath.Dir(path), dataDirectoryMode)
			for _, artifact := range []string{path, path + "-wal", path + "-shm"} {
				assertMode(t, artifact, databaseFileMode)
			}
		})
	}
}

func TestWorkspaceSearchMembershipMigrationBackfillsDefaultsAndCascades(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate(ctx, db, migrations[:4]); err != nil {
		t.Fatalf("create v4 schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, canonical_path, display_name, created_at_ns, updated_at_ns)
		VALUES ('ws-a', '/repo/a', 'Alpha', 1, 1);
		INSERT INTO search_documents (
			entity_type, entity_id, workspace_id, harness, profile_name,
			title, normalized_content, updated_at_ns
		) VALUES ('project', 'ws-a', 'ws-a', 'Codex', 'Work', 'Alpha', 'alpha', 42)
	`); err != nil {
		t.Fatalf("seed v4 project: %v", err)
	}
	if err := migrate(ctx, db, migrations); err != nil {
		t.Fatalf("upgrade to v5: %v", err)
	}

	var profile, harnessName string
	if err := db.QueryRowContext(ctx, `SELECT profile_name FROM workspace_profile_memberships WHERE workspace_id = 'ws-a'`).Scan(&profile); err != nil {
		t.Fatalf("read backfilled profile: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT harness FROM workspace_harness_memberships WHERE workspace_id = 'ws-a'`).Scan(&harnessName); err != nil {
		t.Fatalf("read backfilled harness: %v", err)
	}
	if profile != "Work" || harnessName != "Codex" {
		t.Fatalf("backfilled memberships = profile %q harness %q", profile, harnessName)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = 'ws-a'`); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	for _, table := range []string{"workspace_profile_memberships", "workspace_harness_memberships"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d rows after workspace deletion", table, count)
		}
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

func TestInspectClassifiesRepairableAndUnavailableMissingDocumentsReadOnly(t *testing.T) {
	store := OpenTest(t)
	ctx := context.Background()
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "conversation.jsonl")
	planPath := filepath.Join(root, "plan")
	handoffPath := filepath.Join(root, "handoff.md")
	for path, content := range map[string]string{
		transcriptPath:                         "{}\n",
		filepath.Join(planPath, "manifest.md"): "# Plan\n",
		handoffPath:                            "# Handoff\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	missingTranscript := filepath.Join(root, "missing.jsonl")
	missingPlan := filepath.Join(root, "missing-plan")
	missingHandoff := filepath.Join(root, "missing-handoff.md")
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO workspaces (id, canonical_path, display_name, created_at_ns, updated_at_ns)
		VALUES ('ws', '/repo', 'Repo', 1, 1);
		INSERT INTO conversations (harness, session_id, source_path, message_count, created_at_ns, updated_at_ns)
		VALUES ('codex', 'repairable', ?, 1, 1, 1), ('codex', 'unavailable', ?, 1, 1, 1);
		INSERT INTO plans (id, path, display_name, created_at_ns, updated_at_ns)
		VALUES (?, ?, 'Repairable', 1, 1), (?, ?, 'Unavailable', 1, 1);
		INSERT INTO plan_workspace_links (plan_id, workspace_id, created_at_ns) VALUES (?, 'ws', 1);
		INSERT INTO handoffs (id, workspace_id, path, content_hash, status, created_at_ns, updated_at_ns)
		VALUES ('repairable', 'ws', ?, 'hash', 'final', 1, 1),
		       ('unavailable', 'ws', ?, 'hash', 'final', 1, 1);
		INSERT INTO activity_items (workspace_id, kind, summary, metadata_json, occurred_at_ns, created_at_ns)
		VALUES ('ws', 'complete', 'durable activity', '{}', 1, 1)
	`, transcriptPath, missingTranscript, planPath, planPath, missingPlan, missingPlan,
		missingPlan, handoffPath, missingHandoff); err != nil {
		t.Fatal(err)
	}

	status, err := store.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for entityType, want := range map[string]int{"conversation": 1, "plan": 1, "handoff": 1, "activity": 1} {
		if got := status.Search.RepairableMissingDocuments[entityType]; got != want {
			t.Errorf("repairable %s = %d, want %d", entityType, got, want)
		}
	}
	for _, entityType := range []string{"conversation", "plan", "handoff"} {
		if got := status.Search.UnavailablePlaceholders[entityType]; got != 1 {
			t.Errorf("unavailable %s = %d, want 1", entityType, got)
		}
	}
	if status.Healthy || status.Search.Fresh {
		t.Fatalf("repairable gaps reported healthy: %+v", status.Search)
	}

	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO search_documents (entity_type, entity_id, title, normalized_content, updated_at_ns)
		VALUES ('conversation', 'codex:repairable', 'conversation', 'body', 1),
		       ('plan', ?, 'plan', 'body', 1),
		       ('handoff', 'repairable', 'handoff', 'body', 1),
		       ('activity', '1', 'activity', 'body', 1)
	`, planPath); err != nil {
		t.Fatal(err)
	}
	store.DB().SetMaxOpenConns(1)
	if _, err := store.DB().ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
		t.Fatal(err)
	}
	status, err = store.Inspect(ctx)
	if err != nil {
		t.Fatalf("Inspect on query-only connection: %v", err)
	}
	if !status.Healthy || !status.Search.Fresh || status.Search.IntegrityVerified {
		t.Fatalf("unavailable placeholders should warn but remain healthy: %+v", status.Search)
	}
	if len(status.Search.Warnings) != 3 {
		t.Fatalf("warnings = %v, want three unavailable placeholder warnings", status.Search.Warnings)
	}
}

func TestInspectDetectsFTSRelationshipCorruptionAndExplicitRebuildVerifies(t *testing.T) {
	store := OpenTest(t)
	ctx := context.Background()
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO search_documents (entity_type, entity_id, title, normalized_content, updated_at_ns)
		VALUES ('project', 'derived', 'Derived', 'searchable body', 1)
	`); err != nil {
		t.Fatal(err)
	}
	var rowID int64
	var title, content string
	if err := store.DB().QueryRowContext(ctx, `
		SELECT id, title, normalized_content FROM search_documents WHERE entity_id = 'derived'
	`).Scan(&rowID, &title, &content); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO search_documents_fts(search_documents_fts, rowid, title, normalized_content)
		VALUES ('delete', ?, ?, ?)
	`, rowID, title, content); err != nil {
		t.Fatal(err)
	}

	status, err := store.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Healthy || status.Search.Fresh || status.Search.FTSRowCount != 0 || status.Search.InconsistentDocuments["fts_missing_rows"] != 1 {
		t.Fatalf("corrupt FTS status = %+v", status.Search)
	}
	if err := store.VerifySearchIndex(ctx); err == nil {
		t.Fatal("VerifySearchIndex unexpectedly accepted corrupt external-content index")
	}
	if err := store.RebuildSearchIndex(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifySearchIndex(ctx); err != nil {
		t.Fatalf("VerifySearchIndex after rebuild: %v", err)
	}
	status, err = store.Inspect(ctx)
	if err != nil || !status.Healthy || !status.Search.Fresh || status.Search.FTSRowCount != 1 {
		t.Fatalf("rebuilt FTS status = %+v, %v", status.Search, err)
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

func TestFailedMigrationRollsBackAllPendingVersionsAndLeavesPriorSchemaUsable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate(ctx, db, migrations[:1]); err != nil {
		t.Fatalf("create v1 schema: %v", err)
	}

	failing := []migration{
		migrations[0],
		{
			version: 2,
			apply: func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `CREATE TABLE pending_v2_should_not_survive (id INTEGER PRIMARY KEY)`)
				return err
			},
		},
		{
			version: 3,
			apply: func(ctx context.Context, tx *sql.Tx) error {
				if _, err := tx.ExecContext(ctx, `CREATE TABLE pending_v3_should_not_survive (id INTEGER PRIMARY KEY)`); err != nil {
					return err
				}
				return errors.New("injected migration failure")
			},
		},
	}

	err = migrate(ctx, db, failing)
	if err == nil {
		t.Fatal("migrate unexpectedly succeeded")
	}
	if got, err := schemaVersion(ctx, db); err != nil || got != 1 {
		t.Fatalf("schemaVersion after failed migration = %d, %v; want 1, nil", got, err)
	}
	for _, table := range []string{"pending_v2_should_not_survive", "pending_v3_should_not_survive"} {
		var name string
		err = db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE name = ?`, table).Scan(&name)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("failed migration table %s lookup = %v, want sql.ErrNoRows", table, err)
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM workspaces`).Scan(new(int)); err != nil {
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
