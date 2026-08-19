package agentloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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
