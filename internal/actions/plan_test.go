package actions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/planrun"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// planASL is ASL as PlanRun reads it: one answer per subcommand.
type planASL struct {
	status, preflight string
	calls             [][]string
}

func (a *planASL) Output(_ context.Context, args ...string) ([]byte, error) {
	a.calls = append(a.calls, args)
	switch args[0] {
	case "status":
		return []byte(a.status), nil
	case "preflight":
		if strings.Contains(a.preflight, `"ok":false`) {
			return []byte(a.preflight), errors.New("exit status 1")
		}
		return []byte(a.preflight), nil
	case "supervisor-guide":
		return []byte(`{"skills":[{"invocations":{"` + args[2] + `":"/asl asl run PLAN_DIR"}}]}`), nil
	}
	return nil, errors.New("unexpected asl command")
}

func (a *planASL) asked(subcommand string) bool {
	return slices.ContainsFunc(a.calls, func(call []string) bool { return call[0] == subcommand })
}

func readyASL() *planASL {
	return &planASL{
		status:    `{"ok":true,"counts":{"pending":2,"complete":0},"lock":{"active":false},"next_command":{"complete":false}}`,
		preflight: `{"ok":true,"findings":[]}`,
	}
}

// planFixture is a configuration with one project and a plan in its directory.
func planFixture(t *testing.T) (*config.Config, plans.PlanMeta) {
	t.Helper()
	cfg := continuityConfig(harness.Claude)
	cfg.Projects[0].Directory = t.TempDir()
	return cfg, plans.PlanMeta{Name: "fix things", DirPath: "/plans/fix-things", CWD: cfg.Projects[0].Directory}
}

func planError(t *testing.T, err error, code string) *Error {
	t.Helper()
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	return actionErr
}

func TestPlanRunStartsASupervisingSessionInThePlansDirectory(t *testing.T) {

	cfg, plan := planFixture(t)
	f := tmuxtest.New(t)
	deps := tmuxDeps(f, cfg)
	deps.ASL = readyASL()
	result, err := DefaultRegistry().Execute(context.Background(), deps, PlanRun, Request{Plan: &plan, Parameters: map[string]string{
		"harness": "codex", "review_harness": "claude", "commit_mode": "per-session",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Data[DataPlanProject] != "api" || result.Data[DataPlanProfile] != "work" {
		t.Fatalf("result = %+v", result)
	}
	const command = "asl run /plans/fix-things --harness codex --review-harness claude --commit-per-session"
	if result.Data[DataPlanCommand] != command {
		t.Fatalf("command = %v, want %q", result.Data[DataPlanCommand], command)
	}
	if running := tmuxSessions(t, f, cfg); len(running) != 1 || running[0].Directory != plan.CWD {
		t.Fatalf("running = %+v, want one session in %q", running, plan.CWD)
	}
}

func TestPlanRunOnTmuxReportsTheRuntimeSession(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, plan := planFixture(t)
	deps := tmuxDeps(f, cfg)
	deps.ASL = readyASL()
	result, err := DefaultRegistry().Execute(context.Background(), deps, PlanRun, Request{Plan: &plan})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.Data["runtime_session_id"].(string)
	if running := tmuxSessions(t, f, cfg); len(running) != 1 || id == "" || string(running[0].RuntimeSessionID) != id {
		t.Fatalf("running = %+v, want the session the run reported (%q)", running, id)
	}
}

// TestPlanRunStartsNothingForAPlanThatMayNotBeStarted: each refusal names
// its reason by code, carries what ASL said, and launches nothing.
func TestPlanRunStartsNothingForAPlanThatMayNotBeStarted(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, code string
		edit       func(*planASL)
		findings   int
	}{
		{name: "not ready", code: "plan_not_ready", findings: 1, edit: func(a *planASL) {
			a.preflight = `{"ok":false,"findings":[{"severity":"yellow","category":"manifest-readiness","message":"plan readiness state is 'reviewed'"}]}`
		}},
		{name: "running", code: "plan_running", edit: func(a *planASL) {
			a.status = `{"ok":true,"counts":{"pending":1},"lock":{"active":true},"next_command":{"complete":false}}`
		}},
		{name: "complete", code: "plan_complete", edit: func(a *planASL) {
			a.status = `{"ok":true,"counts":{"pending":0,"complete":4},"lock":{"active":false},"next_command":{"complete":true}}`
		}},
		{name: "asl silent", code: "asl_unavailable", edit: func(a *planASL) { a.preflight = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, plan := planFixture(t)
			asl := readyASL()
			test.edit(asl)
			result, err := DefaultRegistry().Execute(context.Background(), Dependencies{Config: cfg, ASL: asl}, PlanRun, Request{Plan: &plan})
			_ = planError(t, err, test.code)
			if asl.asked("supervisor-guide") {
				t.Fatalf("a refused plan went on to launch: %+v", asl.calls)
			}
			if test.code == "asl_unavailable" {
				return
			}
			readiness, ok := result.Data[DataPlanReadiness].(planrun.Readiness)
			if !ok || len(readiness.Findings) != test.findings {
				t.Fatalf("readiness = %+v, want %d findings", result.Data[DataPlanReadiness], test.findings)
			}
		})
	}
}

func TestPlanRunCheckReportsReadinessAndStartsNothing(t *testing.T) {
	t.Parallel()

	cfg, plan := planFixture(t)
	asl := readyASL()
	// A readiness check needs no terminal backend.
	result, err := DefaultRegistry().Execute(context.Background(), Dependencies{Config: cfg, ASL: asl}, PlanRun, Request{Plan: &plan, Parameters: map[string]string{"check": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || asl.asked("supervisor-guide") {
		t.Fatalf("check changed something: %+v", result)
	}
	if readiness, _ := result.Data[DataPlanReadiness].(planrun.Readiness); !readiness.Ready() {
		t.Fatalf("readiness = %+v, want ready", readiness)
	}
}

func TestPlanRunNeverGuessesTheProject(t *testing.T) {

	cfg, plan := planFixture(t)
	cfg.Accounts = append(cfg.Accounts, config.Account{Name: "home", Harness: harness.Codex, HomeDir: "/tmp/agentmux-home"})
	cfg.Projects = append(cfg.Projects,
		config.Session{Title: "api", Directory: cfg.Projects[0].Directory, Profile: "home"},
		config.Session{Title: "elsewhere", Directory: t.TempDir(), Profile: "work"},
	)
	f := tmuxtest.New(t)
	run := func(plan plans.PlanMeta, parameters map[string]string) (Result, error) {
		deps := tmuxDeps(f, cfg)
		deps.ASL = readyASL()
		return DefaultRegistry().Execute(context.Background(), deps, PlanRun, Request{Plan: &plan, Parameters: parameters})
	}

	// Two projects cover the plan's directory.
	_, err := run(plan, nil)
	if ambiguous := planError(t, err, "ambiguous_project"); len(ambiguous.Candidates) != 2 || ambiguous.Candidates[0].ID != "home/api" || ambiguous.Candidates[1].ID != "work/api" {
		t.Fatalf("candidates = %+v", ambiguous.Candidates)
	}
	if len(tmuxSessions(t, f, cfg)) != 0 {
		t.Fatal("an ambiguous plan launched")
	}

	// A profile narrows them to one, and that profile's harness supervises.
	result, err := run(plan, map[string]string{"profile": "home"})
	if err != nil || result.Data[DataPlanProfile] != "home" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}

	// A plan no project covers needs one named, and runs where the plan says.
	orphan := plans.PlanMeta{DirPath: "/plans/orphan", CWD: t.TempDir()}
	_, err = run(orphan, nil)
	if missing := planError(t, err, "missing_parameter"); len(missing.MissingParameters) != 1 || missing.MissingParameters[0].Name != "project" {
		t.Fatalf("missing = %+v", missing.MissingParameters)
	}
	result, err = run(orphan, map[string]string{"project": "elsewhere"})
	if err != nil || result.Data[DataPlanProject] != "elsewhere" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	matched := false
	for _, running := range tmuxSessions(t, f, cfg) {
		if string(running.RuntimeSessionID) == result.Data["runtime_session_id"] && running.Directory == orphan.CWD {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("orphan launch did not run in %q", orphan.CWD)
	}

	// A candidate's identifier names exactly its project, whatever profile
	// the session is then launched under.
	result, err = run(plan, map[string]string{"project": "work/api", "profile": "home"})
	if err != nil || result.Data[DataPlanProject] != "api" || result.Data[DataPlanProfile] != "home" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}

	_, err = run(plan, map[string]string{"project": "nowhere"})
	_ = planError(t, err, "not_found")
}

func TestPlanRunRefusesOptionsBeforeAskingASL(t *testing.T) {
	t.Parallel()

	cfg, plan := planFixture(t)
	asl := readyASL()
	_, err := DefaultRegistry().Execute(context.Background(), Dependencies{Config: cfg, ASL: asl}, PlanRun, Request{Plan: &plan, Parameters: map[string]string{"harness": "codex", "review_harness": "codex"}})
	_ = planError(t, err, "invalid_arguments")
	if len(asl.calls) != 0 {
		t.Fatalf("asl was asked: %+v", asl.calls)
	}
}
