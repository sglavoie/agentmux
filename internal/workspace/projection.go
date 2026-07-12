package workspace

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/projectresolve"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// RecentActivityWindow is the period during which a workspace without a live
// terminal state is still ordered ahead of an otherwise idle workspace.
const RecentActivityWindow = 24 * time.Hour

// Urgency is the rendering-independent ordering class for a workspace.
// Lower values have more urgency.
type Urgency uint8

const (
	UrgencyAttention Urgency = iota
	UrgencyActive
	UrgencyRecent
	UrgencyIdle
)

// LinkedPlan is the immutable plan data retained in a workspace snapshot.
// It intentionally mirrors only the fields a workspace renderer needs so the
// workspace package does not need to import the plans package (which already
// imports workspace for link suggestions).
type LinkedPlan struct {
	ID         string
	Name       string
	Directory  string
	Files      []string
	Size       int64
	ModifiedAt time.Time
	Snippet    string
}

// PlanSource retrieves the plans explicitly linked to one workspace. A nil
// source is valid and produces snapshots without plans, which preserves a
// useful projection when the plans index is degraded.
type PlanSource interface {
	LinkedPlans(context.Context, string) ([]LinkedPlan, error)
}

// ActivitySource is the durable work-journal seam consumed by snapshots.
// Keeping it narrow lets tests supply deterministic feeds while production
// uses activity.Repository directly.
type ActivitySource interface {
	ListForWorkspaces(context.Context, []string, string, int) (map[string]activity.Feed, error)
}

// SnapshotOptions supplies the point-in-time external state joined into a
// workspace snapshot. ProjectSnapshot receives its immutable catalog as a
// separate argument so synchronization cannot be hidden in routine polling.
type SnapshotOptions struct {
	// Config is retained only for BuildSnapshot compatibility. ProjectSnapshot
	// ignores it because its catalog has already been synchronized explicitly.
	Config           *config.Config
	LiveSessions     []session.RunningSession
	DaemonStates     map[int]daemon.WindowStateSnapshot
	Activity         map[int]time.Time
	Conversations    []transcript.SessionMeta
	Plans            PlanSource
	ActivityFeed     ActivitySource
	ActivityReaderID string
	ActivityLimit    int
	Now              time.Time
	// SkipGitMetadata omits current branch/status/ahead-behind probes. Workspace
	// identity and worktree discovery are unaffected. This is useful for clients
	// such as the dashboard that do not render the Git field.
	SkipGitMetadata bool
}

// Snapshot is a single, self-contained projection suitable for rendering.
// BuildSnapshot deep-copies its inputs, so no slice, map, or pointer in this
// value aliases caller-owned mutable state. Consumers should treat it as
// read-only for the duration of a render.
type Snapshot struct {
	GeneratedAt time.Time
	Workspaces  []WorkspaceSnapshot
	Warnings    []string
}

// WorkspaceSnapshot joins one configured workspace to its live and indexed
// state. Definition fields retain the launch-oriented configuration, while
// the remaining fields are already grouped and ordered for the workspace UI.
type WorkspaceSnapshot struct {
	ID                    string
	ConfiguredProjectID   string
	CanonicalPath         string
	DisplayName           string
	Aliases               []string
	TitleAliases          []string
	DefaultLaunch         config.Session
	LaunchVariants        []config.Session
	Worktrees             []WorktreeContext
	Git                   []GitMetadata
	LiveSessions          []LiveSession
	Conversations         []Conversation
	Plans                 []LinkedPlan
	Activity              []ActivityItem
	UnreadCount           int
	ActivityReadThrough   time.Time
	ActivityLoadedThrough time.Time
	LastDurableActivityAt time.Time
	Urgency               Urgency
	LastMeaningfulAt      time.Time
}

// ActivityItem is the display and navigation subset of one durable journal
// record. Harness/session and window identities allow the TUI to focus a live
// tab or continue the related indexed conversation without parsing metadata.
type ActivityItem struct {
	ID         int64
	Harness    string
	SessionID  string
	WindowID   int
	Kind       string
	Summary    string
	OccurredAt time.Time
	Unread     bool
}

// GitMetadata is the current best-effort Git state for one configured
// worktree. Available is false for non-Git paths and for probes that could
// not complete; those probe failures do not invalidate the rest of a snapshot.
type GitMetadata struct {
	WorktreeID string
	Path       string
	Branch     string
	Head       string
	Dirty      bool
	Ahead      int
	Behind     int
	Available  bool
	Error      string
}

// DaemonState is the daemon-owned state associated with a live Kitty session.
// Available distinguishes an unreachable/degraded daemon from a reachable
// daemon that simply has no state yet for this window.
type DaemonState struct {
	Available             bool
	Observed              bool
	IconState             string
	Looping               bool
	LastAction            string
	LastEventAt           time.Time
	GuardAction           string
	ConversationHarness   string
	ConversationSessionID string
}

// LiveSession joins a Kitty session with its daemon state and the worktree
// context selected from its CWD.
type LiveSession struct {
	Session      session.RunningSession
	WorktreePath string
	ActivityAt   time.Time
	Daemon       DaemonState
}

// Conversation is the recent-conversation subset retained in a workspace
// snapshot. It avoids exposing mutable transcript index internals to a
// renderer while preserving enough identity for continuation actions.
type Conversation struct {
	ID           string
	Harness      string
	Profile      string
	Profiles     []string
	Project      string
	CWD          string
	StartedAt    time.Time
	LastAt       time.Time
	MessageCount int
	Snippet      string
}

// BuildSnapshot is a compatibility wrapper. It performs explicit
// synchronization from Config, then delegates to the read-only ProjectSnapshot
// function. Routine polling must retain and reuse a catalog directly.
func (s *Service) BuildSnapshot(ctx context.Context, opts SnapshotOptions) (Snapshot, error) {
	if opts.Config == nil {
		return Snapshot{}, errors.New("workspace: snapshot requires a non-nil config")
	}
	catalog, err := s.Synchronize(ctx, opts.Config)
	if err != nil {
		return Snapshot{}, err
	}
	return ProjectSnapshot(ctx, catalog, opts)
}

// ProjectSnapshot joins an already synchronized catalog with live and indexed
// state. It performs no workspace SQL mutation or Git topology discovery. The
// returned value deep-copies both catalog and caller-owned mutable inputs.
func ProjectSnapshot(ctx context.Context, catalog Catalog, opts SnapshotOptions) (Snapshot, error) {
	if !catalog.Synchronized() {
		return Snapshot{}, errors.New("workspace: projection requires a synchronized catalog")
	}
	workspaces := catalog.Workspaces()
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	snapshot := Snapshot{
		GeneratedAt: now,
		Workspaces:  make([]WorkspaceSnapshot, len(workspaces)),
	}
	for i, definition := range workspaces {
		item := workspaceSnapshotFromDefinition(definition)
		if opts.Plans != nil {
			linked, err := opts.Plans.LinkedPlans(ctx, definition.ID)
			if err != nil {
				return Snapshot{}, err
			}
			item.Plans = cloneLinkedPlans(linked)
			sortLinkedPlans(item.Plans)
		}
		if !opts.SkipGitMetadata {
			item.Git = currentGitMetadata(ctx, item.Worktrees)
		}
		snapshot.Workspaces[i] = item
	}
	if opts.ActivityFeed != nil && len(snapshot.Workspaces) > 0 {
		workspaceIDs := make([]string, len(snapshot.Workspaces))
		for i := range snapshot.Workspaces {
			workspaceIDs[i] = snapshot.Workspaces[i].ID
		}
		feeds, err := opts.ActivityFeed.ListForWorkspaces(ctx, workspaceIDs, opts.ActivityReaderID, opts.ActivityLimit)
		if err != nil {
			return Snapshot{}, err
		}
		for i := range snapshot.Workspaces {
			feed := feeds[snapshot.Workspaces[i].ID]
			snapshot.Workspaces[i].Activity = activityItems(feed.Items)
			snapshot.Workspaces[i].UnreadCount = feed.UnreadCount
			snapshot.Workspaces[i].ActivityReadThrough = feed.ReadThrough
			snapshot.Workspaces[i].ActivityLoadedThrough = feed.LoadedThrough
			snapshot.Workspaces[i].LastDurableActivityAt = feed.LatestAt
		}
	}

	for _, running := range opts.LiveSessions {
		index, worktreePath, ok := snapshotWorkspaceForSession(snapshot.Workspaces, running.Directory, running.EffectiveProject(), running.EffectiveProfile())
		if !ok {
			continue
		}
		live := LiveSession{
			Session:      cloneRunningSession(running),
			WorktreePath: worktreePath,
			ActivityAt:   opts.Activity[running.WindowID],
			Daemon:       daemonStateFor(running.WindowID, opts.DaemonStates),
		}
		snapshot.Workspaces[index].LiveSessions = append(snapshot.Workspaces[index].LiveSessions, live)
	}

	for _, meta := range opts.Conversations {
		if meta.HiddenFromIndex {
			continue
		}
		index, _, ok := snapshotWorkspaceForSession(snapshot.Workspaces, meta.CWD, meta.Project, meta.DisplayProfile())
		if !ok {
			continue
		}
		snapshot.Workspaces[index].Conversations = append(snapshot.Workspaces[index].Conversations, Conversation{
			ID:           meta.ID,
			Harness:      meta.Harness,
			Profile:      meta.Profile,
			Profiles:     append([]string(nil), meta.Profiles...),
			Project:      meta.Project,
			CWD:          meta.CWD,
			StartedAt:    meta.StartedAt,
			LastAt:       meta.LastAt,
			MessageCount: meta.MessageCount,
			Snippet:      meta.Snippet,
		})
	}

	for i := range snapshot.Workspaces {
		item := &snapshot.Workspaces[i]
		sortLiveSessions(item.LiveSessions)
		sortConversations(item.Conversations)
		item.LastMeaningfulAt = latestMeaningfulActivity(*item)
		item.Urgency = classifyUrgency(*item, now)
	}
	sortWorkspaceSnapshots(snapshot.Workspaces)
	return snapshot, nil
}

func workspaceSnapshotFromDefinition(definition Workspace) WorkspaceSnapshot {
	return WorkspaceSnapshot{
		ID:                  definition.ID,
		ConfiguredProjectID: definition.ConfiguredProjectID,
		CanonicalPath:       definition.CanonicalPath,
		DisplayName:         definition.DisplayName,
		Aliases:             append([]string(nil), definition.Aliases...),
		TitleAliases:        append([]string(nil), definition.TitleAliases...),
		DefaultLaunch:       cloneConfiguredSession(definition.DefaultLaunch),
		LaunchVariants:      cloneConfiguredSessions(definition.LaunchVariants),
		Worktrees:           append([]WorktreeContext(nil), definition.Worktrees...),
	}
}

func cloneConfiguredSessions(in []config.Session) []config.Session {
	out := make([]config.Session, len(in))
	for i := range in {
		out[i] = cloneConfiguredSession(in[i])
	}
	return out
}

func cloneConfiguredSession(in config.Session) config.Session {
	out := in
	out.Args = append([]string(nil), in.Args...)
	out.ClaudeFlags = append([]string(nil), in.ClaudeFlags...)
	out.Env = maps.Clone(in.Env)
	out.Groups = append([]string(nil), in.Groups...)
	return out
}

func cloneRunningSession(in session.RunningSession) session.RunningSession {
	out := in
	out.Groups = append([]string(nil), in.Groups...)
	return out
}

func cloneLinkedPlans(in []LinkedPlan) []LinkedPlan {
	out := make([]LinkedPlan, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Files = append([]string(nil), in[i].Files...)
	}
	return out
}

func activityItems(in []activity.Record) []ActivityItem {
	out := make([]ActivityItem, len(in))
	for i := range in {
		out[i] = ActivityItem{
			ID: in[i].ID, Harness: in[i].Harness, SessionID: in[i].SessionID,
			WindowID: in[i].WindowID, Kind: in[i].Kind, Summary: in[i].Summary,
			OccurredAt: in[i].OccurredAt, Unread: in[i].Unread,
		}
	}
	return out
}

func snapshotWorkspaceForSession(workspaces []WorkspaceSnapshot, directory, title, profile string) (int, string, bool) {
	directory = projectPath(directory)
	bestIndex, bestLen, bestPath := -1, -1, ""
	for i := range workspaces {
		for _, worktree := range workspaces[i].Worktrees {
			if pathContains(worktree.CanonicalPath, directory) && len(worktree.CanonicalPath) > bestLen {
				bestIndex, bestLen, bestPath = i, len(worktree.CanonicalPath), worktree.CanonicalPath
			}
		}
		if pathContains(workspaces[i].CanonicalPath, directory) && len(workspaces[i].CanonicalPath) > bestLen {
			bestIndex, bestLen, bestPath = i, len(workspaces[i].CanonicalPath), workspaces[i].CanonicalPath
		}
	}
	if bestIndex >= 0 {
		return bestIndex, bestPath, true
	}

	// A missing CWD is common in older Kitty metadata. Titles are useful only
	// when they identify one configured launch variant; duplicate project titles
	// deliberately remain unassigned rather than being guessed.
	candidates := make([]int, 0, 1)
	for i := range workspaces {
		if configuredLaunchMatches(workspaces[i], title, profile) {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], "", true
	}
	return 0, "", false
}

func projectPath(path string) string {
	if path == "" {
		return ""
	}
	return projectresolve.AbsPath(strings.TrimSpace(path))
}

func configuredLaunchMatches(item WorkspaceSnapshot, title, profile string) bool {
	if title == "" {
		return false
	}
	launches := append([]config.Session{item.DefaultLaunch}, item.LaunchVariants...)
	for _, launch := range launches {
		if launch.Title == title && (profile == "" || strings.EqualFold(launch.EffectiveProfile(), profile)) {
			return true
		}
	}
	return false
}

func daemonStateFor(windowID int, states map[int]daemon.WindowStateSnapshot) DaemonState {
	if states == nil {
		return DaemonState{}
	}
	state, found := states[windowID]
	if !found {
		return DaemonState{Available: true}
	}
	result := DaemonState{
		Available:   true,
		Observed:    true,
		IconState:   state.IconState,
		Looping:     state.Looping,
		LastAction:  state.LastAction,
		GuardAction: state.GuardAction,
	}
	if state.LastEventAt > 0 {
		result.LastEventAt = time.Unix(state.LastEventAt, 0)
	}
	if state.Conversation != nil {
		result.ConversationHarness = state.Conversation.Harness
		result.ConversationSessionID = state.Conversation.SessionID
	}
	return result
}

func sortLiveSessions(items []LiveSession) {
	sort.Slice(items, func(i, j int) bool {
		left, right := liveUrgency(items[i]), liveUrgency(items[j])
		if left != right {
			return left < right
		}
		if !items[i].ActivityAt.Equal(items[j].ActivityAt) {
			return items[i].ActivityAt.After(items[j].ActivityAt)
		}
		if items[i].Session.Base != items[j].Session.Base {
			return strings.ToLower(items[i].Session.Base) < strings.ToLower(items[j].Session.Base)
		}
		return items[i].Session.WindowID < items[j].Session.WindowID
	})
}

func sortConversations(items []Conversation) {
	sort.Slice(items, func(i, j int) bool {
		if !items[i].LastAt.Equal(items[j].LastAt) {
			return items[i].LastAt.After(items[j].LastAt)
		}
		if items[i].Project != items[j].Project {
			return strings.ToLower(items[i].Project) < strings.ToLower(items[j].Project)
		}
		if items[i].Harness != items[j].Harness {
			return items[i].Harness < items[j].Harness
		}
		return items[i].ID < items[j].ID
	})
}

func sortLinkedPlans(items []LinkedPlan) {
	sort.Slice(items, func(i, j int) bool {
		if !items[i].ModifiedAt.Equal(items[j].ModifiedAt) {
			return items[i].ModifiedAt.After(items[j].ModifiedAt)
		}
		if items[i].Name != items[j].Name {
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		}
		return items[i].Directory < items[j].Directory
	})
}

func latestMeaningfulActivity(item WorkspaceSnapshot) time.Time {
	latest := time.Time{}
	include := func(candidate time.Time) {
		if candidate.After(latest) {
			latest = candidate
		}
	}
	for _, live := range item.LiveSessions {
		include(live.ActivityAt)
		include(live.Daemon.LastEventAt)
	}
	for _, conversation := range item.Conversations {
		include(conversation.LastAt)
	}
	for _, plan := range item.Plans {
		include(plan.ModifiedAt)
	}
	include(item.LastDurableActivityAt)
	return latest
}

func classifyUrgency(item WorkspaceSnapshot, now time.Time) Urgency {
	best := UrgencyIdle
	for _, live := range item.LiveSessions {
		if urgency := liveUrgency(live); urgency < best {
			best = urgency
		}
	}
	if best != UrgencyIdle {
		return best
	}
	if !item.LastDurableActivityAt.IsZero() && now.Sub(item.LastDurableActivityAt) <= RecentActivityWindow {
		return UrgencyRecent
	}
	return UrgencyIdle
}

func liveUrgency(live LiveSession) Urgency {
	state := live.Session.State
	if live.Daemon.Observed && live.Daemon.IconState != "" {
		state = live.Daemon.IconState
	}
	switch state {
	case "key", "error", "warning", "question":
		return UrgencyAttention
	case "bolt", "subagent", "compact":
		return UrgencyActive
	case "stop":
		return UrgencyRecent
	default:
		return UrgencyIdle
	}
}

func sortWorkspaceSnapshots(items []WorkspaceSnapshot) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Urgency != items[j].Urgency {
			return items[i].Urgency < items[j].Urgency
		}
		if !items[i].LastDurableActivityAt.Equal(items[j].LastDurableActivityAt) {
			return items[i].LastDurableActivityAt.After(items[j].LastDurableActivityAt)
		}
		if !items[i].LastMeaningfulAt.Equal(items[j].LastMeaningfulAt) {
			return items[i].LastMeaningfulAt.After(items[j].LastMeaningfulAt)
		}
		left, right := strings.ToLower(items[i].DisplayName), strings.ToLower(items[j].DisplayName)
		if left != right {
			return left < right
		}
		return items[i].ID < items[j].ID
	})
}

func currentGitMetadata(ctx context.Context, worktrees []WorktreeContext) []GitMetadata {
	metadata := make([]GitMetadata, 0, len(worktrees))
	for _, worktree := range worktrees {
		metadata = append(metadata, gitMetadataForWorktree(ctx, worktree))
	}
	sort.Slice(metadata, func(i, j int) bool {
		if metadata[i].Path != metadata[j].Path {
			return metadata[i].Path < metadata[j].Path
		}
		return metadata[i].WorktreeID < metadata[j].WorktreeID
	})
	return metadata
}

func gitMetadataForWorktree(ctx context.Context, worktree WorktreeContext) GitMetadata {
	metadata := GitMetadata{
		WorktreeID: worktree.ID,
		Path:       worktree.CanonicalPath,
		Branch:     worktree.Branch,
	}
	branch, err := gitLine(ctx, worktree.CanonicalPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		metadata.Error = err.Error()
		return metadata
	}
	head, err := gitLine(ctx, worktree.CanonicalPath, "rev-parse", "--short", "HEAD")
	if err != nil {
		metadata.Error = err.Error()
		return metadata
	}
	status, err := gitOutput(ctx, worktree.CanonicalPath, "status", "--porcelain")
	if err != nil {
		metadata.Error = err.Error()
		return metadata
	}
	metadata.Available = true
	metadata.Branch = branch
	metadata.Head = head
	metadata.Dirty = len(strings.TrimSpace(string(status))) > 0

	// Repositories without an upstream are normal. In that case ahead/behind
	// intentionally remains zero while the rest of the Git snapshot is valid.
	if aheadBehind, err := gitOutput(ctx, worktree.CanonicalPath, "rev-list", "--left-right", "--count", "@{upstream}...HEAD"); err == nil {
		var behind, ahead int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(aheadBehind)), "%d\t%d", &behind, &ahead); err == nil {
			metadata.Ahead, metadata.Behind = ahead, behind
		}
	}
	return metadata
}
