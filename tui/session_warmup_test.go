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
	if len(batch) != 5 {
		t.Fatalf("Init batch length = %d, want 5 startup commands", len(batch))
	}

	request, ok := batch[len(batch)-1]().(dataServicesInitRequestMsg)
	if !ok {
		t.Fatalf("startup service message = %T, want dataServicesInitRequestMsg", batch[len(batch)-1]())
	}
	updated, initCmd := m.Update(request)
	m = updated.(Model)
	if initCmd == nil || !m.dataServicesInitInFlight {
		t.Fatalf("service initialization = cmd %v in-flight %v", initCmd != nil, m.dataServicesInitInFlight)
	}
	done, ok := initCmd().(dataServicesReadyMsg)
	if !ok {
		t.Fatalf("service initialization produced unexpected message")
	}
	if done.err != nil {
		t.Fatalf("service initialization error: %v", done.err)
	}
	if done.snapshot.transcriptSearchErr != nil {
		t.Fatalf("cache search error: %v", done.snapshot.transcriptSearchErr)
	}
	if done.snapshot.transcriptIndex == nil {
		t.Fatal("cache load returned nil index")
	}
	if len(done.snapshot.transcriptReaders) != 3 {
		t.Fatalf("reader count = %d, want 3", len(done.snapshot.transcriptReaders))
	}
	if len(done.snapshot.transcriptResults) != 1 {
		t.Fatalf("result count = %d, want only the visible cached session", len(done.snapshot.transcriptResults))
	}
	if got := done.snapshot.transcriptResults[0].Meta.ID; got != "new" {
		t.Fatalf("result ID = %q, want new", got)
	}
	if err := m.dataServices.Close(); err != nil {
		t.Fatalf("close data services: %v", err)
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
