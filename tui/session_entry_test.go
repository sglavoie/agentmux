package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestEnterSessionSearchStartsCacheLoadWhenCacheMissing(t *testing.T) {
	m := testModel()

	updated, cmd := m.Update(keyPress("S"))
	got := updated.(Model)

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

func TestEnterSessionSearchDuringCacheLoadDoesNotDuplicateWork(t *testing.T) {
	m := testModel()
	m.sessionCacheLoadInFlight = true

	updated, cmd := m.Update(keyPress("S"))
	got := updated.(Model)

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
	m := sessionSearchFilterModel(t)
	m.mode = ModeList
	m.sessionQuery = "needle"
	m.filterHarness = []string{harness.Claude}
	m.filterProfile = []string{"work"}
	m.sessionSearchSeq = 7
	m.sessionResults = m.sessionResults[:1]
	warmedID := m.sessionResults[0].Meta.ID

	updated, cmd := m.Update(keyPress("S"))
	got := updated.(Model)

	if cmd != nil {
		t.Fatalf("first warmed entry returned cmd %T; want immediate reuse", cmd)
	}
	if got.sessionLoading {
		t.Fatal("sessionLoading=true after warmed entry")
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
	updated, cmd = got.Update(keyPress("S"))
	got = updated.(Model)
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

func TestSessionWarmRefreshDoneDoesNotOverwriteNewerSearchResults(t *testing.T) {
	newer := transcript.Result{
		Meta:    transcript.SessionMeta{ID: "newer", Harness: harness.Claude},
		Matches: 1,
		Preview: "newer query result",
	}
	stale := transcript.Result{
		Meta:    transcript.SessionMeta{ID: "stale", Harness: harness.Claude},
		Preview: "stale refresh result",
	}
	m := Model{
		mode:                   ModeSessionSearch,
		sessionSearchSeq:       5,
		sessionRefreshInFlight: true,
		sessionLoading:         true,
		sessionResults:         []transcript.Result{newer},
	}

	updated, cmd := m.Update(sessionWarmRefreshDoneMsg{
		seq:     4,
		results: []transcript.Result{stale},
	})
	got := updated.(Model)

	if cmd != nil {
		t.Fatalf("stale warm refresh returned cmd %T; want nil", cmd)
	}
	if got.sessionRefreshInFlight {
		t.Fatal("sessionRefreshInFlight remained true after stale warm refresh")
	}
	if !got.sessionLoading {
		t.Fatal("sessionLoading was cleared even though the newer search is still in flight")
	}
	if len(got.sessionResults) != 1 || got.sessionResults[0].Meta.ID != "newer" {
		t.Fatalf("stale warm refresh overwrote newer results: %+v", got.sessionResults)
	}
}

func TestRenderSessionSearchKeepsWarmedResultsVisibleDuringBackgroundScan(t *testing.T) {
	m := sessionSearchFilterModel(t)
	m.sessionRefreshInFlight = true
	m.sessionLoading = false

	out := stripAnsi(renderSessionSearch(m))
	if !strings.Contains(out, "Past Sessions  [2 results]") {
		t.Fatalf("header did not keep warmed result count visible:\n%s", out)
	}
	if !strings.Contains(out, "scanning transcripts") {
		t.Fatalf("header did not show background scanning state:\n%s", out)
	}
	if strings.Contains(out, "(loading") {
		t.Fatalf("background scan was rendered as query loading:\n%s", out)
	}
	if !strings.Contains(out, "Foo") || !strings.Contains(out, "Bar") {
		t.Fatalf("warmed result rows disappeared during scan:\n%s", out)
	}
}

func TestSessionSearchActionsRemainAvailableDuringBackgroundScan(t *testing.T) {
	t.Run("text search", func(t *testing.T) {
		m := sessionSearchFilterModel(t)
		m.sessionRefreshInFlight = true

		updated, cmd := m.Update(keyPress("/"))
		m = updated.(Model)
		if cmd != nil {
			t.Fatalf("entering search mode returned cmd %T; want nil", cmd)
		}
		updated, cmd = m.Update(keyPress("n"))
		got := updated.(Model)
		if cmd == nil {
			t.Fatal("typing during background scan returned nil cmd; want debounce search")
		}
		if !got.sessionRefreshInFlight {
			t.Fatal("typing cleared sessionRefreshInFlight")
		}
		if !got.sessionLoading {
			t.Fatal("typing did not mark query search as loading")
		}
		if got.sessionQuery != "n" {
			t.Fatalf("sessionQuery = %q, want n", got.sessionQuery)
		}
	})

	t.Run("chip filter", func(t *testing.T) {
		m := sessionSearchFilterModel(t)
		m.sessionRefreshInFlight = true

		updated, cmd := m.Update(keyPress("h"))
		got := updated.(Model)
		if cmd != nil {
			t.Fatalf("opening chip picker returned cmd %T; want nil", cmd)
		}
		if got.mode != ModeChipPicker {
			t.Fatalf("mode = %v, want ModeChipPicker", got.mode)
		}
		if !got.sessionRefreshInFlight {
			t.Fatal("opening chip picker cleared sessionRefreshInFlight")
		}
	})

	t.Run("viewer entry", func(t *testing.T) {
		m := sessionSearchFilterModel(t)
		m.sessionRefreshInFlight = true
		m.transcriptReaders = map[string]transcript.Reader{
			harness.Claude: sessionSearchTestReader{harness: harness.Claude},
		}

		updated, cmd := m.Update(keyPress("enter"))
		got := updated.(Model)
		if cmd == nil {
			t.Fatal("entering viewer during background scan returned nil cmd; want async read")
		}
		if got.mode != ModeSessionView {
			t.Fatalf("mode = %v, want ModeSessionView", got.mode)
		}
		if got.viewerSession == nil || got.viewerSession.ID != "hit" {
			t.Fatalf("viewerSession = %+v, want hit", got.viewerSession)
		}
		if !got.sessionRefreshInFlight {
			t.Fatal("entering viewer cleared sessionRefreshInFlight")
		}
	})

	t.Run("delete prompt", func(t *testing.T) {
		m := sessionSearchFilterModel(t)
		m.sessionRefreshInFlight = true

		updated, cmd := m.Update(keyPress("d"))
		got := updated.(Model)
		if cmd != nil {
			t.Fatalf("opening delete prompt returned cmd %T; want nil", cmd)
		}
		if got.mode != ModeDeleteChoice {
			t.Fatalf("mode = %v, want ModeDeleteChoice", got.mode)
		}
		if got.deleteTarget == nil || got.deleteTarget.ID != "hit" {
			t.Fatalf("deleteTarget = %+v, want hit", got.deleteTarget)
		}
		if !got.sessionRefreshInFlight {
			t.Fatal("opening delete prompt cleared sessionRefreshInFlight")
		}
	})
}

func TestRenderSessionSearchDistinguishesStatusAndErrors(t *testing.T) {
	m := Model{
		mode:                     ModeSessionSearch,
		width:                    100,
		height:                   30,
		sessionLoading:           true,
		sessionCacheLoadInFlight: true,
		sessionRefreshInFlight:   true,
		sessionRefreshErr:        errors.New("cache unavailable"),
		sessionSearchErr:         errors.New("sidecar denied"),
	}

	out := stripAnsi(renderSessionSearch(m))
	for _, want := range []string{
		"loading cache",
		"scanning transcripts",
		"cache load error: cache unavailable",
		"search error: sidecar denied",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in render output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "refresh error: cache unavailable") {
		t.Fatalf("cache load failure was rendered as a refresh error:\n%s", out)
	}

	m = sessionSearchFilterModel(t)
	m.sessionLoading = true
	m.sessionRefreshErr = errors.New("scan denied")
	m.sessionSearchErr = errors.New("query failed")
	out = stripAnsi(renderSessionSearch(m))
	for _, want := range []string{
		"searching",
		"refresh error: scan denied",
		"search error: query failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in render output:\n%s", want, out)
		}
	}
}
