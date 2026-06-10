package agentloop

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPrepareRunBuildsCanonicalSpec(t *testing.T) {
	base := t.TempDir()
	projectDir := mkdirTestDir(t, filepath.Join(base, "project"))
	planDir := seedSplitPlan(t, base, "alpha-aabb112233cc")

	spec, err := PrepareRun(RunRequest{
		ProjectDir: filepath.Join(projectDir, "..", filepath.Base(projectDir)),
		PlanDir:    filepath.Join(planDir, "..", filepath.Base(planDir)),
		Binary:     " /bin/agent-session-loop ",
	})
	if err != nil {
		t.Fatalf("PrepareRun: %v", err)
	}

	wantProject := canonical(t, projectDir)
	wantPlan := canonical(t, planDir)
	if spec.ProjectDir != wantProject {
		t.Errorf("ProjectDir = %q, want %q", spec.ProjectDir, wantProject)
	}
	if spec.PlanDir != wantPlan {
		t.Errorf("PlanDir = %q, want %q", spec.PlanDir, wantPlan)
	}
	if spec.Binary != "/bin/agent-session-loop" {
		t.Errorf("Binary = %q, want /bin/agent-session-loop", spec.Binary)
	}
	wantArgv := []string{"/bin/agent-session-loop", "run", "--cwd", ".", wantPlan, "--unsafe-bypass"}
	if !reflect.DeepEqual(spec.Argv, wantArgv) {
		t.Errorf("Argv = %#v, want %#v", spec.Argv, wantArgv)
	}
}

func TestPrepareRunDefaultsBinary(t *testing.T) {
	base := t.TempDir()
	projectDir := mkdirTestDir(t, filepath.Join(base, "project"))
	planDir := seedSplitPlan(t, base, "alpha-aabb112233cc")

	spec, err := PrepareRun(RunRequest{ProjectDir: projectDir, PlanDir: planDir})
	if err != nil {
		t.Fatalf("PrepareRun: %v", err)
	}
	if spec.Binary != appName {
		t.Errorf("Binary = %q, want %q", spec.Binary, appName)
	}
	if spec.Argv[0] != appName {
		t.Errorf("Argv[0] = %q, want %q", spec.Argv[0], appName)
	}
}

func TestValidatePlanDirRequiresSplitPlanFiles(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{
			name: "missing-manifest",
			files: map[string]string{
				"context.md":   "context",
				"session-1.md": "session",
			},
			wantErr: "missing manifest.md",
		},
		{
			name: "missing-context",
			files: map[string]string{
				"manifest.md":  "manifest",
				"session-1.md": "session",
			},
			wantErr: "missing context.md",
		},
		{
			name: "missing-session",
			files: map[string]string{
				"manifest.md": "manifest",
				"context.md":  "context",
			},
			wantErr: "no session-N.md files",
		},
		{
			name: "expand-file-only",
			files: map[string]string{
				"manifest.md":         "manifest",
				"context.md":          "context",
				"session-1.expand.md": "not a session file",
			},
			wantErr: "no session-N.md files",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planDir := seedPlanFiles(t, root, tt.name, tt.files)
			_, err := ValidatePlanDir(planDir)
			if err == nil {
				t.Fatalf("ValidatePlanDir returned nil error, want %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidatePlanDir error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidatePlanDirRejectsNonDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(path, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ValidatePlanDir(path)
	if err == nil {
		t.Fatal("ValidatePlanDir returned nil error, want non-directory error")
	}
	if !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("ValidatePlanDir error = %q, want non-directory error", err)
	}
}

func TestResolveProjectCWDRejectsMissingAndFiles(t *testing.T) {
	if _, err := ResolveProjectCWD(" "); err == nil {
		t.Fatal("ResolveProjectCWD empty returned nil error")
	}

	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveProjectCWD(path)
	if err == nil {
		t.Fatal("ResolveProjectCWD file returned nil error")
	}
	if !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("ResolveProjectCWD error = %q, want non-directory error", err)
	}
}

func TestBuildLaunchLogPathsUsesHashesAndNames(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "logs")
	projectDir := mkdirTestDir(t, filepath.Join(base, "My Project!"))
	planDir := seedSplitPlan(t, base, "Alpha Plan-aabb112233cc")
	startedAt := time.Date(2026, 6, 10, 17, 4, 5, 123456789, time.UTC)

	paths, err := BuildLaunchLogPaths(root, projectDir, planDir, startedAt)
	if err != nil {
		t.Fatalf("BuildLaunchLogPaths: %v", err)
	}

	if got, want := filepath.Dir(paths.Dir), canonical(t, root); got != want {
		t.Fatalf("log parent = %q, want %q", got, want)
	}
	name := filepath.Base(paths.Dir)
	for _, want := range []string{
		"20260610T170405.123456789Z",
		"my-project",
		shortPathHash(projectDir),
		"alpha-plan-aabb112233cc",
		shortPathHash(planDir),
	} {
		if !strings.Contains(name, want) {
			t.Errorf("log dir name %q missing %q", name, want)
		}
	}
	if paths.Stdout != filepath.Join(paths.Dir, launchStdoutLogName) {
		t.Errorf("Stdout = %q, want file in launch dir", paths.Stdout)
	}
	if paths.Stderr != filepath.Join(paths.Dir, launchStderrLogName) {
		t.Errorf("Stderr = %q, want file in launch dir", paths.Stderr)
	}
	if paths.Metadata != filepath.Join(paths.Dir, launchMetadataName) {
		t.Errorf("Metadata = %q, want launch metadata file", paths.Metadata)
	}
}

func TestBuildLaunchLogPathsRejectsMissingInputs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "logs")
	if _, err := BuildLaunchLogPaths(root, "", "/plans/a", time.Now()); err == nil {
		t.Fatal("BuildLaunchLogPaths missing cwd returned nil error")
	}
	if _, err := BuildLaunchLogPaths(root, "/work/a", "", time.Now()); err == nil {
		t.Fatal("BuildLaunchLogPaths missing plan returned nil error")
	}
}

func TestEnsureNoExactRunDetectsCanonicalDuplicate(t *testing.T) {
	base := t.TempDir()
	projectDir := mkdirTestDir(t, filepath.Join(base, "project"))
	planDir := seedSplitPlan(t, base, "alpha-aabb112233cc")
	candidates := []Candidate{{
		PlanDir: canonical(t, planDir),
		CWD:     canonical(t, projectDir),
		PID:     123,
	}}

	err := EnsureNoExactRun(
		candidates,
		filepath.Join(projectDir, "..", filepath.Base(projectDir)),
		filepath.Join(planDir, "..", filepath.Base(planDir)),
	)
	var dup *DuplicateRunError
	if !errors.As(err, &dup) {
		t.Fatalf("EnsureNoExactRun error = %v, want DuplicateRunError", err)
	}
	if dup.Candidate.PID != 123 {
		t.Errorf("duplicate PID = %d, want 123", dup.Candidate.PID)
	}
}

func TestEnsureNoExactRunRequiresSamePlanAndCWD(t *testing.T) {
	base := t.TempDir()
	projectDir := mkdirTestDir(t, filepath.Join(base, "project"))
	otherProject := mkdirTestDir(t, filepath.Join(base, "other-project"))
	planDir := seedSplitPlan(t, base, "alpha-aabb112233cc")
	otherPlan := seedSplitPlan(t, base, "beta-aabb112233cc")

	tests := []struct {
		name      string
		candidate Candidate
	}{
		{
			name:      "different-project",
			candidate: Candidate{PlanDir: canonical(t, planDir), CWD: canonical(t, otherProject)},
		},
		{
			name:      "different-plan",
			candidate: Candidate{PlanDir: canonical(t, otherPlan), CWD: canonical(t, projectDir)},
		},
		{
			name:      "missing-cwd",
			candidate: Candidate{PlanDir: canonical(t, planDir)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := EnsureNoExactRun([]Candidate{tt.candidate}, projectDir, planDir); err != nil {
				t.Fatalf("EnsureNoExactRun returned %v, want nil", err)
			}
		})
	}
}

func TestFindExactRunRejectsMissingInputs(t *testing.T) {
	if _, _, err := FindExactRun(nil, "", "/plans/a"); err == nil {
		t.Fatal("FindExactRun missing cwd returned nil error")
	}
	if _, _, err := FindExactRun(nil, "/work/a", ""); err == nil {
		t.Fatal("FindExactRun missing plan returned nil error")
	}
}

func seedSplitPlan(t *testing.T, root, name string) string {
	t.Helper()
	return seedPlanFiles(t, root, name, map[string]string{
		"manifest.md":  "# manifest\n",
		"context.md":   "# context\n",
		"session-1.md": "# session 1\n",
	})
}

func seedPlanFiles(t *testing.T, root, name string, files map[string]string) string {
	t.Helper()
	planDir := filepath.Join(root, name)
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", planDir, err)
	}
	for rel, contents := range files {
		path := filepath.Join(planDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir parent for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return planDir
}

func mkdirTestDir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	return path
}
