package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func writeDefaultTranscriptIndex(t *testing.T, home string, entries map[string]transcript.SessionMeta) {
	t.Helper()
	path := defaultTranscriptIndexPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir index dir: %v", err)
	}
	type indexFile struct {
		Entries map[string]transcript.SessionMeta `json:"entries"`
	}
	data, err := json.Marshal(indexFile{Entries: entries})
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
}

func defaultTranscriptIndexPath(home string) string {
	return filepath.Join(home, ".local", "share", "agent-mux", "transcripts.json")
}

func sessionWarmupTestConfig() *config.Config {
	return &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: "/tmp/no-such-agentmux-transcripts",
		}},
	}
}

func TestSessionCacheLoadMissingIndexStartsEmptyWarmRefresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	msg, ok := drainCmd(t, sessionCacheLoadCmd(3)).(sessionCacheLoadDoneMsg)
	if !ok {
		t.Fatalf("sessionCacheLoadCmd produced %T, want sessionCacheLoadDoneMsg", msg)
	}
	if msg.seq != 3 {
		t.Fatalf("seq = %d, want 3", msg.seq)
	}
	if msg.loadErr != nil {
		t.Fatalf("missing cache returned loadErr: %v", msg.loadErr)
	}
	if msg.searchErr != nil {
		t.Fatalf("empty cache search returned searchErr: %v", msg.searchErr)
	}
	if msg.idx == nil {
		t.Fatal("missing cache returned nil index")
	}
	if msg.idx.Len() != 0 {
		t.Fatalf("missing cache index len = %d, want 0", msg.idx.Len())
	}
	if len(msg.results) != 0 {
		t.Fatalf("empty cache produced results: %+v", msg.results)
	}

	m := Model{
		cfg:                      sessionWarmupTestConfig(),
		sessionCacheLoadInFlight: true,
		sessionLoading:           true,
	}
	updated, cmd := m.Update(msg)
	got := updated.(Model)
	if got.sessionCacheLoadInFlight {
		t.Fatal("sessionCacheLoadInFlight remained true after empty cache load")
	}
	if !got.sessionRefreshInFlight {
		t.Fatal("empty cache load did not start background refresh")
	}
	if got.sessionLoading {
		t.Fatal("empty cache load left sessionLoading=true")
	}
	if got.transcriptIndex == nil {
		t.Fatal("empty cache load did not store transcriptIndex")
	}
	if cmd == nil {
		t.Fatal("empty cache load did not return warm refresh command")
	}
}

func TestSessionCacheLoadCorruptIndexSurfacesCacheError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := defaultTranscriptIndexPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir index dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("write corrupt index: %v", err)
	}

	msg, ok := drainCmd(t, sessionCacheLoadCmd(4)).(sessionCacheLoadDoneMsg)
	if !ok {
		t.Fatalf("sessionCacheLoadCmd produced %T, want sessionCacheLoadDoneMsg", msg)
	}
	if msg.seq != 4 {
		t.Fatalf("seq = %d, want 4", msg.seq)
	}
	if msg.loadErr == nil {
		t.Fatal("corrupt cache returned nil loadErr")
	}
	if msg.idx != nil {
		t.Fatal("corrupt cache returned non-nil index")
	}
	if msg.searchErr != nil {
		t.Fatalf("corrupt cache unexpectedly searched: %v", msg.searchErr)
	}

	m := Model{
		mode:                     ModeSessionSearch,
		cfg:                      sessionWarmupTestConfig(),
		sessionCacheLoadInFlight: true,
		sessionLoading:           true,
	}
	updated, cmd := m.Update(msg)
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("corrupt cache load returned cmd %T; want nil", cmd)
	}
	if got.sessionCacheLoadInFlight {
		t.Fatal("sessionCacheLoadInFlight remained true after corrupt cache load")
	}
	if got.sessionLoading {
		t.Fatal("corrupt cache load left sessionLoading=true")
	}
	if got.sessionRefreshErr == nil {
		t.Fatal("corrupt cache load did not surface sessionRefreshErr")
	}
	if got.sessionSearchErr != nil {
		t.Fatalf("corrupt cache load left stale search error: %v", got.sessionSearchErr)
	}
	if got.transcriptIndex != nil {
		t.Fatal("corrupt cache load stored transcriptIndex")
	}
}

func TestInitSessionWarmupLoadsCacheAndProducesResults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	writeDefaultTranscriptIndex(t, home, map[string]transcript.SessionMeta{
		"claude:new": {
			ID:           "new",
			Harness:      harness.Claude,
			Profile:      "work",
			Project:      "api",
			LastAt:       now,
			MessageCount: 4,
			Snippet:      "new cached session",
		},
		"claude:hidden": {
			ID:              "hidden",
			Harness:         harness.Claude,
			Profile:         "work",
			Project:         "api",
			LastAt:          now.Add(time.Hour),
			HiddenFromIndex: true,
			Snippet:         "hidden cached session",
		},
	})

	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(sessionWarmupTestConfig(), client)

	initMsg := m.Init()()
	batch, ok := initMsg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init produced %T, want tea.BatchMsg", initMsg)
	}
	if len(batch) != 5 {
		t.Fatalf("Init batch length = %d, want 5 startup commands", len(batch))
	}

	msgs := flattenMsg(t, batch[len(batch)-1]())
	if len(msgs) != 2 {
		t.Fatalf("warmup produced %d messages, want start + done", len(msgs))
	}
	if _, ok := msgs[0].(sessionCacheLoadStartedMsg); !ok {
		t.Fatalf("first warmup msg = %T, want sessionCacheLoadStartedMsg", msgs[0])
	}
	done, ok := msgs[1].(sessionCacheLoadDoneMsg)
	if !ok {
		t.Fatalf("second warmup msg = %T, want sessionCacheLoadDoneMsg", msgs[1])
	}
	if done.loadErr != nil {
		t.Fatalf("cache load error: %v", done.loadErr)
	}
	if done.searchErr != nil {
		t.Fatalf("cache search error: %v", done.searchErr)
	}
	if done.idx == nil {
		t.Fatal("cache load returned nil index")
	}
	if len(done.readers) != 3 {
		t.Fatalf("reader count = %d, want 3", len(done.readers))
	}
	if len(done.results) != 1 {
		t.Fatalf("result count = %d, want only the visible cached session", len(done.results))
	}
	if got := done.results[0].Meta.ID; got != "new" {
		t.Fatalf("result ID = %q, want new", got)
	}
}

func TestSessionCacheLoadDonePopulatesResultsAndStartsWarmRefresh(t *testing.T) {
	idx := writeTranscriptIndex(t, map[string]transcript.SessionMeta{})
	readers := map[string]transcript.Reader{
		harness.Claude: sessionSearchTestReader{harness: harness.Claude},
	}
	result := transcript.Result{
		Meta: transcript.SessionMeta{ID: "cached", Harness: harness.Claude, LastAt: time.Now()},
	}
	m := Model{
		cfg:                      sessionWarmupTestConfig(),
		sessionCacheLoadInFlight: true,
	}

	updated, cmd := m.Update(sessionCacheLoadDoneMsg{
		seq:     0,
		idx:     idx,
		readers: readers,
		results: []transcript.Result{result},
	})
	got := updated.(Model)
	if got.sessionCacheLoadInFlight {
		t.Fatal("sessionCacheLoadInFlight remained true after cache load")
	}
	if !got.sessionRefreshInFlight {
		t.Fatal("sessionRefreshInFlight was not set before warm refresh command")
	}
	if got.transcriptIndex != idx {
		t.Fatal("transcriptIndex was not stored from cache load")
	}
	if len(got.transcriptReaders) != 1 {
		t.Fatalf("transcriptReaders length = %d, want 1", len(got.transcriptReaders))
	}
	if len(got.sessionResults) != 1 || got.sessionResults[0].Meta.ID != "cached" {
		t.Fatalf("sessionResults not populated from cache load: %+v", got.sessionResults)
	}
	if cmd == nil {
		t.Fatal("cache load did not return warm refresh command")
	}
}

func TestSessionCacheLoadDoneActiveQueryWarmRefreshUsesCurrentQuery(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "claude-home")
	hitPath := filepath.Join(home, "projects", "-tmp-foo", "sess-new-hit.jsonl")
	missPath := filepath.Join(home, "projects", "-tmp-foo", "sess-new-miss.jsonl")
	for _, path := range []string{hitPath, missPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir transcript dir: %v", err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write transcript %s: %v", path, err)
		}
	}
	hitInfo, err := os.Stat(hitPath)
	if err != nil {
		t.Fatalf("stat hit transcript: %v", err)
	}
	missInfo, err := os.Stat(missPath)
	if err != nil {
		t.Fatalf("stat miss transcript: %v", err)
	}

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	cachedHit := transcript.SessionMeta{
		ID: "cached-hit", Harness: harness.Claude, Profile: "work", Project: "Foo",
		LastAt: now.Add(-time.Hour), Snippet: "cached needle row",
	}
	cachedMiss := transcript.SessionMeta{
		ID: "cached-miss", Harness: harness.Claude, Profile: "work", Project: "Foo",
		LastAt: now.Add(-2 * time.Hour), Snippet: "cached unrelated row",
	}
	idx := writeTranscriptIndex(t, map[string]transcript.SessionMeta{
		"claude:cached-hit":  cachedHit,
		"claude:cached-miss": cachedMiss,
	})
	if err := idx.WriteSearchText("claude:cached-hit", "cached needle row"); err != nil {
		t.Fatalf("WriteSearchText cached hit: %v", err)
	}
	if err := idx.WriteSearchText("claude:cached-miss", "cached unrelated row"); err != nil {
		t.Fatalf("WriteSearchText cached miss: %v", err)
	}

	readers := map[string]transcript.Reader{
		harness.Claude: sessionSearchTestReader{
			harness: harness.Claude,
			discover: func(homeDir string, _ transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
				if homeDir != home {
					t.Errorf("Discover homeDir = %q, want %q", homeDir, home)
				}
				return []transcript.SessionMeta{
					{
						ID: "new-hit", Harness: harness.Claude, CWD: "/tmp/foo",
						StartedAt: now, LastAt: now,
						Path: hitPath, FileSize: hitInfo.Size(), FileModTime: hitInfo.ModTime(),
						Snippet: "fresh needle row", SearchText: "fresh needle row",
					},
					{
						ID: "new-miss", Harness: harness.Claude, CWD: "/tmp/foo",
						StartedAt: now.Add(-time.Minute), LastAt: now.Add(-time.Minute),
						Path: missPath, FileSize: missInfo.Size(), FileModTime: missInfo.ModTime(),
						Snippet: "fresh unrelated row", SearchText: "fresh unrelated row",
					},
				}, nil
			},
		},
	}
	m := Model{
		mode:                     ModeSessionSearch,
		cfg:                      &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}},
		sessionCacheLoadInFlight: true,
		sessionLoading:           true,
		sessionSearchSeq:         11,
		sessionQuery:             "needle",
	}

	updated, cmd := m.Update(sessionCacheLoadDoneMsg{
		seq:     0,
		idx:     idx,
		readers: readers,
		results: []transcript.Result{{Meta: cachedHit}},
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("active query cache load did not return search + warm refresh batch")
	}
	top := cmd()
	batch, ok := top.(tea.BatchMsg)
	if !ok {
		t.Fatalf("cache load command produced %T, want tea.BatchMsg", top)
	}
	if len(batch) != 2 {
		t.Fatalf("cache load batch length = %d, want search + warm refresh", len(batch))
	}

	warmMsg, ok := batch[1]().(sessionWarmRefreshDoneMsg)
	if !ok {
		t.Fatalf("warm refresh command produced %T, want sessionWarmRefreshDoneMsg", warmMsg)
	}
	if warmMsg.seq != 11 {
		t.Fatalf("warm refresh seq = %d, want current query seq 11", warmMsg.seq)
	}
	if warmMsg.refreshErr != nil {
		t.Fatalf("warm refresh error: %v", warmMsg.refreshErr)
	}
	if warmMsg.searchErr != nil {
		t.Fatalf("warm refresh search error: %v", warmMsg.searchErr)
	}
	if !hasSessionResultID(warmMsg.results, "new-hit") {
		t.Fatalf("warm refresh results missing new matching transcript: %+v", warmMsg.results)
	}
	if hasSessionResultID(warmMsg.results, "new-miss") || hasSessionResultID(warmMsg.results, "cached-miss") {
		t.Fatalf("warm refresh used empty query instead of preserved text query: %+v", warmMsg.results)
	}

	updated, _ = got.Update(warmMsg)
	got = updated.(Model)
	if !hasSessionResultID(got.sessionResults, "new-hit") {
		t.Fatalf("active-query warm refresh did not publish new matching transcript: %+v", got.sessionResults)
	}
	if hasSessionResultID(got.sessionResults, "new-miss") {
		t.Fatalf("active-query warm refresh published non-matching transcript: %+v", got.sessionResults)
	}
}

func TestSessionWarmRefreshDoneUpdatesResults(t *testing.T) {
	refreshed := transcript.Result{
		Meta: transcript.SessionMeta{ID: "refreshed", Harness: harness.Claude, LastAt: time.Now()},
	}
	m := Model{
		cfg:                    sessionWarmupTestConfig(),
		sessionRefreshInFlight: true,
		sessionResults: []transcript.Result{{
			Meta: transcript.SessionMeta{ID: "old", Harness: harness.Claude},
		}},
	}

	updated, cmd := m.Update(sessionWarmRefreshDoneMsg{
		seq:     0,
		results: []transcript.Result{refreshed},
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("warm refresh done returned unexpected cmd %T", cmd)
	}
	if got.sessionRefreshInFlight {
		t.Fatal("sessionRefreshInFlight remained true after warm refresh")
	}
	if got.sessionLoading {
		t.Fatal("sessionLoading remained true after warm refresh")
	}
	if len(got.sessionResults) != 1 || got.sessionResults[0].Meta.ID != "refreshed" {
		t.Fatalf("sessionResults not updated from warm refresh: %+v", got.sessionResults)
	}
}
