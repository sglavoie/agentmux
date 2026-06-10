package agentloop

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	launchLogDirName      = "agent-session-loop-launches"
	launchMetadataName    = "launch.json"
	launchStdoutLogName   = "stdout.log"
	launchStderrLogName   = "stderr.log"
	launchLogSlugMaxRunes = 48
)

var sessionPlanFilePattern = regexp.MustCompile(`^session-[1-9][0-9]*\.md$`)

// RunRequest describes the pure inputs needed to launch agent-session-loop run.
type RunRequest struct {
	ProjectDir string
	PlanDir    string
	Binary     string
}

// RunSpec is the validated, canonical launch shape consumed by the detached
// process starter.
type RunSpec struct {
	ProjectDir string
	PlanDir    string
	Binary     string
	Argv       []string
}

// LaunchLogPaths names the AMX-owned files used to capture a detached launch.
type LaunchLogPaths struct {
	Dir      string
	Stdout   string
	Stderr   string
	Metadata string
}

// DuplicateRunError reports an already-active loop for the same canonical cwd
// and plan directory.
type DuplicateRunError struct {
	Candidate Candidate
}

func (e *DuplicateRunError) Error() string {
	return fmt.Sprintf("agentloop: active run already exists for plan %s in cwd %s", e.Candidate.PlanDir, e.Candidate.CWD)
}

// PrepareRun validates the selected project cwd and split-plan directory, then
// returns the argv contract used by the detached launcher.
func PrepareRun(req RunRequest) (RunSpec, error) {
	projectDir, err := ResolveProjectCWD(req.ProjectDir)
	if err != nil {
		return RunSpec{}, err
	}
	planDir, err := ValidatePlanDir(req.PlanDir)
	if err != nil {
		return RunSpec{}, err
	}

	binary := strings.TrimSpace(req.Binary)
	if binary == "" {
		binary = appName
	}
	return RunSpec{
		ProjectDir: projectDir,
		PlanDir:    planDir,
		Binary:     binary,
		Argv:       BuildRunArgv(binary, planDir),
	}, nil
}

// ResolveProjectCWD canonicalizes the highlighted project directory and ensures
// it can be used as exec.Cmd.Dir.
func ResolveProjectCWD(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("agentloop: selected project cwd is required")
	}
	canonical, err := CanonicalizePath(path)
	if err != nil {
		return "", fmt.Errorf("agentloop: resolving selected project cwd %s: %w", path, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("agentloop: selected project cwd %s: %w", canonical, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("agentloop: selected project cwd %s is not a directory", canonical)
	}
	return canonical, nil
}

// ValidatePlanDir canonicalizes a split plan directory and verifies the files
// agent-session-loop run needs are present and readable.
func ValidatePlanDir(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("agentloop: plan directory is required")
	}
	canonical, err := CanonicalizePath(path)
	if err != nil {
		return "", fmt.Errorf("agentloop: resolving plan directory %s: %w", path, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("agentloop: plan directory %s: %w", canonical, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("agentloop: plan directory %s is not a directory", canonical)
	}

	for _, name := range []string{"manifest.md", "context.md"} {
		if err := requireReadablePlanFile(canonical, name); err != nil {
			return "", err
		}
	}
	if err := requireSessionPlanFile(canonical); err != nil {
		return "", err
	}
	return canonical, nil
}

// BuildRunArgv constructs the exact process argv for agent-session-loop run.
// The caller is expected to pass a canonical plan directory.
func BuildRunArgv(binary, canonicalPlanDir string) []string {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		binary = appName
	}
	return []string{binary, "run", "--cwd", ".", canonicalPlanDir, "--unsafe-bypass"}
}

// DefaultLaunchLogRoot returns AMX's state-owned launch-log directory.
func DefaultLaunchLogRoot() string {
	var base string
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		base = xdg
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		base = filepath.Join(home, ".local", "state")
	} else {
		base = filepath.Join("~", ".local", "state")
	}
	root := filepath.Join(base, "agent-mux", launchLogDirName)
	if canonical, err := CanonicalizePath(root); err == nil {
		return canonical
	}
	return filepath.Clean(root)
}

// BuildLaunchLogPaths returns deterministic filenames for a single launch. The
// timestamp is part of the directory name, while cwd/plan hashes keep names
// compact and collision-resistant across similarly named projects.
func BuildLaunchLogPaths(root, projectDir, planDir string, startedAt time.Time) (LaunchLogPaths, error) {
	if root == "" {
		root = DefaultLaunchLogRoot()
	}
	root, err := CanonicalizePath(root)
	if err != nil {
		return LaunchLogPaths{}, fmt.Errorf("agentloop: resolving launch log root %s: %w", root, err)
	}
	projectDir = strings.TrimSpace(projectDir)
	if projectDir == "" {
		return LaunchLogPaths{}, errors.New("agentloop: launch log cwd is required")
	}
	projectDir, err = CanonicalizePath(projectDir)
	if err != nil {
		return LaunchLogPaths{}, fmt.Errorf("agentloop: resolving launch log cwd %s: %w", projectDir, err)
	}
	planDir = strings.TrimSpace(planDir)
	if planDir == "" {
		return LaunchLogPaths{}, errors.New("agentloop: launch log plan is required")
	}
	planDir, err = CanonicalizePath(planDir)
	if err != nil {
		return LaunchLogPaths{}, fmt.Errorf("agentloop: resolving launch log plan %s: %w", planDir, err)
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}

	stamp := startedAt.UTC().Format("20060102T150405.000000000Z")
	name := strings.Join([]string{
		stamp,
		pathSlug(projectDir, "cwd"),
		shortPathHash(projectDir),
		pathSlug(planDir, "plan"),
		shortPathHash(planDir),
	}, "-")
	dir := filepath.Join(root, name)
	return LaunchLogPaths{
		Dir:      dir,
		Stdout:   filepath.Join(dir, launchStdoutLogName),
		Stderr:   filepath.Join(dir, launchStderrLogName),
		Metadata: filepath.Join(dir, launchMetadataName),
	}, nil
}

// FindExactRun returns the first active loop matching both canonical project
// cwd and canonical plan directory.
func FindExactRun(candidates []Candidate, projectDir, planDir string) (Candidate, bool, error) {
	projectDir = strings.TrimSpace(projectDir)
	if projectDir == "" {
		return Candidate{}, false, errors.New("agentloop: duplicate-check cwd is required")
	}
	projectDir, err := CanonicalizePath(projectDir)
	if err != nil {
		return Candidate{}, false, fmt.Errorf("agentloop: resolving duplicate-check cwd %s: %w", projectDir, err)
	}
	planDir = strings.TrimSpace(planDir)
	if planDir == "" {
		return Candidate{}, false, errors.New("agentloop: duplicate-check plan is required")
	}
	planDir, err = CanonicalizePath(planDir)
	if err != nil {
		return Candidate{}, false, fmt.Errorf("agentloop: resolving duplicate-check plan %s: %w", planDir, err)
	}

	for _, candidate := range candidates {
		if candidate.CWD == "" || candidate.PlanDir == "" {
			continue
		}
		candidateCWD, err := CanonicalizePath(candidate.CWD)
		if err != nil || candidateCWD != projectDir {
			continue
		}
		candidatePlan, err := CanonicalizePath(candidate.PlanDir)
		if err != nil || candidatePlan != planDir {
			continue
		}
		return candidate, true, nil
	}
	return Candidate{}, false, nil
}

// EnsureNoExactRun applies the duplicate policy for dashboard loop launches.
func EnsureNoExactRun(candidates []Candidate, projectDir, planDir string) error {
	candidate, ok, err := FindExactRun(candidates, projectDir, planDir)
	if err != nil {
		return err
	}
	if ok {
		return &DuplicateRunError{Candidate: candidate}
	}
	return nil
}

func requireReadablePlanFile(dir, name string) error {
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("agentloop: plan directory %s is missing %s", dir, name)
		}
		return fmt.Errorf("agentloop: stat plan file %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("agentloop: plan file %s is a directory", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("agentloop: opening plan file %s: %w", path, err)
	}
	return f.Close()
}

func requireSessionPlanFile(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("agentloop: reading plan directory %s: %w", dir, err)
	}
	for _, entry := range entries {
		if !sessionPlanFilePattern.MatchString(entry.Name()) {
			continue
		}
		if entry.IsDir() {
			return fmt.Errorf("agentloop: plan file %s is a directory", filepath.Join(dir, entry.Name()))
		}
		if err := requireReadablePlanFile(dir, entry.Name()); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("agentloop: plan directory %s has no session-N.md files", dir)
}

func shortPathHash(path string) string {
	hash := pathSHA256(path)
	if len(hash) < 12 {
		return hash
	}
	return hash[:12]
}

func pathSlug(path, fallback string) string {
	base := filepath.Base(path)
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = fallback
	}
	slug := asciiSlug(strings.ToLower(base))
	if slug == "" {
		slug = fallback
	}
	return slug
}

func asciiSlug(value string) string {
	var b strings.Builder
	lastDash := false
	runeCount := 0
	for _, r := range value {
		var out rune
		switch {
		case r >= 'a' && r <= 'z':
			out = r
		case r >= '0' && r <= '9':
			out = r
		case r == '-' || r == '_' || r == '.':
			out = r
		default:
			out = '-'
		}

		if out == '-' {
			if lastDash {
				continue
			}
			lastDash = true
		} else {
			lastDash = false
		}
		b.WriteRune(out)
		runeCount++
		if runeCount >= launchLogSlugMaxRunes {
			break
		}
	}
	return strings.Trim(b.String(), ".-_")
}
