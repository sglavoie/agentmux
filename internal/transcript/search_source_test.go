package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestSearchMessageSourceAcrossReaders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		reader Reader
		body   string
	}{
		{"claude", NewClaudeReader(), `{"type":"user","message":{"role":"user","content":"userneedle"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"harnessneedle"}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"toolneedle"}]}}
`},
		{"codex", NewCodexReader(), `{"type":"event_msg","payload":{"type":"user_message","message":"userneedle"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"harnessneedle"}]}}
{"type":"response_item","payload":{"type":"function_call_output","output":"toolneedle"}}
`},
		{"copilot", NewCopilotReader(), `{"type":"user.message","data":{"content":"userneedle"}}
{"type":"assistant.message","data":{"content":"harnessneedle"}}
{"type":"tool.execution_complete","data":{"result":"toolneedle"}}
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "events.jsonl")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			idx := newTestIndex(t)
			key := IndexKey(tc.reader.Harness(), "one")
			must(t, idx.SetEntryForTest(key, SessionMeta{ID: "one", Harness: tc.reader.Harness(), Path: path, Snippet: "userneedle"}))
			readers := map[string]Reader{tc.reader.Harness(): tc.reader}
			for _, test := range []struct {
				source, query, preview string
				matches                int
			}{
				{"user", "userneedle", "userneedle", 1},
				{"user", "harnessneedle", "", 0},
				{"harness", "harnessneedle", "harnessneedle", 1},
				{"harness", "userneedle", "", 0},
				{"harness", "toolneedle", "toolneedle", 1},
				{"user", "toolneedle", "", 0},
			} {
				results, err := Search(idx, readers, Query{Text: test.query, MessageSource: test.source})
				if err != nil {
					t.Fatal(err)
				}
				if test.matches == 0 {
					if len(results) != 0 {
						t.Fatalf("%s %s: unexpected results %+v", test.source, test.query, results)
					}
					continue
				}
				if len(results) != 1 || results[0].Matches != test.matches || !strings.Contains(results[0].Preview, test.preview) {
					t.Fatalf("%s %s: results %+v", test.source, test.query, results)
				}
			}
			// The source-aware sidecar now answers filtered searches without readers.
			results, err := Search(idx, nil, Query{Text: "harnessneedle", MessageSource: "harness"})
			if err != nil || len(results) != 1 {
				t.Fatalf("sidecar search: %+v, %v", results, err)
			}
		})
	}
}

func TestSearchMessageSourceUpgradesLegacySidecar(t *testing.T) {
	t.Parallel()

	idx := newTestIndex(t)
	key := "claude:one"
	must(t, idx.SetEntryForTest(key, SessionMeta{ID: "one", Harness: "claude", Path: "one", Snippet: "needle user"}))
	must(t, idx.WriteSearchText(key, "needle user\nneedle harness"))
	reader := &dispatchReader{harness: "claude", byPath: map[string][]Message{"one": {
		{Role: "user", Text: "needle user"}, {Role: "assistant", Text: "needle harness"},
	}}}
	result, err := Search(idx, map[string]Reader{"claude": reader}, Query{Text: "needle", MessageSource: "harness"})
	if err != nil || len(result) != 1 || result[0].Matches != 1 || strings.Contains(result[0].Preview, "user") {
		t.Fatalf("filtered result: %+v, %v", result, err)
	}
	raw, err := idx.ReadSearchText(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decodeSourceSidecar(raw); !ok {
		t.Fatalf("legacy sidecar was not upgraded: %q", raw)
	}
}

func TestSearchMessageSourceSkipsLegacyMissWithoutParsing(t *testing.T) {
	t.Parallel()

	idx := newTestIndex(t)
	key := "claude:one"
	must(t, idx.SetEntryForTest(key, SessionMeta{ID: "one", Harness: "claude", Path: "unavailable"}))
	must(t, idx.WriteSearchText(key, "ordinary legacy content"))
	result, err := Search(idx, nil, Query{Text: "absentneedle", MessageSource: "user"})
	if err != nil || len(result) != 0 {
		t.Fatalf("legacy miss: %+v, %v", result, err)
	}
	raw, err := idx.ReadSearchText(key)
	if err != nil || raw != "ordinary legacy content" {
		t.Fatalf("sidecar unexpectedly upgraded: %q, %v", raw, err)
	}
}

func TestClaudeMixedUserToolResultSource(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mixed.jsonl")
	body := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"promptneedle"},{"type":"tool_result","content":"toolneedle"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs, err := NewClaudeReader().ReadMessages(path)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("messages: %+v, %v", msgs, err)
	}
	if !strings.Contains(msgs[0].Text, "promptneedle") || !strings.Contains(msgs[0].Text, "toolneedle") {
		t.Fatalf("display text: %q", msgs[0].Text)
	}
	if strings.Contains(msgs[0].SearchTextForSource("user"), "toolneedle") || strings.Contains(msgs[0].SearchTextForSource("harness"), "promptneedle") {
		t.Fatalf("split failed: %+v", msgs[0])
	}
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:mixed", SessionMeta{ID: "mixed", Harness: "claude", Path: path, Snippet: "promptneedle"}))
	for _, tc := range []struct{ source, query string }{{"user", "promptneedle"}, {"harness", "toolneedle"}} {
		result, err := Search(idx, map[string]Reader{"claude": NewClaudeReader()}, Query{Text: tc.query, MessageSource: tc.source})
		if err != nil || len(result) != 1 || result[0].Matches != 1 {
			t.Fatalf("%+v: %+v, %v", tc, result, err)
		}
		if !strings.Contains(result[0].Preview, tc.query) || strings.Contains(result[0].Preview, map[string]string{"user": "toolneedle", "harness": "promptneedle"}[tc.source]) {
			t.Fatalf("%s preview mixed sources: %q", tc.source, result[0].Preview)
		}
	}
}

func TestClaudeLocalCommandSearchSources(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "commands.jsonl")
	body := `{"type":"user","message":{"role":"user","content":"<command-name>/cost</command-name>"}}
{"type":"user","message":{"role":"user","content":"<local-command-stdout>Total cost: billingneedle</local-command-stdout>"}}
{"type":"user","message":{"role":"user","content":"<local-command-stderr>warningneedle</local-command-stderr>"}}
{"type":"user","message":{"role":"user","content":"<local-command-caveat>caveatneedle</local-command-caveat>"}}
`
	writeTranscript(t, path, body)
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:commands", SessionMeta{ID: "commands", Harness: harness.Claude, Path: path}))
	readers := map[string]Reader{harness.Claude: NewClaudeReader()}
	for _, query := range []string{"/cost", "billingneedle", "warningneedle", "caveatneedle"} {
		for _, source := range []string{"user", "harness", "any"} {
			want := source == "any" || (source == "user") == (query == "/cost")
			// Repeat without readers to exercise the persisted source-aware corpus.
			for _, available := range []map[string]Reader{readers, nil} {
				results, err := Search(idx, available, Query{Text: query, MessageSource: source})
				if err != nil || (len(results) == 1) != want {
					t.Fatalf("query %q source %s: got %+v, %v; want match=%v", query, source, results, err, want)
				}
			}
		}
	}
}

func TestClaudeLocalCommandSearchRefreshesOldSourceSidecars(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	path := filepath.Join(home, "projects", "-tmp-demo", "commands.jsonl")
	writeTranscript(t, path, `{"type":"user","cwd":"/tmp/demo","message":{"role":"user","content":"Check billing"}}
{"type":"user","message":{"role":"user","content":"<local-command-stdout>billingneedle</local-command-stdout>"}}
`)
	reader := NewClaudeReader()
	metas, err := reader.Discover(home, DiscoverOptions{})
	if err != nil || len(metas) != 1 {
		t.Fatalf("discover: %+v, %v", metas, err)
	}
	meta := metas[0]
	meta.ReaderVersion = 6
	meta.SearchText = ""
	idx := newTestIndex(t)
	key := IndexKey(harness.Claude, meta.ID)
	must(t, idx.SetEntryForTest(key, meta))
	must(t, idx.WriteSearchText(key, encodeSourceSidecar(sourceSidecar{
		Version: 2, Any: "check billing\nbillingneedle", User: "check billing\nbillingneedle",
	})))
	readers := map[string]Reader{harness.Claude: reader}
	// Warm the decoded cache with the old classification as well.
	before, err := Search(idx, nil, Query{Text: "billingneedle", MessageSource: "user"})
	if err != nil || len(before) != 1 {
		t.Fatalf("old sidecar: %+v, %v", before, err)
	}
	cfg := &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}}
	must(t, idx.Refresh(cfg, readers))
	for _, source := range []string{"user", "harness"} {
		results, err := Search(idx, readers, Query{Text: "billingneedle", MessageSource: source})
		if err != nil || (len(results) == 1) != (source == "harness") {
			t.Fatalf("after refresh source %s: %+v, %v", source, results, err)
		}
	}
	updated, _ := idx.Get(key)
	if updated.ReaderVersion != claudeReaderVersion {
		t.Fatalf("reader version = %d, want %d", updated.ReaderVersion, claudeReaderVersion)
	}
}

func TestSearchCacheSeesReaderUpgradeFromAnotherIndex(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	path := filepath.Join(home, "projects", "-tmp-demo", "commands.jsonl")
	writeTranscript(t, path, `{"type":"user","cwd":"/tmp/demo","message":{"role":"user","content":"Check billing"}}
{"type":"user","message":{"role":"user","content":"<local-command-stdout>billingneedle</local-command-stdout>"}}
`)
	reader := NewClaudeReader()
	metas, err := reader.Discover(home, DiscoverOptions{})
	if err != nil || len(metas) != 1 {
		t.Fatalf("discover: %+v, %v", metas, err)
	}
	meta := metas[0]
	meta.ReaderVersion = claudeReaderVersion - 1
	meta.SearchText = ""
	idx := newTestIndex(t)
	key := IndexKey(harness.Claude, meta.ID)
	must(t, idx.SetEntryForTest(key, meta))
	must(t, idx.Save())
	must(t, idx.WriteSearchText(key, encodeSourceSidecar(sourceSidecar{
		Version: 2, Any: "check billing\nbillingneedle", User: "check billing\nbillingneedle",
	})))
	before, err := Search(idx, nil, Query{Text: "billingneedle", MessageSource: "user"})
	if err != nil || len(before) != 1 {
		t.Fatalf("old sidecar: %+v, %v", before, err)
	}

	// A second process upgrades the index and sidecar without changing the
	// transcript's path, size, or mtime. The first process then reloads it.
	other, err := LoadIndexAt(idx.Path())
	must(t, err)
	cfg := &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}}
	readers := map[string]Reader{harness.Claude: reader}
	must(t, other.Refresh(cfg, readers))
	_, err = Search(other, readers, Query{Text: "billingneedle", MessageSource: "harness"})
	must(t, err)
	must(t, idx.Refresh(cfg, readers))
	for _, source := range []string{"user", "harness"} {
		results, err := Search(idx, nil, Query{Text: "billingneedle", MessageSource: source})
		if err != nil || (len(results) == 1) != (source == "harness") {
			t.Fatalf("after external upgrade source %s: %+v, %v", source, results, err)
		}
	}
}

func TestSearchSourceSidecarRefreshesAfterTranscriptChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "home", "projects", "-tmp-demo", "one.jsonl")
	stat := writeTranscript(t, path, "old transcript\n")
	content := "old user\nold harness"
	messages := []Message{{Role: "user", Text: "old user"}, {Role: "assistant", Text: "old harness"}}
	reader := &fakeReader{harness: harness.Claude}
	reader.discover = func(string, DiscoverOptions) ([]SessionMeta, error) {
		return []SessionMeta{{ID: "one", Harness: harness.Claude, Path: path, FileSize: stat.Size(), FileModTime: stat.ModTime(), MessageCount: 2, SearchText: content, SessionClass: SessionClassMain}}, nil
	}
	reader.readResult = messages
	readers := map[string]Reader{harness.Claude: reader}
	cfg := &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: filepath.Join(root, "home")}}}
	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatal(err)
	}
	if _, err := Search(idx, readers, Query{Text: "old", MessageSource: "harness"}); err != nil {
		t.Fatal(err)
	}
	if raw, err := idx.ReadSearchText("claude:one"); err != nil {
		t.Fatal(err)
	} else if _, ok := decodeSourceSidecar(raw); !ok {
		t.Fatal("first filtered search did not upgrade sidecar")
	}

	stat = writeTranscript(t, path, "new transcript with changed size\n")
	content = "new user\nnew harness"
	reader.readResult = []Message{{Role: "user", Text: "new user"}, {Role: "assistant", Text: "new harness"}}
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatal(err)
	}
	if result, err := Search(idx, readers, Query{Text: "old", MessageSource: "harness"}); err != nil || len(result) != 0 {
		t.Fatalf("stale source match: %+v, %v", result, err)
	}
	if result, err := Search(idx, readers, Query{Text: "new", MessageSource: "harness"}); err != nil || len(result) != 1 || result[0].Matches != 1 {
		t.Fatalf("new source match: %+v, %v", result, err)
	}

	stat = writeTranscript(t, path, "empty changed transcript contents\n")
	content = ""
	reader.readResult = nil
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatal(err)
	}
	if exists, err := idx.SearchTextExists("claude:one"); err != nil || exists {
		t.Fatalf("stale sidecar after empty refresh: exists=%v, err=%v", exists, err)
	}
	if result, err := Search(idx, readers, Query{Text: "new", MessageSource: "harness"}); err != nil || len(result) != 0 {
		t.Fatalf("empty transcript retained match: %+v, %v", result, err)
	}
}
