package transcript

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/datastore"
)

func newTestRepository(t *testing.T) (*Repository, *datastore.Store) {
	t.Helper()
	store := datastore.OpenTest(t)
	repo, err := NewRepository(store)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	return repo, store
}

func TestRepositoryRoundTripFreshnessAndHiddenState(t *testing.T) {
	ctx := context.Background()
	repo, store := newTestRepository(t)
	now := time.Date(2026, 7, 11, 12, 0, 0, 123, time.FixedZone("fixture", -6*60*60))
	repo.now = func() time.Time { return now }
	meta := SessionMeta{
		ID:           "shared-session",
		Harness:      "claude",
		Profile:      "work",
		Profiles:     []string{"personal", "work"},
		Project:      "Agent Mux",
		CWD:          "/work/agentmux",
		StartedAt:    now.Add(-time.Hour),
		LastAt:       now,
		Path:         "/home/.claude/projects/agentmux/shared-session.jsonl",
		FileSize:     4242,
		FileModTime:  now.Add(-time.Minute),
		MessageCount: 7,
		Snippet:      "Preserve this snippet",
	}
	if err := repo.Upsert(ctx, meta, "first line\nsearchable body"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok, err := repo.Get(ctx, meta.Harness, meta.ID)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.ID != meta.ID || got.Harness != meta.Harness || got.DisplayProfile() != meta.Profile ||
		got.Project != meta.Project || got.CWD != meta.CWD || got.Path != meta.Path ||
		got.FileSize != meta.FileSize || got.MessageCount != meta.MessageCount || got.Snippet != meta.Snippet {
		t.Fatalf("metadata mismatch:\n got %#v\nwant %#v", got, meta)
	}
	if !got.StartedAt.Equal(meta.StartedAt) || !got.LastAt.Equal(meta.LastAt) || !got.FileModTime.Equal(meta.FileModTime) {
		t.Fatalf("timestamps mismatch:\n got %#v\nwant %#v", got, meta)
	}
	if !reflect.DeepEqual(got.Profiles, meta.Profiles) {
		t.Fatalf("Profiles = %#v, want %#v", got.Profiles, meta.Profiles)
	}

	var memberships []struct {
		name     string
		display  int
		position int
	}
	rows, err := store.DB().QueryContext(ctx, `
		SELECT profile_name, is_display_profile, position
		FROM profile_memberships ORDER BY position
	`)
	if err != nil {
		t.Fatalf("query memberships: %v", err)
	}
	for rows.Next() {
		var membership struct {
			name     string
			display  int
			position int
		}
		if err := rows.Scan(&membership.name, &membership.display, &membership.position); err != nil {
			t.Fatalf("scan membership: %v", err)
		}
		memberships = append(memberships, membership)
	}
	_ = rows.Close()
	if len(memberships) != 2 || memberships[0].name != "personal" || memberships[0].position != 0 ||
		memberships[1].name != "work" || memberships[1].display != 1 || memberships[1].position != 1 {
		t.Fatalf("memberships = %#v", memberships)
	}

	content, ok, err := repo.SearchText(ctx, meta.Harness, meta.ID)
	if err != nil || !ok || content != "first line\nsearchable body" {
		t.Fatalf("SearchText: content=%q ok=%v err=%v", content, ok, err)
	}
	fresh, ok, err := repo.FreshByPath(ctx, meta.Path, meta.FileSize, meta.FileModTime)
	if err != nil || !ok || fresh.ID != meta.ID {
		t.Fatalf("FreshByPath: meta=%#v ok=%v err=%v", fresh, ok, err)
	}
	if _, ok, err := repo.FreshByPath(ctx, meta.Path, meta.FileSize+1, meta.FileModTime); err != nil || ok {
		t.Fatalf("stale FreshByPath: ok=%v err=%v", ok, err)
	}
	listed, err := repo.List(ctx, ListFilter{Profile: []string{"personal"}})
	if err != nil || len(listed) != 1 {
		t.Fatalf("List shared profile: len=%d err=%v", len(listed), err)
	}

	if err := repo.SetHidden(ctx, meta.Harness, meta.ID, true); err != nil {
		t.Fatalf("SetHidden: %v", err)
	}
	if _, ok, err := repo.SearchText(ctx, meta.Harness, meta.ID); err != nil || ok {
		t.Fatalf("hidden SearchText: ok=%v err=%v", ok, err)
	}
	if listed, err := repo.List(ctx, ListFilter{}); err != nil || len(listed) != 0 {
		t.Fatalf("visible List after hide: len=%d err=%v", len(listed), err)
	}
	listed, err = repo.List(ctx, ListFilter{IncludeHidden: true})
	if err != nil || len(listed) != 1 || !listed[0].HiddenFromIndex {
		t.Fatalf("hidden List: %#v err=%v", listed, err)
	}
}

func TestRepositoryRepairsSearchFromAvailableTranscriptAndKeepsMissingPlaceholder(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepository(t)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, meta := range []SessionMeta{
		{ID: "repairable", Harness: "codex", Path: path, MessageCount: 1},
		{ID: "unavailable", Harness: "codex", Path: filepath.Join(t.TempDir(), "missing.jsonl"), MessageCount: 1},
	} {
		if err := repo.Upsert(ctx, meta, "stale"); err != nil {
			t.Fatal(err)
		}
		if err := repo.DeleteSearchText(ctx, meta.Harness, meta.ID); err != nil {
			t.Fatal(err)
		}
	}
	readers := map[string]Reader{"codex": readerFunc{harness: "codex", read: func(got string) ([]Message, error) {
		if got != path {
			t.Fatalf("ReadMessages path = %q, want %q", got, path)
		}
		return []Message{{Role: "user", Text: "Recovered transcript body"}}, nil
	}}}
	repaired, err := repo.RepairMissingSearchDocuments(ctx, readers)
	if err != nil || repaired != 1 {
		t.Fatalf("RepairMissingSearchDocuments = %d, %v", repaired, err)
	}
	content, found, err := repo.SearchText(ctx, "codex", "repairable")
	if err != nil || !found || content != "recovered transcript body" {
		t.Fatalf("repaired content = %q, %t, %v", content, found, err)
	}
	if _, found, err := repo.SearchText(ctx, "codex", "unavailable"); err != nil || found {
		t.Fatalf("unavailable placeholder search document found=%t err=%v", found, err)
	}
}

func TestRepositoryRetentionAndDelete(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepository(t)
	now := time.Date(2026, 7, 11, 18, 0, 0, 0, time.UTC)
	repo.now = func() time.Time { return now }
	for _, meta := range []SessionMeta{
		{ID: "old", Harness: "codex", LastAt: now.Add(-31 * 24 * time.Hour)},
		{ID: "recent", Harness: "codex", LastAt: now.Add(-time.Hour)},
		{ID: "unknown", Harness: "codex"},
	} {
		if err := repo.Upsert(ctx, meta, "body for "+meta.ID); err != nil {
			t.Fatalf("Upsert(%s): %v", meta.ID, err)
		}
	}
	hidden, err := repo.HideBefore(ctx, now.Add(-30*24*time.Hour))
	if err != nil || hidden != 1 {
		t.Fatalf("HideBefore: hidden=%d err=%v", hidden, err)
	}
	old, ok, err := repo.Get(ctx, "codex", "old")
	if err != nil || !ok || !old.HiddenFromIndex {
		t.Fatalf("old after retention: %#v ok=%v err=%v", old, ok, err)
	}
	if _, ok, err := repo.SearchText(ctx, "codex", "old"); err != nil || ok {
		t.Fatalf("old search document survived retention: ok=%v err=%v", ok, err)
	}
	if err := repo.Delete(ctx, "codex", "recent"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, err := repo.Get(ctx, "codex", "recent"); err != nil || ok {
		t.Fatalf("Get deleted: ok=%v err=%v", ok, err)
	}
	if err := repo.Delete(ctx, "codex", "recent"); err == nil {
		t.Fatal("second Delete unexpectedly succeeded")
	}
}
