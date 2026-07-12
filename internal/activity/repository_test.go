package activity

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/datastore"
)

func TestRepositoryInsertBatchIsIdempotentAndIndexesActivity(t *testing.T) {
	store := datastore.OpenTest(t)
	repo, err := NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_720_000_000, 0).UTC()
	repo.now = func() time.Time { return now }
	insertWorkspace(t, store, "ws-api")
	conversationID := insertConversation(t, store, "claude", "session-1", "ws-api")

	item := Item{
		DedupeKey:    "request:logical-1",
		Harness:      "claude",
		SessionID:    "session-1",
		WindowID:     42,
		Kind:         "prompt",
		Summary:      "Prompt submitted",
		MetadataJSON: `{"event":"UserPromptSubmit"}`,
		OccurredAt:   now,
	}
	if inserted, err := repo.InsertBatch(context.Background(), []Item{item}); err != nil || inserted != 1 {
		t.Fatalf("first insert = (%d, %v), want (1, nil)", inserted, err)
	}
	if inserted, err := repo.InsertBatch(context.Background(), []Item{item}); err != nil || inserted != 0 {
		t.Fatalf("duplicate insert = (%d, %v), want (0, nil)", inserted, err)
	}

	var count, gotConversation int
	var workspaceID, kind, content string
	if err := store.DB().QueryRowContext(context.Background(), `
		SELECT count(*), COALESCE(max(conversation_id), 0), COALESCE(max(workspace_id), ''),
			COALESCE(max(kind), '') FROM activity_items
	`).Scan(&count, &gotConversation, &workspaceID, &kind); err != nil {
		t.Fatal(err)
	}
	if count != 1 || int64(gotConversation) != conversationID || workspaceID != "ws-api" || kind != "prompt" {
		t.Fatalf("activity row = count=%d conversation=%d workspace=%q kind=%q", count, gotConversation, workspaceID, kind)
	}
	if err := store.DB().QueryRowContext(context.Background(), `
		SELECT normalized_content FROM search_documents WHERE entity_type = 'activity' AND entity_id = '1'
	`).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content == "" {
		t.Fatal("activity search document is empty")
	}
}

func TestRepositoryRepairsSearchDocumentFromDurableActivity(t *testing.T) {
	ctx := context.Background()
	store := datastore.OpenTest(t)
	repo, err := NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.InsertBatch(ctx, []Item{{Kind: "complete", Summary: "durable outcome", MetadataJSON: `{"ok":true}`}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = 'activity'`); err != nil {
		t.Fatal(err)
	}
	repaired, err := repo.RepairMissingSearchDocuments(ctx)
	if err != nil || repaired != 1 {
		t.Fatalf("RepairMissingSearchDocuments = %d, %v", repaired, err)
	}
	var content string
	if err := store.DB().QueryRowContext(ctx, `SELECT normalized_content FROM search_documents WHERE entity_type = 'activity'`).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != "durable outcome\n{\"ok\":true}" {
		t.Fatalf("repaired activity content = %q", content)
	}
}

func TestWorkspaceFeedReadHighWaterPersistsAndLoadsOldestUnreadBatch(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), datastore.DatabaseFileName)
	store, err := datastore.OpenPath(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	insertWorkspace(t, store, "ws-feed")
	base := time.Unix(1_720_100_000, 0).UTC()
	items := []Item{
		{DedupeKey: "one", WorkspaceID: "ws-feed", Kind: "prompt", Summary: "Prompt submitted", OccurredAt: base.Add(time.Minute)},
		{DedupeKey: "two", WorkspaceID: "ws-feed", Kind: "completion", Summary: "Session completed", OccurredAt: base.Add(2 * time.Minute)},
		{DedupeKey: "three", WorkspaceID: "ws-feed", Kind: "attention_input", Summary: "Input requested", OccurredAt: base.Add(3 * time.Minute)},
	}
	if inserted, err := repo.InsertBatch(ctx, items); err != nil || inserted != 3 {
		t.Fatalf("InsertBatch = (%d, %v)", inserted, err)
	}
	feeds, err := repo.ListForWorkspaces(ctx, []string{"ws-feed"}, DefaultReaderID, 2)
	if err != nil {
		t.Fatal(err)
	}
	feed := feeds["ws-feed"]
	if feed.UnreadCount != 3 || len(feed.Items) != 2 || feed.Items[0].Summary != "Session completed" || feed.Items[1].Summary != "Prompt submitted" {
		t.Fatalf("initial feed = %+v, want oldest two unread displayed newest-first", feed)
	}
	if !feed.LoadedThrough.Equal(base.Add(2 * time.Minute)) {
		t.Fatalf("loaded through = %v", feed.LoadedThrough)
	}
	if changed, err := repo.MarkWorkspaceRead(ctx, "ws-feed", DefaultReaderID, feed.LoadedThrough); err != nil || !changed {
		t.Fatalf("MarkWorkspaceRead = (%v, %v)", changed, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = datastore.OpenPath(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err = NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	feeds, err = repo.ListForWorkspaces(ctx, []string{"ws-feed"}, DefaultReaderID, 2)
	if err != nil {
		t.Fatal(err)
	}
	feed = feeds["ws-feed"]
	if feed.UnreadCount != 1 || len(feed.Items) != 1 || feed.Items[0].Summary != "Input requested" || !feed.ReadThrough.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("reopened feed = %+v, want one remaining unread", feed)
	}
	if changed, err := repo.MarkWorkspaceRead(ctx, "ws-feed", DefaultReaderID, base.Add(time.Minute)); err != nil || changed {
		t.Fatalf("stale MarkWorkspaceRead = (%v, %v), want unchanged", changed, err)
	}
	if changed, err := repo.MarkAllWorkspacesRead(ctx, DefaultReaderID); err != nil || changed != 1 {
		t.Fatalf("MarkAllWorkspacesRead = (%d, %v)", changed, err)
	}
	feeds, err = repo.ListForWorkspaces(ctx, []string{"ws-feed"}, DefaultReaderID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := feeds["ws-feed"]; got.UnreadCount != 0 || len(got.Items) != 2 || got.Items[0].Summary != "Input requested" {
		t.Fatalf("read feed = %+v, want latest context", got)
	}
}

func TestRepositoryBackfillsConversationAndWorkspaceFromIdentityAndLiveMapping(t *testing.T) {
	store := datastore.OpenTest(t)
	repo, err := NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_720_000_100, 0).UTC()
	repo.now = func() time.Time { return now }
	ctx := context.Background()
	items := []Item{
		{DedupeKey: "receipt:one", Harness: "claude", SessionID: "late", WindowID: 7, Kind: "session_start", Summary: "Session started", OccurredAt: now},
		{DedupeKey: "receipt:two", Harness: "codex", WindowID: 8, Kind: "completion", Summary: "Session completed", OccurredAt: now},
	}
	if inserted, err := repo.InsertBatch(ctx, items); err != nil || inserted != len(items) {
		t.Fatalf("insert before mappings = (%d, %v)", inserted, err)
	}
	insertWorkspace(t, store, "ws-late")
	lateID := insertConversation(t, store, "claude", "late", "ws-late")
	insertWorkspace(t, store, "ws-live")
	liveID := insertConversation(t, store, "codex", "live", "ws-live")
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO live_conversation_mappings (window_id, conversation_id, harness, observed_at_ns)
		VALUES (8, ?, 'codex', ?)
	`, liveID, now.UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Backfill(ctx); err != nil {
		t.Fatal(err)
	}

	rows, err := store.DB().QueryContext(ctx, `
		SELECT dedupe_key, COALESCE(conversation_id, 0), COALESCE(workspace_id, '')
		FROM activity_items ORDER BY dedupe_key
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]struct {
		conversation int64
		workspace    string
	}{}
	for rows.Next() {
		var key, workspaceID string
		var conversationID int64
		if err := rows.Scan(&key, &conversationID, &workspaceID); err != nil {
			t.Fatal(err)
		}
		got[key] = struct {
			conversation int64
			workspace    string
		}{conversationID, workspaceID}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got["receipt:one"].conversation != lateID || got["receipt:one"].workspace != "ws-late" {
		t.Fatalf("identity backfill = %+v", got["receipt:one"])
	}
	if got["receipt:two"].conversation != liveID || got["receipt:two"].workspace != "ws-live" {
		t.Fatalf("live-mapping backfill = %+v", got["receipt:two"])
	}
}

func TestRepositorySweepOnlyRemovesExpiredActivity(t *testing.T) {
	store := datastore.OpenTest(t)
	repo, err := NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_720_000_200, 0).UTC()
	repo.now = func() time.Time { return now }
	ctx := context.Background()
	conversationID := insertConversation(t, store, "claude", "retained", "")
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO handoffs (id, conversation_id, path, content_hash, status, created_at_ns, updated_at_ns)
		VALUES ('handoff-retained', ?, '/tmp/handoff.md', 'hash', 'final', ?, ?)
	`, conversationID, now.UnixNano(), now.UnixNano()); err != nil {
		t.Fatal(err)
	}
	items := []Item{
		{DedupeKey: "old", Kind: "completion", Summary: "Old completion", OccurredAt: now.Add(-48 * time.Hour)},
		{DedupeKey: "new", Kind: "prompt", Summary: "New prompt", OccurredAt: now.Add(-time.Hour)},
	}
	if _, err := repo.InsertBatch(ctx, items); err != nil {
		t.Fatal(err)
	}
	removed, err := repo.Sweep(ctx, 24*time.Hour)
	if err != nil || removed != 1 {
		t.Fatalf("Sweep = (%d, %v), want (1, nil)", removed, err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM activity_items`,
		`SELECT count(*) FROM search_documents WHERE entity_type = 'activity'`,
		`SELECT count(*) FROM conversations`,
		`SELECT count(*) FROM handoffs`,
	} {
		var count int
		if err := store.DB().QueryRowContext(ctx, query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s = %d, want 1", query, count)
		}
	}
}

func TestWriterSaturatesWithoutBlockingAndReportsDegradedHealth(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	writer := NewWriter(WriterOptions{
		Keep: time.Hour,
		OpenStore: func(context.Context) (*datastore.Store, error) {
			enteredOnce.Do(func() { close(entered) })
			<-release
			return nil, errors.New("sqlite unavailable")
		},
	})
	<-entered // initial retention sweep is now stuck off the caller goroutine.
	for i := 0; i < QueueCapacity+64; i++ {
		writer.Enqueue(Item{DedupeKey: string(rune(i + 1)), Kind: "prompt", Summary: "Prompt submitted"})
	}
	if dropped := writer.Metrics().Dropped; dropped == 0 {
		t.Fatal("full activity queue did not report dropped records")
	}
	close(release)
	writer.Close()
	metrics := writer.Metrics()
	if !metrics.Degraded || metrics.WriteFailures == 0 {
		t.Fatalf("writer metrics = %+v, want degraded failure", metrics)
	}
}

func insertWorkspace(t *testing.T, store *datastore.Store, id string) {
	t.Helper()
	now := time.Now().UnixNano()
	if _, err := store.DB().ExecContext(context.Background(), `
		INSERT INTO workspaces (id, canonical_path, display_name, created_at_ns, updated_at_ns)
		VALUES (?, ?, ?, ?, ?)
	`, id, "/tmp/"+id, id, now, now); err != nil {
		t.Fatal(err)
	}
}

func insertConversation(t *testing.T, store *datastore.Store, harnessName, sessionID, workspaceID string) int64 {
	t.Helper()
	now := time.Now().UnixNano()
	result, err := store.DB().ExecContext(context.Background(), `
		INSERT INTO conversations (
			harness, session_id, workspace_id, source_path, created_at_ns, updated_at_ns
		) VALUES (?, ?, ?, ?, ?, ?)
	`, harnessName, sessionID, nullableString(workspaceID), "/tmp/"+harnessName+"-"+sessionID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRepositoryRejectsInvalidMetadata(t *testing.T) {
	store := datastore.OpenTest(t)
	repo, err := NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.InsertBatch(context.Background(), []Item{{Kind: "prompt", Summary: "Prompt", MetadataJSON: "{"}})
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("invalid metadata error = %v", err)
	}
}
