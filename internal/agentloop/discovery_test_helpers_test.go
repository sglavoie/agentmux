package agentloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// testContractSetID is the contract set this build reads. It is the vendored
// constant rather than a literal: matching is exact now, so a test that spelled
// its own ID would seed projections this build must refuse.
const testContractSetID = ContractSetID

const testStartToken = "Fri Jul 10 13:05:00 2026"

// projectionSeed describes one `agent_mux` projection to write. Zero fields get
// contract-valid defaults, so a test names only what it is actually about.
type projectionSeed struct {
	RunID          string
	Phase          string
	WorkspaceRoot  string
	Harness        string
	KittyWindowID  int
	SessionID      string
	QuestionID     string
	TurnIndex      any
	Sequence       int
	Generation     int
	PID            int
	StartToken     string
	StartedAt      string
	UpdatedAt      string
	FinishedAt     any
	RegistryDigest string
	ContractSetID  string
	// NoProcess forces a live-phase projection to publish a null process, the
	// shape ASL writes when it cannot establish its own birth.
	NoProcess bool
	// Mutate edits the finished projection document immediately before it is
	// written, for tests that need a shape the contract forbids.
	Mutate func(map[string]any)
}

// projectionDocument builds the nested `agent_mux` block for planDir.
func projectionDocument(t *testing.T, planDir string, seed projectionSeed) map[string]any {
	t.Helper()
	phase := seed.Phase
	if phase == "" {
		phase = PhaseTurn
	}
	contractSetID := seed.ContractSetID
	if contractSetID == "" {
		contractSetID = testContractSetID
	}
	generation := seed.Generation
	if generation == 0 {
		generation = 1
	}
	startedAt := seed.StartedAt
	if startedAt == "" {
		startedAt = "2026-07-10T13:05:00+00:00"
	}
	updatedAt := seed.UpdatedAt
	if updatedAt == "" {
		updatedAt = "2026-07-10T13:10:00+00:00"
	}
	runID := seed.RunID
	if runID == "" {
		runID = "run-1"
	}

	var process any
	// ASL publishes a process only while the run holds one; the two resting
	// phases have none by construction.
	if !seed.NoProcess && phase != PhasePaused && phase != PhaseFinished {
		pid := seed.PID
		if pid == 0 {
			pid = 4242
		}
		token := seed.StartToken
		if token == "" {
			token = testStartToken
		}
		process = map[string]any{"pid": pid, "start_token": token}
	}

	var turnIndex any
	if seed.TurnIndex != nil {
		turnIndex = seed.TurnIndex
	}
	var workspaceRoot any
	if seed.WorkspaceRoot != "" {
		workspaceRoot = canonical(t, seed.WorkspaceRoot)
	}
	var kittyWindowID any
	if seed.KittyWindowID > 0 {
		kittyWindowID = seed.KittyWindowID
	}
	var harnessName any
	if seed.Harness != "" {
		harnessName = seed.Harness
	}
	var sessionID any
	if seed.SessionID != "" {
		sessionID = seed.SessionID
	}
	var questionID any
	if seed.QuestionID != "" {
		questionID = seed.QuestionID
	}
	var registryDigest any
	if seed.RegistryDigest != "" {
		registryDigest = seed.RegistryDigest
	}

	document := map[string]any{
		"schema_version":            ProjectionSchema,
		"contract_set_id":           contractSetID,
		"run_id":                    runID,
		"plan_dir":                  canonical(t, planDir),
		"workspace_root":            workspaceRoot,
		"registry_digest":           registryDigest,
		"harness":                   harnessName,
		"kitty_window_id":           kittyWindowID,
		"phase":                     phase,
		"session_id":                sessionID,
		"turn_index":                turnIndex,
		"question_id":               questionID,
		"lifecycle_source_sequence": seed.Sequence,
		"projection_generation":     generation,
		"process":                   process,
		"started_at":                startedAt,
		"updated_at":                updatedAt,
		"finished_at":               seed.FinishedAt,
	}
	if seed.Mutate != nil {
		seed.Mutate(document)
	}
	return document
}

// seedProjection writes a state file carrying seed's projection into the state
// directory ASL would use for planDir, and returns that directory.
func seedProjection(t *testing.T, stateRoot, planDir string, seed projectionSeed) string {
	t.Helper()
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if seed.WorkspaceRoot != "" {
		if err := os.MkdirAll(seed.WorkspaceRoot, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stateDir := stateDirForPlan(t, stateRoot, planDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The surrounding state document carries ASL's own bookkeeping. Discovery
	// must ignore all of it, so the fixtures deliberately include fields the
	// old parser used to read.
	writeJSON(t, filepath.Join(stateDir, stateFileName), map[string]any{
		"run_id":          "ignored-legacy-run",
		"plan_dir":        canonical(t, planDir),
		"cwd":             "/ignored/legacy/cwd",
		"paused":          true,
		"run_finished_at": "2026-07-10T13:59:00+00:00",
		"active_turn":     map[string]any{"session": 9, "turn_index": 9, "harness": "codex"},
		projectionKey:     projectionDocument(t, planDir, seed),
	})
	return stateDir
}

// staticBirth is a birth probe that reports exactly tokens.
func staticBirth(tokens map[int]string) ProcessBirthLookup {
	return func([]int) (map[int]string, bool) { return tokens, true }
}

// matchingBirth reports every queried PID as born at the default token, which
// is what a healthy, unrecycled process looks like.
func matchingBirth() ProcessBirthLookup {
	return func(pids []int) (map[int]string, bool) {
		tokens := make(map[int]string, len(pids))
		for _, pid := range pids {
			tokens[pid] = testStartToken
		}
		return tokens, true
	}
}

func livePIDs(int) bool { return true }

func deadPIDs(int) bool { return false }

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

// observationFor returns the sweep's observation for planDir.
func observationFor(t *testing.T, result DiscoveryResult, stateRoot, planDir string) PlanObservation {
	t.Helper()
	want := canonical(t, stateDirForPlan(t, stateRoot, planDir))
	for _, observation := range result.Observations {
		if observation.StateDir == want {
			return observation
		}
	}
	t.Fatalf("no observation for %s in %+v", planDir, result.Observations)
	return PlanObservation{}
}
