package agentloop

import (
	"os"
	"path/filepath"
	"testing"
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

func TestLookupPlanStateReturnsFinishedRunWorkspaceWithoutLiveness(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	cwd := filepath.Join(t.TempDir(), "repo")
	stateDir := stateDirForPlan(t, root, planDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(stateDir, stateFileName), map[string]any{
		"plan_dir":        canonical(t, planDir),
		"cwd":             cwd,
		"run_finished_at": "2026-07-22T12:00:00Z",
	})

	got, found, err := LookupPlanState(planDir, root)
	if err != nil || !found {
		t.Fatalf("LookupPlanState = %#v, %v, %v", got, found, err)
	}
	if got.PlanDir != canonical(t, planDir) || got.CWD != canonical(t, cwd) || got.StateDir != canonical(t, stateDir) {
		t.Fatalf("LookupPlanState = %#v", got)
	}
}

func TestLookupPlanStateRejectsMismatchedEmbeddedPlan(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	stateDir := stateDirForPlan(t, root, planDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(stateDir, stateFileName), map[string]any{
		"plan_dir": filepath.Join(t.TempDir(), "other"),
		"cwd":      filepath.Join(t.TempDir(), "repo"),
	})

	if _, found, err := LookupPlanState(planDir, root); err == nil || found {
		t.Fatalf("LookupPlanState mismatch = found %v err %v", found, err)
	}
}
