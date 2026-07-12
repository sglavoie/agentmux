package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
)

type unavailableWorkspaceActivityMarker struct{}

func (unavailableWorkspaceActivityMarker) MarkWorkspaceRead(context.Context, string, string, time.Time) (bool, error) {
	return false, errors.New("workspace activity services are not ready")
}

func (unavailableWorkspaceActivityMarker) MarkAllWorkspacesRead(context.Context, string) (int64, error) {
	return 0, errors.New("workspace activity services are not ready")
}

func (m Model) activityMarker() actions.ActivityMarker {
	if m.workspaceActivityMarker != nil {
		return m.workspaceActivityMarker
	}
	if m.dataServices != nil {
		if services, ready := m.dataServices.Snapshot(); ready && services.activityRepository != nil {
			return services.activityRepository
		}
	}
	return unavailableWorkspaceActivityMarker{}
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
