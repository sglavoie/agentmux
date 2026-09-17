package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/commands"
)

func TestListProjectSessionSearchWithReadyCacheDispatchesProjectFilter(t *testing.T) {
	m := sessionSearchFilterModel(t)
	m.mode = ModeList
	m = withListRows(m, session.RunningSession{TabID: 1, WindowID: 10, Base: "Foo", Profile: "work"})
	m.cursor = 0
	m.sessionResults = nil
	seq := m.sessionSearchSeq

	entered, cmd := m.Update(keyPress(keyProjectSessionSearch))
	got := entered.(Model)

	if cmd == nil {
		t.Fatal("project session search with ready cache returned nil cmd; want debounced search")
	}
	if got.mode != ModeSessionSearch {
		t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
	}
	if !got.sessionRefreshInFlight {
		t.Fatal("project session search did not start a background refresh")
	}
	if got.sessionSearchSeq != seq+1 {
		t.Fatalf("sessionSearchSeq = %d, want %d", got.sessionSearchSeq, seq+1)
	}
	if !got.sessionLoading {
		t.Fatal("project session search did not mark search loading")
	}
	if singleChipValue(got.filterProject) != "Foo" {
		t.Fatalf("filterProject = %#v, want Foo", got.filterProject)
	}
	if out := stripAnsi(renderSessionSearch(got)); !strings.Contains(out, "project: Foo") {
		t.Fatalf("project chip was not visible after project search entry:\n%s", out)
	}

	updated, searchCmd := got.Update(sessionSearchDebounceMsg{seq: got.sessionSearchSeq})
	got = updated.(Model)
	doneMsg, ok := drainCmd(t, searchCmd).(sessionSearchDoneMsg)
	if !ok {
		t.Fatalf("project debounce produced %T, want sessionSearchDoneMsg", doneMsg)
	}
	if doneMsg.err != nil {
		t.Fatalf("project-filtered search returned error: %v", doneMsg.err)
	}
	updated, _ = got.Update(doneMsg)
	got = updated.(Model)
	if len(got.sessionResults) != 1 || got.sessionResults[0].Meta.Project != "Foo" {
		t.Fatalf("project-filtered results = %+v, want only Foo", got.sessionResults)
	}
}

func TestListProjectSessionSearchUsesResolvedProjectForWorktreeRow(t *testing.T) {
	m := sessionSearchFilterModel(t)
	m.mode = ModeList
	m = withListRows(m, session.RunningSession{
		TabID:     1,
		WindowID:  10,
		Base:      "vta_ss_oidc_op_worker-security-audit",
		Project:   "sis",
		Profile:   "vta-claude",
		Directory: "/Users/sglavoie/1_dev_projects/vta_ss_oidc_op_worker-security-audit",
	})
	m.cursor = 0

	entered, cmd := m.Update(keyPress(keyProjectSessionSearch))
	got := entered.(Model)

	if cmd == nil {
		t.Fatal("project session search returned nil cmd; want debounced search")
	}
	if singleChipValue(got.filterProject) != "sis" {
		t.Fatalf("filterProject = %#v, want sis", got.filterProject)
	}
	if out := stripAnsi(renderSessionSearch(got)); !strings.Contains(out, "project: sis") {
		t.Fatalf("project chip was not visible after worktree project search entry:\n%s", out)
	}
}

func TestListProjectSessionSearchDuringCacheLoadDoesNotDuplicateWork(t *testing.T) {
	m := loadSessions(testModel(), []session.RunningSession{{TabID: 1, WindowID: 10, Base: "Foo"}})
	m.sessionCacheLoadInFlight = true
	seq := m.sessionSearchSeq

	entered, cmd := m.Update(keyPress(keyProjectSessionSearch))
	got := entered.(Model)

	if cmd != nil {
		t.Fatalf("project session search during cache load returned duplicate cmd %T", cmd)
	}
	if got.mode != ModeSessionSearch {
		t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
	}
	if !got.sessionCacheLoadInFlight {
		t.Fatal("sessionCacheLoadInFlight was cleared while entering project session search")
	}
	if !got.sessionLoading {
		t.Fatal("sessionLoading=false while waiting for first project-filtered cache result")
	}
	if got.sessionSearchSeq != seq {
		t.Fatalf("sessionSearchSeq changed during cache-load entry: got %d want %d", got.sessionSearchSeq, seq)
	}
	if singleChipValue(got.filterProject) != "Foo" {
		t.Fatalf("filterProject = %#v, want Foo", got.filterProject)
	}
}

// An empty list has no project to search: the shortcut is inert and the
// palette command is disabled.
func TestListProjectSessionSearchIsDisabledOnAnEmptyList(t *testing.T) {
	m := loadSessions(testModel(), nil)
	updated, cmd := m.Update(keyPress(keyProjectSessionSearch))
	m = updated.(Model)
	if m.mode != ModeList || cmd != nil {
		t.Fatal("project history shortcut must do nothing without a selection")
	}
	m = pressKey(m, keyColon)
	if m.mode != ModeCommandPalette {
		t.Fatalf("expected the palette to open, got mode %q", uitestModeName(m.mode))
	}
	entry := commandByID(t, commands.SearchProject)
	if availability := entry.AvailabilityFor(m, m.commandContext); availability.Available {
		t.Fatal("the project history search is available with no session selected")
	}
}

func TestListProjectSessionSearchClearsPriorFiltersAndResetsEntryState(t *testing.T) {
	m := sessionSearchFilterModel(t)
	m.mode = ModeList
	m = withListRows(m, session.RunningSession{TabID: 1, WindowID: 10, Project: "Foo"})
	m.cursor = 0
	m.sessionQuery = "needle"
	m.filterHarness = []string{harness.Claude}
	m.filterProfile = []string{"work"}
	m.filterProject = []string{"Bar"}
	m.sessionCursor = 3
	m.sessionOffset = 2
	m.sessionSearchMode = true

	entered, cmd := m.Update(keyPress(keyProjectSessionSearch))
	got := entered.(Model)

	if cmd == nil {
		t.Fatal("project session search returned nil cmd; want debounced search")
	}
	if !got.sessionRefreshInFlight {
		t.Fatal("project session search did not start a background refresh")
	}
	if got.sessionQuery != "" {
		t.Fatalf("sessionQuery = %q, want cleared", got.sessionQuery)
	}
	if len(got.filterHarness) != 0 || len(got.filterProfile) != 0 {
		t.Fatalf("prior filters were not cleared: harness=%#v profile=%#v", got.filterHarness, got.filterProfile)
	}
	if singleChipValue(got.filterProject) != "Foo" {
		t.Fatalf("filterProject = %#v, want Foo", got.filterProject)
	}
	if got.sessionCursor != 0 || got.sessionOffset != 0 || got.sessionSearchMode {
		t.Fatalf("entry state cursor=%d offset=%d searchMode=%v, want 0/0/false",
			got.sessionCursor, got.sessionOffset, got.sessionSearchMode)
	}
}

func TestGlobalSessionSearchDoesNotApplyTheProjectChip(t *testing.T) {
	m := sessionSearchFilterModel(t)
	m.mode = ModeList
	m = withListRows(m, session.RunningSession{TabID: 1, WindowID: 10, Project: "Foo"})
	m.cursor = 0

	got, cmd := runDashboardCommand(t, m, commands.SearchAllSessions)

	if cmd == nil {
		t.Fatal("the global session search returned nil cmd; want background refresh")
	}
	if got.mode != ModeSessionSearch {
		t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
	}
	if !got.sessionRefreshInFlight {
		t.Fatal("dashboard S with warmed cache did not start a background refresh")
	}
	if singleChipValue(got.filterProject) != "" {
		t.Fatalf("filterProject = %#v, want empty global project chip", got.filterProject)
	}
	if out := stripAnsi(renderSessionSearch(got)); !strings.Contains(out, "project: any") {
		t.Fatalf("global session search did not render an unfiltered project chip:\n%s", out)
	}
}

func TestSessionWarmRefreshDoneRerunsLatestProjectSearch(t *testing.T) {
	old := transcript.SessionMeta{ID: "old", Harness: harness.Claude, Project: "Foo", LastAt: time.Now().Add(-time.Hour)}
	fresh := transcript.SessionMeta{ID: "fresh", Harness: harness.Claude, Project: "Foo", LastAt: time.Now()}
	other := transcript.SessionMeta{ID: "other", Harness: harness.Claude, Project: "Bar", LastAt: time.Now()}
	idx := writeTranscriptIndex(t, map[string]transcript.SessionMeta{
		"claude:old":   old,
		"claude:fresh": fresh,
		"claude:other": other,
	})
	stale := transcript.Result{
		Meta:    transcript.SessionMeta{ID: "stale", Harness: harness.Claude},
		Preview: "stale refresh result",
	}
	m := Model{
		mode:                   ModeSessionSearch,
		sessionSearchSeq:       5,
		sessionRefreshInFlight: true,
		sessionLoading:         true,
		sessionResults:         []transcript.Result{{Meta: old}},
		filterProject:          []string{"Foo"},
		transcriptIndex:        idx,
		transcriptReaders:      map[string]transcript.Reader{},
	}

	updated, cmd := m.Update(sessionWarmRefreshDoneMsg{
		seq:     4,
		results: []transcript.Result{stale},
	})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("stale warm refresh returned nil; want latest-query follow-up")
	}
	if got.sessionRefreshInFlight {
		t.Fatal("sessionRefreshInFlight remained true after stale warm refresh")
	}
	if !got.sessionLoading {
		t.Fatal("sessionLoading was cleared even though the newer search is still in flight")
	}
	if got.sessionSearchSeq != 6 {
		t.Fatalf("sessionSearchSeq = %d, want 6", got.sessionSearchSeq)
	}
	if len(got.sessionResults) != 1 || got.sessionResults[0].Meta.ID != "old" {
		t.Fatalf("stale warm refresh overwrote newer results: %+v", got.sessionResults)
	}

	done, ok := drainCmd(t, cmd).(sessionSearchDoneMsg)
	if !ok {
		t.Fatalf("warm refresh follow-up produced %T, want sessionSearchDoneMsg", done)
	}
	updated, _ = got.Update(done)
	got = updated.(Model)
	if len(got.sessionResults) != 2 || got.sessionResults[0].Meta.ID != "fresh" || got.sessionResults[1].Meta.ID != "old" {
		t.Fatalf("latest project rows were not published after refresh: %+v", got.sessionResults)
	}

	updated, _ = got.Update(sessionSearchDoneMsg{seq: 5, results: []transcript.Result{{Meta: stale.Meta}}})
	got = updated.(Model)
	if len(got.sessionResults) != 2 || got.sessionResults[0].Meta.ID != "fresh" {
		t.Fatalf("older in-flight search overwrote refreshed rows: %+v", got.sessionResults)
	}
}
