package agentloop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const launchLogFilePerm = 0o600

var (
	runLaunchCommand      = exec.Command
	runLaunchStartProcess = defaultRunLaunchStartProcess
	runLaunchMkdirAll     = os.MkdirAll
	runLaunchOpenFile     = os.OpenFile
	runLaunchNow          = time.Now
)

// StartRunRequest describes a detached agent-session-loop run launch from the
// dashboard.
type StartRunRequest struct {
	ProjectDir string
	PlanDir    string
	Binary     string
	LogRoot    string

	ActiveRuns []Candidate
	StartedAt  time.Time
}

// RunLaunchResult is the durable launch information written to launch.json and
// returned to TUI integration code.
type RunLaunchResult struct {
	PID        int
	ProjectDir string
	PlanDir    string
	Argv       []string
	StartedAt  time.Time
	LogPaths   LaunchLogPaths
}

type runLaunchMetadata struct {
	PID       int                   `json:"pid"`
	CWD       string                `json:"cwd"`
	PlanDir   string                `json:"plan_dir"`
	Argv      []string              `json:"argv"`
	StartedAt string                `json:"started_at"`
	LogPaths  runLaunchMetadataLogs `json:"log_paths"`
}

type runLaunchMetadataLogs struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Metadata string `json:"metadata"`
}

// StartRun validates the selected project and plan, rejects an exact duplicate
// active run, starts agent-session-loop in the background, and records AMX-owned
// launch metadata.
func StartRun(req StartRunRequest) (RunLaunchResult, error) {
	spec, err := PrepareRun(RunRequest{
		ProjectDir: req.ProjectDir,
		PlanDir:    req.PlanDir,
		Binary:     req.Binary,
	})
	if err != nil {
		return RunLaunchResult{}, err
	}
	if err := EnsureNoExactRun(req.ActiveRuns, spec.ProjectDir, spec.PlanDir); err != nil {
		return RunLaunchResult{}, err
	}

	startedAt := req.StartedAt
	if startedAt.IsZero() {
		startedAt = runLaunchNow()
	}
	startedAt = startedAt.UTC()

	paths, err := BuildLaunchLogPaths(req.LogRoot, spec.ProjectDir, spec.PlanDir, startedAt)
	if err != nil {
		return RunLaunchResult{}, err
	}
	if err := runLaunchMkdirAll(paths.Dir, 0o700); err != nil {
		return RunLaunchResult{}, fmt.Errorf("agentloop: creating launch log directory %s: %w", paths.Dir, err)
	}

	stdout, err := runLaunchOpenFile(paths.Stdout, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, launchLogFilePerm)
	if err != nil {
		return RunLaunchResult{}, fmt.Errorf("agentloop: opening launch stdout log %s: %w", paths.Stdout, err)
	}
	defer stdout.Close()

	stderr, err := runLaunchOpenFile(paths.Stderr, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, launchLogFilePerm)
	if err != nil {
		return RunLaunchResult{}, fmt.Errorf("agentloop: opening launch stderr log %s: %w", paths.Stderr, err)
	}
	defer stderr.Close()

	cmd := runLaunchCommand(spec.Argv[0], spec.Argv[1:]...)
	if cmd == nil {
		return RunLaunchResult{}, errors.New("agentloop: launch command hook returned nil command")
	}
	cmd.Dir = spec.ProjectDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = runLaunchSysProcAttr()

	pid, err := runLaunchStartProcess(cmd)
	if err != nil {
		return RunLaunchResult{}, fmt.Errorf("agentloop: starting agent-session-loop run: %w", err)
	}
	if pid <= 0 {
		return RunLaunchResult{}, fmt.Errorf("agentloop: starting agent-session-loop run returned invalid pid %d", pid)
	}

	result := RunLaunchResult{
		PID:        pid,
		ProjectDir: spec.ProjectDir,
		PlanDir:    spec.PlanDir,
		Argv:       append([]string(nil), spec.Argv...),
		StartedAt:  startedAt,
		LogPaths:   paths,
	}
	if err := writeRunLaunchMetadata(paths.Metadata, result); err != nil {
		return result, err
	}
	return result, nil
}

func defaultRunLaunchStartProcess(cmd *exec.Cmd) (int, error) {
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	if cmd.Process == nil {
		return 0, errors.New("missing process handle")
	}
	return cmd.Process.Pid, nil
}

func writeRunLaunchMetadata(path string, result RunLaunchResult) error {
	f, err := runLaunchOpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, launchLogFilePerm)
	if err != nil {
		return fmt.Errorf("agentloop: opening launch metadata %s: %w", path, err)
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(runLaunchMetadataFromResult(result)); err != nil {
		return fmt.Errorf("agentloop: writing launch metadata %s: %w", path, err)
	}
	return nil
}

func runLaunchMetadataFromResult(result RunLaunchResult) runLaunchMetadata {
	return runLaunchMetadata{
		PID:       result.PID,
		CWD:       result.ProjectDir,
		PlanDir:   result.PlanDir,
		Argv:      append([]string(nil), result.Argv...),
		StartedAt: result.StartedAt.UTC().Format(time.RFC3339Nano),
		LogPaths: runLaunchMetadataLogs{
			Stdout:   result.LogPaths.Stdout,
			Stderr:   result.LogPaths.Stderr,
			Metadata: result.LogPaths.Metadata,
		},
	}
}
