package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/plans"
)

func TestLoopRunPlanSearchPEntersPlanPathMode(t *testing.T) {
	m := testModel()
	m.mode = ModeLoopRunPlanSearch
	m.loopRunProjectDir = t.TempDir()
	m.loopRunProjectTitle = "api"

	updated, cmd := m.Update(keyPress("p"))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("p returned cmd %T, want nil", cmd)
	}
	if got.mode != ModeLoopRunPlanPath {
		t.Fatalf("mode = %v, want ModeLoopRunPlanPath", got.mode)
	}
	if got.loopRunProjectTitle != "api" {
		t.Fatalf("loopRunProjectTitle = %q, want api", got.loopRunProjectTitle)
	}
}

func TestLoopRunPlanPathAbsolutePathBuildsLaunchRequest(t *testing.T) {
	projectDir := t.TempDir()
	planDir := seedLoopRunPathPlan(t, t.TempDir(), "api-aabb112233cc")
	m := loopRunPathTestModel(projectDir)
	m = typeString(m, planDir)

	msg := submitLoopRunPathForMsg(t, m)
	if msg.Err != nil {
		t.Fatalf("launch request validation failed: %v", msg.Err)
	}
	assertLoopRunPathRequest(t, msg, projectDir, planDir)
}

func TestLoopRunPlanPathExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := t.TempDir()
	planDir := seedLoopRunPathPlan(t, home, "api-aabb112233cc")

	m := loopRunPathTestModel(projectDir)
	m = typeString(m, "~/api-aabb112233cc")

	msg := submitLoopRunPathForMsg(t, m)
	if msg.Err != nil {
		t.Fatalf("launch request validation failed: %v", msg.Err)
	}
	assertLoopRunPathRequest(t, msg, projectDir, planDir)
}

func TestLoopRunPlanPathUnquotesSimpleWrappers(t *testing.T) {
	projectDir := t.TempDir()
	planDir := seedLoopRunPathPlan(t, t.TempDir(), "api-aabb112233cc")

	for _, quote := range []string{`"`, `'`, "`"} {
		t.Run("quote_"+quote, func(t *testing.T) {
			m := loopRunPathTestModel(projectDir)
			m = typeString(m, quote+"  "+planDir+"  "+quote)

			msg := submitLoopRunPathForMsg(t, m)
			if msg.Err != nil {
				t.Fatalf("launch request validation failed: %v", msg.Err)
			}
			assertLoopRunPathRequest(t, msg, projectDir, planDir)
		})
	}
}

func TestLoopRunPlanPathInvalidPlanSurfacesPathError(t *testing.T) {
	projectDir := t.TempDir()
	invalidPlanDir := seedPlanDir(t, t.TempDir(), "invalid-aabb112233cc", map[string]string{
		"manifest.md": "# missing context and sessions",
	})
	m := loopRunPathTestModel(projectDir)
	m = typeString(m, invalidPlanDir)

	updated, cmd := m.Update(keyPress("enter"))
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("enter returned nil cmd; want validation command")
	}
	msg, ok := cmd().(loopRunPlanLaunchMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want loopRunPlanLaunchMsg", msg)
	}
	if msg.Err == nil {
		t.Fatal("invalid plan produced nil error")
	}

	updated, _ = got.Update(msg)
	got = updated.(Model)
	if got.loopRunPlanPathErr == nil {
		t.Fatal("loopRunPlanPathErr is nil after validation error")
	}
	status := stripAnsi(renderStatusBar(got))
	if !strings.Contains(status, "Path error:") {
		t.Fatalf("status = %q, want path error", status)
	}
}

func TestLoopRunPlanPathRejectsMissingPathAndProject(t *testing.T) {
	t.Run("missing path", func(t *testing.T) {
		m := loopRunPathTestModel(t.TempDir())
		updated, cmd := m.Update(keyPress("enter"))
		got := updated.(Model)
		if cmd != nil {
			t.Fatalf("enter returned cmd %T, want nil", cmd)
		}
		if got.loopRunPlanPathErr == nil || !strings.Contains(got.loopRunPlanPathErr.Error(), "plan directory") {
			t.Fatalf("loopRunPlanPathErr = %v, want plan directory error", got.loopRunPlanPathErr)
		}
	})

	t.Run("missing project cwd", func(t *testing.T) {
		planDir := seedLoopRunPathPlan(t, t.TempDir(), "api-aabb112233cc")
		m := loopRunPathTestModel("")
		m = typeString(m, planDir)
		updated, cmd := m.Update(keyPress("enter"))
		got := updated.(Model)
		if cmd != nil {
			t.Fatalf("enter returned cmd %T, want nil", cmd)
		}
		if got.loopRunPlanPathErr == nil || !strings.Contains(got.loopRunPlanPathErr.Error(), "selected project cwd") {
			t.Fatalf("loopRunPlanPathErr = %v, want selected cwd error", got.loopRunPlanPathErr)
		}
	})
}

func TestLoopRunPlanPathBackspaceAndCancel(t *testing.T) {
	m := loopRunPathTestModel(t.TempDir())
	m.loopRunPlanPathErr = errors.New("old error")
	m = typeString(m, "ab")

	updated, cmd := m.Update(keyPress("backspace"))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("backspace returned cmd %T, want nil", cmd)
	}
	if got.loopRunPlanPath != "a" {
		t.Fatalf("loopRunPlanPath = %q, want a", got.loopRunPlanPath)
	}
	if got.loopRunPlanPathErr != nil {
		t.Fatalf("loopRunPlanPathErr = %v, want nil", got.loopRunPlanPathErr)
	}

	updated, cmd = got.Update(keyPress("esc"))
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("esc returned cmd %T, want nil", cmd)
	}
	if got.mode != ModeLoopRunPlanSearch {
		t.Fatalf("mode after esc = %v, want ModeLoopRunPlanSearch", got.mode)
	}
	if got.loopRunPlanPath != "" || got.loopRunPlanPathErr != nil {
		t.Fatalf("path state after esc = (%q, %v), want cleared", got.loopRunPlanPath, got.loopRunPlanPathErr)
	}
}

func TestLoopRunPlanPathAcceptsPastedTextWithSpaces(t *testing.T) {
	projectDir := t.TempDir()
	root := t.TempDir()
	planDir := seedLoopRunPathPlan(t, root, "plan with spaces")
	m := loopRunPathTestModel(projectDir)

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: `"` + planDir + `"`}))
	m = updated.(Model)

	msg := submitLoopRunPathForMsg(t, m)
	if msg.Err != nil {
		t.Fatalf("launch request validation failed: %v", msg.Err)
	}
	assertLoopRunPathRequest(t, msg, projectDir, planDir)
}

func seedLoopRunPathPlan(t *testing.T, root, name string) string {
	t.Helper()
	return seedPlanDir(t, root, name, map[string]string{
		"manifest.md":  "# plan",
		"context.md":   "context",
		"session-1.md": "session",
	})
}

func loopRunPathTestModel(projectDir string) Model {
	m := testModel()
	m.mode = ModeLoopRunPlanPath
	m.loopRunProjectDir = projectDir
	m.loopRunProjectTitle = "api"
	return m
}

func submitLoopRunPathForMsg(t *testing.T, m Model) loopRunPlanLaunchMsg {
	t.Helper()
	updated, cmd := m.Update(keyPress("enter"))
	got := updated.(Model)
	if got.mode != ModeLoopRunPlanPath {
		t.Fatalf("mode after enter = %v, want ModeLoopRunPlanPath", got.mode)
	}
	if cmd == nil {
		t.Fatal("enter returned nil cmd; want launch request command")
	}
	msg, ok := cmd().(loopRunPlanLaunchMsg)
	if !ok {
		t.Fatalf("launch request command produced %T, want loopRunPlanLaunchMsg", msg)
	}
	return msg
}

func assertLoopRunPathRequest(t *testing.T, msg loopRunPlanLaunchMsg, projectDir, planDir string) {
	t.Helper()
	wantProject, err := agentloop.CanonicalizePath(projectDir)
	if err != nil {
		t.Fatalf("canonical project: %v", err)
	}
	wantPlan, err := agentloop.CanonicalizePath(planDir)
	if err != nil {
		t.Fatalf("canonical plan: %v", err)
	}
	if msg.Request.ProjectDir != wantProject {
		t.Fatalf("ProjectDir = %q, want %q", msg.Request.ProjectDir, wantProject)
	}
	if msg.Request.PlanDir != wantPlan {
		t.Fatalf("PlanDir = %q, want %q", msg.Request.PlanDir, wantPlan)
	}
	if msg.Plan.DirPath != wantPlan {
		t.Fatalf("Plan.DirPath = %q, want %q", msg.Plan.DirPath, wantPlan)
	}
	if msg.Plan.Name != plans.DisplayName(filepath.Base(wantPlan)) {
		t.Fatalf("Plan.Name = %q, want display name for %q", msg.Plan.Name, wantPlan)
	}
	if _, err := os.Stat(msg.Request.PlanDir); err != nil {
		t.Fatalf("validated plan stat failed: %v", err)
	}
}
