package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/history"
)

func TestSessionMessageSourcePickerPreservesAndResets(t *testing.T) {
	m := sessionSearchFilterModel(t)
	m.mode = ModeSessionSearch
	opened, _ := m.handleSessionSearchNavKey(keyPress("m"))
	m = opened.(Model)
	if m.mode != ModeChipPicker || m.chipPickerKind != chipPickerMessageSource || len(m.chipPickerOptions) != 3 {
		t.Fatalf("picker state: %+v", m.chipPickerOptions)
	}
	m.chipPickerCursor = 2
	staleSeq := m.sessionSearchSeq
	selected, cmd := m.handleChipPickerNavKey(keyPress("enter"))
	m = selected.(Model)
	if m.filterMessageSource != "harness" || m.mode != ModeSessionSearch || cmd == nil {
		t.Fatalf("selection: source=%q mode=%v cmd=%v", m.filterMessageSource, m.mode, cmd)
	}
	if q := m.currentSessionQuery(); q.MessageSource != "harness" {
		t.Fatalf("query source=%q", q.MessageSource)
	}
	if m.sessionSearchSeq <= staleSeq {
		t.Fatal("source change did not invalidate in-flight searches")
	}
	previous := len(m.sessionResults)
	stale, _ := m.handleSessionSearchDone(sessionSearchDoneMsg{seq: staleSeq, results: []transcript.Result{{Meta: transcript.SessionMeta{ID: "stale"}}}})
	m = stale.(Model)
	if len(m.sessionResults) != previous {
		t.Fatal("stale search replaced source-filtered results")
	}
	m.exitSessionSearch()
	entered, _ := m.enterSessionSearch()
	m = entered.(Model)
	if m.filterMessageSource != "harness" {
		t.Fatalf("re-entry lost source: %q", m.filterMessageSource)
	}
	all, _ := m.enterAllSessionSearch()
	m = all.(Model)
	if m.filterMessageSource != "" {
		t.Fatalf("Search All retained source: %q", m.filterMessageSource)
	}
	m.filterMessageSource = "user"
	project, _ := m.enterSessionSearchForProject("demo")
	m = project.(Model)
	if m.filterMessageSource != "" {
		t.Fatalf("project entry retained source: %q", m.filterMessageSource)
	}
}

func TestSessionMessageSourceViewerMatches(t *testing.T) {
	m := Model{mode: ModeSessionView, sessionQuery: "needle", filterMessageSource: "harness", viewerLoadSeq: 3, width: 80, height: 24}
	loaded, _ := m.handleViewerMessagesLoaded(viewerMessagesLoadedMsg{seq: 3, messages: []transcript.Message{
		{Role: "user", Text: "needle prompt"},
		{Role: "assistant", Text: "needle reply"},
		{Role: "tool", Text: "needle output"},
	}})
	m = loaded.(Model)
	if len(m.viewerMessages) != 3 || len(m.viewerMatchLines) != 2 || m.viewerCursor != 1 {
		t.Fatalf("viewer matches=%v cursor=%d messages=%d", m.viewerMatchLines, m.viewerCursor, len(m.viewerMessages))
	}
	m.jumpToNextMatch(1)
	if m.viewerCursor != 2 {
		t.Fatalf("next match cursor=%d", m.viewerCursor)
	}
	m.jumpToNextMatch(1)
	if m.viewerCursor != 1 {
		t.Fatalf("wrapped match cursor=%d", m.viewerCursor)
	}
}

func TestSessionMessageSourceNarrowChip(t *testing.T) {
	in := history.SearchInput{Width: 26, Height: 12, BodyHeight: 10, MessageSourceChip: "harness"}
	out := stripAnsi(in.Render())
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 26 {
			t.Fatalf("line exceeds width: %q", line)
		}
	}
}
