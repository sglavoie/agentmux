package agentloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type loopSeed struct {
	PID           int
	CWD           string
	RunID         string
	RunStartedAt  string
	LockCreatedAt string
	ActiveTurn    *activeTurn
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
	if candidate.OverlayCWD() != canonical(t, cwd) {
		t.Errorf("OverlayCWD = %q, want cwd %q", candidate.OverlayCWD(), canonical(t, cwd))
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
	if got[0].OverlayCWD() != canonical(t, planDir) {
		t.Errorf("OverlayCWD = %q, want plan dir %q", got[0].OverlayCWD(), canonical(t, planDir))
	}
	if got[0].ActiveSession != 0 || got[0].ActiveTurn != 0 {
		t.Errorf("active turn = session %d turn %d, want absent zeros",
			got[0].ActiveSession, got[0].ActiveTurn)
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

func TestSelectForDirectoryMatchesCanonicalCWD(t *testing.T) {
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
	selection, err := SelectForDirectory([]Candidate{match, other}, linkDir)
	if err != nil {
		t.Fatalf("SelectForDirectory: %v", err)
	}
	if selection.Kind != SelectionLaunch {
		t.Fatalf("Kind = %v, want SelectionLaunch", selection.Kind)
	}
	candidate, ok := selection.LaunchCandidate()
	if !ok {
		t.Fatalf("LaunchCandidate returned false")
	}
	if candidate.PlanDir != match.PlanDir {
		t.Errorf("selected PlanDir = %q, want %q", candidate.PlanDir, match.PlanDir)
	}
}

func TestSelectForDirectoryAppliesSelectedDirectoryRules(t *testing.T) {
	a := Candidate{PlanDir: "/plans/a", CWD: "/work/a"}
	b := Candidate{PlanDir: "/plans/b", CWD: "/work/b"}
	c := Candidate{PlanDir: "/plans/c", CWD: "/work/b"}

	none, err := SelectForDirectory(nil, "/work/a")
	if err != nil {
		t.Fatalf("SelectForDirectory none: %v", err)
	}
	if none.Kind != SelectionNone {
		t.Errorf("empty Kind = %v, want SelectionNone", none.Kind)
	}

	noMatch, err := SelectForDirectory([]Candidate{a, b}, "/no/match")
	if err != nil {
		t.Fatalf("SelectForDirectory no match: %v", err)
	}
	if noMatch.Kind != SelectionNone || len(noMatch.Candidates) != 0 {
		t.Errorf("no-match selection = kind %v len %d, want none len 0",
			noMatch.Kind, len(noMatch.Candidates))
	}

	singleGlobal, err := SelectForDirectory([]Candidate{a}, "")
	if err != nil {
		t.Fatalf("SelectForDirectory single global: %v", err)
	}
	if singleGlobal.Kind != SelectionLaunch || len(singleGlobal.Candidates) != 1 {
		t.Errorf("single global selection = kind %v len %d, want launch len 1",
			singleGlobal.Kind, len(singleGlobal.Candidates))
	}

	multiGlobal, err := SelectForDirectory([]Candidate{a, b}, "")
	if err != nil {
		t.Fatalf("SelectForDirectory multi global: %v", err)
	}
	if multiGlobal.Kind != SelectionPicker || len(multiGlobal.Candidates) != 2 {
		t.Errorf("multi global selection = kind %v len %d, want picker len 2",
			multiGlobal.Kind, len(multiGlobal.Candidates))
	}

	multiMatch, err := SelectForDirectory([]Candidate{a, b, c}, "/work/b")
	if err != nil {
		t.Fatalf("SelectForDirectory multi match: %v", err)
	}
	if multiMatch.Kind != SelectionPicker || len(multiMatch.Candidates) != 2 {
		t.Fatalf("multi match selection = kind %v len %d, want picker len 2",
			multiMatch.Kind, len(multiMatch.Candidates))
	}
	if multiMatch.Candidates[0].PlanDir != b.PlanDir || multiMatch.Candidates[1].PlanDir != c.PlanDir {
		t.Errorf("multi match candidates = %#v, want b then c", multiMatch.Candidates)
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
	if seed.ActiveTurn != nil {
		state["active_turn"] = map[string]any{
			"session":    seed.ActiveTurn.Session,
			"turn_index": seed.ActiveTurn.TurnIndex,
		}
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
