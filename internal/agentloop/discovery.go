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
)

const (
	appName       = "agent-session-loop"
	lockFileName  = "lock.json"
	stateFileName = "state.json"
	maxIntValue   = int(^uint(0) >> 1)
)

// PIDChecker reports whether a process id still represents a running process.
type PIDChecker func(pid int) bool

// DiscoverOptions controls active loop discovery. Empty fields use production
// defaults, while tests can inject a state root and PID checker.
type DiscoverOptions struct {
	StateRoot  string
	PIDRunning PIDChecker
}

// Candidate is one active agent-session-loop run discovered from state files.
type Candidate struct {
	PlanDir  string
	StateDir string
	CWD      string
	PID      int

	LockCreatedAt    time.Time
	LockCreatedAtRaw string
	RunStartedAt     time.Time
	RunStartedAtRaw  string

	RunID         string
	ActiveSession int
	ActiveTurn    int
}

// OverlayCWD returns the best working directory for launching commands tied to
// this loop. The loop state cwd wins when present; the plan directory is the
// fallback used by the tail command flow.
func (c Candidate) OverlayCWD() string {
	if c.CWD != "" {
		return c.CWD
	}
	return c.PlanDir
}

// SelectionKind describes the caller action after applying loop selection.
type SelectionKind int

const (
	SelectionNone SelectionKind = iota
	SelectionLaunch
	SelectionPicker
)

// Selection is the result of applying dashboard tail selection rules.
type Selection struct {
	Kind       SelectionKind
	Candidates []Candidate
}

// LaunchCandidate returns the single selected candidate when Kind is
// SelectionLaunch.
func (s Selection) LaunchCandidate() (Candidate, bool) {
	if s.Kind != SelectionLaunch || len(s.Candidates) != 1 {
		return Candidate{}, false
	}
	return s.Candidates[0], true
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
		candidate, ok := readCandidate(stateDir, pidRunning)
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

// SelectForDirectory applies the dashboard tail selection rule.
func SelectForDirectory(candidates []Candidate, selectedDir string) (Selection, error) {
	if len(candidates) == 0 {
		return Selection{Kind: SelectionNone}, nil
	}

	if selectedDir != "" {
		matches, err := MatchingCWD(candidates, selectedDir)
		if err != nil {
			return Selection{}, err
		}
		switch len(matches) {
		case 0:
			return Selection{Kind: SelectionNone}, nil
		case 1:
			return Selection{Kind: SelectionLaunch, Candidates: matches}, nil
		default:
			return Selection{Kind: SelectionPicker, Candidates: matches}, nil
		}
	}

	if len(candidates) == 1 {
		return Selection{Kind: SelectionLaunch, Candidates: candidates}, nil
	}
	return Selection{Kind: SelectionPicker, Candidates: candidates}, nil
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
	RunID        string      `json:"run_id"`
	PlanDir      string      `json:"plan_dir"`
	CWD          string      `json:"cwd"`
	RunStartedAt string      `json:"run_started_at"`
	ActiveTurn   *activeTurn `json:"active_turn"`
}

type activeTurn struct {
	TurnIndex int `json:"turn_index"`
	Session   int `json:"session"`
}

func readCandidate(stateDir string, pidRunning PIDChecker) (Candidate, bool) {
	lock, ok := readLock(filepath.Join(stateDir, lockFileName))
	if !ok || !pidRunning(lock.pid) {
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
		activeSession = state.ActiveTurn.Session
		activeTurnIndex = state.ActiveTurn.TurnIndex
	}

	return Candidate{
		PlanDir:          planDir,
		StateDir:         stateDir,
		CWD:              cwd,
		PID:              lock.pid,
		LockCreatedAt:    parseTimestamp(lock.createdAt),
		LockCreatedAtRaw: lock.createdAt,
		RunStartedAt:     parseTimestamp(state.RunStartedAt),
		RunStartedAtRaw:  state.RunStartedAt,
		RunID:            state.RunID,
		ActiveSession:    activeSession,
		ActiveTurn:       activeTurnIndex,
	}, true
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
