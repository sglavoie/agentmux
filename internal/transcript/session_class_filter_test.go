package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestSearchMainOnlyComposesWithExistingFilters(t *testing.T) {
	idx := newTestIndex(t)
	now := time.Now()
	entries := []SessionMeta{
		{ID: "main-match", Harness: harness.Claude, Profile: "work", Project: "api", SessionClass: SessionClassMain, LastAt: now, SearchText: "needle"},
		{ID: "automated", Harness: harness.Claude, Profile: "work", Project: "api", SessionClass: SessionClassAutomated, LastAt: now, SearchText: "needle"},
		{ID: "unknown", Harness: harness.Claude, Profile: "work", Project: "api", SessionClass: SessionClassUnknown, LastAt: now, SearchText: "needle"},
		{ID: "wrong-project", Harness: harness.Claude, Profile: "work", Project: "other", SessionClass: SessionClassMain, LastAt: now, SearchText: "needle"},
		{ID: "hidden", Harness: harness.Claude, Profile: "work", Project: "api", SessionClass: SessionClassMain, HiddenFromIndex: true, LastAt: now, SearchText: "needle"},
	}
	for _, meta := range entries {
		if err := idx.SetEntryForTest(IndexKey(meta.Harness, meta.ID), meta); err != nil {
			t.Fatal(err)
		}
	}

	results, err := Search(idx, nil, Query{
		Text: "needle", Harness: []string{harness.Claude}, Profile: []string{"work"},
		Project: []string{"api"}, MainOnly: true,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Meta.ID != "main-match" {
		t.Fatalf("results = %+v, want only main-match", results)
	}

	all := idx.List(ListFilter{Project: []string{"api"}})
	if len(all) != 3 {
		t.Fatalf("default list retained rows = %d, want main + automated + unknown", len(all))
	}
}

type migrationClassifyingReader struct {
	classifyCalls int
	discoverCalls int
}

func (*migrationClassifyingReader) Harness() string { return harness.Claude }
func (r *migrationClassifyingReader) Discover(_ string, opts DiscoverOptions) ([]SessionMeta, error) {
	r.discoverCalls++
	if opts.MinTime.IsZero() {
		return nil, os.ErrInvalid
	}
	return nil, nil
}
func (*migrationClassifyingReader) ReadMessages(string) ([]Message, error) { return nil, nil }
func (r *migrationClassifyingReader) ClassifySession(string) (SessionClass, error) {
	r.classifyCalls++
	return SessionClassMain, nil
}

func TestRefreshMigratesLegacyClassBeyondCutoffWithoutRebuildingMetadata(t *testing.T) {
	idx := newTestIndex(t)
	path := filepath.Join(t.TempDir(), "old.jsonl")
	if err := os.WriteFile(path, []byte("metadata marker only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	meta := SessionMeta{
		ID: "legacy", Harness: harness.Claude, Path: path,
		FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 41,
		Snippet: "cached snippet", SearchText: "cached search body",
	}
	key := IndexKey(meta.Harness, meta.ID)
	if err := idx.SetEntryForTest(key, meta); err != nil {
		t.Fatal(err)
	}

	reader := &migrationClassifyingReader{}
	cfg := &config.Config{
		Defaults: config.Defaults{SessionArchive: config.SessionArchiveConfig{ScanMaxAgeDays: 7}},
		Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: t.TempDir()}},
	}
	readers := map[string]Reader{harness.Claude: reader}
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got, ok := idx.Get(key)
	if !ok {
		t.Fatal("legacy entry disappeared")
	}
	if got.SessionClass != SessionClassMain || got.MessageCount != 41 || got.Snippet != "cached snippet" {
		t.Fatalf("migrated metadata = %+v", got)
	}
	searchText, err := idx.ReadSearchText(key)
	if err != nil || searchText != "cached search body" {
		t.Fatalf("cached search text = %q, %v", searchText, err)
	}
	if reader.classifyCalls != 1 {
		t.Fatalf("classification calls = %d, want 1", reader.classifyCalls)
	}
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if reader.classifyCalls != 1 {
		t.Fatalf("classification reran after persistence: calls = %d", reader.classifyCalls)
	}
}

func TestMigrationPersistsMissingSourcesAsUnknown(t *testing.T) {
	idx := newTestIndex(t)
	path := filepath.Join(t.TempDir(), "missing.jsonl")
	meta := SessionMeta{
		ID: "missing", Harness: harness.Claude, Path: path,
		MessageCount: 9, Snippet: "keep cached metadata", SearchText: "keep cached search",
	}
	key := IndexKey(meta.Harness, meta.ID)
	if err := idx.SetEntryForTest(key, meta); err != nil {
		t.Fatal(err)
	}
	readers := map[string]Reader{harness.Claude: NewClaudeReader()}
	if err := idx.MigrateSessionClasses(readers); err != nil {
		t.Fatalf("MigrateSessionClasses: %v", err)
	}
	got, ok := idx.Get(key)
	if !ok || got.SessionClass != SessionClassUnknown {
		t.Fatalf("missing-source class = %q, present = %t; want unknown", got.SessionClass, ok)
	}
	if got.MessageCount != 9 || got.Snippet != "keep cached metadata" {
		t.Fatalf("migration rebuilt cached metadata: %+v", got)
	}

	// Unknown is an explicit completed migration result: even if the source
	// appears later, cache loading does not silently reclassify it.
	if err := os.WriteFile(path, []byte(`{"type":"user","entrypoint":"cli"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := idx.MigrateSessionClasses(readers); err != nil {
		t.Fatalf("second MigrateSessionClasses: %v", err)
	}
	got, _ = idx.Get(key)
	if got.SessionClass != SessionClassUnknown {
		t.Fatalf("completed unknown migration was retried: class = %q", got.SessionClass)
	}
}
