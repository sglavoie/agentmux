package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestSessionSearchResultPreservesNewerSelection(t *testing.T) {
	t.Parallel()

	m := sessionSearchFilterModel(t)
	m.sessionSearchSeq = 3
	m.sessionCursor = 1
	selectedID := m.sessionResults[m.sessionCursor].Meta.ID

	updated, _ := m.Update(sessionSearchDoneMsg{
		seq: 3,
		results: []transcript.Result{
			m.sessionResults[1],
			m.sessionResults[0],
		},
	})
	got := updated.(Model)
	if got.sessionCursor != 0 || got.sessionResults[got.sessionCursor].Meta.ID != selectedID {
		t.Fatalf("selection moved after reordered result: cursor=%d results=%+v", got.sessionCursor, got.sessionResults)
	}
}

func TestSessionViewerStaleResultCannotOverwriteNewerSelection(t *testing.T) {
	t.Parallel()

	m := Model{
		mode:             ModeSessionView,
		viewerSession:    &transcript.SessionMeta{ID: "newer", Harness: harness.Claude},
		viewerMessages:   []transcript.Message{{Text: "newer selection"}},
		viewerLoading:    true,
		viewerLoadSeq:    2,
		viewerCursor:     0,
		viewerMatchIdx:   0,
		viewerMatchLines: []int{0},
	}
	updated, cmd := m.Update(viewerMessagesLoadedMsg{
		seq:      1,
		messages: []transcript.Message{{Text: "stale selection"}},
	})
	got := updated.(Model)
	if cmd != nil || !got.viewerLoading || got.viewerSession.ID != "newer" || len(got.viewerMessages) != 1 || got.viewerMessages[0].Text != "newer selection" {
		t.Fatalf("stale viewer result changed newer selection: %+v", got)
	}
}

func TestRenderSessionSearchKeepsWarmedResultsVisibleDuringBackgroundScan(t *testing.T) {
	t.Parallel()

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
	if !strings.Contains(renderSessionSearch(m), styleBanner.Render("scanning transcripts…")) {
		t.Fatalf("header scanning state was not rendered with banner emphasis")
	}
	if strings.Contains(out, "(loading") {
		t.Fatalf("background scan was rendered as query loading:\n%s", out)
	}
	if !strings.Contains(out, "Foo") || !strings.Contains(out, "Bar") {
		t.Fatalf("warmed result rows disappeared during scan:\n%s", out)
	}
}

func TestSessionSearchActionsRemainAvailableDuringBackgroundScan(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
		"loading past sessions",
		"scanning transcripts",
		"Cache load error: cache unavailable",
		"Search error: sidecar denied",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in render output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Refresh error: cache unavailable") {
		t.Fatalf("cache load failure was rendered as a refresh error:\n%s", out)
	}

	m = sessionSearchFilterModel(t)
	m.sessionLoading = true
	m.sessionRefreshErr = errors.New("scan denied")
	m.sessionSearchErr = errors.New("query failed")
	out = stripAnsi(renderSessionSearch(m))
	for _, want := range []string{
		"searching",
		"Refresh error: scan denied",
		"Search error: query failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in render output:\n%s", want, out)
		}
	}
}
