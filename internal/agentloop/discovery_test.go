package agentloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type loopSeed struct {
	PID              int
	CWD              string
	Harness          string
	KittyWindowID    int
	RunID            string
	RunStartedAt     string
	RunFinishedAt    any
	HasRunFinishedAt bool
	LockCreatedAt    string
	ActiveTurn       *activeTurn
}

func TestDiscoverIncludesActiveLock(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	planDir := filepath.Join(base, "plan")
	cwd := filepath.Join(base, "project")
	lockCreatedAt := "2026-06-04T13:00:00+00:00"
	runStartedAt := "2026-06-04T13:05:00.123456+00:00"
	stateDir := seedLoop(t, root, planDir, loopSeed{
		PID:           123,
		CWD:           cwd,
		RunID:         "run-1",
		RunStartedAt:  runStartedAt,
		LockCreatedAt: lockCreatedAt,
		ActiveTurn:    &activeTurn{Session: 2, TurnIndex: 7},
	})

	got, err := Discover(DiscoverOptions{
		StateRoot: root,
		PIDRunning: func(pid int) bool {
			return pid == 123
		},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(got))
	}

	candidate := got[0]
	if candidate.PlanDir != canonical(t, planDir) {
		t.Errorf("PlanDir = %q, want %q", candidate.PlanDir, canonical(t, planDir))
	}
	if candidate.StateDir != canonical(t, stateDir) {
		t.Errorf("StateDir = %q, want %q", candidate.StateDir, canonical(t, stateDir))
	}
	if candidate.CWD != canonical(t, cwd) {
		t.Errorf("CWD = %q, want %q", candidate.CWD, canonical(t, cwd))
	}
	if candidate.Harness != "" {
		t.Errorf("Harness = %q, want empty legacy harness", candidate.Harness)
	}
	if candidate.WindowID != 0 {
		t.Errorf("WindowID = %d, want empty legacy window id", candidate.WindowID)
	}
	if candidate.PID != 123 {
		t.Errorf("PID = %d, want 123", candidate.PID)
	}
	if candidate.RunID != "run-1" {
		t.Errorf("RunID = %q, want run-1", candidate.RunID)
	}
	if candidate.ActiveSession != 2 || candidate.ActiveTurn != 7 {
		t.Errorf("active turn = session %d turn %d, want session 2 turn 7",
			candidate.ActiveSession, candidate.ActiveTurn)
	}
	if candidate.LockCreatedAtRaw != lockCreatedAt || candidate.LockCreatedAt.IsZero() {
		t.Errorf("LockCreatedAt = %q/%v, want parsed %q",
			candidate.LockCreatedAtRaw, candidate.LockCreatedAt, lockCreatedAt)
	}
	if candidate.RunStartedAtRaw != runStartedAt || candidate.RunStartedAt.IsZero() {
		t.Errorf("RunStartedAt = %q/%v, want parsed %q",
			candidate.RunStartedAtRaw, candidate.RunStartedAt, runStartedAt)
	}
}

func TestDiscoverExcludesStalePID(t *testing.T) {
	root := t.TempDir()
	seedLoop(t, root, filepath.Join(t.TempDir(), "plan"), loopSeed{
		PID:           999,
		CWD:           filepath.Join(t.TempDir(), "project"),
		RunStartedAt:  "2026-06-04T13:05:00+00:00",
		LockCreatedAt: "2026-06-04T13:00:00+00:00",
	})

	got, err := Discover(DiscoverOptions{
		StateRoot: root,
		PIDRunning: func(int) bool {
			return false
		},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(candidates) = %d, want 0", len(got))
	}
}

func TestDiscoverExcludesFinishedRunWithLivePID(t *testing.T) {
	root := t.TempDir()
	seedLoop(t, root, filepath.Join(t.TempDir(), "plan"), loopSeed{
		PID:              123,
		CWD:              filepath.Join(t.TempDir(), "project"),
		RunStartedAt:     "2026-06-04T13:05:00+00:00",
		RunFinishedAt:    "2026-06-04T13:15:00+00:00",
		HasRunFinishedAt: true,
		LockCreatedAt:    "2026-06-04T13:00:00+00:00",
	})

	got, err := Discover(DiscoverOptions{
		StateRoot: root,
		PIDRunning: func(pid int) bool {
			return pid == 123
		},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(candidates) = %d, want 0", len(got))
	}
}

func TestDiscoverIncludesUnfinishedRunForSameCWD(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	cwd := filepath.Join(base, "project")
	finishedPlan := filepath.Join(base, "finished-plan")
	activePlan := filepath.Join(base, "active-plan")

	seedLoop(t, root, finishedPlan, loopSeed{
		PID:              123,
		CWD:              cwd,
		RunStartedAt:     "2026-06-04T13:05:00+00:00",
		RunFinishedAt:    "2026-06-04T13:15:00+00:00",
		HasRunFinishedAt: true,
		LockCreatedAt:    "2026-06-04T13:00:00+00:00",
	})
	seedLoop(t, root, activePlan, loopSeed{
		PID:           456,
		CWD:           cwd,
		RunStartedAt:  "2026-06-04T13:10:00+00:00",
		LockCreatedAt: "2026-06-04T13:00:00+00:00",
	})

	got, err := Discover(DiscoverOptions{
		StateRoot: root,
		PIDRunning: func(pid int) bool {
			return pid == 123 || pid == 456
		},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(got))
	}
	if got[0].PlanDir != canonical(t, activePlan) {
		t.Errorf("PlanDir = %q, want active plan %q", got[0].PlanDir, canonical(t, activePlan))
	}
	if got[0].CWD != canonical(t, cwd) {
		t.Errorf("CWD = %q, want %q", got[0].CWD, canonical(t, cwd))
	}
}

func TestDiscoverRunFinishedAtBackwardCompatibleValues(t *testing.T) {
	cases := []struct {
		name             string
		hasRunFinishedAt bool
		runFinishedAt    any
	}{
		{name: "missing"},
		{name: "null", hasRunFinishedAt: true, runFinishedAt: nil},
		{name: "empty", hasRunFinishedAt: true, runFinishedAt: ""},
		{name: "whitespace", hasRunFinishedAt: true, runFinishedAt: " \t\n"},
		{name: "malformed-type", hasRunFinishedAt: true, runFinishedAt: map[string]any{"bad": true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			planDir := filepath.Join(t.TempDir(), "plan")
			seedLoop(t, root, planDir, loopSeed{
				PID:              123,
				CWD:              filepath.Join(t.TempDir(), "project"),
				RunStartedAt:     "2026-06-04T13:05:00+00:00",
				RunFinishedAt:    tc.runFinishedAt,
				HasRunFinishedAt: tc.hasRunFinishedAt,
				LockCreatedAt:    "2026-06-04T13:00:00+00:00",
			})

			got, err := Discover(DiscoverOptions{
				StateRoot:  root,
				PIDRunning: func(int) bool { return true },
			})
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("len(candidates) = %d, want 1", len(got))
			}
			if got[0].PlanDir != canonical(t, planDir) {
				t.Errorf("PlanDir = %q, want %q", got[0].PlanDir, canonical(t, planDir))
			}
		})
	}
}

func TestDiscoverUsesActiveTurnHarnessBeforeTopLevelHarness(t *testing.T) {
	root := t.TempDir()
	seedLoop(t, root, filepath.Join(t.TempDir(), "plan"), loopSeed{
		PID:           123,
		CWD:           filepath.Join(t.TempDir(), "project"),
		Harness:       "claude",
		RunStartedAt:  "2026-06-04T13:05:00+00:00",
		LockCreatedAt: "2026-06-04T13:00:00+00:00",
		ActiveTurn:    &activeTurn{Session: 2, TurnIndex: 7, Harness: " CoPiLot "},
	})

	got, err := Discover(DiscoverOptions{
		StateRoot:  root,
		PIDRunning: func(int) bool { return true },
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(got))
	}
	if got[0].Harness != "copilot" {
		t.Fatalf("Harness = %q, want copilot", got[0].Harness)
	}
}

func TestDiscoverFallsBackToTopLevelHarness(t *testing.T) {
	root := t.TempDir()
	seedLoop(t, root, filepath.Join(t.TempDir(), "plan"), loopSeed{
		PID:           123,
		CWD:           filepath.Join(t.TempDir(), "project"),
		Harness:       " CoDeX ",
		RunStartedAt:  "2026-06-04T13:05:00+00:00",
		LockCreatedAt: "2026-06-04T13:00:00+00:00",
		ActiveTurn:    &activeTurn{Session: 2, TurnIndex: 7},
	})

	got, err := Discover(DiscoverOptions{
		StateRoot:  root,
		PIDRunning: func(int) bool { return true },
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(got))
	}
	if got[0].Harness != "codex" {
		t.Fatalf("Harness = %q, want codex", got[0].Harness)
	}
}

func TestDiscoverReadsKittyWindowID(t *testing.T) {
	root := t.TempDir()
	seedLoop(t, root, filepath.Join(t.TempDir(), "plan"), loopSeed{
		PID:           123,
		CWD:           filepath.Join(t.TempDir(), "project"),
		Harness:       "codex",
		KittyWindowID: 51,
		RunStartedAt:  "2026-06-04T13:05:00+00:00",
		LockCreatedAt: "2026-06-04T13:00:00+00:00",
	})

	got, err := Discover(DiscoverOptions{
		StateRoot:  root,
		PIDRunning: func(int) bool { return true },
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(got))
	}
	if got[0].WindowID != 51 {
		t.Fatalf("WindowID = %d, want 51", got[0].WindowID)
	}
}

func TestDiscoverLeavesUnknownHarnessEmpty(t *testing.T) {
	root := t.TempDir()
	seedLoop(t, root, filepath.Join(t.TempDir(), "plan"), loopSeed{
		PID:           123,
		CWD:           filepath.Join(t.TempDir(), "project"),
		Harness:       "future-harness",
		RunStartedAt:  "2026-06-04T13:05:00+00:00",
		LockCreatedAt: "2026-06-04T13:00:00+00:00",
		ActiveTurn:    &activeTurn{Session: 2, TurnIndex: 7, Harness: "other"},
	})

	got, err := Discover(DiscoverOptions{
		StateRoot:  root,
		PIDRunning: func(int) bool { return true },
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(got))
	}
	if got[0].Harness != "" {
		t.Fatalf("Harness = %q, want empty", got[0].Harness)
	}
}

func TestDiscoverSkipsMalformedAndMissingFiles(t *testing.T) {
	root := t.TempDir()

	badLockDir := filepath.Join(root, "bad-lock")
	if err := os.MkdirAll(badLockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badLockDir, lockFileName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	stateOnlyDir := filepath.Join(root, "state-only")
	if err := os.MkdirAll(stateOnlyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(stateOnlyDir, stateFileName), map[string]any{
		"plan_dir": filepath.Join(t.TempDir(), "state-only-plan"),
	})

	missingStatePlan := filepath.Join(t.TempDir(), "missing-state-plan")
	missingStateDir := stateDirForPlan(t, root, missingStatePlan)
	if err := os.MkdirAll(missingStateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(missingStateDir, lockFileName), map[string]any{
		"pid":        101,
		"plan_dir":   canonical(t, missingStatePlan),
		"created_at": "2026-06-04T13:00:00+00:00",
	})

	malformedStatePlan := filepath.Join(t.TempDir(), "malformed-state-plan")
	malformedStateDir := stateDirForPlan(t, root, malformedStatePlan)
	if err := os.MkdirAll(malformedStateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(malformedStateDir, lockFileName), map[string]any{
		"pid":        102,
		"plan_dir":   canonical(t, malformedStatePlan),
		"created_at": "2026-06-04T13:00:00+00:00",
	})
	if err := os.WriteFile(filepath.Join(malformedStateDir, stateFileName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	validPlan := filepath.Join(t.TempDir(), "valid-plan")
	seedLoop(t, root, validPlan, loopSeed{
		PID:           103,
		CWD:           filepath.Join(t.TempDir(), "valid-cwd"),
		RunStartedAt:  "2026-06-04T13:05:00+00:00",
		LockCreatedAt: "2026-06-04T13:00:00+00:00",
	})

	got, err := Discover(DiscoverOptions{
		StateRoot: root,
		PIDRunning: func(pid int) bool {
			return pid >= 101 && pid <= 103
		},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(got))
	}
	if got[0].PlanDir != canonical(t, validPlan) {
		t.Errorf("PlanDir = %q, want valid plan %q", got[0].PlanDir, canonical(t, validPlan))
	}
}

func TestDiscoverAllowsMissingCWDAndActiveTurn(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedLoop(t, root, planDir, loopSeed{
		PID:           123,
		RunStartedAt:  "2026-06-04T13:05:00+00:00",
		LockCreatedAt: "2026-06-04T13:00:00+00:00",
	})

	got, err := Discover(DiscoverOptions{
		StateRoot:  root,
		PIDRunning: func(int) bool { return true },
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(got))
	}
	if got[0].CWD != "" {
		t.Errorf("CWD = %q, want empty", got[0].CWD)
	}
	if got[0].ActiveSession != 0 || got[0].ActiveTurn != 0 {
		t.Errorf("active turn = session %d turn %d, want absent zeros",
			got[0].ActiveSession, got[0].ActiveTurn)
	}
	if got[0].Harness != "" {
		t.Errorf("Harness = %q, want empty", got[0].Harness)
	}
}

func TestDiscoverMissingRootReturnsEmpty(t *testing.T) {
	got, err := Discover(DiscoverOptions{
		StateRoot: filepath.Join(t.TempDir(), "does-not-exist"),
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(candidates) = %d, want 0", len(got))
	}
}

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
		{PlanDir: "/plans/a", RunStartedAt: base, LockCreatedAt: base},
		{PlanDir: "/plans/newest", RunStartedAt: base.Add(2 * time.Minute), LockCreatedAt: base},
		{PlanDir: "/plans/lock-newer", RunStartedAt: base, LockCreatedAt: base.Add(time.Minute)},
		{PlanDir: "/plans/0", RunStartedAt: base, LockCreatedAt: base},
	}

	SortCandidates(candidates)
	got := []string{
		candidates[0].PlanDir,
		candidates[1].PlanDir,
		candidates[2].PlanDir,
		candidates[3].PlanDir,
	}
	want := []string{"/plans/newest", "/plans/lock-newer", "/plans/0", "/plans/a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %#v, want %#v", got, want)
		}
	}
}

func seedLoop(t *testing.T, stateRoot, planDir string, seed loopSeed) string {
	t.Helper()
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if seed.CWD != "" {
		if err := os.MkdirAll(seed.CWD, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	stateDir := stateDirForPlan(t, stateRoot, planDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeJSON(t, filepath.Join(stateDir, lockFileName), map[string]any{
		"pid":        seed.PID,
		"plan_dir":   canonical(t, planDir),
		"created_at": seed.LockCreatedAt,
	})

	state := map[string]any{
		"run_id":         seed.RunID,
		"plan_dir":       canonical(t, planDir),
		"run_started_at": seed.RunStartedAt,
	}
	if seed.CWD != "" {
		state["cwd"] = seed.CWD
	}
	if seed.Harness != "" {
		state["harness"] = seed.Harness
	}
	if seed.KittyWindowID > 0 {
		state["kitty_window_id"] = seed.KittyWindowID
	}
	if seed.ActiveTurn != nil {
		activeTurnState := map[string]any{
			"session":    seed.ActiveTurn.Session,
			"turn_index": seed.ActiveTurn.TurnIndex,
		}
		if seed.ActiveTurn.Harness != "" {
			activeTurnState["harness"] = seed.ActiveTurn.Harness
		}
		state["active_turn"] = activeTurnState
	}
	if seed.HasRunFinishedAt {
		state["run_finished_at"] = seed.RunFinishedAt
	}
	writeJSON(t, filepath.Join(stateDir, stateFileName), state)
	return stateDir
}

func stateDirForPlan(t *testing.T, stateRoot, planDir string) string {
	t.Helper()
	return filepath.Join(stateRoot, pathSHA256(canonical(t, planDir)))
}

func canonical(t *testing.T, path string) string {
	t.Helper()
	got, err := CanonicalizePath(path)
	if err != nil {
		t.Fatalf("CanonicalizePath(%q): %v", path, err)
	}
	return got
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
