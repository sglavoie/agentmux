package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/projectresolve"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
)

type projectionPlanSource map[string][]LinkedPlan

func (s projectionPlanSource) LinkedPlans(_ context.Context, workspaceID string) ([]LinkedPlan, error) {
	return s[workspaceID], nil
}

type projectionActivitySource map[string]activity.Feed

func (s projectionActivitySource) ListForWorkspaces(_ context.Context, _ []string, _ string, _ int) (map[string]activity.Feed, error) {
	return s, nil
}

func TestBuildSnapshotAggregatesProfilesDuplicatesAndLinkedPlans(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 11, 18, 0, 0, 0, time.UTC)
	apiDir := t.TempDir()
	duplicateDir := t.TempDir()
	apiCWD := filepath.Join(apiDir, "service")
	if err := os.MkdirAll(apiCWD, 0o755); err != nil {
		t.Fatalf("Mkdir API service: %v", err)
	}
	cfg := workspaceConfig(
		config.Session{ID: "api", Title: "API", Directory: apiDir, Profile: "work", Env: map[string]string{"TEAM": "platform"}, Groups: []string{"backend"}},
		config.Session{Title: "API personal", Directory: apiDir, Profile: "personal"},
		config.Session{ID: "duplicate-api", Title: "API", Directory: duplicateDir, Profile: "work"},
	)
	service := New(datastore.OpenTest(t))
	planFiles := []string{"session-10.md"}
	liveSessions := []session.RunningSession{
		{WindowID: 11, Base: "API", Project: "API", Profile: "work", Directory: apiCWD, State: "stop", Groups: []string{"backend"}},
		{WindowID: 22, Base: "API", Project: "API", Profile: "work", Directory: duplicateDir, State: "bolt"},
	}
	conversations := []transcript.SessionMeta{
		{ID: "api-session", Harness: "claude", Profile: "personal", Profiles: []string{"personal"}, Project: "API personal", CWD: apiCWD, LastAt: now.Add(-3 * time.Minute), Snippet: "API conversation"},
		{ID: "duplicate-session", Harness: "codex", Profile: "work", Project: "API", CWD: duplicateDir, LastAt: now.Add(-4 * time.Minute)},
		{ID: "hidden", Harness: "claude", Profile: "work", Project: "API", CWD: apiDir, HiddenFromIndex: true},
	}
	snapshot, err := service.BuildSnapshot(ctx, SnapshotOptions{
		Config:       cfg,
		LiveSessions: liveSessions,
		DaemonStates: map[int]daemon.WindowStateSnapshot{
			11: {IconState: "warning", LastAction: "permission", LastEventAt: now.Add(-time.Minute).Unix()},
			22: {IconState: "bolt", LastAction: "active", LastEventAt: now.Add(-2 * time.Minute).Unix()},
		},
		Activity: map[int]time.Time{
			11: now.Add(-time.Minute),
			22: now.Add(-2 * time.Minute),
		},
		Conversations: conversations,
		Plans: projectionPlanSource{
			"api": {{ID: "plan-api", Name: "API rollout", Directory: "/plans/api", Files: planFiles, ModifiedAt: now.Add(-5 * time.Minute)}},
		},
		ActivityFeed: projectionActivitySource{
			"api": {Items: []activity.Record{{ID: 1, Summary: "Permission requested", OccurredAt: now.Add(-time.Minute), Unread: true}}, UnreadCount: 1, LoadedThrough: now.Add(-time.Minute), LatestAt: now.Add(-time.Minute)},
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if len(snapshot.Workspaces) != 2 {
		t.Fatalf("workspace count = %d, want 2: %#v", len(snapshot.Workspaces), snapshot.Workspaces)
	}

	api := snapshot.Workspaces[0]
	if api.ID != "api" || api.Urgency != UrgencyAttention {
		t.Fatalf("first workspace = %#v, want attention-ranked api", api)
	}
	if len(api.LaunchVariants) != 1 || api.LaunchVariants[0].EffectiveProfile() != "personal" {
		t.Errorf("launch variants = %#v, want personal variant", api.LaunchVariants)
	}
	if len(api.LiveSessions) != 1 || !api.LiveSessions[0].Daemon.Available || !api.LiveSessions[0].Daemon.Observed || api.LiveSessions[0].Daemon.IconState != "warning" {
		t.Errorf("api live daemon join = %#v, want observed warning", api.LiveSessions)
	}
	if len(api.Conversations) != 1 || api.Conversations[0].ID != "api-session" {
		t.Errorf("api conversations = %#v, want one visible conversation", api.Conversations)
	}
	if len(api.Plans) != 1 || api.Plans[0].ID != "plan-api" {
		t.Errorf("api plans = %#v, want linked plan", api.Plans)
	}
	if len(api.Activity) != 1 || api.Activity[0].Summary != "Permission requested" || api.UnreadCount != 1 || !api.LastDurableActivityAt.Equal(now.Add(-time.Minute)) {
		t.Errorf("api activity = %#v unread=%d latest=%v", api.Activity, api.UnreadCount, api.LastDurableActivityAt)
	}
	if got := snapshot.Workspaces[1]; got.ID != "duplicate-api" || got.Urgency != UrgencyActive {
		t.Errorf("second workspace = %#v, want active duplicate-title workspace", got)
	}

	// The snapshot must not share mutable config, live, transcript, or plan
	// backing storage with its inputs.
	cfg.Projects[0].Env["TEAM"] = "changed"
	liveSessions[0].Groups[0] = "changed"
	conversations[0].Profiles[0] = "changed"
	planFiles[0] = "changed"
	if api.DefaultLaunch.Env["TEAM"] != "platform" || api.LiveSessions[0].Session.Groups[0] != "backend" || api.Conversations[0].Profiles[0] != "personal" || api.Plans[0].Files[0] != "session-10.md" {
		t.Errorf("snapshot unexpectedly aliases mutable input: %#v", api)
	}
}

func TestBuildSnapshotOrdersLiveUrgencyBeforeDurableActivityThenRecency(t *testing.T) {
	now := time.Date(2026, time.July, 11, 20, 0, 0, 0, time.UTC)
	attentionDir, newerDir, olderDir := t.TempDir(), t.TempDir(), t.TempDir()
	snapshot, err := New(datastore.OpenTest(t)).BuildSnapshot(context.Background(), SnapshotOptions{
		Config: workspaceConfig(
			config.Session{ID: "attention", Title: "Attention", Directory: attentionDir, Profile: "work"},
			config.Session{ID: "newer", Title: "Newer", Directory: newerDir, Profile: "work"},
			config.Session{ID: "older", Title: "Older", Directory: olderDir, Profile: "work"},
		),
		LiveSessions: []session.RunningSession{{WindowID: 9, Project: "Attention", Profile: "work", Directory: attentionDir, State: "key"}},
		ActivityFeed: projectionActivitySource{
			"attention": {LatestAt: now.Add(-48 * time.Hour)},
			"newer":     {LatestAt: now.Add(-time.Minute)},
			"older":     {LatestAt: now.Add(-time.Hour)},
		},
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{snapshot.Workspaces[0].ID, snapshot.Workspaces[1].ID, snapshot.Workspaces[2].ID}
	want := []string{"attention", "newer", "older"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("workspace order = %v, want %v", got, want)
		}
	}
	if snapshot.Workspaces[1].Urgency != UrgencyRecent || snapshot.Workspaces[2].Urgency != UrgencyRecent {
		t.Fatalf("durable activity urgency = %v/%v, want recent", snapshot.Workspaces[1].Urgency, snapshot.Workspaces[2].Urgency)
	}
}

func TestBuildSnapshotIncludesLinkedWorktreeAndCurrentGitMetadata(t *testing.T) {
	git := requireGit(t)
	ctx := context.Background()
	root := t.TempDir()
	primary := filepath.Join(root, "repo")
	linked := filepath.Join(root, "repo-feature")
	if err := os.Mkdir(primary, 0o755); err != nil {
		t.Fatalf("Mkdir primary: %v", err)
	}
	runGit(t, git, primary, "init")
	runGit(t, git, primary, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "--no-verify", "-m", "initial")
	runGit(t, git, primary, "worktree", "add", "-b", "feature/workspace", linked)
	linkedCWD := filepath.Join(linked, "service")
	if err := os.MkdirAll(linkedCWD, 0o755); err != nil {
		t.Fatalf("Mkdir linked service: %v", err)
	}

	snapshot, err := New(datastore.OpenTest(t)).BuildSnapshot(ctx, SnapshotOptions{
		Config:       workspaceConfig(config.Session{ID: "repo", Title: "Repo", Directory: primary, Profile: "work"}),
		LiveSessions: []session.RunningSession{{WindowID: 9, Project: "Repo", Profile: "work", Directory: linkedCWD}},
		Now:          time.Now(),
	})
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if len(snapshot.Workspaces) != 1 {
		t.Fatalf("workspace count = %d, want 1", len(snapshot.Workspaces))
	}
	item := snapshot.Workspaces[0]
	if len(item.Worktrees) != 2 {
		t.Fatalf("worktrees = %#v, want primary and linked", item.Worktrees)
	}
	if len(item.LiveSessions) != 1 || item.LiveSessions[0].WorktreePath != projectresolve.AbsPath(linked) {
		t.Errorf("live worktree mapping = %#v, want linked path %q", item.LiveSessions, projectresolve.AbsPath(linked))
	}
	var linkedGit *GitMetadata
	for i := range item.Git {
		if item.Git[i].Path == projectresolve.AbsPath(linked) {
			linkedGit = &item.Git[i]
			break
		}
	}
	if linkedGit == nil || !linkedGit.Available || linkedGit.Branch != "feature/workspace" || linkedGit.Head == "" {
		t.Errorf("linked Git metadata = %#v, want available feature branch with head", linkedGit)
	}

	withoutGit, err := New(datastore.OpenTest(t)).BuildSnapshot(ctx, SnapshotOptions{
		Config:          workspaceConfig(config.Session{ID: "repo", Title: "Repo", Directory: primary, Profile: "work"}),
		LiveSessions:    []session.RunningSession{{WindowID: 9, Project: "Repo", Profile: "work", Directory: linkedCWD}},
		Now:             time.Now(),
		SkipGitMetadata: true,
	})
	if err != nil {
		t.Fatalf("BuildSnapshot without Git metadata: %v", err)
	}
	if got := withoutGit.Workspaces[0]; len(got.Git) != 0 || len(got.Worktrees) != 2 || got.LiveSessions[0].WorktreePath != projectresolve.AbsPath(linked) {
		t.Errorf("skip Git snapshot = Git %#v worktrees %#v live %#v", got.Git, got.Worktrees, got.LiveSessions)
	}
}

func TestBuildSnapshotKeepsNoLiveWorkspaceAndMarksDegradedDaemon(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	first := t.TempDir()
	second := t.TempDir()
	snapshot, err := New(datastore.OpenTest(t)).BuildSnapshot(ctx, SnapshotOptions{
		Config: workspaceConfig(
			config.Session{ID: "first", Title: "First", Directory: first, Profile: "work"},
			config.Session{ID: "second", Title: "Second", Directory: second, Profile: "personal"},
		),
		LiveSessions: []session.RunningSession{{WindowID: 1, Project: "First", Profile: "work", Directory: first, State: "stop"}},
		Activity:     map[int]time.Time{1: now},
		// Nil DaemonStates represents a failed/degraded daemon query.
		DaemonStates: nil,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if len(snapshot.Workspaces) != 2 {
		t.Fatalf("workspace count = %d, want 2", len(snapshot.Workspaces))
	}
	var firstItem, secondItem *WorkspaceSnapshot
	for i := range snapshot.Workspaces {
		switch snapshot.Workspaces[i].ID {
		case "first":
			firstItem = &snapshot.Workspaces[i]
		case "second":
			secondItem = &snapshot.Workspaces[i]
		}
	}
	if firstItem == nil || len(firstItem.LiveSessions) != 1 || firstItem.LiveSessions[0].Daemon.Available {
		t.Errorf("degraded daemon state = %#v, want unavailable daemon on live workspace", firstItem)
	}
	if secondItem == nil || len(secondItem.LiveSessions) != 0 || secondItem.Urgency != UrgencyIdle {
		t.Errorf("no-live workspace = %#v, want retained idle workspace", secondItem)
	}
}

func TestBuildSnapshotUsesDirectLegacySessionsAlias(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Profiles: []config.Account{{Name: "work", Harness: "claude", Color: "blue"}},
		Sessions: []config.Session{{ID: "legacy", Title: "Legacy", Directory: dir, Profile: "work"}},
	}
	snapshot, err := New(datastore.OpenTest(t)).BuildSnapshot(context.Background(), SnapshotOptions{Config: cfg})
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].ID != "legacy" {
		t.Errorf("legacy sessions snapshot = %#v, want workspace legacy", snapshot.Workspaces)
	}
}

func TestProjectSnapshotRemainsReadOnlyBesideUnrelatedWriter(t *testing.T) {
	ctx := context.Background()
	reader := datastore.OpenTest(t)
	service := New(reader)
	catalog, err := service.Synchronize(ctx, workspaceConfig(config.Session{
		ID: "api", Title: "API", Directory: t.TempDir(), Profile: "work",
	}))
	if err != nil {
		t.Fatalf("Synchronize: %v", err)
	}
	feed, err := activity.NewRepository(reader)
	if err != nil {
		t.Fatalf("activity repository: %v", err)
	}

	writer, err := datastore.OpenPath(ctx, reader.Path())
	if err != nil {
		t.Fatalf("open independent writer: %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	tx, err := writer.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET display_name = display_name WHERE id = 'api'`); err != nil {
		t.Fatalf("hold writer transaction: %v", err)
	}

	projectionCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	snapshot, err := ProjectSnapshot(projectionCtx, catalog, SnapshotOptions{
		ActivityFeed:     feed,
		ActivityReaderID: activity.DefaultReaderID,
		ActivityLimit:    1,
		SkipGitMetadata:  true,
	})
	if err != nil {
		t.Fatalf("ProjectSnapshot beside writer: %v", err)
	}
	if len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].ID != "api" {
		t.Fatalf("snapshot beside writer = %#v", snapshot.Workspaces)
	}
}
