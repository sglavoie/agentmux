package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/planrun"
	"github.com/sglavoie/agentmux/internal/plans"
)

// planRunRecorder stands in for the plan.run action: it keeps what the form
// submitted and answers with whatever the test queued.
type planRunRecorder struct {
	plans      []plans.PlanMeta
	parameters []map[string]string
	answer     planRunDoneMsg
}

// planRunModel is the Plans view on one plan whose directory two projects
// cover, with the form's submission recorded instead of run.
func planRunModel(t *testing.T) (Model, *planRunRecorder) {
	t.Helper()
	m := testModelWithSessions()
	m.cfg = &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "claude"}, {Name: "home", Harness: "codex"}},
		Projects: []config.Session{
			{Title: "api", Directory: "/home/user/api", Profile: "work"},
			{Title: "web", Directory: "/home/user/web", Profile: "home"},
			{Title: "old", Directory: "/home/user/old", Profile: "work", Archived: true},
		},
	}
	m.mode = ModePlanSearch
	m.planResults = []plans.Result{{Meta: plans.PlanMeta{
		Name: "fix-things", DirPath: "/plans/fix-things", CWD: "/home/user/api", Project: "api", Projects: []string{"api"},
	}}}
	recorder := &planRunRecorder{}
	m.planRunCommand = func(seq int, plan plans.PlanMeta, parameters map[string]string) tea.Cmd {
		recorder.plans = append(recorder.plans, plan)
		recorder.parameters = append(recorder.parameters, parameters)
		answer := recorder.answer
		answer.seq = seq
		return func() tea.Msg { return answer }
	}
	return m, recorder
}

// planRunStep feeds msg, then the form's own answer when the command it
// returned produced one.
func planRunStep(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	updated, cmd := m.Update(msg)
	m = updated.(Model)
	if cmd == nil {
		return m
	}
	if done, ok := cmd().(planRunDoneMsg); ok {
		updated, _ = m.Update(done)
		m = updated.(Model)
	}
	return m
}

// runFormKey is a key press as the run form reads one.
func runFormKey(key string) tea.KeyPressMsg {
	switch key {
	case keyEnter:
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case keyEsc:
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
	}
	runes := []rune(key)
	return tea.KeyPressMsg(tea.Key{Code: runes[0], Text: key})
}

func TestRunFormOffersTheProjectsThePlanNames(t *testing.T) {
	t.Parallel()

	m, _ := planRunModel(t)
	m = planRunStep(t, m, runFormKey(keyRunPlan))
	if m.mode != ModePlanRun || !m.planRun.Active {
		t.Fatalf("%q on a plan left mode %v, want the run form", keyRunPlan, m.mode)
	}
	if len(m.planRun.Projects) != 1 || m.planRun.Projects[0].Title != "api" {
		t.Fatalf("projects = %+v, want the one the plan's directory names", m.planRun.Projects)
	}
	if got := m.activeRootMode(); got != ModePlanSearch {
		t.Fatalf("the form's backdrop is %v, want the Plans list", got)
	}

	// A plan no project covers can be started from any that is not archived.
	m, _ = planRunModel(t)
	m.planResults[0].Meta.Project, m.planResults[0].Meta.Projects = "", nil
	m = planRunStep(t, m, runFormKey(keyRunPlan))
	if len(m.planRun.Projects) != 2 {
		t.Fatalf("projects = %+v, want every active project", m.planRun.Projects)
	}

	// With no plan under the cursor the key does nothing.
	m, _ = planRunModel(t)
	m.planResults = nil
	if m = planRunStep(t, m, runFormKey(keyRunPlan)); m.mode != ModePlanSearch {
		t.Fatalf("mode = %v with no plan selected", m.mode)
	}
}

func TestRunFormSubmitsItsChoicesAndClosesOnAStartedRun(t *testing.T) {
	t.Parallel()

	m, recorder := planRunModel(t)
	m = planRunStep(t, m, runFormKey(keyRunPlan))
	// Supervisor → home, harness → claude, review harness → codex.
	for _, key := range []string{keyJ, keyL, keyJ, keyL, keyJ, keyL, keyL, keyL} {
		m = planRunStep(t, m, runFormKey(key))
	}
	m = planRunStep(t, m, runFormKey(keyEnter))
	if len(recorder.parameters) != 1 {
		t.Fatalf("submissions = %d, want 1", len(recorder.parameters))
	}
	want := map[string]string{"project": "work/api", "profile": "home", "harness": "claude", "review_harness": "codex"}
	got := recorder.parameters[0]
	if len(got) != len(want) {
		t.Fatalf("parameters = %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("parameters = %v, want %v", got, want)
		}
	}
	if recorder.plans[0].DirPath != "/plans/fix-things" {
		t.Fatalf("plan = %+v", recorder.plans[0])
	}
	if m.mode != ModePlanSearch || m.planRun.Active {
		t.Fatalf("a started run left the form open (mode %v)", m.mode)
	}
}

// TestRunFormKeepsARefusedPlanOpenOnWhatASLSaid: the form stays, shows each
// finding, and a second enter asks again.
func TestRunFormKeepsARefusedPlanOpenOnWhatASLSaid(t *testing.T) {
	t.Parallel()

	m, recorder := planRunModel(t)
	readiness := planrun.Readiness{State: planrun.StateNotReady, Findings: []planrun.Finding{{Severity: "red", Category: "plan-structure", Message: "session-3.md is missing"}}}
	recorder.answer = planRunDoneMsg{
		result: actions.Result{Data: map[string]any{actions.DataPlanReadiness: readiness}},
		err:    &actions.Error{Code: "plan_not_ready", Message: readiness.Summary()},
	}
	m = planRunStep(t, m, runFormKey(keyRunPlan))
	m = planRunStep(t, m, runFormKey(keyEnter))
	if m.mode != ModePlanRun || m.planRun.InFlight || !m.planRun.Warn {
		t.Fatalf("mode = %v, form = %+v", m.mode, m.planRun)
	}
	m.width, m.height = 100, 30
	box := ansi.Strip(renderPlanRunBox(m))
	for _, text := range []string{"not ready to run", "plan-structure: session-3.md is missing"} {
		if !strings.Contains(box, text) {
			t.Fatalf("the form does not say %q:\n%s", text, box)
		}
	}
	m = planRunStep(t, m, runFormKey(keyEnter))
	if len(recorder.parameters) != 2 {
		t.Fatalf("submissions = %d, want a second one", len(recorder.parameters))
	}
	m = planRunStep(t, m, runFormKey(keyEsc))
	if m.mode != ModePlanSearch || m.planRun.Active {
		t.Fatalf("esc left mode %v", m.mode)
	}
}

func TestRunFormRefusesChoicesASLWouldRefuseWithoutAsking(t *testing.T) {
	t.Parallel()

	m, recorder := planRunModel(t)
	m = planRunStep(t, m, runFormKey(keyRunPlan))
	// Harness and review harness both on the first harness.
	for _, key := range []string{keyJ, keyJ, keyL, keyJ, keyL} {
		m = planRunStep(t, m, runFormKey(key))
	}
	m = planRunStep(t, m, runFormKey(keyEnter))
	if len(recorder.parameters) != 0 || !m.planRun.Warn || !strings.Contains(m.planRun.Notice, "must differ") {
		t.Fatalf("submissions = %d, notice = %q", len(recorder.parameters), m.planRun.Notice)
	}
}

func TestRunFormDropsAnAnswerToAnEarlierForm(t *testing.T) {
	t.Parallel()

	m, _ := planRunModel(t)
	m = planRunStep(t, m, runFormKey(keyRunPlan))
	stale := planRunDoneMsg{seq: m.planRun.Seq}
	m = planRunStep(t, m, runFormKey(keyEsc))
	m = planRunStep(t, m, runFormKey(keyRunPlan))
	updated, _ := m.Update(stale)
	if m = updated.(Model); m.mode != ModePlanRun || !m.planRun.Active {
		t.Fatalf("an answer to a closed form closed the next one (mode %v)", m.mode)
	}
}

// planRunSubmitThenEsc submits the form and closes it before the answer
// lands, returning that answer for the test to deliver.
func planRunSubmitThenEsc(t *testing.T, m Model) (Model, planRunDoneMsg) {
	t.Helper()
	m = planRunStep(t, m, runFormKey(keyRunPlan))
	updated, cmd := m.Update(runFormKey(keyEnter))
	m = updated.(Model)
	if !m.planRun.InFlight || cmd == nil {
		t.Fatalf("enter did not submit the form: %+v", m.planRun)
	}
	done, ok := cmd().(planRunDoneMsg)
	if !ok {
		t.Fatal("the submission produced no answer")
	}
	m = planRunStep(t, m, runFormKey(keyEsc))
	if m.mode != ModePlanSearch || m.planRun.Active {
		t.Fatalf("esc during a submission left mode %v", m.mode)
	}
	return m, done
}

// planRunRefreshRequested reports whether a topology refresh was started or
// queued between before and after.
func planRunRefreshRequested(before, after Model) bool {
	return after.topologyRefreshNextSeq != before.topologyRefreshNextSeq || after.topologyRefreshPending
}

// TestRunFormReportsAStartedRunAfterEsc: esc while the run is being started
// closes the form, but the plan.run action launches the session anyway, so
// its answer still says so and refreshes the list.
func TestRunFormReportsAStartedRunAfterEsc(t *testing.T) {
	t.Parallel()

	m, _ := planRunModel(t)
	m, done := planRunSubmitThenEsc(t, m)
	updated, cmd := m.Update(done)
	got := updated.(Model)
	if n := len(got.toasts); n == 0 || got.toasts[n-1].Severity != toastSuccess ||
		got.toasts[n-1].Text != "Started run of "+m.planResults[0].Meta.Name {
		t.Fatalf("a run started after esc was not reported: %+v", got.toasts)
	}
	if cmd == nil || !planRunRefreshRequested(m, got) {
		t.Fatal("a run started after esc requested no refresh")
	}
	if got.mode != ModePlanSearch || got.planRun.Active {
		t.Fatalf("the answer reopened the form (mode %v)", got.mode)
	}
	if again, _ := got.Update(done); len(again.(Model).toasts) != len(got.toasts) {
		t.Fatal("a repeated answer was reported twice")
	}
}

// TestRunFormReportsAFailedRunAfterEsc: a refusal that lands after esc has
// no form to show it in, so it is an error toast instead.
func TestRunFormReportsAFailedRunAfterEsc(t *testing.T) {
	t.Parallel()

	m, recorder := planRunModel(t)
	recorder.answer = planRunDoneMsg{err: &actions.Error{Code: "asl_unavailable", Message: "asl not found"}}
	m, done := planRunSubmitThenEsc(t, m)
	updated, _ := m.Update(done)
	got := updated.(Model)
	if n := len(got.toasts); n == 0 || got.toasts[n-1].Severity != toastError ||
		!strings.Contains(got.toasts[n-1].Text, "agent-session-loop did not answer") {
		t.Fatalf("a run that failed after esc was not reported: %+v", got.toasts)
	}
}

// TestRunFormReportsAnEarlierSubmissionWithoutTouchingTheNewerForm: the
// answer to a submission whose form was closed is reported, and the form
// opened since keeps its state.
func TestRunFormReportsAnEarlierSubmissionWithoutTouchingTheNewerForm(t *testing.T) {
	t.Parallel()

	m, _ := planRunModel(t)
	m, done := planRunSubmitThenEsc(t, m)
	m = planRunStep(t, m, runFormKey(keyRunPlan))
	before := m.planRun
	updated, _ := m.Update(done)
	got := updated.(Model)
	if got.mode != ModePlanRun || !got.planRun.Active || got.planRun.Seq != before.Seq ||
		got.planRun.InFlight != before.InFlight || got.planRun.Notice != before.Notice {
		t.Fatalf("an earlier answer changed the newer form: %+v, was %+v", got.planRun, before)
	}
	if n := len(got.toasts); n == 0 || got.toasts[n-1].Severity != toastSuccess {
		t.Fatalf("the earlier run was not reported: %+v", got.toasts)
	}
}
