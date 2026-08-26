package agentloop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	appName       = "agent-session-loop"
	stateFileName = "state.json"
	maxIntValue   = int(^uint(0) >> 1)
)

// PIDChecker reports whether a process id still represents a running process.
type PIDChecker func(pid int) bool

// Run phases as published by agent-session-loop's projection. The string values
// match the daemon reducer's ASL phase vocabulary so a discovered candidate's
// phase can seed a lease without translation.
const (
	PhaseStarting     = "starting"     // run alive, before its first turn
	PhaseTurn         = "turn"         // a session turn is executing
	PhaseVerification = "verification" // a verification pass is running
	PhaseBetween      = "between"      // run alive, idle between turns
	PhaseWaiting      = "waiting"      // run alive, blocked on a mailbox reply
	PhasePaused       = "paused"       // run paused awaiting a human
	PhaseFinished     = "finished"     // run over
)

// DiscoverOptions controls active loop discovery. Empty fields use production
// defaults, while tests can inject a state root, PID checker, and birth probe.
type DiscoverOptions struct {
	StateRoot  string
	PIDRunning PIDChecker
	// ProcessBirth resolves published PIDs to their birth tokens. Nil uses the
	// real `ps` probe.
	ProcessBirth ProcessBirthLookup
	// IncludePaused also returns paused runs. A pause finalizes the loop
	// process, so paused runs hold no PID even though they are still awaiting a
	// human. Only reconciliation opts in: routing and loop marker consumers
	// must keep seeing live processes only.
	IncludePaused bool
	// Cache memoizes projection decodes across sweeps. Nil reads through, which
	// is what a one-shot caller wants; a repeating caller should hold one, since
	// the state documents the projection is nested in are orders of magnitude
	// larger than the projection itself.
	Cache *ProjectionCache
}

// Candidate is one present agent-session-loop run, projected from the atomic
// `agent_mux` block ASL publishes. Every field comes from that block; AgentMux
// reads nothing else out of ASL's state, and holds no parser for state that
// does not carry it.
type Candidate struct {
	PlanDir  string
	StateDir string
	// CWD is the run's workspace root. It is empty for a plan that has no
	// workspace, which the projection spells as null rather than as a guess.
	CWD      string
	Harness  string
	WindowID int
	PID      int
	// StartToken is the birth time of PID, which is what distinguishes the loop
	// process from whatever else the kernel may later hand that PID to.
	StartToken string

	RunID string
	// ContractSetID is the ASL/AgentMux contract set this run publishes under,
	// retained so a compatibility problem can be reported with the peer's own
	// identifier rather than as a generic parse failure.
	ContractSetID  string
	RegistryDigest string

	// Phase is the run's lifecycle phase, Paused a convenience for the resting
	// phase that still holds a lease. SessionID and TurnIndex locate the run
	// inside its plan (TurnIndex is -1 when no turn is in flight), and
	// QuestionID names the mailbox question a waiting run is blocked on. The
	// question body never appears: the projection does not carry it.
	Phase      string
	Paused     bool
	SessionID  string
	TurnIndex  int
	QuestionID string

	// SourceSequence and Generation order evidence against itself.
	// SourceSequence counts lifecycle notifications, Generation counts durable
	// state transitions; neither derives from the other, so a run whose hooks
	// were all dropped still advances its generation.
	SourceSequence uint64
	Generation     uint64

	RunStartedAt    time.Time
	RunStartedAtRaw string
	UpdatedAt       time.Time
	UpdatedAtRaw    string
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

// DefaultStateRoot mirrors agent-session-loop's operational state location. It
// is the fallback for a deployment that has not recorded a canonical state root
// in configuration; pairing persists one so the two sides cannot drift.
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

// StateDirFor returns the state directory agent-session-loop uses for planDir
// under stateRoot.
func StateDirFor(planDir, stateRoot string) (string, error) {
	canonicalPlan, err := CanonicalizePath(planDir)
	if err != nil {
		return "", fmt.Errorf("agentloop: resolving plan directory %s: %w", planDir, err)
	}
	if canonicalPlan == "" {
		return "", errors.New("agentloop: plan directory is empty")
	}
	if stateRoot == "" {
		stateRoot = DefaultStateRoot()
	}
	canonicalRoot, err := CanonicalizePath(stateRoot)
	if err != nil {
		return "", fmt.Errorf("agentloop: resolving state root %s: %w", stateRoot, err)
	}
	return filepath.Join(canonicalRoot, pathSHA256(canonicalPlan)), nil
}

// LookupPlanState reads the durable workspace association for one exact plan
// directory. A plan with no projection, or a projection with no workspace root,
// returns found=false so callers fall through to their own metadata rather than
// inheriting an association from unrelated state. The embedded plan_dir must
// match, so relocated or copied state never classifies a plan it does not
// describe.
func LookupPlanState(planDir, stateRoot string) (state PlanState, found bool, err error) {
	canonicalPlan, err := CanonicalizePath(planDir)
	if err != nil {
		return PlanState{}, false, fmt.Errorf("agentloop: resolving plan directory %s: %w", planDir, err)
	}
	stateDir, err := StateDirFor(planDir, stateRoot)
	if err != nil {
		return PlanState{}, false, err
	}
	statePath := filepath.Join(stateDir, stateFileName)
	projection, err := ReadProjection(statePath)
	if err != nil {
		switch ProjectionReason(err) {
		case ReasonUnreadableState, ReasonMissingProjection:
			// No usable state for this plan is an ordinary answer, not a fault.
			return PlanState{}, false, nil
		default:
			return PlanState{}, false, fmt.Errorf("agentloop: reading plan state %s: %w", statePath, err)
		}
	}
	statePlan, err := CanonicalizePath(projection.PlanDir)
	if err != nil || statePlan != canonicalPlan {
		return PlanState{}, false, fmt.Errorf("agentloop: plan state %s belongs to %q, not %q", statePath, projection.PlanDir, canonicalPlan)
	}
	if projection.WorkspaceRoot == "" {
		return PlanState{}, false, nil
	}
	cwd, err := CanonicalizePath(projection.WorkspaceRoot)
	if err != nil {
		return PlanState{}, false, fmt.Errorf("agentloop: resolving plan state workspace root %s: %w", projection.WorkspaceRoot, err)
	}
	return PlanState{PlanDir: canonicalPlan, StateDir: stateDir, CWD: cwd}, true, nil
}

// Discover scans the configured state root and returns present candidates
// sorted newest-first. It is the presence-only view of DiscoverPlans, for
// callers that act on runs rather than reason about leases; a caller that must
// distinguish "gone" from "could not tell" has to use DiscoverPlans, because
// that distinction is exactly what this return type cannot express.
func Discover(opts DiscoverOptions) ([]Candidate, error) {
	result, err := DiscoverPlans(opts)
	if err != nil {
		return nil, err
	}
	return result.Candidates(opts.IncludePaused), nil
}

// SortCandidates orders candidates newest-first with deterministic tie breaks.
func SortCandidates(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if !a.RunStartedAt.Equal(b.RunStartedAt) {
			return a.RunStartedAt.After(b.RunStartedAt)
		}
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
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
	return matchingCanonicalCWD(candidates, selectedCanonical), nil
}

// MatchingWorkspace returns loop candidates for target's workspace, preferring
// candidates with target's harness when present. If a different same-workspace
// harness has an open target, that harness owns the loop marker; otherwise the
// function falls back to CWD-only workspace matching.
//
// Candidate WindowID is treated as authoritative only when it identifies a
// managed window in openTargets. A candidate whose WindowID points outside the
// managed set (for example, an ASL pane nested inside a managed tab) is still
// eligible for CWD/harness fallback so the loop marker reaches the managed
// session that owns the workspace. This is Kitty window topology, not ASL
// contract compatibility: the projection legitimately carries a null window ID,
// and dropping the fallback would strand split-tab routing.
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
