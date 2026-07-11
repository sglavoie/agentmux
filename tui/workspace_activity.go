package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/datastore"
)

// defaultWorkspaceActivityMarker keeps the shared action registry independent
// from datastore construction. Each palette/automatic mutation is short-lived
// and owns its connection lifetime; projection reads continue on their own
// background connection.
type defaultWorkspaceActivityMarker struct{}

func (defaultWorkspaceActivityMarker) MarkWorkspaceRead(ctx context.Context, workspaceID, readerID string, through time.Time) (bool, error) {
	store, err := datastore.Open(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = store.Close() }()
	repo, err := activity.NewRepository(store)
	if err != nil {
		return false, err
	}
	return repo.MarkWorkspaceRead(ctx, workspaceID, readerID, through)
}

func (defaultWorkspaceActivityMarker) MarkAllWorkspacesRead(ctx context.Context, readerID string) (int64, error) {
	store, err := datastore.Open(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = store.Close() }()
	repo, err := activity.NewRepository(store)
	if err != nil {
		return 0, err
	}
	return repo.MarkAllWorkspacesRead(ctx, readerID)
}

func (m Model) activityMarker() actions.ActivityMarker {
	if m.workspaceActivityMarker != nil {
		return m.workspaceActivityMarker
	}
	return defaultWorkspaceActivityMarker{}
}

// markSelectedWorkspaceRead advances only through the immutable batch already
// loaded in the selected workspace. The optimistic local update prevents a
// repeated Tab/Enter from emitting duplicate mutations while the normal action
// refresh converges with SQLite.
func (m *Model) markSelectedWorkspaceRead() tea.Cmd {
	if m.workspaceCursor < 0 || m.workspaceCursor >= len(m.workspaceSnapshot.Workspaces) {
		return nil
	}
	item := &m.workspaceSnapshot.Workspaces[m.workspaceCursor]
	through := item.ActivityLoadedThrough
	if through.IsZero() || item.UnreadCount == 0 {
		return nil
	}
	loadedUnread := 0
	for i := range item.Activity {
		if item.Activity[i].Unread && !item.Activity[i].OccurredAt.After(through) {
			item.Activity[i].Unread = false
			loadedUnread++
		}
	}
	item.UnreadCount -= loadedUnread
	if item.UnreadCount < 0 {
		item.UnreadCount = 0
	}
	if through.After(item.ActivityReadThrough) {
		item.ActivityReadThrough = through
	}
	item.ActivityLoadedThrough = time.Time{}
	target := actions.WorkspaceTarget{ID: item.ID, LoadedThrough: through}
	return paletteActionCmd(*m, actions.MarkRead, actions.Request{Workspace: &target})
}

func (m *Model) markAllWorkspaceActivityRead() tea.Cmd {
	for wi := range m.workspaceSnapshot.Workspaces {
		item := &m.workspaceSnapshot.Workspaces[wi]
		for ai := range item.Activity {
			item.Activity[ai].Unread = false
		}
		item.UnreadCount = 0
		item.ActivityLoadedThrough = time.Time{}
	}
	return paletteActionCmd(*m, actions.MarkAllRead, actions.Request{})
}
