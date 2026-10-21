package agentloop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslcontract"
)

func TestDiscoverProjectsEveryContractField(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	base := t.TempDir()
	planDir := filepath.Join(base, "plan")
	workspace := filepath.Join(base, "project")
	stateDir := seedProjection(t, root, planDir, projectionSeed{
		RunID:          "run-1",
		Phase:          PhaseTurn,
		WorkspaceRoot:  workspace,
		Harness:        "codex",
		KittyWindowID:  51,
		SessionID:      "33.3",
		TurnIndex:      7,
		QuestionID:     "q-1",
		Sequence:       12,
		Generation:     4,
		PID:            123,
		RegistryDigest: "3398ee41",
		StartedAt:      "2026-06-04T13:05:00.123456+00:00",
		UpdatedAt:      "2026-06-04T13:06:00+00:00",
	})

	got, err := Discover(DiscoverOptions{
		StateRoot:    root,
		PIDRunning:   func(pid int) bool { return pid == 123 },
		ProcessBirth: matchingBirth(),
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
	// The surrounding state document names /ignored/legacy/cwd; only the
	// projection's workspace root may reach a candidate.
	if candidate.CWD != canonical(t, workspace) {
		t.Errorf("CWD = %q, want the projection's workspace root %q", candidate.CWD, canonical(t, workspace))
	}
	if candidate.Harness != "codex" {
		t.Errorf("Harness = %q, want codex", candidate.Harness)
	}
	if candidate.WindowID != 51 {
		t.Errorf("WindowID = %d, want 51", candidate.WindowID)
	}
	if candidate.PID != 123 || candidate.StartToken != testStartToken {
		t.Errorf("process = %d/%q, want 123/%q", candidate.PID, candidate.StartToken, testStartToken)
	}
	if candidate.RunID != "run-1" || candidate.ContractSetID != testContractSetID {
		t.Errorf("identity = %q/%q", candidate.RunID, candidate.ContractSetID)
	}
	if candidate.SessionID != "33.3" || candidate.TurnIndex != 7 || candidate.QuestionID != "q-1" {
		t.Errorf("position = %q/%d/%q, want 33.3/7/q-1", candidate.SessionID, candidate.TurnIndex, candidate.QuestionID)
	}
	if candidate.SourceSequence != 12 || candidate.Generation != 4 {
		t.Errorf("counters = %d/%d, want 12/4", candidate.SourceSequence, candidate.Generation)
	}
	if candidate.RegistryDigest != "3398ee41" {
		t.Errorf("RegistryDigest = %q", candidate.RegistryDigest)
	}
	if candidate.RunStartedAtRaw != "2026-06-04T13:05:00.123456+00:00" || candidate.RunStartedAt.IsZero() {
		t.Errorf("RunStartedAt = %q/%v", candidate.RunStartedAtRaw, candidate.RunStartedAt)
	}
	if candidate.UpdatedAtRaw != "2026-06-04T13:06:00+00:00" || candidate.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt = %q/%v", candidate.UpdatedAtRaw, candidate.UpdatedAt)
	}
}

// The hard paired cutover: state written by an ASL that predates the projection
// yields no candidate AND no claim of absence. Reporting it absent is what
// would let an unpaired upgrade retire every healthy run's lease at once.
func TestDiscoverTreatsStateWithoutAProjectionAsIndeterminate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	stateDir := stateDirForPlan(t, root, planDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(stateDir, stateFileName), map[string]any{
		"run_id":         "legacy-run",
		"plan_dir":       canonical(t, planDir),
		"cwd":            filepath.Join(t.TempDir(), "project"),
		"run_started_at": "2026-06-04T13:05:00+00:00",
		"active_turn":    map[string]any{"session": 1, "turn_index": 1},
	})

	result, err := DiscoverPlans(DiscoverOptions{StateRoot: root, PIDRunning: livePIDs})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	observation := observationFor(t, result, root, planDir)
	if observation.Presence != PresenceIndeterminate || observation.Reason != ReasonMissingProjection {
		t.Fatalf("observation = %+v, want indeterminate/%s", observation, ReasonMissingProjection)
	}
	if len(result.Candidates(true)) != 0 {
		t.Fatalf("candidates = %+v, want none", result.Candidates(true))
	}
}

func TestDiscoverRejectsUnsupportedContractSet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		seed projectionSeed
	}{
		{
			name: "different family",
			seed: projectionSeed{ContractSetID: "asl-amx-2.0000000000000000"},
		},
		{
			name: "newer schema",
			seed: projectionSeed{Mutate: func(d map[string]any) { d["schema_version"] = 2 }},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			planDir := filepath.Join(t.TempDir(), "plan")
			seedProjection(t, root, planDir, tc.seed)

			result, err := DiscoverPlans(DiscoverOptions{
				StateRoot:    root,
				PIDRunning:   livePIDs,
				ProcessBirth: matchingBirth(),
			})
			if err != nil {
				t.Fatalf("DiscoverPlans: %v", err)
			}
			observation := observationFor(t, result, root, planDir)
			if observation.Presence != PresenceIndeterminate || observation.Reason != ReasonUnsupportedContract {
				t.Fatalf("observation = %+v, want indeterminate/%s", observation, ReasonUnsupportedContract)
			}
		})
	}
}

// The projection's member set is exact in both directions: absence is spelled
// null, so a missing member is a corrupt document rather than a default, and an
// unknown member means the peer is describing something this build cannot
// interpret.
func TestDiscoverRejectsInexactProjectionMemberSet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing member", func(d map[string]any) { delete(d, "question_id") }},
		{"unknown member", func(d map[string]any) { d["future_field"] = 1 }},
		{"unknown phase", func(d map[string]any) { d["phase"] = "compacting" }},
		{"relative plan dir", func(d map[string]any) { d["plan_dir"] = "relative/plan" }},
		{"zero generation", func(d map[string]any) { d["projection_generation"] = 0 }},
		{"empty run id", func(d map[string]any) { d["run_id"] = "" }},
		{"untrimmed harness", func(d map[string]any) { d["harness"] = " codex " }},
		{"malformed timestamp", func(d map[string]any) { d["started_at"] = "not-a-time" }},
		{"process without token", func(d map[string]any) { d["process"] = map[string]any{"pid": 5} }},
		{"paused with a process", func(d map[string]any) {
			d["phase"] = PhasePaused
			d["process"] = map[string]any{"pid": 5, "start_token": testStartToken}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			planDir := filepath.Join(t.TempDir(), "plan")
			seedProjection(t, root, planDir, projectionSeed{Mutate: tc.mutate})

			result, err := DiscoverPlans(DiscoverOptions{
				StateRoot:    root,
				PIDRunning:   livePIDs,
				ProcessBirth: matchingBirth(),
			})
			if err != nil {
				t.Fatalf("DiscoverPlans: %v", err)
			}
			observation := observationFor(t, result, root, planDir)
			if observation.Presence != PresenceIndeterminate || observation.Reason != ReasonInvalidProjection {
				t.Fatalf("observation = %+v, want indeterminate/%s", observation, ReasonInvalidProjection)
			}
		})
	}
}

// One corrupt plan must not hide the healthy plans in the same sweep, and must
// not be reported absent itself.
func TestDiscoverIsolatesOneCorruptPlan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	healthyPlan := filepath.Join(t.TempDir(), "healthy")
	seedProjection(t, root, healthyPlan, projectionSeed{RunID: "run-healthy", PID: 200})

	corruptDir := filepath.Join(root, "corrupt")
	if err := os.MkdirAll(corruptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, stateFileName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	emptyDir := filepath.Join(root, "no-state")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:    root,
		PIDRunning:   livePIDs,
		ProcessBirth: matchingBirth(),
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	present, absent, indeterminate := result.Counts()
	if present != 1 || absent != 0 || indeterminate != 2 {
		t.Fatalf("counts = %d/%d/%d, want 1 present, 0 absent, 2 indeterminate", present, absent, indeterminate)
	}
	candidates := result.Candidates(true)
	if len(candidates) != 1 || candidates[0].RunID != "run-healthy" {
		t.Fatalf("candidates = %+v, want only run-healthy", candidates)
	}
	if !result.HasIndeterminate() {
		t.Fatal("HasIndeterminate = false, want true")
	}
}

// State copied into the wrong directory describes a run that may well be alive
// somewhere else, so it withholds evidence instead of reporting absence.
func TestDiscoverTreatsMisplacedStateAsIndeterminate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realPlan := filepath.Join(t.TempDir(), "real-plan")
	otherPlan := filepath.Join(t.TempDir(), "other-plan")
	stateDir := stateDirForPlan(t, root, otherPlan)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(stateDir, stateFileName), map[string]any{
		projectionKey: projectionDocument(t, realPlan, projectionSeed{RunID: "run-moved"}),
	})

	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:    root,
		PIDRunning:   livePIDs,
		ProcessBirth: matchingBirth(),
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	observation := observationFor(t, result, root, otherPlan)
	if observation.Presence != PresenceIndeterminate || observation.Reason != ReasonMisplacedState {
		t.Fatalf("observation = %+v, want indeterminate/%s", observation, ReasonMisplacedState)
	}
}

func TestDiscoverMissingRootReturnsEmpty(t *testing.T) {
	t.Parallel()

	result, err := DiscoverPlans(DiscoverOptions{StateRoot: filepath.Join(t.TempDir(), "does-not-exist")})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	if len(result.Observations) != 0 {
		t.Fatalf("observations = %+v, want none", result.Observations)
	}
}

// The contract publishes harness names already lowercased, so the only
// judgement left here is whether AgentMux knows the name. An unknown harness
// stays empty rather than being guessed at.
func TestDiscoverKeepsOnlyKnownHarnesses(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"copilot":        "copilot",
		"codex":          "codex",
		"future-harness": "",
	}
	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			root := t.TempDir()
			planDir := filepath.Join(t.TempDir(), "plan")
			seedProjection(t, root, planDir, projectionSeed{Harness: raw})

			got, err := Discover(DiscoverOptions{
				StateRoot:    root,
				PIDRunning:   livePIDs,
				ProcessBirth: matchingBirth(),
			})
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("len(candidates) = %d, want 1", len(got))
			}
			if got[0].Harness != want {
				t.Fatalf("Harness = %q, want %q", got[0].Harness, want)
			}
		})
	}
}

// A plan with no workspace publishes a null workspace root. Routing then falls
// back to window identity rather than inheriting another plan's directory.
func TestDiscoverAllowsPlanWithoutAWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedProjection(t, root, planDir, projectionSeed{KittyWindowID: 7})

	got, err := Discover(DiscoverOptions{
		StateRoot:    root,
		PIDRunning:   livePIDs,
		ProcessBirth: matchingBirth(),
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(got))
	}
	if got[0].CWD != "" || got[0].WindowID != 7 || got[0].TurnIndex != -1 {
		t.Fatalf("candidate = %+v, want empty CWD, window 7, turn index -1", got[0])
	}
}

func TestLookupPlanStateReadsWorkspaceRootFromTheProjection(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	workspace := filepath.Join(t.TempDir(), "repo")
	stateDir := seedProjection(t, root, planDir, projectionSeed{
		Phase:         PhaseFinished,
		WorkspaceRoot: workspace,
		FinishedAt:    "2026-07-22T12:00:00+00:00",
	})

	got, found, err := LookupPlanState(planDir, root)
	if err != nil || !found {
		t.Fatalf("LookupPlanState = %#v, %v, %v", got, found, err)
	}
	if got.PlanDir != canonical(t, planDir) || got.CWD != canonical(t, workspace) || got.StateDir != canonical(t, stateDir) {
		t.Fatalf("LookupPlanState = %#v", got)
	}
}

// A plan with no workspace root has no association to report, and the caller
// falls through to its own metadata rather than being handed a guess.
func TestLookupPlanStateReportsNotFoundWithoutAWorkspaceRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedProjection(t, root, planDir, projectionSeed{})

	got, found, err := LookupPlanState(planDir, root)
	if err != nil || found {
		t.Fatalf("LookupPlanState = %#v, %v, %v; want not found without error", got, found, err)
	}
}

func TestLookupPlanStateIgnoresStateWithoutAProjection(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	stateDir := stateDirForPlan(t, root, planDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(stateDir, stateFileName), map[string]any{
		"plan_dir": canonical(t, planDir),
		"cwd":      filepath.Join(t.TempDir(), "repo"),
	})

	got, found, err := LookupPlanState(planDir, root)
	if err != nil || found {
		t.Fatalf("LookupPlanState = %#v, %v, %v; want not found without error", got, found, err)
	}
}

func TestLookupPlanStateRejectsMismatchedEmbeddedPlan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	otherPlan := filepath.Join(t.TempDir(), "other")
	stateDir := stateDirForPlan(t, root, planDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(stateDir, stateFileName), map[string]any{
		projectionKey: projectionDocument(t, otherPlan, projectionSeed{
			WorkspaceRoot: filepath.Join(t.TempDir(), "repo"),
		}),
	})

	if _, found, err := LookupPlanState(planDir, root); err == nil || found {
		t.Fatalf("LookupPlanState mismatch = found %v err %v", found, err)
	}
}

func TestDiscoverReviewedLoopContractStillRequiresLiveProcess(t *testing.T) {
	t.Parallel()

	root, planDir := t.TempDir(), t.TempDir()
	seedProjection(t, root, planDir, projectionSeed{
		RunID: "current-run", Phase: PhaseTurn, PID: 123, KittyWindowID: 3,
		ContractSetID: aslcontract.LoopCompatibleContractSetID,
	})
	for _, alive := range []bool{true, false} {
		result, err := DiscoverPlans(DiscoverOptions{
			StateRoot: root, PIDRunning: func(int) bool { return alive }, ProcessBirth: matchingBirth(),
		})
		if err != nil {
			t.Fatal(err)
		}
		candidates := result.Candidates(false)
		if alive {
			if len(candidates) != 1 || candidates[0].RunID != "current-run" || candidates[0].WindowID != 3 {
				t.Fatalf("live reviewed-contract loop was not discovered: %+v", result)
			}
		} else if len(candidates) != 0 || result.Observations[0].Presence != PresenceConfirmedAbsent {
			t.Fatalf("exited reviewed-contract loop still appears active: %+v", result)
		}
	}
}
