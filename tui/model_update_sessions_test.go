package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
)

func TestSessionsMsg_RestoresCursorByTabIDWhenBaseDuplicates(t *testing.T) {
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
	updated, _ := m.Update(sessionsMsg{sessions: refreshed})
	m = updated.(Model)

	if got := m.filtered[m.cursor].TabID; got != 2 {
		t.Fatalf("cursor restored to tab %d, want tab 2", got)
	}
}
