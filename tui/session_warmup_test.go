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
	for key, meta := range entries {
		if meta.SessionClass == "" {
			meta.SessionClass = transcript.SessionClassMain
			entries[key] = meta
		}
	}
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
	if msg.seq != 3 || msg.loadErr != nil || msg.searchErr != nil || msg.idx == nil {
		t.Fatalf("cold cache load = seq %d idx %p load %v search %v", msg.seq, msg.idx, msg.loadErr, msg.searchErr)
	}
	if msg.idx.Len() != 0 || len(msg.results) != 0 {
		t.Fatalf("cold cache snapshot = len %d results %d", msg.idx.Len(), len(msg.results))
	}

	m := Model{cfg: sessionWarmupTestConfig(), sessionCacheLoadInFlight: true, sessionLoading: true}
	updated, cmd := m.Update(msg)
	got := updated.(Model)
	if got.sessionCacheLoadInFlight || !got.sessionRefreshInFlight || got.sessionLoading || got.transcriptIndex == nil || cmd == nil {
		t.Fatalf("cold cache handoff = cache-loading %v refreshing %v loading %v index %p cmd %v", got.sessionCacheLoadInFlight, got.sessionRefreshInFlight, got.sessionLoading, got.transcriptIndex, cmd != nil)
	}
}

func TestSessionCacheLoadMalformedIndexPublishesRecoveringSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := defaultTranscriptIndexPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := drainCmd(t, sessionCacheLoadCmd(4)).(sessionCacheLoadDoneMsg)
	if msg.idx == nil || msg.loadErr == nil || msg.searchErr != nil {
		t.Fatalf("recovering cache load = idx %p load %v search %v", msg.idx, msg.loadErr, msg.searchErr)
	}
	if matches, _ := filepath.Glob(path + ".corrupt-*"); len(matches) != 1 {
		t.Fatalf("quarantined caches = %v, want one", matches)
	}

	m := Model{mode: ModeSessionSearch, cfg: sessionWarmupTestConfig(), sessionCacheLoadInFlight: true, sessionLoading: true, sessionSearchSeq: 4}
	updated, cmd := m.Update(msg)
	got := updated.(Model)
	if got.transcriptIndex == nil || got.sessionRefreshErr == nil || !got.sessionRefreshInFlight || got.sessionLoading || cmd == nil {
		t.Fatalf("recovered snapshot = index %p refreshErr %v refreshing %v loading %v cmd %v", got.transcriptIndex, got.sessionRefreshErr, got.sessionRefreshInFlight, got.sessionLoading, cmd != nil)
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
			SearchText:   "new cached session",
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
	if len(batch) != 6 {
		t.Fatalf("Init batch length = %d, want 6 startup commands", len(batch))
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
	if done.loadErr != nil || done.searchErr != nil {
		t.Fatalf("cache load errors = load %v search %v", done.loadErr, done.searchErr)
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
	databasePath := filepath.Join(home, ".local", "share", "agent-mux", "agent-mux.db")
	if _, err := os.Stat(databasePath); !os.IsNotExist(err) {
		t.Fatalf("startup created database %s: %v", databasePath, err)
	}
}

func TestSessionWarmRefreshDoneUpdatesResults(t *testing.T) {
	t.Parallel()

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
