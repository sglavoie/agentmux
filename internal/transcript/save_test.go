package transcript

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
)

func TestSaveLoad_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcripts.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	key := IndexKey(harness.Claude, "sess-1")
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID:           "sess-1",
		Harness:      harness.Claude,
		Profile:      "work",
		Project:      "Foo",
		Snippet:      "hello world",
		MessageCount: 3,
		StartedAt:    time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}))
	if err := idx.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File should exist and be valid JSON with the expected shape.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	var stored indexFile
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decode: %v\nfile: %s", err, data)
	}
	if _, ok := stored.Entries[key]; !ok {
		t.Errorf("saved file missing entry %s; got: %s", key, data)
	}

	loaded, err := LoadIndexAt(path)
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	got, ok := loaded.Get(key)
	if !ok {
		t.Fatalf("entry lost across save/load")
	}
	if got.Snippet != "hello world" {
		t.Errorf("Snippet = %q after reload", got.Snippet)
	}
}

func TestSearchTextPath_IsStableHashOfIndexKey(t *testing.T) {
	idx, err := LoadIndexAt(filepath.Join(t.TempDir(), "transcripts.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	key := IndexKey(harness.Claude, "sess-1")
	sum := sha256.Sum256([]byte(key))
	want := filepath.Join(filepath.Dir(idx.Path()), "transcripts.search", hex.EncodeToString(sum[:])+".txt")

	if got := idx.SearchTextPath(key); got != want {
		t.Fatalf("SearchTextPath(%q) = %q, want %q", key, got, want)
	}
}

func TestSearchTextSidecar_WriteReadStatsAndDelete(t *testing.T) {
	idx, err := LoadIndexAt(filepath.Join(t.TempDir(), "transcripts.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	key := IndexKey(harness.Claude, "sess-1")
	if err := idx.WriteSearchText(key, "first body\nsecond body"); err != nil {
		t.Fatalf("WriteSearchText: %v", err)
	}
	if _, err := os.Stat(idx.SearchTextPath(key) + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary sidecar remained after successful write: %v", err)
	}
	exists, err := idx.SearchTextExists(key)
	if err != nil {
		t.Fatalf("SearchTextExists: %v", err)
	}
	if !exists {
		t.Fatalf("sidecar missing after WriteSearchText")
	}
	got, err := idx.ReadSearchText(key)
	if err != nil {
		t.Fatalf("ReadSearchText: %v", err)
	}
	if got != "first body\nsecond body" {
		t.Fatalf("ReadSearchText = %q", got)
	}
	count, size, err := idx.SearchStoreStats()
	if err != nil {
		t.Fatalf("SearchStoreStats: %v", err)
	}
	if count != 1 || size != int64(len(got)) {
		t.Fatalf("SearchStoreStats = (%d, %d), want (1, %d)", count, size, len(got))
	}
	if err := idx.DeleteSearchText(key); err != nil {
		t.Fatalf("DeleteSearchText: %v", err)
	}
	if err := idx.DeleteSearchText(key); err != nil {
		t.Fatalf("DeleteSearchText missing sidecar: %v", err)
	}
	exists, err = idx.SearchTextExists(key)
	if err != nil {
		t.Fatalf("SearchTextExists after delete: %v", err)
	}
	if exists {
		t.Fatalf("sidecar exists after DeleteSearchText")
	}
	count, size, err = idx.SearchStoreStats()
	if err != nil {
		t.Fatalf("SearchStoreStats after delete: %v", err)
	}
	if count != 0 || size != 0 {
		t.Fatalf("SearchStoreStats after delete = (%d, %d), want (0, 0)", count, size)
	}
}

func TestSave_WritesMetadataOnlyJSONAndSearchSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcripts.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	key := IndexKey(harness.Claude, "sess-1")
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID:           "sess-1",
		Harness:      harness.Claude,
		Snippet:      "hello world",
		MessageCount: 3,
		SearchText:   "hello world\nneedle body",
	}))

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if json.Valid(data) == false {
		t.Fatalf("saved file is not valid JSON: %s", data)
	}
	if string(data) == "" {
		t.Fatalf("saved file is empty")
	}
	if strings.Contains(string(data), "search_text") {
		t.Fatalf("saved metadata JSON contains search_text: %s", data)
	}
	sidecar, err := idx.ReadSearchText(key)
	if err != nil {
		t.Fatalf("ReadSearchText: %v", err)
	}
	if sidecar != "hello world\nneedle body" {
		t.Fatalf("sidecar = %q", sidecar)
	}

	loaded, err := LoadIndexAt(path)
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	meta, ok := loaded.Get(key)
	if !ok {
		t.Fatalf("entry missing after LoadIndexAt")
	}
	if meta.SearchText != "" {
		t.Fatalf("SearchText loaded from metadata JSON = %q, want empty", meta.SearchText)
	}
}

func TestLoadIndex_MigratesLegacySearchTextToSidecars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcripts.json")
	key1 := IndexKey(harness.Claude, "legacy-1")
	key2 := IndexKey(harness.Codex, "legacy-2")
	body1 := strings.Repeat("alpha needle body\n", 64*1024)
	body2 := strings.Repeat("beta haystack body\n", 64*1024)

	legacy := indexFile{Entries: map[string]SessionMeta{
		key1: {
			ID:           "legacy-1",
			Harness:      harness.Claude,
			Snippet:      "alpha",
			MessageCount: 5,
			SearchText:   body1,
		},
		key2: {
			ID:           "legacy-2",
			Harness:      harness.Codex,
			Snippet:      "beta",
			MessageCount: 7,
			SearchText:   body2,
		},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy index: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write legacy index: %v", err)
	}

	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	if idx.Len() != 2 {
		t.Fatalf("Len = %d, want 2", idx.Len())
	}
	for _, key := range []string{key1, key2} {
		meta, ok := idx.Get(key)
		if !ok {
			t.Fatalf("entry %s missing after legacy load", key)
		}
		if meta.SearchText != "" {
			t.Fatalf("entry %s retained %d bytes of SearchText in memory", key, len(meta.SearchText))
		}
	}
	got1, err := idx.ReadSearchText(key1)
	if err != nil {
		t.Fatalf("ReadSearchText key1: %v", err)
	}
	if got1 != body1 {
		t.Fatalf("sidecar key1 length/content mismatch: got %d bytes", len(got1))
	}
	got2, err := idx.ReadSearchText(key2)
	if err != nil {
		t.Fatalf("ReadSearchText key2: %v", err)
	}
	if got2 != body2 {
		t.Fatalf("sidecar key2 length/content mismatch: got %d bytes", len(got2))
	}
	count, size, err := idx.SearchStoreStats()
	if err != nil {
		t.Fatalf("SearchStoreStats: %v", err)
	}
	if count != 2 || size != int64(len(body1)+len(body2)) {
		t.Fatalf("SearchStoreStats = (%d, %d), want (2, %d)", count, size, len(body1)+len(body2))
	}

	if err := idx.Save(); err != nil {
		t.Fatalf("Save after migration: %v", err)
	}
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten index: %v", err)
	}
	if strings.Contains(string(rewritten), "search_text") {
		t.Fatalf("rewritten index still contains legacy search_text")
	}
}
