package tui

import (
	"context"
	"errors"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/planrun"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/runform"
)

// The run form (ModePlanRun): starting an agent-session-loop run of the plan
// under the Plans cursor. tui/runform owns what the form holds and how it is
// drawn; this file owns the flow, which is short because whether a plan may
// be started, and what starting it launches, is decided by the plan.run
// action the command line runs too.

// planRunDoneMsg carries what one submission of the run form came to.
type planRunDoneMsg struct {
	seq    int
	result actions.Result
	err    error
}

// planRunProjects are the projects the form offers for plan: the ones the
// Plans index matched to its working directory, or every project when it
// matched none, so a plan no project covers can still be started from one.
func planRunProjects(cfg *config.Config, plan plans.PlanMeta) []config.Session {
	active := actions.PlanLaunchProjects(cfg)
	names := plan.ProjectNames()
	var matched []config.Session
	for _, project := range active {
		if slices.Contains(names, project.Title) {
			matched = append(matched, project)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	return active
}

func configuredProfileNames(cfg *config.Config) []string {
	accounts := cfg.Accounts
	if len(accounts) == 0 {
		accounts = cfg.Profiles
	}
	names := make([]string, 0, len(accounts))
	for _, account := range accounts {
		names = append(names, account.Name)
	}
	return names
}

// openPlanRunForm raises the form over the Plans list for one captured plan.
func (m Model) openPlanRunForm(plan plans.PlanMeta) (tea.Model, tea.Cmd) {
	if m.cfg == nil {
		return m, nil
	}
	projects, profiles := planRunProjects(m.cfg, plan), configuredProfileNames(m.cfg)
	if len(projects) == 0 || len(profiles) == 0 {
		return m, m.pushToast(Toast{Severity: toastError, Text: "a run launches from a configured project, and none is configured"})
	}
	m.planRun = runform.Open(plan, projects, profiles, m.planRun.Seq+1)
	m.mode = ModePlanRun
	return m, nil
}

// closePlanRunForm drops the form. Seq survives, so an answer still in
// flight to the closed form cannot match the next one.
func (m *Model) closePlanRunForm() {
	m.planRun = runform.State{Seq: m.planRun.Seq}
	m.mode = ModePlanSearch
}

func (m Model) handlePlanRunKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.planRun.Active {
		m.mode = ModePlanSearch
		return m, nil
	}
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, keyQ:
		m.closePlanRunForm()
		return m, nil
	}
	if m.planRun.InFlight {
		// One submission at a time: the answer on its way is about the
		// choices as they stood.
		return m, nil
	}
	switch msg.String() {
	case keyJ, keyDown, keyTab:
		m.planRun.Move(1)
	case keyK, keyUp, keyShiftTab:
		m.planRun.Move(-1)
	case keyL, keyRight, keySpace:
		m.planRun.Cycle(1)
	case "h", keyLeft:
		m.planRun.Cycle(-1)
	case keyEnter:
		return m.submitPlanRun()
	}
	return m, nil
}

func (m Model) submitPlanRun() (tea.Model, tea.Cmd) {
	if err := m.planRun.Options().Validate(); err != nil {
		m.planRun.Notice, m.planRun.Warn = sentenceCase(err.Error())+".", true
		return m, nil
	}
	parameters := m.planRun.Parameters()
	if project, ok := m.planRun.SelectedProject(); ok {
		parameters["project"] = actions.PlanProjectID(project)
	}
	m.planRun.Seq++
	m.planRun.InFlight = true
	m.planRun.Readiness, m.planRun.Notice, m.planRun.Warn = nil, "", false
	return m, m.runPlanRunCmd(m.planRun.Seq, m.planRun.Plan, parameters)
}

func (m Model) runPlanRunCmd(seq int, plan plans.PlanMeta, parameters map[string]string) tea.Cmd {
	if m.planRunCommand != nil {
		return m.planRunCommand(seq, plan, parameters)
	}
	deps := commands.TerminalDependencies(m.client, m.cfg)
	return func() tea.Msg {
		result, err := commands.Registry.Execute(context.Background(), deps, actions.PlanRun, actions.Request{Plan: &plan, Parameters: parameters})
		return planRunDoneMsg{seq: seq, result: result, err: err}
	}
}

// handlePlanRunDone shows what a submission came to. A started run closes
// the form: the run is a session now, and the list is where it shows. A plan
// that may not be started keeps the form open on what agent-session-loop
// said about it.
func (m Model) handlePlanRunDone(msg planRunDoneMsg) (tea.Model, tea.Cmd) {
	if !m.planRun.Active || msg.seq != m.planRun.Seq {
		return m, nil
	}
	m.planRun.InFlight = false
	if msg.err == nil {
		title := m.planRun.PlanTitle()
		m.closePlanRunForm()
		return m, tea.Batch(m.pushToast(Toast{Severity: toastSuccess, Text: "started run of " + title}), m.requestTopologyRefresh())
	}
	if readiness, ok := msg.result.Data[actions.DataPlanReadiness].(planrun.Readiness); ok {
		m.planRun.Readiness = &readiness
		if !readiness.Ready() {
			m.planRun.Notice, m.planRun.Warn = sentenceCase(readiness.Summary())+".", true
			return m, nil
		}
	}
	m.planRun.Notice, m.planRun.Warn = planRunFailureText(msg.err), true
	return m, nil
}

// planRunFailureText is a failed submission in the form's words. A missing
// agent-session-loop is the one failure the operator fixes somewhere else,
// so it says where.
func planRunFailureText(err error) string {
	var actionErr *actions.Error
	if errors.As(err, &actionErr) && actionErr.Code == "asl_unavailable" {
		return "agent-session-loop did not answer: " + actionErr.Message + ". `amx doctor` reports its pairing."
	}
	return sentenceCase(err.Error())
}

func renderPlanRunBox(m Model) string { return runform.Render(m.planRun, m.width) }
