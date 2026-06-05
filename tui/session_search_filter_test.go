package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// writeTranscriptIndex serializes a hand-crafted index to disk so a TUI
// test can load it via transcript.LoadIndexAt without exporting the
// transcript package's private state. Returns the loaded *Index.
func writeTranscriptIndex(t *testing.T, entries map[string]transcript.SessionMeta) *transcript.Index {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcripts.json")
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
	idx, err := transcript.LoadIndexAt(path)
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	return idx
}

type sessionSearchTestReader struct {
	harness  string
	discover func(homeDir string, opts transcript.DiscoverOptions) ([]transcript.SessionMeta, error)
}

func (r sessionSearchTestReader) Harness() string { return r.harness }
func (r sessionSearchTestReader) Discover(homeDir string, opts transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	if r.discover != nil {
		return r.discover(homeDir, opts)
	}
	return nil, nil
}
func (r sessionSearchTestReader) ReadMessages(string) ([]transcript.Message, error) {
	return nil, nil
}

// sessionSearchFilterModel returns a Model preloaded with two index entries
// — one whose search sidecar contains "needle", one that does not — and
// placed directly into ModeSessionSearch with the empty-query result set
// already populated.
func sessionSearchFilterModel(t *testing.T) Model {
	t.Helper()
	now := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	hit := transcript.SessionMeta{
		ID: "hit", Harness: harness.Claude, Profile: "work", Project: "Foo",
		MessageCount: 2,
		Snippet:      "alpha needle beta", LastAt: now,
	}
	miss := transcript.SessionMeta{
		ID: "miss", Harness: harness.Claude, Profile: "work", Project: "Bar",
		MessageCount: 1,
		Snippet:      "no match here", LastAt: now.Add(-time.Hour),
	}
	idx := writeTranscriptIndex(t, map[string]transcript.SessionMeta{
		"claude:hit":  hit,
		"claude:miss": miss,
	})
	if err := idx.WriteSearchText("claude:hit", "alpha needle beta"); err != nil {
		t.Fatalf("WriteSearchText hit: %v", err)
	}
	if err := idx.WriteSearchText("claude:miss", "no match here"); err != nil {
		t.Fatalf("WriteSearchText miss: %v", err)
	}
	// Empty readers map is sufficient because both entries have search
	// sidecars (the fast path skips reader lookups entirely).
	m := Model{
		mode:              ModeSessionSearch,
		width:             100,
		height:            30,
		cfg:               &config.Config{},
		transcriptIndex:   idx,
		transcriptReaders: map[string]transcript.Reader{},
		sessionResults: []transcript.Result{
			{Meta: hit, Preview: hit.Snippet},
			{Meta: miss, Preview: miss.Snippet},
		},
	}
	return m
}

// drainCmd executes a tea.Cmd and returns the resulting message. Batched
// commands are flattened so tests can assert against a single concrete msg.
func drainCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("drainCmd: nil cmd")
	}
	return cmd()
}

// TestSessionSearch_TypingShrinksResultSet drives the dispatch chain end to
// end: ModeSessionSearch → "/" → "n" → debounce → search → done. After the
// final sessionSearchDoneMsg the model's sessionResults must contain only
// the entry whose search sidecar matches the typed needle. Guards against
// future regressions to the seq-dispatch glue between
// dispatchSessionSearch, sessionSearchDebounceMsg, and sessionSearchDoneMsg.
func TestSessionSearch_TypingShrinksResultSet(t *testing.T) {
	m := sessionSearchFilterModel(t)
	if len(m.sessionResults) != 2 {
		t.Fatalf("setup: want 2 results, got %d", len(m.sessionResults))
	}

	// `/` flips into search-text editing mode.
	m = pressKey(m, "/")
	if !m.sessionSearchMode {
		t.Fatalf("after `/`: sessionSearchMode=false, want true")
	}

	// Type "needle" one character at a time. Each keystroke bumps
	// sessionSearchSeq; the returned tea.Cmd is a debounce tick we drop
	// because the user outpaces the debounce window in the live runtime.
	for _, r := range "needle" {
		m = pressKey(m, string(r))
	}
	if m.sessionQuery != "needle" {
		t.Fatalf("sessionQuery=%q, want %q", m.sessionQuery, "needle")
	}

	// The real dispatch path waits 250ms inside tea.Tick before emitting
	// sessionSearchDebounceMsg. We synthesize the post-tick msg directly
	// with the live seq so the test does not pay the debounce in wall-clock
	// time.
	debounceMsg := sessionSearchDebounceMsg{seq: m.sessionSearchSeq}

	// Feed the debounce msg through Update; that handler returns the
	// runSessionSearchCmd which actually calls transcript.Search.
	updated, cmd := m.Update(debounceMsg)
	m = updated.(Model)
	doneMsg, ok := drainCmd(t, cmd).(sessionSearchDoneMsg)
	if !ok {
		t.Fatalf("debounce dispatch produced %T, want sessionSearchDoneMsg", doneMsg)
	}
	if doneMsg.err != nil {
		t.Fatalf("search returned error: %v", doneMsg.err)
	}

	// Final state: m.sessionResults should be the filtered set.
	updated, _ = m.Update(doneMsg)
	m = updated.(Model)
	if len(m.sessionResults) != 1 {
		t.Fatalf("after done: len(sessionResults)=%d, want 1", len(m.sessionResults))
	}
	if m.sessionResults[0].Meta.ID != "hit" {
		t.Errorf("filtered result ID=%q, want hit", m.sessionResults[0].Meta.ID)
	}
	if m.sessionLoading {
		t.Errorf("sessionLoading still true after done msg")
	}
}

// TestSessionSearch_StaleRefreshDoesNotOverwriteTypedResults locks in the
// session-1 root-cause shape. A typed-query result set is in place; then a
// late sessionsRefreshDoneMsg arrives bearing an older seq. The refresh
// handler must drop it (msg.seq < m.sessionSearchSeq) instead of
// dispatching a fresh empty-query search that would overwrite the
// typed-query results.
func TestSessionSearch_StaleRefreshDoesNotOverwriteTypedResults(t *testing.T) {
	m := sessionSearchFilterModel(t)

	// Simulate the post-typing state: query set, seq advanced, results
	// reflect the filtered set.
	m.sessionQuery = "needle"
	m.sessionSearchSeq = 5
	m.sessionResults = []transcript.Result{
		{Meta: transcript.SessionMeta{ID: "hit", Harness: harness.Claude}, Matches: 1, Preview: "alpha needle beta"},
	}

	// A stale refresh from before the user started typing arrives.
	updated, cmd := m.Update(sessionsRefreshDoneMsg{seq: 1, err: nil})
	m = updated.(Model)
	if cmd != nil {
		t.Errorf("stale refresh produced a follow-up cmd; want nil so the typed query stands")
	}
	if len(m.sessionResults) != 1 || m.sessionResults[0].Meta.ID != "hit" {
		t.Errorf("typed-query results were overwritten by stale refresh: %+v", m.sessionResults)
	}
}

func TestSessionSearchDeleteChoiceInvalidatesLateWarmRefresh(t *testing.T) {
	for _, tc := range []struct {
		name       string
		key        string
		wantEntry  bool
		wantHidden bool
		wantToast  string
	}{
		{
			name:       "hide from index",
			key:        "i",
			wantEntry:  true,
			wantHidden: true,
			wantToast:  "Hid",
		},
		{
			name:      "delete entry",
			key:       "f",
			wantEntry: false,
			wantToast: "Deleted",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sessionSearchFilterModel(t)
			target := m.sessionResults[0].Meta
			staleSeq := m.sessionSearchSeq
			m.sessionRefreshInFlight = true
			m.mode = ModeDeleteChoice
			m.deleteTarget = &target

			updated, _ := m.Update(keyPress(tc.key))
			m = updated.(Model)
			if m.mode != ModeSessionSearch {
				t.Fatalf("mode = %v, want ModeSessionSearch", m.mode)
			}
			if !m.sessionLoading {
				t.Fatal("delete choice did not mark the follow-up search as loading")
			}
			if m.sessionSearchSeq <= staleSeq {
				t.Fatalf("sessionSearchSeq = %d, want > stale seq %d", m.sessionSearchSeq, staleSeq)
			}
			if len(m.toasts) == 0 || !strings.Contains(m.toasts[0].Text, tc.wantToast) {
				t.Fatalf("toast = %+v, want text containing %q", m.toasts, tc.wantToast)
			}
			assertStatusBarContains(t, m, tc.wantToast)

			key := transcript.IndexKey(target.Harness, target.ID)
			gotMeta, exists := m.transcriptIndex.Get(key)
			if exists != tc.wantEntry {
				t.Fatalf("entry exists = %v, want %v", exists, tc.wantEntry)
			}
			if exists && gotMeta.HiddenFromIndex != tc.wantHidden {
				t.Fatalf("HiddenFromIndex = %v, want %v", gotMeta.HiddenFromIndex, tc.wantHidden)
			}

			results, err := transcript.Search(m.transcriptIndex, m.transcriptReaders, m.currentSessionQuery())
			if err != nil {
				t.Fatalf("follow-up search: %v", err)
			}
			updated, _ = m.Update(sessionSearchDoneMsg{
				seq:     m.sessionSearchSeq,
				results: results,
				err:     nil,
			})
			m = updated.(Model)
			if len(m.sessionResults) != 1 || m.sessionResults[0].Meta.ID != "miss" {
				t.Fatalf("follow-up search did not remove target: %+v", m.sessionResults)
			}

			updated, cmd := m.Update(sessionWarmRefreshDoneMsg{
				seq:     staleSeq,
				results: []transcript.Result{{Meta: target, Preview: "stale warm refresh row"}},
			})
			m = updated.(Model)
			if cmd != nil {
				t.Fatalf("stale warm refresh returned cmd %T; want nil", cmd)
			}
			if len(m.sessionResults) != 1 || m.sessionResults[0].Meta.ID != "miss" {
				t.Fatalf("late warm refresh resurrected deleted target: %+v", m.sessionResults)
			}
		})
	}
}

func TestSessionsRefreshCmdPrunesOldEntries(t *testing.T) {
	old := transcript.SessionMeta{
		ID:      "old",
		Harness: harness.Claude,
		LastAt:  time.Now().Add(-45 * 24 * time.Hour),
	}
	idx := writeTranscriptIndex(t, map[string]transcript.SessionMeta{})
	cfg := &config.Config{}
	cfg.Defaults.SessionArchive.KeepDays = 30
	cfg.Profiles = []config.Account{{
		Name:    "work",
		Harness: harness.Claude,
		HomeDir: t.TempDir(),
	}}
	readers := map[string]transcript.Reader{
		harness.Claude: sessionSearchTestReader{
			harness: harness.Claude,
			discover: func(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
				return []transcript.SessionMeta{old}, nil
			},
		},
	}

	msg, ok := drainCmd(t, sessionsRefreshCmd(idx, cfg, readers, 7)).(sessionsRefreshDoneMsg)
	if !ok {
		t.Fatalf("refresh cmd produced %T, want sessionsRefreshDoneMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("refresh/prune returned error: %v", msg.err)
	}
	if msg.seq != 7 {
		t.Fatalf("seq = %d, want 7", msg.seq)
	}
	meta, ok := idx.Get("claude:old")
	if !ok {
		t.Fatal("old entry missing after refresh/prune")
	}
	if !meta.HiddenFromIndex {
		t.Fatal("old entry was not hidden by refresh-time prune")
	}
}

func TestFormatSessionWhenFutureClampsToZero(t *testing.T) {
	future := time.Now().Add(5 * time.Second)
	if got := formatSessionWhen(future); got != " 0m   ago" {
		t.Fatalf("formatSessionWhen(future) = %q, want %q", got, " 0m   ago")
	}
}
