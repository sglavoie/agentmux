package agentloop

import (
	"os"
	"path/filepath"
	"testing"
)

// seedLoopState writes a lock + state pair from raw state fields so phase
// tests can exercise shapes loopSeed does not model (verification, pause,
// non-integer session labels).
func seedLoopState(t *testing.T, stateRoot, planDir string, pid int, state map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := stateDirForPlan(t, stateRoot, planDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(stateDir, lockFileName), map[string]any{
		"pid":        pid,
		"plan_dir":   canonical(t, planDir),
		"created_at": "2026-07-10T13:00:00+00:00",
	})
	state["plan_dir"] = canonical(t, planDir)
	writeJSON(t, filepath.Join(stateDir, stateFileName), state)
	return stateDir
}

func TestDiscoverTurnPhaseWithHeartbeat(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	heartbeat := "2026-07-10T13:10:30.123456+00:00"
	seedLoopState(t, root, planDir, 123, map[string]any{
		"run_id":         "run-turn",
		"run_started_at": "2026-07-10T13:05:00+00:00",
		"active_turn": map[string]any{
			"session":           4,
			"turn_index":        2,
			"last_heartbeat_at": heartbeat,
		},
	})

	got, err := Discover(DiscoverOptions{StateRoot: root, PIDRunning: func(int) bool { return true }})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(got))
	}
	c := got[0]
	if c.Phase != PhaseTurn || c.ActiveVerification || c.Paused {
		t.Errorf("phase = %q verification=%v paused=%v; want turn/false/false", c.Phase, c.ActiveVerification, c.Paused)
	}
	if c.HeartbeatAtRaw != heartbeat || c.HeartbeatAt.IsZero() {
		t.Errorf("heartbeat = %q/%v, want parsed %q", c.HeartbeatAtRaw, c.HeartbeatAt, heartbeat)
	}
	if c.RunID != "run-turn" {
		t.Errorf("RunID = %q, want run-turn", c.RunID)
	}
}

func TestDiscoverVerificationPhaseUsesCheckStart(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	checkStarted := "2026-07-10T13:20:00+00:00"
	seedLoopState(t, root, planDir, 123, map[string]any{
		"run_id":         "run-verify",
		"run_started_at": "2026-07-10T13:05:00+00:00",
		"active_verification": map[string]any{
			"session":                  8,
			"turn_index":               3,
			"started_at":               "2026-07-10T13:19:00+00:00",
			"current_check_started_at": checkStarted,
		},
	})

	got, err := Discover(DiscoverOptions{StateRoot: root, PIDRunning: func(int) bool { return true }})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(got))
	}
	c := got[0]
	if c.Phase != PhaseVerification || !c.ActiveVerification {
		t.Errorf("phase = %q verification=%v; want verification/true", c.Phase, c.ActiveVerification)
	}
	if c.HeartbeatAtRaw != checkStarted {
		t.Errorf("heartbeat = %q, want current check start %q", c.HeartbeatAtRaw, checkStarted)
	}
}

func TestDiscoverVerificationPhasePrefersLastHeartbeat(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	lastHeartbeat := "2026-07-10T13:21:30+00:00"
	seedLoopState(t, root, planDir, 123, map[string]any{
		"run_id":         "run-verify-hb",
		"run_started_at": "2026-07-10T13:05:00+00:00",
		"active_verification": map[string]any{
			"session":                  8,
			"turn_index":               3,
			"started_at":               "2026-07-10T13:19:00+00:00",
			"current_check_started_at": "2026-07-10T13:20:00+00:00",
			"last_heartbeat_at":        lastHeartbeat,
		},
	})

	got, err := Discover(DiscoverOptions{StateRoot: root, PIDRunning: func(int) bool { return true }})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(got))
	}
	c := got[0]
	if c.Phase != PhaseVerification || !c.ActiveVerification {
		t.Errorf("phase = %q verification=%v; want verification/true", c.Phase, c.ActiveVerification)
	}
	if c.HeartbeatAtRaw != lastHeartbeat || c.HeartbeatAt.IsZero() {
		t.Errorf("heartbeat = %q/%v, want verification last heartbeat %q", c.HeartbeatAtRaw, c.HeartbeatAt, lastHeartbeat)
	}
}

func TestDiscoverBetweenPhaseWithoutActiveTurn(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedLoopState(t, root, planDir, 123, map[string]any{
		"run_id":         "run-between",
		"run_started_at": "2026-07-10T13:05:00+00:00",
	})

	got, err := Discover(DiscoverOptions{StateRoot: root, PIDRunning: func(int) bool { return true }})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 || got[0].Phase != PhaseBetween {
		t.Fatalf("candidates = %+v; want one between-phase candidate", got)
	}
	if !got[0].HeartbeatAt.IsZero() || got[0].HeartbeatAtRaw != "" {
		t.Errorf("heartbeat = %q/%v, want empty between turns", got[0].HeartbeatAtRaw, got[0].HeartbeatAt)
	}
}

// A pause finalizes the loop process: the PID dies and run_finished_at is
// written. Default discovery must keep hiding it (routing and loop markers
// require live processes); IncludePaused exposes it for reconciliation.
func TestDiscoverPausedRunOnlyWhenIncluded(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedLoopState(t, root, planDir, 999, map[string]any{
		"run_id":          "run-paused",
		"run_started_at":  "2026-07-10T13:05:00+00:00",
		"run_finished_at": "2026-07-10T13:30:00+00:00",
		"paused":          true,
		"pause_reason":    "verify-failed",
	})
	deadPID := func(int) bool { return false }

	got, err := Discover(DiscoverOptions{StateRoot: root, PIDRunning: deadPID})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("default discovery = %+v; want paused run hidden", got)
	}

	got, err = Discover(DiscoverOptions{StateRoot: root, PIDRunning: deadPID, IncludePaused: true})
	if err != nil {
		t.Fatalf("Discover(IncludePaused): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1 paused candidate", len(got))
	}
	c := got[0]
	if c.Phase != PhasePaused || !c.Paused {
		t.Errorf("phase = %q paused=%v; want paused/true", c.Phase, c.Paused)
	}
	if c.RunID != "run-paused" {
		t.Errorf("RunID = %q, want run-paused", c.RunID)
	}
}

// A dotted session label ("33.3" for an inserted sub-session) must not fail
// the whole state decode — dropping a live run from discovery would let
// reconciliation revoke a healthy lease.
func TestDiscoverToleratesNonIntegerSessionLabel(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedLoopState(t, root, planDir, 123, map[string]any{
		"run_id":         "run-dotted",
		"run_started_at": "2026-07-10T13:05:00+00:00",
		"active_turn": map[string]any{
			"session":    "33.3",
			"turn_index": 2,
		},
	})

	got, err := Discover(DiscoverOptions{StateRoot: root, PIDRunning: func(int) bool { return true }})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1 (dotted session must not drop the run)", len(got))
	}
	if got[0].Phase != PhaseTurn || got[0].ActiveSession != 0 || got[0].ActiveTurn != 2 {
		t.Errorf("candidate = %+v; want turn phase, session degraded to 0, turn 2", got[0])
	}
}
