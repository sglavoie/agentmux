package agentloop

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func stubRunLaunchStartProcess(t *testing.T, fn func(*exec.Cmd) (int, error)) {
	t.Helper()
	old := runLaunchStartProcess
	runLaunchStartProcess = fn
	t.Cleanup(func() { runLaunchStartProcess = old })
}

func stubRunLaunchOpenFile(t *testing.T, fn func(string, int, os.FileMode) (*os.File, error)) {
	t.Helper()
	old := runLaunchOpenFile
	runLaunchOpenFile = fn
	t.Cleanup(func() { runLaunchOpenFile = old })
}

func TestStartRunStartsDetachedProcessAndWritesMetadata(t *testing.T) {
	base := t.TempDir()
	projectDir := mkdirTestDir(t, filepath.Join(base, "project"))
	planDir := seedSplitPlan(t, base, "alpha-aabb112233cc")
	logRoot := filepath.Join(base, "launches")
	startedAt := time.Date(2026, 6, 10, 17, 45, 2, 345678901, time.FixedZone("test", -4*60*60))
	wantEnv := []string{"PATH=/custom/bin", "SECRET_TOKEN=keep-private"}

	var startedCmd *exec.Cmd
	stubRunLaunchStartProcess(t, func(cmd *exec.Cmd) (int, error) {
		startedCmd = cmd
		return 4242, nil
	})

	result, err := StartRun(StartRunRequest{
		ProjectDir: filepath.Join(projectDir, "..", filepath.Base(projectDir)),
		PlanDir:    filepath.Join(planDir, "..", filepath.Base(planDir)),
		Env:        wantEnv,
		LogRoot:    logRoot,
		StartedAt:  startedAt,
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if startedCmd == nil {
		t.Fatal("StartRun did not call process starter")
	}

	wantProject := canonical(t, projectDir)
	wantPlan := canonical(t, planDir)
	wantArgv := []string{appName, "run", "--cwd", ".", wantPlan, "--unsafe-bypass"}
	if !reflect.DeepEqual(startedCmd.Args, wantArgv) {
		t.Fatalf("cmd Args = %#v, want %#v", startedCmd.Args, wantArgv)
	}
	if startedCmd.Dir != wantProject {
		t.Fatalf("cmd Dir = %q, want %q", startedCmd.Dir, wantProject)
	}
	if startedCmd.SysProcAttr == nil {
		t.Fatal("cmd SysProcAttr is nil, want detached process attributes")
	}
	if !reflect.DeepEqual(startedCmd.Env, wantEnv) {
		t.Fatalf("cmd Env = %#v, want %#v", startedCmd.Env, wantEnv)
	}
	if got := fileName(t, startedCmd.Stdout); got != result.LogPaths.Stdout {
		t.Fatalf("cmd stdout = %q, want %q", got, result.LogPaths.Stdout)
	}
	if got := fileName(t, startedCmd.Stderr); got != result.LogPaths.Stderr {
		t.Fatalf("cmd stderr = %q, want %q", got, result.LogPaths.Stderr)
	}

	if result.PID != 4242 {
		t.Fatalf("result PID = %d, want 4242", result.PID)
	}
	if result.ProjectDir != wantProject || result.PlanDir != wantPlan {
		t.Fatalf("result paths = %q/%q, want %q/%q", result.ProjectDir, result.PlanDir, wantProject, wantPlan)
	}
	if !reflect.DeepEqual(result.Argv, wantArgv) {
		t.Fatalf("result Argv = %#v, want %#v", result.Argv, wantArgv)
	}
	if !result.StartedAt.Equal(startedAt.UTC()) {
		t.Fatalf("result StartedAt = %v, want %v", result.StartedAt, startedAt.UTC())
	}

	for _, path := range []string{result.LogPaths.Stdout, result.LogPaths.Stderr, result.LogPaths.Metadata} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat launch file %s: %v", path, err)
		}
	}

	var metadata runLaunchMetadata
	raw, err := os.ReadFile(result.LogPaths.Metadata)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v\n%s", err, raw)
	}
	if strings.Contains(string(raw), "SECRET_TOKEN") {
		t.Fatalf("metadata leaked environment: %s", raw)
	}
	if metadata.PID != 4242 {
		t.Errorf("metadata PID = %d, want 4242", metadata.PID)
	}
	if metadata.CWD != wantProject || metadata.PlanDir != wantPlan {
		t.Errorf("metadata paths = %q/%q, want %q/%q", metadata.CWD, metadata.PlanDir, wantProject, wantPlan)
	}
	if !reflect.DeepEqual(metadata.Argv, wantArgv) {
		t.Errorf("metadata Argv = %#v, want %#v", metadata.Argv, wantArgv)
	}
	if metadata.StartedAt != startedAt.UTC().Format(time.RFC3339Nano) {
		t.Errorf("metadata started_at = %q, want %q", metadata.StartedAt, startedAt.UTC().Format(time.RFC3339Nano))
	}
	if metadata.LogPaths.Stdout != result.LogPaths.Stdout ||
		metadata.LogPaths.Stderr != result.LogPaths.Stderr ||
		metadata.LogPaths.Metadata != result.LogPaths.Metadata {
		t.Errorf("metadata log paths = %#v, want %#v", metadata.LogPaths, result.LogPaths)
	}
}

func TestStartRunRejectsDuplicateBeforeStarting(t *testing.T) {
	base := t.TempDir()
	projectDir := mkdirTestDir(t, filepath.Join(base, "project"))
	planDir := seedSplitPlan(t, base, "alpha-aabb112233cc")
	startCalls := 0
	stubRunLaunchStartProcess(t, func(*exec.Cmd) (int, error) {
		startCalls++
		return 4242, nil
	})

	_, err := StartRun(StartRunRequest{
		ProjectDir: projectDir,
		PlanDir:    planDir,
		LogRoot:    filepath.Join(base, "launches"),
		ActiveRuns: []Candidate{{
			CWD:     canonical(t, projectDir),
			PlanDir: canonical(t, planDir),
			PID:     123,
		}},
	})
	var dup *DuplicateRunError
	if !errors.As(err, &dup) {
		t.Fatalf("StartRun error = %v, want DuplicateRunError", err)
	}
	if startCalls != 0 {
		t.Fatalf("process start calls = %d, want 0", startCalls)
	}
}

func TestStartRunSurfacesStartFailure(t *testing.T) {
	base := t.TempDir()
	projectDir := mkdirTestDir(t, filepath.Join(base, "project"))
	planDir := seedSplitPlan(t, base, "alpha-aabb112233cc")
	startErr := errors.New("boom")
	stubRunLaunchStartProcess(t, func(*exec.Cmd) (int, error) {
		return 0, startErr
	})

	result, err := StartRun(StartRunRequest{
		ProjectDir: projectDir,
		PlanDir:    planDir,
		LogRoot:    filepath.Join(base, "launches"),
		StartedAt:  time.Date(2026, 6, 10, 18, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("StartRun returned nil error, want start failure")
	}
	if !strings.Contains(err.Error(), "starting agent-session-loop run") {
		t.Fatalf("StartRun error = %q, want start context", err)
	}
	if result.PID != 0 {
		t.Fatalf("result PID = %d, want zero result on start failure", result.PID)
	}
}

func TestStartRunSurfacesLogOpenFailureBeforeStarting(t *testing.T) {
	base := t.TempDir()
	projectDir := mkdirTestDir(t, filepath.Join(base, "project"))
	planDir := seedSplitPlan(t, base, "alpha-aabb112233cc")
	startCalls := 0
	stubRunLaunchStartProcess(t, func(*exec.Cmd) (int, error) {
		startCalls++
		return 4242, nil
	})
	stubRunLaunchOpenFile(t, func(path string, flag int, perm os.FileMode) (*os.File, error) {
		if filepath.Base(path) == launchStdoutLogName {
			return nil, errors.New("cannot open stdout")
		}
		return os.OpenFile(path, flag, perm)
	})

	_, err := StartRun(StartRunRequest{
		ProjectDir: projectDir,
		PlanDir:    planDir,
		LogRoot:    filepath.Join(base, "launches"),
		StartedAt:  time.Date(2026, 6, 10, 18, 10, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("StartRun returned nil error, want stdout open failure")
	}
	if !strings.Contains(err.Error(), "opening launch stdout log") {
		t.Fatalf("StartRun error = %q, want stdout log context", err)
	}
	if startCalls != 0 {
		t.Fatalf("process start calls = %d, want 0", startCalls)
	}
}

func TestStartRunReturnsMetadataErrorAfterProcessStarts(t *testing.T) {
	base := t.TempDir()
	projectDir := mkdirTestDir(t, filepath.Join(base, "project"))
	planDir := seedSplitPlan(t, base, "alpha-aabb112233cc")
	startCalls := 0
	stubRunLaunchStartProcess(t, func(*exec.Cmd) (int, error) {
		startCalls++
		return 4242, nil
	})
	stubRunLaunchOpenFile(t, func(path string, flag int, perm os.FileMode) (*os.File, error) {
		if filepath.Base(path) == launchMetadataName {
			return nil, errors.New("cannot write metadata")
		}
		return os.OpenFile(path, flag, perm)
	})

	result, err := StartRun(StartRunRequest{
		ProjectDir: projectDir,
		PlanDir:    planDir,
		LogRoot:    filepath.Join(base, "launches"),
		StartedAt:  time.Date(2026, 6, 10, 18, 20, 0, 0, time.UTC),
	})
	var metadataErr *LaunchMetadataError
	if !errors.As(err, &metadataErr) {
		t.Fatalf("StartRun error = %v, want LaunchMetadataError", err)
	}
	if startCalls != 1 {
		t.Fatalf("process start calls = %d, want 1", startCalls)
	}
	if result.PID != 4242 || metadataErr.Result.PID != 4242 {
		t.Fatalf("started result PID = %d / metadata result PID = %d, want 4242", result.PID, metadataErr.Result.PID)
	}
	if result.LogPaths.Metadata == "" || metadataErr.Result.LogPaths.Metadata != result.LogPaths.Metadata {
		t.Fatalf("metadata paths = %q / %q, want populated and matching", result.LogPaths.Metadata, metadataErr.Result.LogPaths.Metadata)
	}
	if !strings.Contains(err.Error(), "launch metadata failed after starting pid 4242") {
		t.Fatalf("metadata error = %q, want post-start context", err)
	}
}

func fileName(t *testing.T, value any) string {
	t.Helper()
	f, ok := value.(*os.File)
	if !ok {
		t.Fatalf("value = %T, want *os.File", value)
	}
	return f.Name()
}
