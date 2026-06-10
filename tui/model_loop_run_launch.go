package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/plans"
)

type loopRunStartDoneMsg struct {
	Request agentloop.RunRequest
	Plan    plans.PlanMeta
	Result  agentloop.RunLaunchResult
	Err     error
}

var loopRunStart = agentloop.StartRun

func (m Model) handleLoopRunPlanLaunchMsg(msg loopRunPlanLaunchMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m.handleLoopRunPlanLaunchValidationError(msg)
	}
	m.planSearchErr = nil
	m.loopRunPlanPathErr = nil
	m.actionErr = nil
	return m, loopRunStartCmd(msg.Request, msg.Plan, m.activeLoopCandidates)
}

func (m Model) handleLoopRunPlanLaunchValidationError(msg loopRunPlanLaunchMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeLoopRunPlanPath:
		m.loopRunPlanPathErr = msg.Err
	case ModeLoopRunPlanSearch:
		m.planSearchErr = msg.Err
	default:
		m.actionErr = msg.Err
	}
	return m, nil
}

func loopRunStartCmd(req agentloop.RunRequest, plan plans.PlanMeta, activeRuns []agentloop.Candidate) tea.Cmd {
	active := append([]agentloop.Candidate(nil), activeRuns...)
	return func() tea.Msg {
		binary := strings.TrimSpace(req.Binary)
		if binary == "" || binary == agentSessionLoopBinary {
			resolved, err := resolveAgentSessionLoopBinary()
			if err != nil {
				return loopRunStartDoneMsg{
					Request: req,
					Plan:    plan,
					Err:     err,
				}
			}
			binary = resolved
		}
		req.Binary = binary
		result, err := loopRunStart(agentloop.StartRunRequest{
			ProjectDir: req.ProjectDir,
			PlanDir:    req.PlanDir,
			Binary:     req.Binary,
			Env:        agentSessionLoopExecEnv(req.Binary),
			ActiveRuns: active,
		})
		return loopRunStartDoneMsg{
			Request: req,
			Plan:    plan,
			Result:  result,
			Err:     err,
		}
	}
}

func (m Model) handleLoopRunStartDone(msg loopRunStartDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m.handleLoopRunStartError(msg)
	}

	m.exitLoopRunLaunchFlow()
	m.actionErr = nil
	var cmds []tea.Cmd
	if c := m.pushToast(Toast{
		Severity: toastSuccess,
		Text:     loopRunStartedToastText(msg),
	}); c != nil {
		cmds = append(cmds, c)
	}
	cmds = append(cmds, m.nextActiveLoopRefreshCmd())
	return m, tea.Batch(cmds...)
}

func (m Model) handleLoopRunStartError(msg loopRunStartDoneMsg) (tea.Model, tea.Cmd) {
	var duplicate *agentloop.DuplicateRunError
	if errors.As(msg.Err, &duplicate) {
		m.exitLoopRunLaunchFlow()
		m.actionErr = nil
		if duplicate.Candidate.PlanDir != "" || duplicate.Candidate.CWD != "" {
			m.activeLoopCandidates = upsertActiveLoopCandidate(m.activeLoopCandidates, duplicate.Candidate)
		}
		var cmds []tea.Cmd
		if c := m.pushToast(Toast{
			Severity: toastWarning,
			Text:     loopRunDuplicateToastText(msg),
		}); c != nil {
			cmds = append(cmds, c)
		}
		cmds = append(cmds, m.nextActiveLoopRefreshCmd())
		return m, tea.Batch(cmds...)
	}

	var metadataErr *agentloop.LaunchMetadataError
	if errors.As(msg.Err, &metadataErr) {
		if metadataErr.Result.PID > 0 {
			msg.Result = metadataErr.Result
		}
		m.exitLoopRunLaunchFlow()
		m.actionErr = nil
		var cmds []tea.Cmd
		if c := m.pushToast(Toast{
			Severity: toastWarning,
			Text:     loopRunStartedWithWarningToastText(msg),
		}); c != nil {
			cmds = append(cmds, c)
		}
		cmds = append(cmds, m.nextActiveLoopRefreshCmd())
		return m, tea.Batch(cmds...)
	}

	m.actionErr = msg.Err
	if c := m.pushToast(Toast{
		Severity: toastError,
		Text:     loopRunFailedToastText(msg),
	}); c != nil {
		return m, c
	}
	return m, nil
}

func (m *Model) exitLoopRunLaunchFlow() {
	m.mode = ModeList
	m.planSearchMode = false
	m.planQuery = ""
	m.planSearchErr = nil
	m.loopRunProjectDir = ""
	m.loopRunProjectTitle = ""
	m.loopRunPlanPath = ""
	m.loopRunPlanPathErr = nil
}

func (m *Model) nextActiveLoopRefreshCmd() tea.Cmd {
	m.activeLoopRefreshSeq++
	return activeLoopRefreshCmd(m.activeLoopRefreshSeq)
}

func loopRunStartedToastText(msg loopRunStartDoneMsg) string {
	text := fmt.Sprintf("Loop started: %s (pid %d)", loopRunPlanTitle(msg), msg.Result.PID)
	if path := strings.TrimSpace(msg.Result.LogPaths.Dir); path != "" {
		text += " logs: " + path
	} else if path := strings.TrimSpace(msg.Result.LogPaths.Stdout); path != "" {
		text += " logs: " + path
	}
	return text
}

func loopRunDuplicateToastText(msg loopRunStartDoneMsg) string {
	return fmt.Sprintf("Loop already running: %s - press t to follow", loopRunPlanTitle(msg))
}

func loopRunStartedWithWarningToastText(msg loopRunStartDoneMsg) string {
	return loopRunStartedToastText(msg) + " metadata warning: " + msg.Err.Error()
}

func loopRunFailedToastText(msg loopRunStartDoneMsg) string {
	return fmt.Sprintf("Loop launch failed: %s - %s", loopRunPlanTitle(msg), msg.Err)
}

func loopRunPlanTitle(msg loopRunStartDoneMsg) string {
	if title := strings.TrimSpace(msg.Plan.Name); title != "" {
		return title
	}
	if path := strings.TrimSpace(msg.Plan.DirPath); path != "" {
		return plans.DisplayName(filepath.Base(path))
	}
	if path := strings.TrimSpace(msg.Request.PlanDir); path != "" {
		return plans.DisplayName(filepath.Base(path))
	}
	return "plan"
}

func upsertActiveLoopCandidate(candidates []agentloop.Candidate, candidate agentloop.Candidate) []agentloop.Candidate {
	out := append([]agentloop.Candidate(nil), candidates...)
	for i := range out {
		if out[i].PlanDir == candidate.PlanDir && out[i].CWD == candidate.CWD {
			out[i] = candidate
			return out
		}
	}
	return append(out, candidate)
}
