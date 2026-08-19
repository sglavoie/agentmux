package agentloop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	appName       = "agent-session-loop"
	lockFileName  = "lock.json"
	stateFileName = "state.json"
	maxIntValue   = int(^uint(0) >> 1)
)

// PIDChecker reports whether a process id still represents a running process.
type PIDChecker func(pid int) bool

// Run phases derived from persisted loop state. The string values match the
// daemon reducer's ASL phase vocabulary so a discovered candidate's phase can
// seed a lease without translation.
const (
	PhaseTurn         = "turn"         // a session turn is executing
	PhaseVerification = "verification" // a verification pass is running
	PhaseBetween      = "between"      // run alive, idle between turns
	PhaseWaiting      = "waiting"      // run alive, blocked on a mailbox reply
	PhasePaused       = "paused"       // run paused awaiting a human
)

// DiscoverOptions controls active loop discovery. Empty fields use production
// defaults, while tests can inject a state root and PID checker.
type DiscoverOptions struct {
	StateRoot  string
	PIDRunning PIDChecker
	// IncludePaused also returns paused runs. A pause finalizes the loop
	// process (the lock PID dies and run_finished_at is written), so paused
	// runs are invisible to the default liveness checks even though they are
	// still awaiting a human. Only reconciliation opts in: routing and loop
	// marker consumers must keep seeing live processes only.
	IncludePaused bool
}

// Candidate is one active agent-session-loop run discovered from state files.
type Candidate struct {
	PlanDir  string
	StateDir string
	CWD      string
	Harness  string
	WindowID int
	PID      int

	LockCreatedAt    time.Time
	LockCreatedAtRaw string
	RunStartedAt     time.Time
	RunStartedAtRaw  string

	RunID         string
	ActiveSession int
	ActiveTurn    int

	// Phase is the run's derived lifecycle phase (PhaseTurn, PhaseVerification,
	// PhaseBetween, PhaseWaiting, or PhasePaused). ActiveVerification reports whether a
	// verification pass is in flight, Paused whether the run is paused awaiting
	// a human, and HeartbeatAt the most recent liveness signal persisted by the
	// run (the active turn's last heartbeat, or the verification's current
	// check start). HeartbeatAtRaw preserves the unparsed timestamp.
	Phase              string
	ActiveVerification bool
	Paused             bool
	HeartbeatAt        time.Time
	HeartbeatAtRaw     string
}

// PlanState is the durable workspace association retained for a plan after
// its ASL process exits. Unlike Candidate, it has no liveness requirement.
type PlanState struct {
	PlanDir  string
	StateDir string
	CWD      string
}

// WorkspaceTarget describes an open workspace session that can receive loop
// marker state.
type WorkspaceTarget struct {
	CWD      string
	Harness  string
	WindowID int
}

// DefaultStateRoot mirrors agent-session-loop's operational state location.
func DefaultStateRoot() string {
	var base string
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		base = xdg
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		base = filepath.Join(home, ".local", "state")
	} else {
		base = filepath.Join("~", ".local", "state")
	}

	root := filepath.Join(base, appName)
	if canonical, err := CanonicalizePath(root); err == nil {
		return canonical
	}
	return filepath.Clean(root)
}

// LookupPlanState reads the durable state for one exact plan directory. A
// missing state file or a valid state without cwd returns found=false. The
// state-directory hash and embedded plan_dir must both match so callers never
// classify a plan from unrelated or relocated state.
func LookupPlanState(planDir, stateRoot string) (state PlanState, found bool, err error) {
	canonicalPlan, err := CanonicalizePath(planDir)
	if err != nil {
		return PlanState{}, false, fmt.Errorf("agentloop: resolving plan directory %s: %w", planDir, err)
	}
	if canonicalPlan == "" {
		return PlanState{}, false, errors.New("agentloop: plan directory is empty")
	}
	if stateRoot == "" {
		stateRoot = DefaultStateRoot()
	}
	canonicalRoot, err := CanonicalizePath(stateRoot)
	if err != nil {
		return PlanState{}, false, fmt.Errorf("agentloop: resolving state root %s: %w", stateRoot, err)
	}
	stateDir := filepath.Join(canonicalRoot, pathSHA256(canonicalPlan))
	statePath := filepath.Join(stateDir, stateFileName)
	data, err := os.ReadFile(statePath)
	if errors.Is(err, fs.ErrNotExist) {
		return PlanState{}, false, nil
	}
	if err != nil {
		return PlanState{}, false, fmt.Errorf("agentloop: reading plan state %s: %w", statePath, err)
	}
	var persisted stateFile
	if err := json.Unmarshal(data, &persisted); err != nil {
		return PlanState{}, false, fmt.Errorf("agentloop: decoding plan state %s: %w", statePath, err)
	}
	if strings.TrimSpace(persisted.PlanDir) == "" {
		return PlanState{}, false, fmt.Errorf("agentloop: plan state %s has no plan_dir", statePath)
	}
	statePlan, err := CanonicalizePath(persisted.PlanDir)
	if err != nil || statePlan != canonicalPlan {
		return PlanState{}, false, fmt.Errorf("agentloop: plan state %s belongs to %q, not %q", statePath, persisted.PlanDir, canonicalPlan)
	}
	if strings.TrimSpace(persisted.CWD) == "" {
		return PlanState{}, false, nil
	}
	cwd, err := CanonicalizePath(persisted.CWD)
	if err != nil {
		return PlanState{}, false, fmt.Errorf("agentloop: resolving plan state cwd %s: %w", persisted.CWD, err)
	}
	return PlanState{PlanDir: canonicalPlan, StateDir: stateDir, CWD: cwd}, true, nil
}

// Discover scans the configured state root and returns active candidates sorted
// newest-first. Per-run malformed or stale state is skipped so one bad state
// directory does not hide other active loops.
func Discover(opts DiscoverOptions) ([]Candidate, error) {
	root := opts.StateRoot
	if root == "" {
		root = DefaultStateRoot()
	}
	canonicalRoot, err := CanonicalizePath(root)
	if err != nil {
		return nil, fmt.Errorf("agentloop: resolving state root %s: %w", root, err)
	}

	entries, err := os.ReadDir(canonicalRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("agentloop: reading state root %s: %w", canonicalRoot, err)
	}

	pidRunning := opts.PIDRunning
	if pidRunning == nil {
		pidRunning = IsPIDRunning
	}

	candidates := make([]Candidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		stateDir, err := CanonicalizePath(filepath.Join(canonicalRoot, entry.Name()))
		if err != nil {
			continue
		}
		candidate, ok := readCandidate(stateDir, pidRunning, opts.IncludePaused)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	SortCandidates(candidates)
	return candidates, nil
}

// SortCandidates orders candidates newest-first with deterministic tie breaks.
func SortCandidates(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if !a.RunStartedAt.Equal(b.RunStartedAt) {
			return a.RunStartedAt.After(b.RunStartedAt)
		}
		if !a.LockCreatedAt.Equal(b.LockCreatedAt) {
			return a.LockCreatedAt.After(b.LockCreatedAt)
		}
		return a.PlanDir < b.PlanDir
	})
}

// MatchingCWD returns candidates whose loop cwd matches selectedDir after
// canonicalization.
func MatchingCWD(candidates []Candidate, selectedDir string) ([]Candidate, error) {
	if selectedDir == "" {
		return nil, nil
	}
	selectedCanonical, err := CanonicalizePath(selectedDir)
	if err != nil {
		return nil, fmt.Errorf("agentloop: resolving selected directory %s: %w", selectedDir, err)
	}

	matches := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.CWD == "" {
			continue
		}
		cwd, err := CanonicalizePath(candidate.CWD)
		if err != nil {
			continue
		}
		if cwd == selectedCanonical {
			matches = append(matches, candidate)
		}
	}
	return matches, nil
}

// MatchingWorkspace returns loop candidates for target's workspace, preferring
// candidates with target's harness when present. If a different same-workspace
// harness has an open target, that harness owns the loop marker; otherwise the
// function falls back to CWD-only workspace matching for legacy state.
//
// Candidate WindowID is treated as authoritative only when it identifies a
// managed window in openTargets. A candidate whose WindowID points outside the
// managed set (for example, an ASL pane nested inside a managed tab) is still
// eligible for CWD/harness fallback so the loop marker reaches the managed
// session that owns the workspace.
func MatchingWorkspace(candidates []Candidate, target WorkspaceTarget, openTargets []WorkspaceTarget) ([]Candidate, error) {
	workspaceCandidates := candidates
	if target.WindowID > 0 {
		matches := matchingWindow(candidates, target.WindowID)
		if len(matches) > 0 {
			return matches, nil
		}
		workspaceCandidates = candidatesEligibleForWindowFallback(candidates, managedWindowIDs(openTargets, target.WindowID))
	}

	if target.CWD == "" {
		return nil, nil
	}
	selectedCanonical, err := CanonicalizePath(target.CWD)
	if err != nil {
		return nil, fmt.Errorf("agentloop: resolving selected directory %s: %w", target.CWD, err)
	}

	matches := matchingCanonicalCWD(workspaceCandidates, selectedCanonical)
	if len(matches) == 0 {
		return nil, nil
	}

	targetHarness := normalizeKnownHarness(target.Harness)
	if targetHarness != "" {
		sameHarness := filterHarness(matches, targetHarness)
		if len(sameHarness) > 0 {
			return sameHarness, nil
		}
	}

	if workspaceHasOpenCandidateHarness(matches, selectedCanonical, openTargets) {
		return nil, nil
	}
	return matches, nil
}

// HasMatchingCWD reports whether any candidate's cwd matches selectedDir after
// canonicalization.
func HasMatchingCWD(candidates []Candidate, selectedDir string) (bool, error) {
	matches, err := MatchingCWD(candidates, selectedDir)
	if err != nil {
		return false, err
	}
	return len(matches) > 0, nil
}

// HasMatchingWorkspace reports whether target owns a loop marker under
// MatchingWorkspace's workspace/harness rules.
func HasMatchingWorkspace(candidates []Candidate, target WorkspaceTarget, openTargets []WorkspaceTarget) (bool, error) {
	matches, err := MatchingWorkspace(candidates, target, openTargets)
	if err != nil {
		return false, err
	}
	return len(matches) > 0, nil
}

// CanonicalizePath mirrors agent-session-loop's realpath(abs(expanduser(path)))
// behavior without requiring the path to exist.
func CanonicalizePath(path string) (string, error) {
	expanded := expandUser(path)
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return filepath.Clean(abs), nil
}
