package tui

import (
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
)

func stubLoopRunStart(t *testing.T, fn func(agentloop.StartRunRequest) (agentloop.RunLaunchResult, error)) {
	t.Helper()
	old := loopRunStart
	loopRunStart = fn
	t.Cleanup(func() { loopRunStart = old })
}

func loopRunLaunchMsg(projectDir, planDir string) loopRunPlanLaunchMsg {
	return loopRunPlanLaunchMsg{
		Request: agentloop.RunRequest{
			ProjectDir: projectDir,
			PlanDir:    planDir,
		},
		Plan: plans.PlanMeta{
			Name:    "api rollout",
			DirPath: planDir,
		},
	}
}

func TestLoopRunPlanLaunchStartsDetachedRunAndRefreshesActiveLoops(t *testing.T) {
	projectDir := t.TempDir()
	planDir := seedLoopRunPathPlan(t, t.TempDir(), "api-aabb112233cc")
	existing := agentloop.Candidate{CWD: projectDir, PlanDir: filepath.Join(t.TempDir(), "other-plan"), PID: 111}
	refreshed := agentloop.Candidate{CWD: projectDir, PlanDir: planDir, PID: 4242}
	var gotReq agentloop.StartRunRequest
	stubLoopRunStart(t, func(req agentloop.StartRunRequest) (agentloop.RunLaunchResult, error) {
		gotReq = req
		return agentloop.RunLaunchResult{
			PID:        4242,
			ProjectDir: req.ProjectDir,
			PlanDir:    req.PlanDir,
			LogPaths:   agentloop.LaunchLogPaths{Dir: filepath.Join(t.TempDir(), "logs")},
		}, nil
	})
	stubLoopTailDiscover(t, []agentloop.Candidate{refreshed}, nil)

	m := testModel()
	m.mode = ModeLoopRunPlanSearch
	m.loopRunProjectDir = projectDir
	m.loopRunProjectTitle = "api"
	m.activeLoopCandidates = []agentloop.Candidate{existing}
	m.toasts = []Toast{{Severity: toastInfo, Text: "existing"}}

	updated, cmd := m.Update(loopRunLaunchMsg(projectDir, planDir))
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("launch request returned nil cmd; want detached start command")
	}
	started, ok := cmd().(loopRunStartDoneMsg)
	if !ok {
		t.Fatalf("start cmd returned %T, want loopRunStartDoneMsg", started)
	}
	if started.Err != nil {
		t.Fatalf("start cmd err = %v, want nil", started.Err)
	}
	if gotReq.ProjectDir != projectDir || gotReq.PlanDir != planDir {
		t.Fatalf("start request paths = %q/%q, want %q/%q", gotReq.ProjectDir, gotReq.PlanDir, projectDir, planDir)
	}
	if gotReq.Binary != agentSessionLoopBinary {
		t.Fatalf("start request binary = %q, want %q", gotReq.Binary, agentSessionLoopBinary)
	}
	if !reflect.DeepEqual(gotReq.ActiveRuns, []agentloop.Candidate{existing}) {
		t.Fatalf("start request active runs = %#v, want existing candidate", gotReq.ActiveRuns)
	}

	updated, cmd = got.Update(started)
	got = updated.(Model)
	if got.mode != ModeList {
		t.Fatalf("mode after successful launch = %v, want ModeList", got.mode)
	}
	if got.loopRunProjectDir != "" || got.loopRunProjectTitle != "" || got.planQuery != "" {
		t.Fatalf("launch context not cleared: project=%q title=%q query=%q", got.loopRunProjectDir, got.loopRunProjectTitle, got.planQuery)
	}
	if got.actionErr != nil {
		t.Fatalf("actionErr = %v, want nil", got.actionErr)
	}
	if len(got.toasts) == 0 || got.toasts[0].Severity != toastSuccess {
		t.Fatalf("success toast missing: %+v", got.toasts)
	}
	if text := got.toasts[0].Text; !strings.Contains(text, "pid 4242") || !strings.Contains(text, "logs:") {
		t.Fatalf("success toast = %q, want pid and logs", text)
	}
	if cmd == nil {
		t.Fatal("success handler returned nil cmd; want active-loop refresh")
	}
	msg, ok := cmd().(activeLoopsMsg)
	if !ok {
		t.Fatalf("success follow-up cmd returned %T, want activeLoopsMsg", msg)
	}
	if msg.seq != got.activeLoopRefreshSeq {
		t.Fatalf("active refresh seq = %d, want %d", msg.seq, got.activeLoopRefreshSeq)
	}
}

func TestLoopRunPlanLaunchDuplicateWarnsAndKeepsFollowAvailable(t *testing.T) {
	projectDir := t.TempDir()
	planDir := seedLoopRunPathPlan(t, t.TempDir(), "api-aabb112233cc")
	duplicate := agentloop.Candidate{CWD: projectDir, PlanDir: planDir, PID: 9876}
	stubLoopRunStart(t, func(agentloop.StartRunRequest) (agentloop.RunLaunchResult, error) {
		return agentloop.RunLaunchResult{}, &agentloop.DuplicateRunError{Candidate: duplicate}
	})
	stubLoopTailDiscover(t, []agentloop.Candidate{duplicate}, nil)
	stubLoopTailLookPath(t, "/bin/agent-session-loop", nil)
	var execCalls []*exec.Cmd
	stubLoopTailExecProcess(t, nil, &execCalls)

	m := loadSessions(testModel(), []session.RunningSession{{
		Base:      "api",
		Project:   "api",
		Directory: projectDir,
	}})
	m.mode = ModeLoopRunPlanSearch
	m.loopRunProjectDir = projectDir
	m.loopRunProjectTitle = "api"
	m.toasts = []Toast{{Severity: toastInfo, Text: "existing"}}

	updated, cmd := m.Update(loopRunLaunchMsg(projectDir, planDir))
	if cmd == nil {
		t.Fatal("launch request returned nil cmd")
	}
	done := cmd().(loopRunStartDoneMsg)
	updated, _ = updated.(Model).Update(done)
	got := updated.(Model)

	if got.mode != ModeList {
		t.Fatalf("mode after duplicate = %v, want ModeList", got.mode)
	}
	if got.actionErr != nil {
		t.Fatalf("actionErr = %v, want nil for duplicate warning", got.actionErr)
	}
	if len(got.toasts) == 0 || got.toasts[0].Severity != toastWarning {
		t.Fatalf("duplicate warning toast missing: %+v", got.toasts)
	}
	if !strings.Contains(got.toasts[0].Text, "press t to follow") {
		t.Fatalf("duplicate toast = %q, want follow hint", got.toasts[0].Text)
	}

	updated, cmd = got.Update(keyPress("t"))
	if cmd == nil {
		t.Fatal("t after duplicate returned nil cmd; want loop-tail discovery")
	}
	selection, ok := cmd().(loopTailSelectionMsg)
	if !ok {
		t.Fatalf("t command returned %T, want loopTailSelectionMsg", selection)
	}
	updated, cmd = updated.(Model).Update(selection)
	if cmd == nil {
		t.Fatal("tail selection returned nil cmd; want exec command")
	}
	tailDone, ok := cmd().(loopTailDoneMsg)
	if !ok {
		t.Fatalf("tail command returned %T, want loopTailDoneMsg", tailDone)
	}
	if tailDone.err != nil {
		t.Fatalf("tail done err = %v, want nil", tailDone.err)
	}
	if len(execCalls) != 1 {
		t.Fatalf("tail exec calls = %d, want 1", len(execCalls))
	}
	wantArgs := []string{"/bin/agent-session-loop", "tail", planDir}
	if !reflect.DeepEqual(execCalls[0].Args, wantArgs) {
		t.Fatalf("tail Args = %#v, want %#v", execCalls[0].Args, wantArgs)
	}
}

func TestLoopRunPlanLaunchStartErrorStaysInLaunchMode(t *testing.T) {
	projectDir := t.TempDir()
	planDir := seedLoopRunPathPlan(t, t.TempDir(), "api-aabb112233cc")
	startErr := errors.New("spawn failed")
	stubLoopRunStart(t, func(agentloop.StartRunRequest) (agentloop.RunLaunchResult, error) {
		return agentloop.RunLaunchResult{}, startErr
	})

	m := testModel()
	m.mode = ModeLoopRunPlanPath
	m.loopRunProjectDir = projectDir
	m.loopRunProjectTitle = "api"

	updated, cmd := m.Update(loopRunLaunchMsg(projectDir, planDir))
	if cmd == nil {
		t.Fatal("launch request returned nil cmd")
	}
	done := cmd().(loopRunStartDoneMsg)
	updated, cmd = updated.(Model).Update(done)
	got := updated.(Model)

	if got.mode != ModeLoopRunPlanPath {
		t.Fatalf("mode after start error = %v, want ModeLoopRunPlanPath", got.mode)
	}
	if !errors.Is(got.actionErr, startErr) {
		t.Fatalf("actionErr = %v, want start error", got.actionErr)
	}
	if len(got.toasts) == 0 || got.toasts[0].Severity != toastError {
		t.Fatalf("error toast missing: %+v", got.toasts)
	}
	if !strings.Contains(got.toasts[0].Text, "spawn failed") {
		t.Fatalf("error toast = %q, want start error", got.toasts[0].Text)
	}
	if cmd == nil {
		t.Fatal("error toast should arm toast tick")
	}
}

func TestLoopRunPlanLaunchValidationErrorStillSurfacesInMode(t *testing.T) {
	pathErr := errors.New("bad plan")
	m := testModel()
	m.mode = ModeLoopRunPlanPath

	updated, cmd := m.Update(loopRunPlanLaunchMsg{Err: pathErr})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("validation error returned cmd %T, want nil", cmd)
	}
	if !errors.Is(got.loopRunPlanPathErr, pathErr) {
		t.Fatalf("path error = %v, want validation error", got.loopRunPlanPathErr)
	}
}

func TestToastWarningMapsToStatusWarning(t *testing.T) {
	m := testModel()
	m.pushToast(Toast{Severity: toastWarning, Text: "duplicate"})
	msg, ok := latestActiveToastStatus(m, m.toasts[0].Expires.Add(-1))
	if !ok {
		t.Fatal("latest toast not found")
	}
	if msg.Severity != statusWarning {
		t.Fatalf("status severity = %v, want statusWarning", msg.Severity)
	}
}
