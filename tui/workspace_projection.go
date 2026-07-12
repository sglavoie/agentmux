package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	activityjournal "github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
)

// workspaceProjectionMsg is a completed background workspace snapshot load.
// seq is compared with Model.workspaceProjectionSeq so slow scans cannot
// replace a newer Kitty/daemon refresh.
type workspaceProjectionMsg struct {
	seq           int
	catalog       workspace.Catalog
	snapshot      workspace.Snapshot
	conversations map[string]transcript.SessionMeta
	err           error
}

// workspacePlanSource adapts the plans facade without creating a package
// cycle: plans already imports workspace for link suggestions, so workspace
// exposes its small PlanSource interface instead of importing plans itself.
type workspacePlanSource struct{ index *plans.Index }

func (s workspacePlanSource) LinkedPlans(_ context.Context, workspaceID string) ([]workspace.LinkedPlan, error) {
	metas, err := s.index.ListByWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	items := make([]workspace.LinkedPlan, len(metas))
	for i, meta := range metas {
		items[i] = workspace.LinkedPlan{
			ID:         plans.PlanKey(meta.DirPath),
			Name:       meta.Name,
			Directory:  meta.DirPath,
			Files:      append([]string(nil), meta.Files...),
			Size:       meta.Size,
			ModifiedAt: meta.ModTime,
			Snippet:    meta.Snippet,
		}
	}
	return items, nil
}

// dispatchWorkspaceProjection starts the next immutable workspace snapshot.
// It intentionally captures current live state before returning to Bubble Tea;
// filesystem, SQLite, and Git work stay off the Update path.
func (m *Model) dispatchWorkspaceProjection() tea.Cmd {
	if m.cfg == nil || m.workspaceProjectionDisabled {
		return nil
	}
	if m.dataServices != nil {
		if _, ready := m.dataServices.Snapshot(); !ready {
			return nil
		}
	}
	if m.workspaceProjectionInFlight {
		m.workspaceProjectionQueued = true
		return nil
	}
	m.workspaceProjectionSeq++
	m.workspaceProjectionInFlight = true
	if m.workspaceSnapshot.GeneratedAt.IsZero() && m.workspaceLoadErr == nil {
		m.workspaceLoading = true
	}
	var services dataServicesSnapshot
	if m.dataServices != nil {
		services, _ = m.dataServices.Snapshot()
	}
	if services.transcriptIndex == nil {
		services.transcriptIndex = m.transcriptIndex
	}
	if services.planIndex == nil {
		services.planIndex = m.plansIndex
	}
	if !services.workspaceCatalog.Synchronized() {
		services.workspaceCatalog = m.workspaceCatalog
	}
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return workspaceProjectionCmd(
		ctx,
		m.workspaceProjectionSeq,
		append([]session.RunningSession(nil), m.sessions...),
		cloneDaemonStateSnapshots(m.daemonStates),
		cloneWorkspaceActivity(m.lastActivity),
		services,
	)
}

func workspaceProjectionCmd(ctx context.Context, seq int, live []session.RunningSession, states map[int]daemon.WindowStateSnapshot, activity map[int]time.Time, services dataServicesSnapshot) tea.Cmd {
	return func() tea.Msg {
		if err := ctx.Err(); err != nil {
			return workspaceProjectionMsg{seq: seq, err: err}
		}
		warnings := make([]string, 0, 2)
		conversations := make([]transcript.SessionMeta, 0)
		if services.transcriptIndex != nil {
			conversations = services.transcriptIndex.List(transcript.ListFilter{})
			if err := services.transcriptIndex.DegradedReason(); err != nil {
				warnings = append(warnings, "recent conversations: "+err.Error())
			}
		}
		conversationMeta := make(map[string]transcript.SessionMeta, len(conversations))
		for _, meta := range conversations {
			conversationMeta[transcript.IndexKey(meta.Harness, meta.ID)] = cloneWorkspaceConversationMeta(meta)
		}

		if services.planIndex != nil {
			if err := services.planIndex.DegradedReason(); err != nil {
				warnings = append(warnings, "linked plans: "+err.Error())
			}
		}

		if !services.workspaceCatalog.Synchronized() {
			return workspaceProjectionMsg{seq: seq, conversations: conversationMeta, err: errors.New("workspace data services are not ready")}
		}
		var source workspace.PlanSource
		if services.planIndex != nil {
			source = workspacePlanSource{index: services.planIndex}
		}
		if services.activityRepository == nil {
			return workspaceProjectionMsg{seq: seq, conversations: conversationMeta, err: errors.New("workspace activity repository is not ready")}
		}
		snapshot, err := workspace.ProjectSnapshot(ctx, services.workspaceCatalog, workspace.SnapshotOptions{
			LiveSessions:     live,
			DaemonStates:     states,
			Activity:         activity,
			Conversations:    conversations,
			Plans:            source,
			ActivityFeed:     services.activityRepository,
			ActivityReaderID: activityjournal.DefaultReaderID,
			SkipGitMetadata:  true,
		})
		if err != nil {
			return workspaceProjectionMsg{seq: seq, conversations: conversationMeta, err: err}
		}
		snapshot.Warnings = append(snapshot.Warnings, warnings...)
		return workspaceProjectionMsg{seq: seq, catalog: services.workspaceCatalog, snapshot: snapshot, conversations: conversationMeta}
	}
}

func cloneDaemonStateSnapshots(in map[int]daemon.WindowStateSnapshot) map[int]daemon.WindowStateSnapshot {
	if in == nil {
		return nil
	}
	out := make(map[int]daemon.WindowStateSnapshot, len(in))
	for id, state := range in {
		out[id] = state
	}
	return out
}

func cloneWorkspaceActivity(in map[int]time.Time) map[int]time.Time {
	if in == nil {
		return nil
	}
	out := make(map[int]time.Time, len(in))
	for id, at := range in {
		out[id] = at
	}
	return out
}

func (m Model) handleWorkspaceProjectionMsg(msg workspaceProjectionMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.workspaceProjectionSeq {
		return m, nil
	}
	m.workspaceProjectionInFlight = false
	m.workspaceLoading = false
	if msg.err != nil {
		m.workspaceLoadErr = msg.err
		return m.finishWorkspaceProjection()
	}
	m.workspaceLoadErr = nil
	if msg.catalog.Synchronized() {
		m.workspaceCatalog = msg.catalog
	}
	selectedID := selectedWorkspaceID(m.workspaceSnapshot, m.workspaceCursor)
	selectedDetailID := selectedWorkspaceDetailID(m)
	m.workspaceSnapshot = msg.snapshot
	m.workspaceConversationMeta = cloneWorkspaceConversationMetas(msg.conversations)
	m.workspaceCursor = workspaceCursorForID(m.workspaceSnapshot.Workspaces, selectedID)
	m.workspaceDetailCursor = workspaceDetailCursorForID(m, selectedDetailID)
	m.clampWorkspaceSelection()
	return m.finishWorkspaceProjection()
}

func (m Model) finishWorkspaceProjection() (tea.Model, tea.Cmd) {
	if !m.workspaceProjectionQueued {
		return m, nil
	}
	m.workspaceProjectionQueued = false
	return m, m.dispatchWorkspaceProjection()
}

func selectedWorkspaceDetailID(m Model) string {
	item, ok := m.selectedWorkspaceDetail()
	if !ok {
		return ""
	}
	return workspaceDetailID(item)
}

func workspaceDetailCursorForID(m Model, selectedID string) int {
	if selectedID == "" {
		return m.workspaceDetailCursor
	}
	selected, ok := m.selectedWorkspace()
	if !ok {
		return 0
	}
	for i, item := range workspaceItems(selected) {
		if workspaceDetailID(item) == selectedID {
			return i
		}
	}
	return m.workspaceDetailCursor
}

func cloneWorkspaceConversationMeta(meta transcript.SessionMeta) transcript.SessionMeta {
	meta.Profiles = append([]string(nil), meta.Profiles...)
	return meta
}

func cloneWorkspaceConversationMetas(in map[string]transcript.SessionMeta) map[string]transcript.SessionMeta {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]transcript.SessionMeta, len(in))
	for key, meta := range in {
		out[key] = cloneWorkspaceConversationMeta(meta)
	}
	return out
}

func selectedWorkspaceID(snapshot workspace.Snapshot, cursor int) string {
	if cursor < 0 || cursor >= len(snapshot.Workspaces) {
		return ""
	}
	return snapshot.Workspaces[cursor].ID
}

// workspaceCursorForID restores an existing selection after urgency sorting.
// A new or removed ID safely falls back to the first workspace.
func workspaceCursorForID(items []workspace.WorkspaceSnapshot, selectedID string) int {
	for i := range items {
		if items[i].ID == selectedID {
			return i
		}
	}
	return 0
}
