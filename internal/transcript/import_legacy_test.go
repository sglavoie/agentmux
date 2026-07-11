package transcript

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/searchstore"
)

func TestImportLegacyIndexEquivalentAndIdempotent(t *testing.T) {
	ctx := context.Background()
	repo, store := newTestRepository(t)
	now := time.Date(2026, 7, 11, 18, 30, 0, 987654321, time.FixedZone("fixture", -6*60*60))
	repo.now = func() time.Time { return now }
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "transcripts.json")
	entries := map[string]SessionMeta{
		"claude:shared": {
			ID: "shared", Harness: "claude", Profile: "work",
			Profiles: []string{"personal", "work"}, Project: "Agent Mux", CWD: "/work/agentmux",
			StartedAt: now.Add(-2 * time.Hour), LastAt: now.Add(-time.Hour),
			Path: "/legacy/shared.jsonl", FileSize: 101, FileModTime: now.Add(-time.Minute),
			MessageCount: 3, Snippet: "shared snippet",
		},
		"codex:missing-sidecar": {
			ID: "missing-sidecar", Harness: "codex", Profile: "personal",
			Profiles: []string{"personal"}, Project: "Other", CWD: "/work/other",
			StartedAt: now.Add(-4 * time.Hour), LastAt: now.Add(-3 * time.Hour),
			Path: "/legacy/missing.jsonl", FileSize: 202, FileModTime: now.Add(-2 * time.Minute),
			MessageCount: 2, Snippet: "rebuilt snippet",
		},
		"copilot:hidden": {
			ID: "hidden", Harness: "copilot", Profile: "work", Profiles: []string{"work"},
			Path: "/legacy/hidden/events.jsonl", FileSize: 303, FileModTime: now,
			MessageCount: 5, Snippet: "hidden snippet", HiddenFromIndex: true,
		},
	}
	raw, err := json.MarshalIndent(indexFile{Entries: entries}, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(indexPath, raw, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	legacyStore := searchstore.New(indexPath)
	if err := legacyStore.Write("claude:shared", "legacy sidecar body"); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	sidecarPath := legacyStore.Path("claude:shared")
	sidecarBefore, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar before import: %v", err)
	}
	readerCalls := 0
	reader := &fakeReader{
		harness: "codex",
		readResult: []Message{
			{Role: "user", Text: "Needle One"},
			{Role: "assistant", Text: "Second NEEDLE"},
		},
	}
	// Wrap the simple fake so the test can prove the missing sidecar path was
	// rebuilt without requiring a real harness transcript fixture.
	readers := map[string]Reader{"codex": readerFunc{
		harness: "codex",
		read: func(path string) ([]Message, error) {
			readerCalls++
			return reader.ReadMessages(path)
		},
	}}

	result, err := repo.ImportLegacyIndex(ctx, indexPath, readers)
	if err != nil {
		t.Fatalf("ImportLegacyIndex: %v", err)
	}
	if !result.SourceFound || !result.Imported || result.RecordCount != 3 || result.RebuiltSearchDocs != 1 || len(result.Fingerprint) != 64 {
		t.Fatalf("result = %#v", result)
	}
	if readerCalls != 1 {
		t.Fatalf("readerCalls = %d, want 1", readerCalls)
	}
	listed, err := repo.List(ctx, ListFilter{IncludeHidden: true})
	if err != nil || len(listed) != 3 {
		t.Fatalf("List: len=%d err=%v", len(listed), err)
	}
	shared, ok, err := repo.Get(ctx, "claude", "shared")
	if err != nil || !ok {
		t.Fatalf("Get shared: ok=%v err=%v", ok, err)
	}
	if shared.DisplayProfile() != "work" || !reflect.DeepEqual(shared.Profiles, []string{"personal", "work"}) ||
		shared.Project != "Agent Mux" || shared.CWD != "/work/agentmux" || shared.Snippet != "shared snippet" ||
		!shared.StartedAt.Equal(entries["claude:shared"].StartedAt) || !shared.LastAt.Equal(entries["claude:shared"].LastAt) {
		t.Fatalf("shared metadata = %#v", shared)
	}
	if content, ok, err := repo.SearchText(ctx, "claude", "shared"); err != nil || !ok || content != "legacy sidecar body" {
		t.Fatalf("shared SearchText: content=%q ok=%v err=%v", content, ok, err)
	}
	if content, ok, err := repo.SearchText(ctx, "codex", "missing-sidecar"); err != nil || !ok || content != "needle one\nsecond needle" {
		t.Fatalf("rebuilt SearchText: content=%q ok=%v err=%v", content, ok, err)
	}
	if _, ok, err := repo.SearchText(ctx, "copilot", "hidden"); err != nil || ok {
		t.Fatalf("hidden SearchText: ok=%v err=%v", ok, err)
	}
	record, ok, err := repo.LegacyTranscriptImportRecord(ctx)
	if err != nil || !ok || record.SourcePath != indexPath || record.Fingerprint != result.Fingerprint || record.RecordCount != 3 || !record.CompletedAt.Equal(now) {
		t.Fatalf("import record = %#v ok=%v err=%v", record, ok, err)
	}

	var updatedBefore int64
	if err := store.DB().QueryRowContext(ctx, `
		SELECT updated_at_ns FROM conversations WHERE harness = 'claude' AND session_id = 'shared'
	`).Scan(&updatedBefore); err != nil {
		t.Fatalf("read updated_at before rerun: %v", err)
	}
	repo.now = func() time.Time { return now.Add(time.Hour) }
	result2, err := repo.ImportLegacyIndex(ctx, indexPath, readers)
	if err != nil {
		t.Fatalf("second ImportLegacyIndex: %v", err)
	}
	if result2.Imported || result2.Fingerprint != result.Fingerprint || result2.RecordCount != 3 {
		t.Fatalf("second result = %#v", result2)
	}
	var count int
	var updatedAfter int64
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM conversations`).Scan(&count); err != nil {
		t.Fatalf("count conversations: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx, `
		SELECT updated_at_ns FROM conversations WHERE harness = 'claude' AND session_id = 'shared'
	`).Scan(&updatedAfter); err != nil {
		t.Fatalf("read updated_at after rerun: %v", err)
	}
	if count != 3 || updatedAfter != updatedBefore {
		t.Fatalf("rerun changed database: count=%d updated before=%d after=%d", count, updatedBefore, updatedAfter)
	}
	jsonAfter, err := os.ReadFile(indexPath)
	if err != nil || !reflect.DeepEqual(jsonAfter, raw) {
		t.Fatalf("legacy JSON changed: err=%v", err)
	}
	sidecarAfter, err := os.ReadFile(sidecarPath)
	if err != nil || !reflect.DeepEqual(sidecarAfter, sidecarBefore) {
		t.Fatalf("legacy sidecar changed: err=%v", err)
	}
	if _, err := os.Stat(legacyStore.Path("codex:missing-sidecar")); !os.IsNotExist(err) {
		t.Fatalf("missing sidecar was written during import: err=%v", err)
	}
}

func TestImportLegacyIndexCurrentHarnessFixtures(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepository(t)
	readers := map[string]Reader{
		"claude":  NewClaudeReader(),
		"codex":   NewCodexReader(),
		"copilot": NewCopilotReader(),
	}
	homes := map[string]string{
		"claude":  claudeFixtureRoot,
		"codex":   codexFixtureRoot,
		"copilot": copilotFixtureRoot,
	}
	entries := make(map[string]SessionMeta)
	for harnessName, reader := range readers {
		metas, err := reader.Discover(homes[harnessName], DiscoverOptions{})
		if err != nil {
			t.Fatalf("Discover(%s): %v", harnessName, err)
		}
		for _, meta := range metas {
			meta.Profile = "fixture-display"
			meta.Profiles = []string{"fixture-shared", "fixture-display"}
			meta.Project = "Fixture Project"
			meta.SearchText = "" // Saved transcripts.json keeps content in sidecars.
			entries[IndexKey(meta.Harness, meta.ID)] = meta
		}
	}
	if len(entries) != 4 {
		t.Fatalf("discovered %d fixture entries, want 4", len(entries))
	}
	raw, err := json.Marshal(indexFile{Entries: entries})
	if err != nil {
		t.Fatalf("marshal fixture index: %v", err)
	}
	indexPath := filepath.Join(t.TempDir(), "transcripts.json")
	if err := os.WriteFile(indexPath, raw, 0o644); err != nil {
		t.Fatalf("write fixture index: %v", err)
	}

	result, err := repo.ImportLegacyIndex(ctx, indexPath, readers)
	if err != nil {
		t.Fatalf("ImportLegacyIndex: %v", err)
	}
	if !result.Imported || result.RecordCount != 4 || result.RebuiltSearchDocs != 4 {
		t.Fatalf("result = %#v", result)
	}
	for key, want := range entries {
		got, ok, err := repo.Get(ctx, want.Harness, want.ID)
		if err != nil || !ok {
			t.Fatalf("Get(%s): ok=%v err=%v", key, ok, err)
		}
		if got.DisplayProfile() != want.DisplayProfile() || !reflect.DeepEqual(got.Profiles, want.Profiles) ||
			got.Project != want.Project || got.CWD != want.CWD || got.Path != want.Path ||
			got.FileSize != want.FileSize || got.MessageCount != want.MessageCount || got.Snippet != want.Snippet ||
			!got.FileModTime.Equal(want.FileModTime) || !got.StartedAt.Equal(want.StartedAt) || !got.LastAt.Equal(want.LastAt) {
			t.Fatalf("metadata mismatch for %s:\n got %#v\nwant %#v", key, got, want)
		}
		messages, err := readers[want.Harness].ReadMessages(want.Path)
		if err != nil {
			t.Fatalf("ReadMessages(%s): %v", key, err)
		}
		content, ok, err := repo.SearchText(ctx, want.Harness, want.ID)
		if err != nil || !ok || content != messagesSearchText(messages) {
			t.Fatalf("SearchText(%s): content=%q ok=%v err=%v", key, content, ok, err)
		}
	}
}

func TestImportLegacyIndexMalformedMetadataDoesNotPartiallyImport(t *testing.T) {
	ctx := context.Background()
	repo, store := newTestRepository(t)
	indexPath := filepath.Join(t.TempDir(), "transcripts.json")
	malformed := `{"entries":{"claude:good":{"id":"good","harness":"claude"},"claude:broken":`
	if err := os.WriteFile(indexPath, []byte(malformed), 0o644); err != nil {
		t.Fatalf("write malformed index: %v", err)
	}
	_, err := repo.ImportLegacyIndex(ctx, indexPath, nil)
	if err == nil || !strings.Contains(err.Error(), "decoding transcript index "+indexPath) || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("error = %v", err)
	}
	var conversations, imports int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM conversations`).Scan(&conversations); err != nil {
		t.Fatalf("count conversations: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM import_records`).Scan(&imports); err != nil {
		t.Fatalf("count imports: %v", err)
	}
	if conversations != 0 || imports != 0 {
		t.Fatalf("partial import: conversations=%d imports=%d", conversations, imports)
	}
}

func TestImportLegacyIndexRejectsIdentityMismatchBeforeTransaction(t *testing.T) {
	ctx := context.Background()
	repo, store := newTestRepository(t)
	indexPath := filepath.Join(t.TempDir(), "transcripts.json")
	raw, err := json.Marshal(indexFile{Entries: map[string]SessionMeta{
		"claude:good":      {ID: "good", Harness: "claude"},
		"claude:wrong-key": {ID: "different-id", Harness: "claude"},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(indexPath, raw, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	_, err = repo.ImportLegacyIndex(ctx, indexPath, nil)
	if err == nil || !strings.Contains(err.Error(), `legacy entry "claude:wrong-key" has metadata identity "claude:different-id"`) {
		t.Fatalf("error = %v", err)
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM conversations`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("partial import count = %d", count)
	}
}

type readerFunc struct {
	harness string
	read    func(string) ([]Message, error)
}

func (r readerFunc) Harness() string                                         { return r.harness }
func (r readerFunc) Discover(string, DiscoverOptions) ([]SessionMeta, error) { return nil, nil }
func (r readerFunc) ReadMessages(path string) ([]Message, error)             { return r.read(path) }
