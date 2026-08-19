package agentloop

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"syscall"
)

// IsPIDRunning mirrors agent-session-loop's os.kill(pid, 0) check.
func IsPIDRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err == nil {
		return true
	} else if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

type stateFile struct {
	RunID              string              `json:"run_id"`
	PlanDir            string              `json:"plan_dir"`
	CWD                string              `json:"cwd"`
	Harness            string              `json:"harness"`
	RunStartedAt       string              `json:"run_started_at"`
	RunFinishedAt      json.RawMessage     `json:"run_finished_at"`
	Paused             bool                `json:"paused"`
	ActiveTurn         *activeTurn         `json:"active_turn"`
	ActiveVerification *activeVerification `json:"active_verification"`
	WaitingForReply    *waitingForReply    `json:"waiting_for_reply"`
	KittyWindowID      int                 `json:"kitty_window_id"`
}

// waitingForReply is the marker agent-session-loop persists while a run is
// blocked on a supervisor mailbox reply, and removes once the wait ends. Only
// the question identity is read: the question body never reaches discovery.
type waitingForReply struct {
	QuestionID string `json:"question_id"`
}

type activeTurn struct {
	TurnIndex       int      `json:"turn_index"`
	Session         looseInt `json:"session"`
	Harness         string   `json:"harness"`
	LastHeartbeatAt string   `json:"last_heartbeat_at"`
}

type activeVerification struct {
	TurnIndex             int      `json:"turn_index"`
	Session               looseInt `json:"session"`
	StartedAt             string   `json:"started_at"`
	CurrentCheckStartedAt string   `json:"current_check_started_at"`
	LastHeartbeatAt       string   `json:"last_heartbeat_at"`
}

// looseInt decodes a state-file session identifier that agent-session-loop may
// persist as a number or a dotted string (e.g. "33.3" for an inserted
// sub-session). A non-integer value degrades to 0 instead of failing the whole
// state decode — dropping a live run from discovery because its session label
// is not an integer would let reconciliation revoke a healthy lease.
type looseInt int

func (l *looseInt) UnmarshalJSON(data []byte) error {
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		*l = looseInt(n)
		return nil
	}
	*l = 0
	return nil
}

func readCandidate(stateDir string, pidRunning PIDChecker, includePaused bool) (Candidate, bool) {
	lock, ok := readLock(filepath.Join(stateDir, lockFileName))
	if !ok {
		return Candidate{}, false
	}

	planDir, err := CanonicalizePath(lock.planDir)
	if err != nil || planDir == "" {
		return Candidate{}, false
	}
	if stateHash := filepath.Base(stateDir); isHexSHA256(stateHash) && stateHash != pathSHA256(planDir) {
		return Candidate{}, false
	}

	state, ok := readState(filepath.Join(stateDir, stateFileName))
	if !ok {
		return Candidate{}, false
	}
	// A pause finalizes the loop process: run_finished_at is written and the
	// lock PID dies even though the run is still awaiting a human. Callers that
	// opted in via IncludePaused see it anyway; every other consumer keeps the
	// liveness contract (running PID, unfinished run).
	pausedCandidate := includePaused && state.Paused
	if !pausedCandidate {
		if !pidRunning(lock.pid) {
			return Candidate{}, false
		}
		if hasRunFinishedAt(state.RunFinishedAt) {
			return Candidate{}, false
		}
	}
	if state.PlanDir != "" {
		statePlanDir, err := CanonicalizePath(state.PlanDir)
		if err != nil || statePlanDir != planDir {
			return Candidate{}, false
		}
	}

	cwd := ""
	if state.CWD != "" {
		cwd, err = CanonicalizePath(state.CWD)
		if err != nil {
			return Candidate{}, false
		}
	}

	activeSession := 0
	activeTurnIndex := 0
	if state.ActiveTurn != nil {
		activeSession = int(state.ActiveTurn.Session)
		activeTurnIndex = state.ActiveTurn.TurnIndex
	}

	candidateHarness := effectiveHarness(state)
	windowID := 0
	if state.KittyWindowID > 0 {
		windowID = state.KittyWindowID
	}
	heartbeatRaw := heartbeatTimestamp(state)
	return Candidate{
		PlanDir:            planDir,
		StateDir:           stateDir,
		CWD:                cwd,
		Harness:            candidateHarness,
		WindowID:           windowID,
		PID:                lock.pid,
		LockCreatedAt:      parseTimestamp(lock.createdAt),
		LockCreatedAtRaw:   lock.createdAt,
		RunStartedAt:       parseTimestamp(state.RunStartedAt),
		RunStartedAtRaw:    state.RunStartedAt,
		RunID:              state.RunID,
		ActiveSession:      activeSession,
		ActiveTurn:         activeTurnIndex,
		Phase:              statePhase(state),
		ActiveVerification: state.ActiveVerification != nil,
		Paused:             state.Paused,
		HeartbeatAt:        parseTimestamp(heartbeatRaw),
		HeartbeatAtRaw:     heartbeatRaw,
	}, true
}

// statePhase derives the run's lifecycle phase from persisted state. Pause wins
// (a paused run's active turn/verification snapshots are already cleared, but a
// racing writer must still classify as paused); an in-flight verification
// outranks the turn it verifies; an active turn is executing; a live
// waiting_for_reply marker means the run is blocked on a mailbox reply between
// turns; anything else is alive between turns. Waiting deliberately ranks below
// executing work so a marker a racing writer has not yet cleared cannot mask a
// turn that has already resumed.
func statePhase(state stateFile) string {
	switch {
	case state.Paused:
		return PhasePaused
	case state.ActiveVerification != nil:
		return PhaseVerification
	case state.ActiveTurn != nil:
		return PhaseTurn
	case state.WaitingForReply != nil:
		return PhaseWaiting
	default:
		return PhaseBetween
	}
}
