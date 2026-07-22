package agentloop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
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
	// PhaseBetween, or PhasePaused). ActiveVerification reports whether a
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
	KittyWindowID      int                 `json:"kitty_window_id"`
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
// outranks the turn it verifies; an active turn is executing; anything else is
// alive between turns.
func statePhase(state stateFile) string {
	switch {
	case state.Paused:
		return PhasePaused
	case state.ActiveVerification != nil:
		return PhaseVerification
	case state.ActiveTurn != nil:
		return PhaseTurn
	default:
		return PhaseBetween
	}
}

// heartbeatTimestamp picks the freshest liveness timestamp the run persists:
// the active turn's per-heartbeat stamp, else the verification's per-heartbeat
// stamp (persisted by newer loops), else the verification's current check
// start, else the verification start.
func heartbeatTimestamp(state stateFile) string {
	if state.ActiveTurn != nil && strings.TrimSpace(state.ActiveTurn.LastHeartbeatAt) != "" {
		return state.ActiveTurn.LastHeartbeatAt
	}
	if state.ActiveVerification != nil {
		if raw := strings.TrimSpace(state.ActiveVerification.LastHeartbeatAt); raw != "" {
			return raw
		}
		if raw := strings.TrimSpace(state.ActiveVerification.CurrentCheckStartedAt); raw != "" {
			return raw
		}
		return strings.TrimSpace(state.ActiveVerification.StartedAt)
	}
	return ""
}

func matchingWindow(candidates []Candidate, windowID int) []Candidate {
	matches := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.WindowID <= 0 {
			continue
		}
		if candidate.WindowID == windowID {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func candidatesEligibleForWindowFallback(candidates []Candidate, managed map[int]struct{}) []Candidate {
	matches := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.WindowID <= 0 {
			matches = append(matches, candidate)
			continue
		}
		if _, isManaged := managed[candidate.WindowID]; !isManaged {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func managedWindowIDs(openTargets []WorkspaceTarget, targetWindowID int) map[int]struct{} {
	managed := make(map[int]struct{}, len(openTargets)+1)
	for _, target := range openTargets {
		if target.WindowID > 0 {
			managed[target.WindowID] = struct{}{}
		}
	}
	if targetWindowID > 0 {
		managed[targetWindowID] = struct{}{}
	}
	return managed
}

func matchingCanonicalCWD(candidates []Candidate, selectedCanonical string) []Candidate {
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
	return matches
}

func filterHarness(candidates []Candidate, targetHarness string) []Candidate {
	matches := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Harness == targetHarness {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func workspaceHasOpenCandidateHarness(candidates []Candidate, selectedCanonical string, openTargets []WorkspaceTarget) bool {
	openHarnesses := make(map[string]struct{})
	for _, target := range openTargets {
		if target.CWD == "" {
			continue
		}
		cwd, err := CanonicalizePath(target.CWD)
		if err != nil || cwd != selectedCanonical {
			continue
		}
		targetHarness := normalizeKnownHarness(target.Harness)
		if targetHarness != "" {
			openHarnesses[targetHarness] = struct{}{}
		}
	}
	for _, candidate := range candidates {
		if candidate.Harness == "" {
			continue
		}
		if _, ok := openHarnesses[candidate.Harness]; ok {
			return true
		}
	}
	return false
}

func effectiveHarness(state stateFile) string {
	if state.ActiveTurn != nil {
		if h := normalizeKnownHarness(state.ActiveTurn.Harness); h != "" {
			return h
		}
	}
	return normalizeKnownHarness(state.Harness)
}

func normalizeKnownHarness(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case harness.Codex:
		return harness.Codex
	case harness.Copilot:
		return harness.Copilot
	case harness.Claude:
		return harness.Claude
	default:
		return ""
	}
}

func hasRunFinishedAt(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return strings.TrimSpace(value) != ""
}

type lockInfo struct {
	pid       int
	planDir   string
	createdAt string
}

func readLock(path string) (lockInfo, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return lockInfo{}, false
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return lockInfo{}, false
	}

	pid, ok := coercePositiveInt(raw["pid"])
	if !ok {
		return lockInfo{}, false
	}
	planDir, ok := raw["plan_dir"].(string)
	if !ok || planDir == "" {
		return lockInfo{}, false
	}

	createdAt, _ := raw["created_at"].(string)
	return lockInfo{pid: pid, planDir: planDir, createdAt: createdAt}, true
}

func readState(path string) (stateFile, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return stateFile{}, false
	}

	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return stateFile{}, false
	}
	return state, true
}

func coercePositiveInt(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		if v <= 0 || v > float64(maxIntValue) || v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	case string:
		n, err := strconv.ParseInt(v, 10, 0)
		if err != nil || n <= 0 || n > int64(maxIntValue) {
			return 0, false
		}
		return int(n), true
	case int:
		return v, v > 0
	case int64:
		if v <= 0 || v > int64(maxIntValue) {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

func parseTimestamp(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

func expandUser(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func pathSHA256(path string) string {
	canonical, err := CanonicalizePath(path)
	if err != nil {
		canonical = filepath.Clean(path)
	}
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

func isHexSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
