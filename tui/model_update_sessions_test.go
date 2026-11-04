package tui

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

func TestSessionsMsg_RestoresCursorByTabIDWhenBaseDuplicates(t *testing.T) {
	t.Parallel()

	m := testModel()
	initial := []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Profile: "work", Account: "work", Project: "api", Harness: "claude", Directory: "/work/api"},
		{TabID: 2, WindowID: 22, Base: "api", Profile: "personal", Account: "personal", Project: "api", Harness: "claude", Directory: "/personal/api"},
	}
	m = loadSessions(m, initial)
	m.cursor = 1

	refreshed := []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Profile: "work", Account: "work", Project: "api", Harness: "claude", Directory: "/work/api"},
		{TabID: 2, WindowID: 22, Base: "api", Profile: "personal", Account: "personal", Project: "api", Harness: "claude", Directory: "/personal/api"},
	}
	m = applySessions(m, commands.SessionsMsg{Sessions: refreshed})

	if got := m.filtered[m.cursor].TabID; got != 2 {
		t.Fatalf("cursor restored to tab %d, want tab 2", got)
	}
}

func TestSessionsMsgPresentationReorderPreservesSelectionAndPreviewTarget(t *testing.T) {
	t.Parallel()

	old := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	newest := old.Add(time.Hour)
	m := loadSessions(testModel(), []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", State: "key", PresentationChangedAt: old},
		{TabID: 2, WindowID: 22, Base: "web", State: "question", PresentationChangedAt: newest},
	})
	for i := range m.items.Items {
		if s, ok := m.items.SessionAt(i); ok && s.TabID == 1 {
			m.cursor = i
		}
	}
	if m.cursor == 0 {
		t.Fatal("fixture did not initially place selected tab 1 after newer tab 2")
	}
	m.retargetPreview(11)
	previewSeq := m.previewTargetSeq

	m = applySessions(m, commands.SessionsMsg{Sessions: []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", State: "key", PresentationChangedAt: newest},
		{TabID: 2, WindowID: 22, Base: "web", State: "question", PresentationChangedAt: old},
	}})

	selected, ok := m.selectedSession()
	if !ok || selected.TabID != 1 {
		t.Fatalf("selection after recency reorder = %+v (ok=%v), want tab 1", selected, ok)
	}
	if m.cursor != 0 {
		t.Errorf("selected tab moved to cursor %d, want reordered position 0", m.cursor)
	}
	if m.previewWindowID != selected.WindowID {
		t.Errorf("preview target = window %d, want selected window %d", m.previewWindowID, selected.WindowID)
	}
	if m.previewTargetSeq != previewSeq {
		t.Errorf("unchanged preview target bumped sequence from %d to %d", previewSeq, m.previewTargetSeq)
	}
}

func TestSessionsMsg_InitialSourceTabSelectsMatchingSession(t *testing.T) {
	t.Parallel()

	m := testModel()
	m = applySessions(m, commands.SessionsMsg{
		Sessions: []session.RunningSession{
			{TabID: 1, WindowID: 11, Base: "needs-permission", Account: "work", State: "key"},
			{TabID: 2, WindowID: 22, Base: "source-tab", Account: "work"},
		},
		SelectionTabID: 2,
	})

	if got := m.filtered[m.cursor].TabID; got != 2 {
		t.Fatalf("initial cursor selected tab %d, want source tab 2", got)
	}
}

func TestSessionsMsg_UnmappedInitialSourceDoesNotOverrideSelection(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModel(), []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "first", Account: "work"},
		{TabID: 2, WindowID: 22, Base: "selected", Account: "work"},
	})
	for i := range m.filtered {
		if m.filtered[i].TabID == 2 {
			m.cursor = i
		}
	}

	m = applySessions(m, commands.SessionsMsg{
		Sessions: []session.RunningSession{
			{TabID: 1, WindowID: 11, Base: "first", Account: "work"},
			{TabID: 2, WindowID: 22, Base: "selected", Account: "work"},
		},
		SelectionTabID: 999,
	})

	if got := m.filtered[m.cursor].TabID; got != 2 {
		t.Fatalf("unmapped source changed selection to tab %d, want tab 2", got)
	}
}

func TestSessionsMsg_InitialSourceWithNoSessionsLeavesCursorUnchanged(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.cursor = 0
	m = applySessions(m, commands.SessionsMsg{SelectionTabID: 2})

	if m.cursor != 0 || len(m.filtered) != 0 {
		t.Fatalf("empty startup state = cursor %d, rows %d; want cursor 0 and no rows", m.cursor, len(m.filtered))
	}
}

func TestSessionsMsg_DuplicateSelectionRequestDoesNotStealCursorAgain(t *testing.T) {
	t.Parallel()

	sessions := []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "first", Account: "work"},
		{TabID: 2, WindowID: 22, Base: "requested", Account: "work"},
	}
	m := testModel()
	m = applySessions(m, commands.SessionsMsg{
		Sessions:       sessions,
		SelectionTabID: 2,
		SelectionToken: "request-1",
	})
	for i := range m.filtered {
		if m.filtered[i].TabID == 1 {
			m.cursor = i
		}
	}

	m = applySessions(m, commands.SessionsMsg{
		Sessions:       sessions,
		SelectionTabID: 2,
		SelectionToken: "request-1",
	})

	if got := m.filtered[m.cursor].TabID; got != 1 {
		t.Fatalf("duplicate request moved cursor to tab %d, want current tab 1", got)
	}
}
