package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/workspace"
)

func TestWorkspaceProjectionCursorFollowsWorkspaceIDAndDropsStaleResults(t *testing.T) {
	m := Model{
		workspaceProjectionSeq: 2,
		workspaceCursor:        1,
		workspaceSnapshot: workspace.Snapshot{Workspaces: []workspace.WorkspaceSnapshot{
			{ID: "alpha", DisplayName: "Alpha"},
			{ID: "bravo", DisplayName: "Bravo"},
		}},
	}

	updated, _ := m.Update(workspaceProjectionMsg{
		seq:      1,
		snapshot: workspace.Snapshot{Workspaces: []workspace.WorkspaceSnapshot{{ID: "alpha"}}},
	})
	m = updated.(Model)
	if got := selectedWorkspaceID(m.workspaceSnapshot, m.workspaceCursor); got != "bravo" {
		t.Fatalf("stale projection changed selection to %q, want bravo", got)
	}

	updated, _ = m.Update(workspaceProjectionMsg{
		seq: 2,
		snapshot: workspace.Snapshot{Workspaces: []workspace.WorkspaceSnapshot{
			{ID: "bravo", DisplayName: "Bravo"},
			{ID: "alpha", DisplayName: "Alpha"},
		}},
	})
	m = updated.(Model)
	if got := selectedWorkspaceID(m.workspaceSnapshot, m.workspaceCursor); got != "bravo" {
		t.Errorf("selection after urgency reorder = %q, want bravo", got)
	}
}

func TestSessionsRefreshStartsSequenceProtectedWorkspaceProjection(t *testing.T) {
	m := Model{cfg: &config.Config{}}
	updated, cmd := m.Update(sessionsMsg{})
	m = updated.(Model)
	if m.workspaceProjectionSeq != 1 || !m.workspaceLoading {
		t.Errorf("workspace load state = seq %d loading %v, want seq 1 and loading", m.workspaceProjectionSeq, m.workspaceLoading)
	}
	if cmd == nil {
		t.Error("sessions refresh returned nil command, want async workspace projection")
	}
}
