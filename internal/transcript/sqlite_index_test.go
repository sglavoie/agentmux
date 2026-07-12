package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/searchstore"
)

func TestEnableSQLiteIndexFallbackRetainsLegacyReadPathAndReason(t *testing.T) {
	idx := newTestIndex(t)
	key := IndexKey(harness.Claude, "fallback")
	if err := idx.SetEntryForTest(key, SessionMeta{
		ID:           "fallback",
		Harness:      harness.Claude,
		MessageCount: 1,
		SearchText:   "legacy needle",
	}); err != nil {
		t.Fatalf("set legacy entry: %v", err)
	}
	idx = EnableSQLiteIndex(context.Background(), idx, nil, nil)
	if idx.UsesSQLite() || idx.DegradedReason() == nil {
		t.Fatalf("fallback state: SQLite=%v reason=%v", idx.UsesSQLite(), idx.DegradedReason())
	}
	results, err := Search(idx, nil, Query{Text: "needle"})
	if err != nil || len(results) != 1 || results[0].Matches != 1 {
		t.Fatalf("legacy fallback search = %#v, %v", results, err)
	}
	status, err := idx.Status()
	if err != nil || !strings.Contains(status.DegradedReason, "SQLite unavailable") {
		t.Fatalf("fallback status = %#v, %v", status, err)
	}
}

func TestLoadSQLiteIndexWrapperOwnsAndClosesStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	idx, err := LoadSQLiteIndex(filepath.Join(t.TempDir(), "transcripts.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	repo := idx.repository()
	if repo == nil {
		t.Fatalf("wrapper facade degraded: %v", idx.DegradedReason())
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := repo.store.DB().PingContext(context.Background()); err == nil {
		t.Fatal("owned wrapper store remained open after Close")
	}
}

func TestEnableSQLiteIndexImportFailureDoesNotCloseBorrowedStore(t *testing.T) {
	idx := newTestIndex(t)
	if err := idx.SetEntryForTest(IndexKey(harness.Claude, "borrowed"), SessionMeta{
		ID: "borrowed", Harness: harness.Claude, MessageCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	store := datastore.OpenTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	idx = EnableSQLiteIndex(ctx, idx, store, nil)
	if idx.UsesSQLite() || idx.DegradedReason() == nil {
		t.Fatalf("import failure did not degrade facade: SQLite=%t reason=%v", idx.UsesSQLite(), idx.DegradedReason())
	}
	if err := store.DB().PingContext(context.Background()); err != nil {
		t.Fatalf("import failure closed borrowed store: %v", err)
	}
}

func TestSQLiteIndexFacadeSearchHideAndDeleteLeaveLegacySidecarUntouched(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "transcripts.json")
	sourcePath := filepath.Join(dir, "home", "session.jsonl")
	writeTranscript(t, sourcePath, "{}\n")
	key := IndexKey(harness.Claude, "sqlite-facade")
	legacy := indexFile{Entries: map[string]SessionMeta{
		key: {
			ID:           "sqlite-facade",
			Harness:      harness.Claude,
			Profile:      "work",
			Profiles:     []string{"work", "personal"},
			Path:         sourcePath,
			MessageCount: 1,
			LastAt:       time.Date(2026, 7, 11, 18, 0, 0, 0, time.UTC),
		},
	}}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	if err := os.WriteFile(indexPath, raw, 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	legacyStore := searchstore.New(indexPath)
	if err := legacyStore.Write(key, "needle once\nneedle twice"); err != nil {
		t.Fatalf("write legacy sidecar: %v", err)
	}
	sidecarBefore, err := os.ReadFile(legacyStore.Path(key))
	if err != nil {
		t.Fatalf("read legacy sidecar: %v", err)
	}

	idx, err := LoadIndexAt(indexPath)
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	idx = EnableSQLiteIndex(ctx, idx, datastore.OpenTest(t), nil)
	if !idx.UsesSQLite() || idx.DegradedReason() != nil {
		t.Fatalf("SQLite facade not enabled: enabled=%v reason=%v", idx.UsesSQLite(), idx.DegradedReason())
	}

	results, err := Search(idx, nil, Query{Text: "needle"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Matches != 2 || results[0].Preview != "needle once" {
		t.Fatalf("Search results = %#v, want one exact SQLite match result", results)
	}
	if err := idx.SetHidden(key, true); err != nil {
		t.Fatalf("SetHidden: %v", err)
	}
	if results, err := Search(idx, nil, Query{}); err != nil || len(results) != 0 {
		t.Fatalf("hidden SQLite search = %#v, %v", results, err)
	}
	if err := idx.SetHidden(key, false); err != nil {
		t.Fatalf("unhide: %v", err)
	}
	// Unhide deliberately leaves no search document; supply one through the
	// repository facade before exercising physical source deletion.
	if err := idx.WriteSearchText(key, "needle restored"); err != nil {
		t.Fatalf("WriteSearchText SQLite: %v", err)
	}
	if err := idx.Delete(key, true); err != nil {
		t.Fatalf("Delete with source file: %v", err)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("source transcript survived Delete: %v", err)
	}
	if _, ok := idx.Get(key); ok {
		t.Fatal("deleted conversation remained in SQLite facade")
	}
	sidecarAfter, err := os.ReadFile(legacyStore.Path(key))
	if err != nil {
		t.Fatalf("read legacy sidecar after SQLite mutations: %v", err)
	}
	if string(sidecarAfter) != string(sidecarBefore) {
		t.Fatalf("SQLite facade changed legacy sidecar:\n got %q\nwant %q", sidecarAfter, sidecarBefore)
	}
}

func TestRebuildSQLiteRebuildsDocumentsWithoutDeletingLegacySidecars(t *testing.T) {
	tmp := t.TempDir()
	idx, err := LoadIndexAt(filepath.Join(tmp, "transcripts.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	staleKey := IndexKey(harness.Copilot, "legacy-sidecar")
	if err := idx.WriteSearchText(staleKey, "legacy sidecar remains"); err != nil {
		t.Fatalf("write legacy sidecar: %v", err)
	}
	idx = EnableSQLiteIndex(context.Background(), idx, datastore.OpenTest(t), nil)
	if !idx.UsesSQLite() {
		t.Fatalf("SQLite facade disabled: %v", idx.DegradedReason())
	}

	sourcePath := filepath.Join(tmp, "home", "session.jsonl")
	info := writeTranscript(t, sourcePath, "{}\n")
	home := filepath.Join(tmp, "home")
	reader := &fakeReader{
		harness: harness.Claude,
		discover: func(gotHome string, _ DiscoverOptions) ([]SessionMeta, error) {
			if gotHome != home {
				t.Fatalf("Discover home = %q, want %q", gotHome, home)
			}
			return []SessionMeta{{
				ID:           "rebuilt",
				Harness:      harness.Claude,
				Path:         sourcePath,
				FileSize:     info.Size(),
				FileModTime:  info.ModTime(),
				LastAt:       time.Now(),
				MessageCount: 1,
				SearchText:   "rebuilt needle",
			}}, nil
		},
	}
	result, err := RebuildSQLite(idx, &config.Config{Profiles: []config.Account{{
		Name: "work", Harness: harness.Claude, HomeDir: home,
	}}}, map[string]Reader{harness.Claude: reader}, true)
	if err != nil {
		t.Fatalf("RebuildSQLite: %v", err)
	}
	if result.Status.Backend != "sqlite" || result.Status.SearchDocumentCount != 1 || result.Status.MissingSearchDocuments != 0 {
		t.Fatalf("SQLite rebuild status = %#v", result.Status)
	}
	if got, err := idx.ReadSearchText(IndexKey(harness.Claude, "rebuilt")); err != nil || got != "rebuilt needle" {
		t.Fatalf("SQLite rebuilt document = %q, %v", got, err)
	}
	if got, err := os.ReadFile(idx.SearchTextPath(staleKey)); err != nil || string(got) != "legacy sidecar remains" {
		t.Fatalf("legacy sidecar changed by SQLite rebuild: %q, %v", got, err)
	}
}

func TestRebuildSQLiteSourceFailurePreservesExistingDocuments(t *testing.T) {
	idx, err := LoadIndexAt(filepath.Join(t.TempDir(), "transcripts.json"))
	if err != nil {
		t.Fatal(err)
	}
	idx = EnableSQLiteIndex(context.Background(), idx, datastore.OpenTest(t), nil)
	repo := idx.repository()
	meta := SessionMeta{ID: "existing", Harness: harness.Claude, Path: "/missing/source.jsonl", MessageCount: 1}
	if err := repo.Upsert(context.Background(), meta, "last known good search text"); err != nil {
		t.Fatal(err)
	}
	reader := &fakeReader{harness: harness.Claude, discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
		return nil, errors.New("injected source failure")
	}}
	_, err = RebuildSQLite(idx, &config.Config{Profiles: []config.Account{{
		Name: "work", Harness: harness.Claude, HomeDir: "/unavailable",
	}}}, map[string]Reader{harness.Claude: reader}, true)
	if err == nil {
		t.Fatal("RebuildSQLite unexpectedly succeeded")
	}
	got, err := idx.ReadSearchText(IndexKey(harness.Claude, "existing"))
	if err != nil || got != "last known good search text" {
		t.Fatalf("existing search document after failure = %q, %v", got, err)
	}
}
