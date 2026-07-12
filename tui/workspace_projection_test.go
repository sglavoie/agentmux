package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
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

func TestWorkspaceProjectionCoalescesRefreshesAndPublishesCurrentResult(t *testing.T) {
	m := Model{cfg: &config.Config{}}
	updated, first := m.Update(sessionsMsg{})
	m = updated.(Model)
	if first == nil || !m.workspaceProjectionInFlight || m.workspaceProjectionSeq != 1 {
		t.Fatalf("first projection = cmd %v in-flight %v seq %d", first != nil, m.workspaceProjectionInFlight, m.workspaceProjectionSeq)
	}

	updated, second := m.Update(sessionsMsg{})
	m = updated.(Model)
	if second != nil || !m.workspaceProjectionQueued || m.workspaceProjectionSeq != 1 {
		t.Fatalf("overlap = cmd %v queued %v seq %d, want coalesced behind seq 1", second != nil, m.workspaceProjectionQueued, m.workspaceProjectionSeq)
	}

	snapshot := workspace.Snapshot{
		GeneratedAt: time.Now(),
		Workspaces:  []workspace.WorkspaceSnapshot{{ID: "api", DisplayName: "API"}},
	}
	updated, follow := m.Update(workspaceProjectionMsg{seq: 1, snapshot: snapshot})
	m = updated.(Model)
	if len(m.workspaceSnapshot.Workspaces) != 1 || m.workspaceSnapshot.Workspaces[0].ID != "api" {
		t.Fatalf("completed projection was not published: %#v", m.workspaceSnapshot)
	}
	if m.workspaceLoading || follow == nil || !m.workspaceProjectionInFlight || m.workspaceProjectionSeq != 2 || m.workspaceProjectionQueued {
		t.Fatalf("follow-up = loading %v cmd %v in-flight %v seq %d queued %v", m.workspaceLoading, follow != nil, m.workspaceProjectionInFlight, m.workspaceProjectionSeq, m.workspaceProjectionQueued)
	}
}

func TestWorkspaceProjectionFailurePreservesSnapshotAndErrorDuringRetry(t *testing.T) {
	old := workspace.Snapshot{
		GeneratedAt: time.Now().Add(-time.Minute),
		Workspaces:  []workspace.WorkspaceSnapshot{{ID: "api", DisplayName: "API"}},
	}
	m := Model{
		cfg:                         &config.Config{},
		workspaceSnapshot:           old,
		workspaceProjectionSeq:      1,
		workspaceProjectionInFlight: true,
		workspaceProjectionQueued:   true,
	}
	loadErr := errors.New("database busy")
	updated, retry := m.Update(workspaceProjectionMsg{seq: 1, err: loadErr})
	m = updated.(Model)
	if !errors.Is(m.workspaceLoadErr, loadErr) || selectedWorkspaceID(m.workspaceSnapshot, 0) != "api" {
		t.Fatalf("failed refresh = err %v snapshot %#v", m.workspaceLoadErr, m.workspaceSnapshot)
	}
	if retry == nil || !m.workspaceProjectionInFlight || m.workspaceProjectionSeq != 2 || m.workspaceLoading {
		t.Fatalf("retry = cmd %v in-flight %v seq %d loading %v", retry != nil, m.workspaceProjectionInFlight, m.workspaceProjectionSeq, m.workspaceLoading)
	}
}

func TestWorkspaceProjectionRetainsSynchronizedCatalogAcrossPolls(t *testing.T) {
	service := workspace.New(datastore.OpenTest(t))
	catalog, err := service.Synchronize(context.Background(), &config.Config{
		Projects: []config.Session{{ID: "api", Title: "API", Directory: t.TempDir(), Profile: "work"}},
	})
	if err != nil {
		t.Fatalf("Synchronize: %v", err)
	}
	m := Model{cfg: &config.Config{}, workspaceProjectionSeq: 1, workspaceProjectionInFlight: true}
	updated, _ := m.Update(workspaceProjectionMsg{
		seq: 1, catalog: catalog,
		snapshot: workspace.Snapshot{GeneratedAt: time.Now(), Workspaces: []workspace.WorkspaceSnapshot{{ID: "api"}}},
	})
	m = updated.(Model)
	if !m.workspaceCatalog.Synchronized() || len(m.workspaceCatalog.Workspaces()) != 1 {
		t.Fatalf("published catalog = %#v", m.workspaceCatalog.Workspaces())
	}

	updated, cmd := m.Update(sessionsMsg{})
	m = updated.(Model)
	if cmd == nil || !m.workspaceCatalog.Synchronized() || m.workspaceProjectionSeq != 2 {
		t.Fatalf("poll discarded catalog: cmd %v synchronized %v seq %d", cmd != nil, m.workspaceCatalog.Synchronized(), m.workspaceProjectionSeq)
	}
}
