package tui

// Worktree fixtures. Between them they cover every state the Worktrees view has
// to tell apart — clean, dirty, live, looping, missing, metadata-invalid — plus
// the lifecycle outcomes the harness scripts against them in
// uitest_driver_fixture.go: reopen, cleanup, partial failure, and refusal.

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/worktree"
)

func worktreesFixture() UITestFixture {
	fx := basicFixture()
	fx.Name = "worktrees"
	fx.Description = "managed worktrees spanning clean, dirty, live, looping, missing, and invalid states"
	fx.Size = UITestSize{Width: 120, Height: 32}
	now := time.Now().Add(-2 * time.Hour)
	merged := true
	base := func(id, project, branch, path string) actions.WorktreeView {
		return actions.WorktreeView{Entry: worktree.Entry{
			ID: id, Classification: worktree.ClassificationValid,
			ProjectLabel: project, Branch: branch, Path: path,
			Profile: "work", Harness: "claude", CreatedAt: now,
			Exists: true, GitAvailable: true, StatusAvailable: true,
			Registered: true, Actionable: true,
		}, LiveSessions: []actions.WorktreeLiveSession{}, Warnings: []string{}}
	}
	clean := base("wt-clean", "agentmux", "amx/clean", "/tmp/uitest/worktrees/clean")
	clean.ReachableFromSource = &merged
	dirty := base("wt-dirty", "api", "amx/dirty", "/tmp/uitest/worktrees/dirty")
	dirty.Dirty = true
	live := base("wt-live", "backend", "amx/live", "/tmp/uitest/worktrees/live")
	live.Usage.Live = true
	live.LiveSessions = []actions.WorktreeLiveSession{{ID: "7", TabID: 7, Directory: live.Path}}
	looping := base("wt-loop", "worker", "amx/looping", "/tmp/uitest/worktrees/looping")
	looping.Usage.Looping = true
	looping.LoopCount = 1
	missing := base("wt-missing", "legacy", "amx/missing", "/tmp/uitest/worktrees/missing")
	missing.Classification = worktree.ClassificationMissing
	missing.Exists, missing.GitAvailable, missing.StatusAvailable, missing.Actionable = false, false, false, false
	invalid := base("wt-invalid", "broken", "amx/invalid", "/tmp/uitest/worktrees/invalid")
	invalid.Classification = worktree.ClassificationMetadataInvalid
	invalid.Actionable = false
	invalid.Warnings = []string{"metadata checksum mismatch"}
	fx.Worktrees = []actions.WorktreeView{clean, dirty, live, looping, missing, invalid}
	return fx
}

func worktreeLifecycleFixture() UITestFixture {
	fx := basicFixture()
	fx.Name = "worktree_lifecycle"
	fx.Description = "deterministic worktree reopen, cleanup, recovery, and refusal outcomes"
	fx.Size = UITestSize{Width: 132, Height: 34}
	now := time.Now().Add(-90 * time.Minute)
	merged, unmerged := true, false
	base := func(id, project string) actions.WorktreeView {
		path := "/tmp/uitest/worktrees/" + id
		entry := worktree.Entry{
			ID: id, Classification: worktree.ClassificationValid,
			ProjectLabel: project, Branch: "amx/" + strings.TrimPrefix(id, "wt-"), Path: path,
			SourceRoot: "/tmp/uitest/sources/" + project, SourceBranch: "main",
			Profile: "work", Harness: "claude", CreatedAt: now,
			Exists: true, GitAvailable: true, StatusAvailable: true, Registered: true, Actionable: true,
		}
		fx.Config.Sessions = append(fx.Config.Sessions, config.Session{Title: project, Directory: entry.SourceRoot, Profile: "work"})
		return actions.WorktreeView{Entry: entry, LiveSessions: []actions.WorktreeLiveSession{}, Warnings: []string{}}
	}
	live := base("wt-live", "live-project")
	live.Usage.Live = true
	live.LiveSessions = []actions.WorktreeLiveSession{{ID: "71", TabID: 71, WindowID: 711, Project: live.ProjectLabel, Profile: "work", Harness: "claude", HarnessSessionID: "native-live-71", Directory: live.Path}}
	multi := base("wt-multi", "multi-project")
	multi.Usage.Live = true
	multi.LiveSessions = []actions.WorktreeLiveSession{
		{ID: "81", TabID: 81, WindowID: 811, Project: multi.ProjectLabel, Profile: "work", Harness: "claude", HarnessSessionID: "native-multi-81", Directory: multi.Path},
		{ID: "82", TabID: 82, WindowID: 822, Project: multi.ProjectLabel, Profile: "personal", Harness: "codex", Directory: multi.Path},
	}
	resume := base("wt-resume", "resume-project")
	fresh := base("wt-fresh", "fresh-project")
	profile := base("wt-profile", "profile-project")
	profile.Profile = "removed-profile"
	clean := base("wt-cleanup", "cleanup-project")
	clean.ReachableFromSource = &merged
	deleteMerged := base("wt-delete", "delete-project")
	deleteMerged.ReachableFromSource = &merged
	unmergedEntry := base("wt-unmerged", "unmerged-project")
	unmergedEntry.ReachableFromSource = &unmerged
	dirty := base("wt-dirty", "dirty-project")
	dirty.Dirty = true
	looping := base("wt-looping", "looping-project")
	looping.Usage.Looping, looping.LoopCount = true, 1
	partial := base("wt-partial", "partial-project")
	partial.ReachableFromSource = &merged
	missing := base("wt-missing", "missing-project")
	missing.Classification = worktree.ClassificationMissing
	missing.Exists, missing.GitAvailable, missing.StatusAvailable, missing.Actionable = false, false, false, false
	invalid := base("wt-invalid", "invalid-project")
	invalid.Classification, invalid.Actionable = worktree.ClassificationMetadataInvalid, false
	invalid.Warnings = []string{"metadata checksum mismatch"}
	fx.Worktrees = []actions.WorktreeView{live, multi, resume, fresh, profile, clean, deleteMerged, unmergedEntry, dirty, looping, partial, missing, invalid}
	return fx
}
