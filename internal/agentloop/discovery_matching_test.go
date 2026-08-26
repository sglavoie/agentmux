package agentloop

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMatchingCWDMatchesCanonicalCWD(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(base, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	match := Candidate{PlanDir: "/plans/match", CWD: canonical(t, realDir)}
	other := Candidate{PlanDir: "/plans/other", CWD: canonical(t, filepath.Join(base, "other"))}
	matches, err := MatchingCWD([]Candidate{match, other}, linkDir)
	if err != nil {
		t.Fatalf("MatchingCWD: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}
	if matches[0].PlanDir != match.PlanDir {
		t.Errorf("matched PlanDir = %q, want %q", matches[0].PlanDir, match.PlanDir)
	}

	hasMatch, err := HasMatchingCWD([]Candidate{match, other}, linkDir)
	if err != nil {
		t.Fatalf("HasMatchingCWD: %v", err)
	}
	if !hasMatch {
		t.Fatal("HasMatchingCWD = false, want true")
	}
	hasMatch, err = HasMatchingCWD([]Candidate{match}, filepath.Join(base, "unmatched"))
	if err != nil {
		t.Fatalf("HasMatchingCWD unmatched: %v", err)
	}
	if hasMatch {
		t.Fatal("HasMatchingCWD unmatched = true, want false")
	}
}

func TestMatchingWorkspacePrefersSameHarnessWhenTargetIsOpen(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "project")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	copilotLoop := Candidate{PlanDir: "/plans/copilot", CWD: canonical(t, workspace), Harness: "copilot"}
	openTargets := []WorkspaceTarget{
		{CWD: workspace, Harness: "codex"},
		{CWD: workspace, Harness: "copilot"},
	}

	matches, err := MatchingWorkspace([]Candidate{copilotLoop},
		WorkspaceTarget{CWD: workspace, Harness: "copilot"}, openTargets)
	if err != nil {
		t.Fatalf("MatchingWorkspace copilot: %v", err)
	}
	if len(matches) != 1 || matches[0].PlanDir != copilotLoop.PlanDir {
		t.Fatalf("copilot matches = %#v, want copilot loop", matches)
	}

	matches, err = MatchingWorkspace([]Candidate{copilotLoop},
		WorkspaceTarget{CWD: workspace, Harness: "codex"}, openTargets)
	if err != nil {
		t.Fatalf("MatchingWorkspace codex: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("codex matches = %#v, want none while copilot target is open", matches)
	}
}

func TestMatchingWorkspaceFallsBackToCWDWhenNoSameHarnessTargetIsOpen(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "project")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	copilotLoop := Candidate{PlanDir: "/plans/copilot", CWD: canonical(t, workspace), Harness: "copilot"}
	openTargets := []WorkspaceTarget{
		{CWD: workspace, Harness: "codex"},
	}

	matches, err := MatchingWorkspace([]Candidate{copilotLoop},
		WorkspaceTarget{CWD: workspace, Harness: "codex"}, openTargets)
	if err != nil {
		t.Fatalf("MatchingWorkspace: %v", err)
	}
	if len(matches) != 1 || matches[0].PlanDir != copilotLoop.PlanDir {
		t.Fatalf("matches = %#v, want CWD fallback to copilot loop", matches)
	}
}

func TestMatchingWorkspaceLegacyCandidateMatchesByCWD(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "project")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	legacyLoop := Candidate{PlanDir: "/plans/legacy", CWD: canonical(t, workspace)}
	openTargets := []WorkspaceTarget{
		{CWD: workspace, Harness: "codex"},
		{CWD: workspace, Harness: "copilot"},
	}

	hasMatch, err := HasMatchingWorkspace([]Candidate{legacyLoop},
		WorkspaceTarget{CWD: workspace, Harness: "codex"}, openTargets)
	if err != nil {
		t.Fatalf("HasMatchingWorkspace codex: %v", err)
	}
	if !hasMatch {
		t.Fatal("HasMatchingWorkspace codex = false, want true for legacy CWD match")
	}

	hasMatch, err = HasMatchingWorkspace([]Candidate{legacyLoop},
		WorkspaceTarget{CWD: workspace, Harness: "copilot"}, openTargets)
	if err != nil {
		t.Fatalf("HasMatchingWorkspace copilot: %v", err)
	}
	if !hasMatch {
		t.Fatal("HasMatchingWorkspace copilot = false, want true for legacy CWD match")
	}
}

func TestMatchingWorkspaceUsesWindowIDBeforeCWD(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "project")
	worktree := filepath.Join(base, "project--ws14")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}

	loop := Candidate{
		PlanDir:  "/plans/window",
		CWD:      canonical(t, worktree),
		Harness:  "codex",
		WindowID: 51,
	}
	openTargets := []WorkspaceTarget{
		{CWD: workspace, Harness: "codex", WindowID: 51},
		{CWD: workspace, Harness: "codex", WindowID: 52},
	}

	matches, err := MatchingWorkspace([]Candidate{loop},
		WorkspaceTarget{CWD: workspace, Harness: "codex", WindowID: 51}, openTargets)
	if err != nil {
		t.Fatalf("MatchingWorkspace same window: %v", err)
	}
	if len(matches) != 1 || matches[0].PlanDir != loop.PlanDir {
		t.Fatalf("same-window matches = %#v, want window-specific loop", matches)
	}

	matches, err = MatchingWorkspace([]Candidate{loop},
		WorkspaceTarget{CWD: workspace, Harness: "codex", WindowID: 52}, openTargets)
	if err != nil {
		t.Fatalf("MatchingWorkspace different window: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("different-window matches = %#v, want none", matches)
	}

	legacyLoop := Candidate{
		PlanDir: "/plans/legacy",
		CWD:     canonical(t, workspace),
		Harness: "codex",
	}
	matches, err = MatchingWorkspace([]Candidate{loop, legacyLoop},
		WorkspaceTarget{CWD: workspace, Harness: "codex", WindowID: 52}, openTargets)
	if err != nil {
		t.Fatalf("MatchingWorkspace legacy fallback: %v", err)
	}
	if len(matches) != 1 || matches[0].PlanDir != legacyLoop.PlanDir {
		t.Fatalf("legacy fallback matches = %#v, want legacy CWD loop", matches)
	}
}

func TestMatchingWorkspaceFallsBackWhenCandidateWindowIDIsUnmanaged(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "project")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	// Candidate's WindowID points at an ASL pane nested inside the managed
	// tab; that ID is not part of the managed session set.
	const aslPaneWindowID = 99
	const managedWindowID = 51

	loop := Candidate{
		PlanDir:  "/plans/asl-pane",
		CWD:      canonical(t, workspace),
		Harness:  "codex",
		WindowID: aslPaneWindowID,
	}
	openTargets := []WorkspaceTarget{
		{CWD: workspace, Harness: "codex", WindowID: managedWindowID},
	}

	matches, err := MatchingWorkspace([]Candidate{loop},
		WorkspaceTarget{CWD: workspace, Harness: "codex", WindowID: managedWindowID}, openTargets)
	if err != nil {
		t.Fatalf("MatchingWorkspace: %v", err)
	}
	if len(matches) != 1 || matches[0].PlanDir != loop.PlanDir {
		t.Fatalf("matches = %#v, want unmanaged-WindowID loop to fall back to CWD", matches)
	}
}

func TestMatchingWorkspaceDoesNotFallBackWhenCandidateWindowIDIsAnotherManagedWindow(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "project")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	loop := Candidate{
		PlanDir:  "/plans/other-managed",
		CWD:      canonical(t, workspace),
		Harness:  "codex",
		WindowID: 51,
	}
	openTargets := []WorkspaceTarget{
		{CWD: workspace, Harness: "codex", WindowID: 51},
		{CWD: workspace, Harness: "codex", WindowID: 52},
	}

	matches, err := MatchingWorkspace([]Candidate{loop},
		WorkspaceTarget{CWD: workspace, Harness: "codex", WindowID: 52}, openTargets)
	if err != nil {
		t.Fatalf("MatchingWorkspace: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("matches = %#v, want none; loop is tied to another managed window", matches)
	}
}

func TestMatchingWorkspaceLegacyNoWindowIDStillFallsBack(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "project")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	legacyLoop := Candidate{
		PlanDir: "/plans/legacy-no-window",
		CWD:     canonical(t, workspace),
		Harness: "codex",
	}
	openTargets := []WorkspaceTarget{
		{CWD: workspace, Harness: "codex", WindowID: 51},
	}

	matches, err := MatchingWorkspace([]Candidate{legacyLoop},
		WorkspaceTarget{CWD: workspace, Harness: "codex", WindowID: 51}, openTargets)
	if err != nil {
		t.Fatalf("MatchingWorkspace: %v", err)
	}
	if len(matches) != 1 || matches[0].PlanDir != legacyLoop.PlanDir {
		t.Fatalf("matches = %#v, want legacy CWD fallback to still work", matches)
	}
}

func TestSortCandidatesNewestFirstWithTieBreaks(t *testing.T) {
	base := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	candidates := []Candidate{
		{PlanDir: "/plans/a", RunStartedAt: base, UpdatedAt: base},
		{PlanDir: "/plans/newest", RunStartedAt: base.Add(2 * time.Minute), UpdatedAt: base},
		{PlanDir: "/plans/fresher", RunStartedAt: base, UpdatedAt: base.Add(time.Minute)},
		{PlanDir: "/plans/0", RunStartedAt: base, UpdatedAt: base},
	}

	SortCandidates(candidates)
	got := []string{
		candidates[0].PlanDir,
		candidates[1].PlanDir,
		candidates[2].PlanDir,
		candidates[3].PlanDir,
	}
	want := []string{"/plans/newest", "/plans/fresher", "/plans/0", "/plans/a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %#v, want %#v", got, want)
		}
	}
}
