package transcript

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestRefreshRetainsSearchTextWhenSidecarWriteFails(t *testing.T) {
	t.Parallel()

	idx := newTestIndex(t)
	key := IndexKey(harness.Claude, "one")
	path := filepath.Join(t.TempDir(), "one.jsonl")
	info := writeTranscript(t, path, "old content\n")
	meta := SessionMeta{ID: "one", Harness: harness.Claude, Path: path,
		FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1,
		SessionClass: SessionClassMain, SearchText: "oldneedle"}
	cfg := &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: t.TempDir()}}}
	reader := &fakeReader{harness: harness.Claude, discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
		return []SessionMeta{meta}, nil
	}}
	readers := map[string]Reader{harness.Claude: reader}
	must(t, idx.Refresh(cfg, readers))

	// Preserve a readable, old sidecar while making its directory temporarily
	// unavailable for writes. This works even when tests run as root.
	store := idx.SearchStoreDir()
	backup := store + ".backup"
	must(t, os.Rename(store, backup))
	must(t, os.WriteFile(store, []byte("blocking file"), 0o600))
	info = writeTranscript(t, path, "new content with an appended message\n")
	meta.FileSize, meta.FileModTime, meta.SearchText = info.Size(), info.ModTime(), "newneedle"
	if err := idx.Refresh(cfg, readers); err == nil {
		t.Fatal("Refresh succeeded despite the unavailable sidecar directory")
	}

	// Both this process and a new reader must retain the freshly parsed body
	// alongside its metadata until the sidecar can be written successfully.
	loaded, warning := LoadIndexAt(idx.Path())
	if warning == nil || loaded == nil {
		t.Fatalf("LoadIndexAt: nil index = %t, warning = %v; want usable metadata and a migration warning", loaded == nil, warning)
	}
	for _, index := range []*Index{idx, loaded} {
		got, ok := index.Get(key)
		if !ok || got.SearchText != "newneedle" || got.FileSize != info.Size() {
			t.Errorf("new search corpus was lost after failed write: %+v, %t", got, ok)
		}
		results, err := Search(index, nil, Query{Text: "newneedle"})
		if err != nil || len(results) != 1 {
			t.Errorf("search with failed sidecar = %+v, %v; want one result", results, err)
		}
		results, err = Search(index, nil, Query{Text: "oldneedle"})
		if err != nil || len(results) != 0 {
			t.Errorf("search retained obsolete content = %+v, %v", results, err)
		}
	}

	must(t, os.Remove(store))
	must(t, os.Rename(backup, store))
	// A source-filtered search must also prefer the pending new corpus over
	// the old sidecar, whose lack of the term would otherwise hide this row.
	sourceReader := &dispatchReader{harness: harness.Claude, byPath: map[string][]Message{
		path: {{Role: "user", Text: "newneedle"}},
	}}
	results, err := Search(idx, map[string]Reader{harness.Claude: sourceReader}, Query{Text: "newneedle", MessageSource: "user"})
	if err != nil || len(results) != 1 {
		t.Errorf("source-filtered search with stale sidecar = %+v, %v; want one result", results, err)
	}
	must(t, idx.Save())
	if got, err := idx.ReadSearchText(key); err != nil || got != "newneedle" {
		t.Fatalf("retry did not repair sidecar: %q, %v", got, err)
	}
	if got, _ := idx.Get(key); got.SearchText != "" {
		t.Fatalf("successful retry retained inline search body: %q", got.SearchText)
	}
}
