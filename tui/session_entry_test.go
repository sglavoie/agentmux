package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/launch"
)

func TestEnterSessionSearchStartsCacheLoadWhenCacheMissing(t *testing.T) {
	t.Parallel()

	m := testModel()

	got, cmd := runDashboardCommand(t, m, commands.SearchAllSessions)

	if got.mode != ModeSessionSearch {
		t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
	}
	if cmd == nil {
		t.Fatal("entering without a transcript cache returned nil cmd; want cache load")
	}
	if !got.sessionCacheLoadInFlight {
		t.Fatal("sessionCacheLoadInFlight=false after starting cache load")
	}
	if !got.sessionLoading {
		t.Fatal("sessionLoading=false with no cached rows available")
	}
	if got.sessionRefreshInFlight {
		t.Fatal("sessionRefreshInFlight=true before cache load completed")
	}
	if got.sessionSearchSeq != m.sessionSearchSeq {
		t.Fatalf("sessionSearchSeq changed on cache-load entry: got %d want %d", got.sessionSearchSeq, m.sessionSearchSeq)
	}
}

func TestLegacySessionSearchKeepsEnterViewerAndAddsContinuityActions(t *testing.T) {
	t.Parallel()

	meta := transcript.SessionMeta{ID: "source-1", Harness: harness.Claude, Profile: "work", Project: "api"}
	base := Model{
		mode:                   ModeSessionSearch,
		cfg:                    &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude}, {Name: "target", Harness: harness.Codex}}},
		transcriptReaders:      map[string]transcript.Reader{harness.Claude: transcript.NewClaudeReader()},
		sessionResults:         []transcript.Result{{Meta: meta}},
		sessionRefreshInFlight: true,
	}

	updated, cmd := base.Update(keyPress("enter"))
	if got := updated.(Model); got.mode != ModeSessionView || cmd == nil {
		t.Fatalf("enter = mode %v cmd %T, want transcript viewer with read cmd", got.mode, cmd)
	}
	for _, key := range []string{"c", "f"} {
		updated, cmd := base.Update(keyPress(key))
		got := updated.(Model)
		if got.mode != ModeSessionSearch || cmd == nil {
			t.Fatalf("%s = mode %v cmd %T, want continuity command without leaving search", key, got.mode, cmd)
		}
	}
	updated, cmd = base.Update(keyPress("w"))
	got := updated.(Model)
	if got.mode != ModeConfirm || cmd != nil || got.confirmAction != "fork worktree" || got.isolatedForkConversation == nil || got.isolatedForkConversation.ID != meta.ID {
		t.Fatalf("w = mode %v action %q conversation=%+v cmd=%T, want isolated-fork confirmation", got.mode, got.confirmAction, got.isolatedForkConversation, cmd)
	}
	updated, cmd = got.Update(keyPress("n"))
	got = updated.(Model)
	if got.mode != ModeSessionSearch || got.isolatedForkConversation != nil || cmd != nil {
		t.Fatalf("cancel worktree fork = mode %v conversation=%+v cmd=%T, want search with cleared request", got.mode, got.isolatedForkConversation, cmd)
	}
	for _, tc := range []struct {
		key      string
		wantEdit bool
	}{
		{key: "H"},
		{key: "E", wantEdit: true},
	} {
		updated, cmd := base.Update(keyPress(tc.key))
		got := updated.(Model)
		if got.mode != ModeConfigPicker || cmd != nil {
			t.Fatalf("%s = mode %v cmd %T, want profile picker without launch", tc.key, got.mode, cmd)
		}
		if got.configPickerPurpose != launch.ConfigPickerHandoff || got.handoffConversation == nil || got.handoffConversation.ID != meta.ID || got.handoffEdit != tc.wantEdit {
			t.Fatalf("%s handoff picker = %+v, want source handoff with edit=%v", tc.key, got, tc.wantEdit)
		}
	}

	footer := stripAnsi(renderFooter(base))
	for _, want := range []string{"enter view", "c continue", "f fork", "w worktree", "H handoff", "E edit handoff"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer = %q, want %q", footer, want)
		}
	}
}

func TestEnterSessionSearchDuringCacheLoadDoesNotDuplicateWork(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.sessionCacheLoadInFlight = true

	got, cmd := runDashboardCommand(t, m, commands.SearchAllSessions)

	if got.mode != ModeSessionSearch {
		t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
	}
	if cmd != nil {
		t.Fatalf("entering during cache load returned duplicate cmd %T", cmd)
	}
	if !got.sessionCacheLoadInFlight {
		t.Fatal("sessionCacheLoadInFlight was cleared while entering")
	}
	if !got.sessionLoading {
		t.Fatal("sessionLoading=false while waiting for the first cache result set")
	}
}

func TestEnterSessionSearchReusesWarmedResultsOnRepeatedEntry(t *testing.T) {
	t.Parallel()

	m := sessionSearchFilterModel(t)
	m.mode = ModeList
	m.sessionQuery = "needle"
	m.filterHarness = []string{harness.Claude}
	m.filterProfile = []string{"work"}
	m.sessionSearchSeq = 7
	m.sessionResults = m.sessionResults[:1]
	warmedID := m.sessionResults[0].Meta.ID

	entered, cmd := m.enterSessionSearch()
	got := entered.(Model)

	if cmd == nil {
		t.Fatal("first warmed entry returned nil cmd; want background refresh")
	}
	if got.sessionLoading {
		t.Fatal("sessionLoading=true after warmed entry")
	}
	if !got.sessionRefreshInFlight {
		t.Fatal("sessionRefreshInFlight=false after warmed entry")
	}
	if got.sessionSearchSeq != 7 {
		t.Fatalf("sessionSearchSeq = %d, want unchanged 7", got.sessionSearchSeq)
	}
	if got.sessionQuery != "needle" {
		t.Fatalf("sessionQuery = %q, want preserved query", got.sessionQuery)
	}
	if singleChipValue(got.filterHarness) != harness.Claude || singleChipValue(got.filterProfile) != "work" {
		t.Fatalf("chip filters were not preserved: harness=%v profile=%v", got.filterHarness, got.filterProfile)
	}
	if len(got.sessionResults) != 1 || got.sessionResults[0].Meta.ID != warmedID {
		t.Fatalf("warmed results changed on entry: %+v", got.sessionResults)
	}

	got.mode = ModeList
	entered, cmd = got.enterSessionSearch()
	got = entered.(Model)
	if cmd != nil {
		t.Fatalf("repeated warmed entry returned cmd %T; want no duplicate refresh", cmd)
	}
	if got.sessionSearchSeq != 7 {
		t.Fatalf("sessionSearchSeq changed on repeated entry: got %d want 7", got.sessionSearchSeq)
	}
	if len(got.sessionResults) != 1 || got.sessionResults[0].Meta.ID != warmedID {
		t.Fatalf("warmed results changed on repeated entry: %+v", got.sessionResults)
	}
}

func TestEnterSessionSearchWithWarmCacheRefreshesAndShowsNewTranscript(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	home := filepath.Join(tmp, "claude-home")
	transcriptPath := filepath.Join(home, "projects", "-tmp-foo", "sess-new.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	info, err := os.Stat(transcriptPath)
	if err != nil {
		t.Fatalf("stat transcript: %v", err)
	}

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	warmed := transcript.SessionMeta{
		ID: "old", Harness: harness.Claude, Profile: "work", Project: "Foo",
		MessageCount: 1,
		Snippet:      "old cached row", LastAt: now.Add(-time.Hour),
	}
	idx := writeTranscriptIndex(t, map[string]transcript.SessionMeta{
		"claude:old": warmed,
	})
	readers := map[string]transcript.Reader{
		harness.Claude: sessionSearchTestReader{
			harness: harness.Claude,
			discover: func(homeDir string, _ transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
				if homeDir != home {
					t.Errorf("Discover homeDir = %q, want %q", homeDir, home)
				}
				return []transcript.SessionMeta{{
					ID:           "new",
					Harness:      harness.Claude,
					CWD:          "/tmp/foo",
					StartedAt:    now,
					LastAt:       now,
					Path:         transcriptPath,
					FileSize:     info.Size(),
					FileModTime:  info.ModTime(),
					MessageCount: 2,
					Snippet:      "newly written row",
				}}, nil
			},
		},
	}
	m := Model{
		mode:              ModeList,
		width:             100,
		height:            30,
		cfg:               &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}},
		transcriptIndex:   idx,
		transcriptReaders: readers,
		sessionResults:    []transcript.Result{{Meta: warmed, Preview: warmed.Snippet}},
	}

	got, cmd := runDashboardCommand(t, m, commands.SearchAllSessions)
	if cmd == nil {
		t.Fatal("warm Past Sessions re-entry returned nil cmd; want background refresh for newly written transcripts")
	}
	if got.mode != ModeSessionSearch {
		t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
	}
	if !got.sessionRefreshInFlight {
		t.Fatal("warm Past Sessions re-entry did not mark refresh in flight")
	}
	if got.sessionLoading {
		t.Fatal("warm Past Sessions re-entry hid warmed rows behind a loading state")
	}
	if len(got.sessionResults) != 1 || got.sessionResults[0].Meta.ID != "old" {
		t.Fatalf("warmed rows changed before refresh finished: %+v", got.sessionResults)
	}

	msg := drainCmd(t, cmd)
	switch msg := msg.(type) {
	case sessionWarmRefreshDoneMsg:
		if msg.refreshErr != nil {
			t.Fatalf("warm refresh error: %v", msg.refreshErr)
		}
		if msg.searchErr != nil {
			t.Fatalf("warm refresh search error: %v", msg.searchErr)
		}
		updated, _ := got.Update(msg)
		got = updated.(Model)
	case sessionsRefreshDoneMsg:
		if msg.err != nil {
			t.Fatalf("refresh error: %v", msg.err)
		}
		updated, searchCmd := got.Update(msg)
		got = updated.(Model)
		doneMsg, ok := drainCmd(t, searchCmd).(sessionSearchDoneMsg)
		if !ok {
			t.Fatalf("refresh follow-up produced %T, want sessionSearchDoneMsg", doneMsg)
		}
		if doneMsg.err != nil {
			t.Fatalf("refresh follow-up search error: %v", doneMsg.err)
		}
		updated, _ = got.Update(doneMsg)
		got = updated.(Model)
	default:
		t.Fatalf("warm Past Sessions re-entry produced %T, want refresh completion message", msg)
	}

	if got.sessionRefreshInFlight {
		t.Fatal("sessionRefreshInFlight remained true after refresh completion")
	}
	if !hasSessionResultID(got.sessionResults, "new") {
		t.Fatalf("refreshed results missing newly written transcript: %+v", got.sessionResults)
	}
}

func TestEnterSessionSearchWithPreservedQueryRefreshesMatchingRows(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	home := filepath.Join(tmp, "claude-home")
	transcriptPath := filepath.Join(home, "projects", "-tmp-foo", "sess-new.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	info, err := os.Stat(transcriptPath)
	if err != nil {
		t.Fatalf("stat transcript: %v", err)
	}

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	warmed := transcript.SessionMeta{
		ID: "old", Harness: harness.Claude, Profile: "work", Project: "Foo",
		MessageCount: 1,
		Snippet:      "old cached needle row", LastAt: now.Add(-time.Hour),
	}
	idx := writeTranscriptIndex(t, map[string]transcript.SessionMeta{
		"claude:old": warmed,
	})
	if err := idx.WriteSearchText("claude:old", "old cached needle row"); err != nil {
		t.Fatalf("WriteSearchText old: %v", err)
	}
	readers := map[string]transcript.Reader{
		harness.Claude: sessionSearchTestReader{
			harness: harness.Claude,
			discover: func(homeDir string, _ transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
				if homeDir != home {
					t.Errorf("Discover homeDir = %q, want %q", homeDir, home)
				}
				return []transcript.SessionMeta{{
					ID:           "new",
					Harness:      harness.Claude,
					CWD:          "/tmp/foo",
					StartedAt:    now,
					LastAt:       now,
					Path:         transcriptPath,
					FileSize:     info.Size(),
					FileModTime:  info.ModTime(),
					MessageCount: 2,
					Snippet:      "fresh needle row",
					SearchText:   "fresh needle row",
				}}, nil
			},
		},
	}
	m := Model{
		mode:              ModeList,
		width:             100,
		height:            30,
		cfg:               &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}},
		transcriptIndex:   idx,
		transcriptReaders: readers,
		sessionQuery:      "needle",
		sessionSearchSeq:  7,
		sessionResults:    []transcript.Result{{Meta: warmed, Preview: warmed.Snippet}},
	}

	entered, cmd := m.enterSessionSearch()
	got := entered.(Model)
	if cmd == nil {
		t.Fatal("warm Past Sessions re-entry returned nil cmd; want background refresh")
	}
	if got.sessionLoading {
		t.Fatal("preserved-query warm entry hid warmed rows behind a loading state")
	}
	if len(got.sessionResults) != 1 || got.sessionResults[0].Meta.ID != "old" {
		t.Fatalf("warmed rows changed before refresh finished: %+v", got.sessionResults)
	}

	msg, ok := drainCmd(t, cmd).(sessionWarmRefreshDoneMsg)
	if !ok {
		t.Fatalf("warm Past Sessions re-entry produced %T, want sessionWarmRefreshDoneMsg", msg)
	}
	if msg.seq != 7 {
		t.Fatalf("warm refresh seq = %d, want preserved query seq 7", msg.seq)
	}
	if msg.refreshErr != nil {
		t.Fatalf("warm refresh error: %v", msg.refreshErr)
	}
	if msg.searchErr != nil {
		t.Fatalf("warm refresh search error: %v", msg.searchErr)
	}
	if !hasSessionResultID(msg.results, "new") {
		t.Fatalf("warm refresh query results missing newly matching transcript: %+v", msg.results)
	}

	updated, _ := got.Update(msg)
	got = updated.(Model)
	if !hasSessionResultID(got.sessionResults, "new") {
		t.Fatalf("refreshed preserved-query results missing new transcript: %+v", got.sessionResults)
	}
}

func hasSessionResultID(results []transcript.Result, id string) bool {
	for _, result := range results {
		if result.Meta.ID == id {
			return true
		}
	}
	return false
}
