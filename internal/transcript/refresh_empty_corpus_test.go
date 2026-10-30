package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestRefreshEmptyCorpusSurvivesSidecarRemovalFailure(t *testing.T) {
	t.Parallel()
	for _, rebuild := range []bool{false, true} {
		name := "refresh"
		if rebuild {
			name = "rebuild"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			path := filepath.Join(home, "projects", "-tmp-foo", "one.jsonl")
			writeTranscript(t, path, `{"type":"user","message":{"role":"user","content":"obsolete needle"},"cwd":"/tmp/foo"}`+"\n")
			cfg := &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}}
			readers := map[string]Reader{harness.Claude: NewClaudeReader()}
			idx := newTestIndex(t)
			must(t, idx.Refresh(cfg, readers))
			key := IndexKey(harness.Claude, "one")
			// Warm the decoded corpus too: pending removal must supersede it.
			results, err := Search(idx, readers, Query{Text: "obsolete needle"})
			if err != nil || len(results) != 1 {
				t.Fatalf("initial search = %+v, %v", results, err)
			}
			store := idx.SearchStoreDir()
			backup := store + ".backup"
			must(t, os.Rename(store, backup))
			must(t, os.WriteFile(store, []byte("blocking file"), 0o600))
			info := writeTranscript(t, path, `{"type":"system","cwd":"/tmp/foo"}`+"\n")
			if rebuild {
				err = idx.rebuildFilesystem(cfg, readers)
			} else {
				err = idx.Refresh(cfg, readers)
			}
			if err == nil {
				t.Fatal("empty reparse succeeded despite failed sidecar removal")
			}
			loaded, _ := LoadIndexAt(idx.Path())
			if loaded == nil {
				t.Fatal("pending cleanup made the metadata unusable")
			}
			for _, index := range []*Index{idx, loaded} {
				meta, ok := index.Get(key)
				if !ok || meta.FileSize != info.Size() || meta.MessageCount != 0 || meta.Snippet != "" || !meta.SearchTextPending {
					t.Fatalf("fresh empty metadata lost: %+v, %t", meta, ok)
				}
			}
			must(t, os.Remove(store))
			must(t, os.Rename(backup, store))
			for _, index := range []*Index{idx, loaded} {
				for _, source := range []string{"any", "user", "harness"} {
					results, err := Search(index, nil, Query{Text: "obsolete needle", MessageSource: source})
					if err != nil || len(results) != 0 {
						t.Errorf("stale %s corpus remained searchable: %+v, %v", source, results, err)
					}
				}
			}
			// Saving retries cleanup even though there is no inline text.
			must(t, idx.Save())
			if _, err := os.Lstat(idx.SearchTextPath(key)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("retry left the obsolete sidecar: %v", err)
			}
			fresh, err := LoadIndexAt(idx.Path())
			must(t, err)
			if meta, _ := fresh.Get(key); meta.SearchTextPending {
				t.Error("successful cleanup retained pending state")
			}
			results, err = Search(fresh, readers, Query{Text: "obsolete needle"})
			if err != nil || len(results) != 0 {
				t.Errorf("search after cleanup = %+v, %v", results, err)
			}
			writeTranscript(t, path, `{"type":"user","message":{"role":"user","content":"replacement needle"},"cwd":"/tmp/foo"}`+"\n")
			must(t, fresh.Refresh(cfg, readers))
			results, err = Search(fresh, nil, Query{Text: "replacement needle"})
			if err != nil || len(results) != 1 {
				t.Errorf("new transcript content remained suppressed: %+v, %v", results, err)
			}
		})
	}
}

func TestRefreshDiscardsObsoleteCorpusAfterEmptyReparse(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"reader upgrade", "rebuild", "source replacement"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			path := filepath.Join(home, "projects", "-tmp-foo", "one.jsonl")
			info := writeTranscript(t, path, `{"type":"system","timestamp":"2026-05-01T12:00:00Z","cwd":"/tmp/foo"}`+"\n")
			meta := SessionMeta{
				ID: "one", Harness: harness.Claude, Path: path,
				FileSize: info.Size(), FileModTime: info.ModTime(),
				MessageCount: 1, Snippet: "obsolete needle", SearchText: "obsolete needle",
				SessionClass: SessionClassMain, ReaderVersion: claudeReaderVersion,
			}
			if reason == "reader upgrade" {
				meta.ReaderVersion--
			}
			cfg := &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}}
			idx := newTestIndex(t)
			seed := &fakeReader{harness: harness.Claude, discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
				return []SessionMeta{meta}, nil
			}}
			must(t, idx.Refresh(cfg, map[string]Reader{harness.Claude: seed}))
			if reason == "source replacement" {
				// The same conversation can move to another rollout with the
				// same size and timestamp. Those facts alone are not identity.
				oldPath := path + ".old"
				must(t, os.Rename(path, oldPath))
				key := IndexKey(harness.Claude, "one")
				previous, _ := idx.Get(key)
				previous.Path = oldPath
				must(t, idx.SetEntryForTest(key, previous))
				data, err := os.ReadFile(oldPath)
				must(t, err)
				must(t, os.WriteFile(path, data, 0o600))
				must(t, os.Chtimes(path, info.ModTime(), info.ModTime()))
			}
			readers := map[string]Reader{harness.Claude: NewClaudeReader()}
			if reason == "rebuild" {
				must(t, idx.rebuildFilesystem(cfg, readers))
			} else {
				must(t, idx.Refresh(cfg, readers))
			}
			key := IndexKey(harness.Claude, "one")
			got, ok := idx.Get(key)
			if !ok || got.MessageCount != 0 || got.Snippet != "" || got.ReaderVersion != claudeReaderVersion {
				t.Errorf("empty reparse retained obsolete metadata: %+v, %t", got, ok)
			}
			loaded, err := LoadIndexAt(idx.Path())
			must(t, err)
			for _, index := range []*Index{idx, loaded} {
				results, err := Search(index, readers, Query{Text: "obsolete needle"})
				if err != nil || len(results) != 0 {
					t.Errorf("obsolete text remained searchable: %+v, %v", results, err)
				}
			}
		})
	}
}
