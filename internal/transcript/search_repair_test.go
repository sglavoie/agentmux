package transcript

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

type searchMutationReader struct {
	Reader
	read func(string) ([]Message, error)
}

func TestSearchRepairsLargeFallbackBatch(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"any", "user"} {
		t.Run(source, func(t *testing.T) {
			idx := newTestIndex(t)
			const count = 130
			for n := range count {
				id := strconv.Itoa(n)
				idx.entries[IndexKey("claude", id)] = SessionMeta{ID: id, Harness: "claude", Path: id}
			}
			must(t, idx.Save())
			readers := map[string]Reader{"claude": &fakeReader{readResult: []Message{{Role: "user", Text: "needle"}}}}
			results, err := Search(idx, readers, Query{Text: "needle", MessageSource: source})
			if err != nil || len(results) != count {
				t.Fatalf("source search returned %d results, %v", len(results), err)
			}
			// Reopen to bypass the in-memory corpus cache. Every repair,
			// including the final partial batch, must be durable and usable.
			reopened, err := LoadIndexAt(idx.Path())
			must(t, err)
			results, err = Search(reopened, nil, Query{Text: "needle", MessageSource: source})
			if err != nil || len(results) != count {
				t.Fatalf("repaired search returned %d results, %v", len(results), err)
			}
		})
	}
}

func (r searchMutationReader) ReadMessages(path string) ([]Message, error) {
	return r.read(path)
}

func TestSearchRepairPreservesConcurrentMutation(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"any", "user"} {
		for _, mutation := range []string{"refresh", "hide", "delete"} {
			t.Run(source+"/"+mutation, func(t *testing.T) {
				idx := newTestIndex(t)
				path := filepath.Join(t.TempDir(), "session.jsonl")
				info := writeTranscript(t, path, "old needle")
				meta := SessionMeta{ID: "one", Harness: "claude", Path: path,
					FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1}
				key := IndexKey(meta.Harness, meta.ID)
				must(t, idx.SetEntryForTest(key, meta))
				// A separate index models a daemon/CLI mutation during the
				// source read, before this search attempts its sidecar repair.
				other, err := LoadIndexAt(idx.Path())
				must(t, err)
				reader := searchMutationReader{read: func(string) ([]Message, error) {
					var err error
					switch mutation {
					case "refresh":
						if err = os.WriteFile(path, []byte("new needle with more text"), 0o600); err != nil {
							return nil, err
						}
						info, statErr := os.Stat(path)
						if statErr != nil {
							return nil, statErr
						}
						fresh := meta
						fresh.FileSize, fresh.FileModTime = info.Size(), info.ModTime()
						fresh.SearchText = "new needle with more text"
						cfg := &config.Config{Profiles: []config.Account{{Name: "work", Harness: "claude", HomeDir: filepath.Dir(path)}}}
						err = other.Refresh(cfg, map[string]Reader{"claude": &fakeReader{
							discover: func(string, DiscoverOptions) ([]SessionMeta, error) { return []SessionMeta{fresh}, nil },
						}})
					case "hide":
						err = other.SetHidden(key, true)
					case "delete":
						err = other.Delete(key, false)
					}
					return []Message{{Role: "user", Text: "old needle"}}, err
				}}
				results, err := Search(idx, map[string]Reader{"claude": reader}, Query{Text: "needle", MessageSource: source})
				must(t, err)
				if len(results) != 1 {
					t.Fatalf("in-flight search lost its source result: %+v", results)
				}
				raw, err := idx.ReadSearchText(key)
				if mutation == "refresh" {
					if err != nil || raw != "new needle with more text" {
						t.Fatalf("search overwrote refreshed sidecar: %q, %v", raw, err)
					}
				} else if !os.IsNotExist(err) {
					t.Fatalf("search recreated %s sidecar: %q, %v", mutation, raw, err)
				}
			})
		}
	}
}
