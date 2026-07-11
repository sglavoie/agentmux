package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	activityjournal "github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/datastore"
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
	snapshot      workspace.Snapshot
	conversations map[string]transcript.SessionMeta
	planIndex     *plans.Index
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
	m.workspaceProjectionSeq++
	m.workspaceLoading = true
	m.workspaceLoadErr = nil
	return workspaceProjectionCmd(
		m.workspaceProjectionSeq,
		m.cfg,
		append([]session.RunningSession(nil), m.sessions...),
		cloneDaemonStateSnapshots(m.daemonStates),
		cloneWorkspaceActivity(m.lastActivity),
		m.transcriptIndex,
		m.workspacePlansIndex,
	)
}

func workspaceProjectionCmd(seq int, cfg *config.Config, live []session.RunningSession, states map[int]daemon.WindowStateSnapshot, activity map[int]time.Time, transcriptIndex *transcript.Index, planIndex *plans.Index) tea.Cmd {
	return func() tea.Msg {
		warnings := make([]string, 0, 2)
		if transcriptIndex == nil {
			var err error
			transcriptIndex, err = transcript.LoadIndex()
			if err != nil {
				warnings = append(warnings, "recent conversations: "+err.Error())
			}
		}
		conversations := make([]transcript.SessionMeta, 0)
		if transcriptIndex != nil {
			conversations = transcriptIndex.List(transcript.ListFilter{})
			if err := transcriptIndex.DegradedReason(); err != nil {
				warnings = append(warnings, "recent conversations: "+err.Error())
			}
		}
		conversationMeta := make(map[string]transcript.SessionMeta, len(conversations))
		for _, meta := range conversations {
			conversationMeta[transcript.IndexKey(meta.Harness, meta.ID)] = cloneWorkspaceConversationMeta(meta)
		}

		if planIndex == nil {
			var err error
			planIndex, err = plans.LoadIndex()
			if err != nil {
				warnings = append(warnings, "linked plans: "+err.Error())
			}
		}
		if planIndex != nil {
			if err := planIndex.DegradedReason(); err != nil {
				warnings = append(warnings, "linked plans: "+err.Error())
			}
		}

		store, err := datastore.Open(context.Background())
		if err != nil {
			return workspaceProjectionMsg{seq: seq, conversations: conversationMeta, planIndex: planIndex, err: fmt.Errorf("opening workspace datastore: %w", err)}
		}
		defer func() { _ = store.Close() }()

		var source workspace.PlanSource
		if planIndex != nil {
			source = workspacePlanSource{index: planIndex}
		}
		activityRepo, err := activityjournal.NewRepository(store)
		if err != nil {
			return workspaceProjectionMsg{seq: seq, conversations: conversationMeta, planIndex: planIndex, err: err}
		}
		snapshot, err := workspace.New(store).BuildSnapshot(context.Background(), workspace.SnapshotOptions{
			Config:           cfg,
			LiveSessions:     live,
			DaemonStates:     states,
			Activity:         activity,
			Conversations:    conversations,
			Plans:            source,
			ActivityFeed:     activityRepo,
			ActivityReaderID: activityjournal.DefaultReaderID,
		})
		if err != nil {
			return workspaceProjectionMsg{seq: seq, conversations: conversationMeta, planIndex: planIndex, err: err}
		}
		snapshot.Warnings = append(snapshot.Warnings, warnings...)
		return workspaceProjectionMsg{seq: seq, snapshot: snapshot, conversations: conversationMeta, planIndex: planIndex}
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
	m.workspaceLoading = false
	if msg.err != nil {
		m.workspaceLoadErr = msg.err
		return m, nil
	}
	if m.workspacePlansIndex == nil && msg.planIndex != nil {
		m.workspacePlansIndex = msg.planIndex
	}
	selectedID := selectedWorkspaceID(m.workspaceSnapshot, m.workspaceCursor)
	selectedDetailID := selectedWorkspaceDetailID(m)
	m.workspaceSnapshot = msg.snapshot
	m.workspaceConversationMeta = cloneWorkspaceConversationMetas(msg.conversations)
	m.workspaceCursor = workspaceCursorForID(m.workspaceSnapshot.Workspaces, selectedID)
	m.workspaceDetailCursor = workspaceDetailCursorForID(m, selectedDetailID)
	m.clampWorkspaceSelection()
	return m, nil
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
