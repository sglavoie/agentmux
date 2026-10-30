package transcript

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

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
