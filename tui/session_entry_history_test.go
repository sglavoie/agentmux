package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// historyModel is a history view opened on a conversation, before its cache
// load has answered, beside the fixture whose index and rows it will receive:
// "hit" is the newest row and "miss" the one under it.
func historyModel(t *testing.T, sessionID string) (Model, Model) {
	t.Helper()
	fixture := sessionSearchFilterModel(t)
	m := NewHistoryModel(testModel().cfg, nil, conversation.Identity{Harness: harness.Claude, SessionID: sessionID})
	m.width, m.height = fixture.width, fixture.height
	return m, fixture
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestHistoryViewStartsInPastSessionsOnTheStartupCacheLoad(t *testing.T) {
	t.Parallel()

	m, _ := historyModel(t, "miss")
	if m.mode != ModeSessionSearch || !m.sessionLoading || !sessionQueryIsEmpty(m.currentSessionQuery()) {
		t.Fatalf("mode=%v loading=%v query=%+v, want unfiltered Past Sessions loading", m.mode, m.sessionLoading, m.currentSessionQuery())
	}
	if !m.sessionCacheLoadInFlight {
		t.Fatal("the view did not join the cache load Init dispatches")
	}
	// Entering again while that load runs starts no second one.
	if _, cmd := m.enterAllSessionSearch(); cmd != nil {
		t.Fatal("a second cache load would start beside the startup one")
	}
}

func TestHistoryViewSelectsTheWantedConversationWhenResultsPublish(t *testing.T) {
	t.Parallel()

	m, fixture := historyModel(t, "miss")
	m = send(m, sessionCacheLoadDoneMsg{seq: m.sessionSearchSeq, idx: fixture.transcriptIndex, readers: fixture.transcriptReaders, results: fixture.sessionResults})
	if m.sessionCursor != 1 || m.sessionResults[m.sessionCursor].Meta.ID != "miss" {
		t.Fatalf("cursor = %d, want the wanted conversation", m.sessionCursor)
	}
	if m.sessionWantedKey != "" {
		t.Fatal("the wanted conversation was not spent once found")
	}
	// Later results follow the selection, not the conversation asked for.
	m = pressKey(m, "k")
	m = send(m, sessionWarmRefreshDoneMsg{seq: m.sessionSearchSeq, results: fixture.sessionResults})
	if m.sessionCursor != 0 {
		t.Fatalf("cursor = %d after the operator moved, want it left at 0", m.sessionCursor)
	}
}

func TestHistoryViewFindsAConversationOnlyTheRefreshLists(t *testing.T) {
	t.Parallel()

	m, fixture := historyModel(t, "miss")
	m = send(m, sessionCacheLoadDoneMsg{seq: m.sessionSearchSeq, idx: fixture.transcriptIndex, readers: fixture.transcriptReaders, results: fixture.sessionResults[:1]})
	if m.sessionCursor != 0 || m.sessionWantedKey == "" {
		t.Fatalf("cursor=%d wanted=%q, want the top row and the conversation still wanted", m.sessionCursor, m.sessionWantedKey)
	}
	m = send(m, sessionWarmRefreshDoneMsg{seq: m.sessionSearchSeq, results: fixture.sessionResults})
	if m.sessionCursor != 1 {
		t.Fatalf("cursor = %d, want the conversation the refresh found", m.sessionCursor)
	}
}

func TestHistoryViewLeavesTheCursorAtTheTopForAnAbsentConversation(t *testing.T) {
	t.Parallel()

	m, fixture := historyModel(t, "gone")
	m = send(m, sessionCacheLoadDoneMsg{seq: m.sessionSearchSeq, idx: fixture.transcriptIndex, readers: fixture.transcriptReaders, results: fixture.sessionResults})
	m = send(m, sessionWarmRefreshDoneMsg{seq: m.sessionSearchSeq, results: fixture.sessionResults})
	if m.sessionCursor != 0 || len(m.sessionResults) != 2 {
		t.Fatalf("cursor=%d rows=%d, want the top of the full list", m.sessionCursor, len(m.sessionResults))
	}
}

func TestHistoryViewQuitsInsteadOfShowingTheSessionList(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"esc", "q"} {
		m, _ := historyModel(t, "miss")
		updated, cmd := m.Update(keyPress(key))
		if !isQuit(cmd) {
			t.Fatalf("%s did not quit the history view (mode %v)", key, updated.(Model).mode)
		}
	}
	// The ordinary dashboard still goes back to its list.
	list := sessionSearchFilterModel(t)
	updated, cmd := list.Update(keyPress("esc"))
	if isQuit(cmd) || updated.(Model).mode != ModeList {
		t.Fatal("leaving Past Sessions on the dashboard no longer returns to the list")
	}
}

func TestHistoryViewSettlesOnPastSessionsWhereTheDashboardSettlesOnItsList(t *testing.T) {
	t.Parallel()

	m, fixture := historyModel(t, "miss")
	m = send(m, sessionCacheLoadDoneMsg{seq: m.sessionSearchSeq, idx: fixture.transcriptIndex, readers: fixture.transcriptReaders, results: fixture.sessionResults})
	m.openCommandPalette()
	updated, cmd := m.Update(keyPress("esc"))
	got := updated.(Model)
	if isQuit(cmd) || got.mode != ModeSessionSearch {
		t.Fatalf("closing the palette: mode=%v quit=%v, want Past Sessions kept open", got.mode, isQuit(cmd))
	}
	if got.sessionCursor != 1 {
		t.Fatalf("cursor = %d, want the selection kept", got.sessionCursor)
	}
}

func TestNativeHistoryQuerySurvivesInitialCacheAndSelectsTheConversation(t *testing.T) {
	fixture := sessionSearchFilterModel(t)
	m := NewHistoryModelWithQuery(testModel().cfg, nil, conversation.Identity{Harness: harness.Claude, SessionID: "hit"}, "needle")
	updated, cmd := m.handleSessionCacheLoadDone(sessionCacheLoadDoneMsg{seq: m.sessionSearchSeq, idx: fixture.transcriptIndex, readers: fixture.transcriptReaders, results: fixture.sessionResults})
	m = updated.(Model)
	if m.sessionQuery != "needle" || m.sessionWantedKey != "claude:hit" || cmd == nil {
		t.Fatalf("query/selection was lost: %q %q", m.sessionQuery, m.sessionWantedKey)
	}
	// Exercise the same indexed search the pending command dispatches.
	results, err := transcript.Search(fixture.transcriptIndex, fixture.transcriptReaders, m.currentSessionQuery())
	if err != nil {
		t.Fatal(err)
	}
	m.publishSessionResults(results)
	m.width, m.height = 120, 30
	if !strings.Contains(stripAnsi(renderSessionSearch(m)), "Conversation Actions") {
		t.Fatal("exact conversation scope must be visible")
	}
	if len(m.sessionResults) != 1 || m.sessionResults[m.sessionCursor].Meta.ID != "hit" {
		t.Fatal("transferred query did not select its matching conversation")
	}
}
