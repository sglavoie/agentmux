package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
)

func resetIndexCommandTestState(t *testing.T) {
	t.Helper()
	oldLoadConfig := indexLoadConfig
	oldOpenStore := indexOpenStore
	oldTranscriptPath := indexTranscriptPath
	oldPlanPath := indexPlanPath
	oldPlanRoot := indexPlanRoot
	oldReaders := indexReaders
	oldNewKitty := indexNewKitty
	oldDaemonHealth := indexDaemonHealth
	oldReadFile := indexReadFile
	oldStatusOut := indexStatusCmd.OutOrStdout()
	oldRebuildOut := indexRebuildCmd.OutOrStdout()
	t.Cleanup(func() {
		indexLoadConfig = oldLoadConfig
		indexOpenStore = oldOpenStore
		indexTranscriptPath = oldTranscriptPath
		indexPlanPath = oldPlanPath
		indexPlanRoot = oldPlanRoot
		indexReaders = oldReaders
		indexNewKitty = oldNewKitty
		indexDaemonHealth = oldDaemonHealth
		indexReadFile = oldReadFile
		indexStatusCmd.SetOut(oldStatusOut)
		indexRebuildCmd.SetOut(oldRebuildOut)
		_ = indexStatusCmd.Flags().Set("json", "false")
		_ = indexRebuildCmd.Flags().Set("json", "false")
	})
}

func TestIndexStatusJSONIncludesOperationalAndWriterHealth(t *testing.T) {
	resetIndexCommandTestState(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent-mux.db")
	indexOpenStore = func(context.Context) (*datastore.Store, error) { return datastore.OpenPath(ctx, path) }
	seed, err := datastore.OpenPath(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := transcript.NewRepository(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(ctx, transcript.SessionMeta{ID: "status", Harness: harness.Claude, MessageCount: 1}, "body"); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.DB().ExecContext(ctx, `
		INSERT INTO import_records (source_name, source_path, fingerprint, completed_at_ns, record_count)
		VALUES ('status-snapshot', '/rollback.json', 'fingerprint', 1, 1)
	`); err != nil {
		t.Fatal(err)
	}
	before := transcriptStatusMutationSnapshot(t, seed)
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	indexDaemonHealth = func() daemon.Health {
		return daemon.Health{Alive: true, ActivityWriter: &activity.Metrics{Queued: 4, Persisted: 4}}
	}
	if err := indexStatusCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	indexStatusCmd.SetOut(&out)
	if err := indexStatusCmd.RunE(indexStatusCmd, nil); err != nil {
		t.Fatal(err)
	}
	payloadBytes := append([]byte(nil), out.Bytes()...)
	if err := indexStatusCmd.RunE(indexStatusCmd, nil); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		SchemaVersion int             `json:"schema_version"`
		Data          indexStatusData `json:"data"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != JSONSchemaVersion || !payload.Data.Datastore.Healthy ||
		payload.Data.ActivityWriter == nil || payload.Data.ActivityWriter.Persisted != 4 {
		t.Fatalf("payload = %+v", payload)
	}
	verify, err := datastore.OpenPath(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = verify.Close() }()
	if after := transcriptStatusMutationSnapshot(t, verify); !reflect.DeepEqual(after, before) {
		t.Fatalf("index status mutated imports/entities: before=%v after=%v", before, after)
	}
}

func TestIndexRebuildPreservesDurableStateAndRefreshesDerivedData(t *testing.T) {
	resetIndexCommandTestState(t)
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "agent-mux.db")
	transcriptPath := filepath.Join(root, "transcripts.json")
	planIndexPath := filepath.Join(root, "plans.json")
	planRoot := filepath.Join(root, "agent-plans")
	projectDir := filepath.Join(root, "project")
	homeDir := filepath.Join(root, "profile-home")
	for _, dir := range []string{projectDir, homeDir, planRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	planDir := filepath.Join(planRoot, "delivery-aabb112233cc")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "manifest.md"), []byte("# Delivery\nproject "+projectDir), 0o600); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(homeDir, "session.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: homeDir}},
		Projects: []config.Session{{ID: "api", Title: "API", Directory: projectDir, Profile: "work"}},
	}
	store, err := datastore.OpenPath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	workspaces, err := workspace.New(store).Build(ctx, cfg)
	if err != nil || len(workspaces) != 1 {
		t.Fatalf("Build = %+v, %v", workspaces, err)
	}
	transcriptRepo, err := transcript.NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := transcriptRepo.Upsert(ctx, transcript.SessionMeta{
		ID: "session-1", Harness: harness.Claude, Profile: "work", Profiles: []string{"work"}, Path: sourcePath, CWD: projectDir,
		FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1,
		StartedAt: time.Now().Add(-time.Hour), LastAt: time.Now(),
	}, "stale conversation body"); err != nil {
		t.Fatal(err)
	}
	if err := transcriptRepo.DeleteSearchText(ctx, harness.Claude, "session-1"); err != nil {
		t.Fatal(err)
	}
	planRepo, _ := plans.NewRepository(store)
	planMeta := plans.PlanMeta{Name: "delivery", DirPath: planDir, Files: []string{"manifest.md"}, ModTime: time.Now()}
	if err := planRepo.Upsert(ctx, planMeta, "old derived body"); err != nil {
		t.Fatal(err)
	}
	if err := planRepo.LinkWorkspace(ctx, planDir, workspaces[0].ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixNano()
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO handoffs (id, workspace_id, plan_id, path, content_hash, status, created_at_ns, updated_at_ns)
		VALUES ('handoff-1', ?, ?, ?, 'hash', 'final', ?, ?);
		INSERT INTO activity_items (workspace_id, kind, summary, metadata_json, occurred_at_ns, created_at_ns)
		VALUES (?, 'complete', 'durable activity', '{}', ?, ?);
		INSERT INTO workspace_read_markers (workspace_id, reader_id, read_at_ns) VALUES (?, 'local', ?);
		INSERT INTO worktree_contexts (id, workspace_id, canonical_path, is_primary, created_at_ns, updated_at_ns)
		VALUES ('wt-1', ?, ?, 0, ?, ?);
		INSERT INTO managed_worktrees (id, workspace_id, worktree_context_id, path, branch, base_ref, status, created_at_ns, updated_at_ns)
		VALUES ('managed-1', ?, 'wt-1', ?, 'amx/test', 'HEAD', 'active', ?, ?);
		INSERT INTO search_documents (entity_type, entity_id, workspace_id, title, normalized_content, updated_at_ns)
		VALUES ('handoff', 'handoff-1', ?, 'handoff', 'durable handoff', ?);
		INSERT INTO search_documents (entity_type, entity_id, workspace_id, title, normalized_content, updated_at_ns)
		VALUES ('activity', '1', ?, 'complete', 'durable activity', ?)
	`, workspaces[0].ID, planDir, filepath.Join(root, "handoff.md"), now, now,
		workspaces[0].ID, now, now, workspaces[0].ID, now,
		workspaces[0].ID, filepath.Join(root, "managed"), now, now,
		workspaces[0].ID, filepath.Join(root, "managed"), now, now,
		workspaces[0].ID, now, workspaces[0].ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		DELETE FROM search_documents WHERE entity_type IN ('handoff', 'activity')
	`); err != nil {
		t.Fatal(err)
	}
	var corruptRowID int64
	var corruptTitle, corruptContent string
	if err := store.DB().QueryRowContext(ctx, `
		SELECT id, title, normalized_content FROM search_documents WHERE entity_type = 'project' LIMIT 1
	`).Scan(&corruptRowID, &corruptTitle, &corruptContent); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO search_documents_fts(search_documents_fts, rowid, title, normalized_content)
		VALUES ('delete', ?, ?, ?)
	`, corruptRowID, corruptTitle, corruptContent); err != nil {
		t.Fatal(err)
	}
	protectedBefore, err := store.SnapshotProtectedState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if protectedBefore.Counts["workspaces"] != 1 || protectedBefore.Counts["conversations"] != 1 {
		t.Fatalf("protected source counts = %v", protectedBefore.Counts)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reader := fakeTranscriptReader{harness: harness.Claude, discover: func(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
		return []transcript.SessionMeta{{
			ID: "session-1", Harness: harness.Claude, Path: sourcePath, CWD: projectDir,
			FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1,
			StartedAt: time.Now().Add(-time.Hour), LastAt: time.Now(), SearchText: "fresh conversation body",
		}}, nil
	}}
	indexLoadConfig = func() (*config.Config, error) { return cfg, nil }
	indexOpenStore = func(context.Context) (*datastore.Store, error) { return datastore.OpenPath(ctx, dbPath) }
	indexTranscriptPath = func() string { return transcriptPath }
	indexPlanPath = func() string { return planIndexPath }
	indexPlanRoot = func() string { return planRoot }
	indexReaders = func() map[string]transcript.Reader { return map[string]transcript.Reader{harness.Claude: reader} }
	indexNewKitty = func() kitty.KittyClient { return kitty.NewClientWithSocket(kitty.NewMockRunner(nil), "") }

	result, err := rebuildIndexes(ctx)
	if err != nil {
		t.Fatalf("rebuildIndexes: %v", err)
	}
	if result.Conversations != 1 || result.Plans != 1 || !result.Datastore.Healthy || !result.Datastore.Search.IntegrityVerified {
		t.Fatalf("result = %+v", result)
	}
	if result.RepairedDocuments["activity"] != 1 || result.Unavailable["handoff"] != 1 {
		t.Fatalf("repair classification = repaired %v unavailable %v", result.RepairedDocuments, result.Unavailable)
	}
	if result.ProtectedState.SHA256 != protectedBefore.SHA256 {
		t.Fatalf("protected state changed: before %+v after %+v", protectedBefore, result.ProtectedState)
	}
	verify, err := datastore.OpenPath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = verify.Close() }()
	var content string
	if err := verify.DB().QueryRowContext(ctx, `SELECT normalized_content FROM search_documents WHERE entity_type = 'conversation'`).Scan(&content); err != nil || content != "fresh conversation body" {
		t.Fatalf("conversation search content = %q, %v", content, err)
	}
	var activityDocuments, handoffDocuments int
	if err := verify.DB().QueryRowContext(ctx, `SELECT count(*) FROM search_documents WHERE entity_type = 'activity'`).Scan(&activityDocuments); err != nil {
		t.Fatal(err)
	}
	if err := verify.DB().QueryRowContext(ctx, `SELECT count(*) FROM search_documents WHERE entity_type = 'handoff'`).Scan(&handoffDocuments); err != nil {
		t.Fatal(err)
	}
	if activityDocuments != 1 || handoffDocuments != 0 {
		t.Fatalf("derived repair counts: activity=%d handoff=%d", activityDocuments, handoffDocuments)
	}
}
