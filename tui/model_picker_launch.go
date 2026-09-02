package tui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/launch"
)

// restartWithAccountCmd closes and relaunches a session under a different account.
func (m Model) restartWithAccountCmd(acc config.Account) tea.Cmd {
	sess := dashboardSessionTemplate(m.cfg, m.targetSession)
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = actionDoneMsg{err: fmt.Errorf("panic in restartWithAccount: %v", r)}
			}
		}()
		_, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{Kitty: m.client, Config: m.cfg}, actions.Restart, actions.Request{
			Session: &m.targetSession, Project: &sess, Parameters: map[string]string{"profile": acc.Name}, Confirmed: true,
		})
		return actionDoneMsg{err: err}
	}
}

func (m *Model) launchBatchCmd(todo []launch.Queued, quitOnSuccess bool) tea.Cmd {
	if len(todo) == 0 {
		return nil
	}

	m.launchBatchID++
	batchID := m.launchBatchID
	total := len(todo)
	m.launchBatchDone = 0
	m.launchBatchErrors = 0
	m.launchBatchQuitOnSuccess = quitOnSuccess

	// The toast-tick cmd (returned by replaceToastByID when the transient
	// status list was empty) is a tea.Tick that blocks for toastTickInterval
	// before it emits. Keeping it OUTSIDE tea.Sequence below is critical:
	// inside the sequence it would gate the first launch on the tick's 250ms timer.
	var toastCmd tea.Cmd
	startText := launch.StartText(total)
	if c := m.replaceToastByID(batchID, Toast{
		Severity: toastInfo,
		Text:     startText,
		Expires:  time.Now().Add(progressToastTTL),
	}); c != nil {
		toastCmd = c
	}

	launchCmds := make([]tea.Cmd, 0, total)
	for _, q := range todo {
		sess := q.Session
		title := q.Title
		launchCmds = append(launchCmds, func() tea.Msg {
			_, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{Kitty: m.client, Config: m.cfg}, actions.Launch, actions.Request{Project: &sess})
			return launchProgressMsg{batchID: batchID, total: total, title: title, err: err}
		})
	}
	seq := tea.Sequence(launchCmds...)
	if toastCmd != nil {
		return tea.Batch(toastCmd, seq)
	}
	return seq
}

func (m *Model) dashboardLaunchWithAccountCmd(acc config.Account) tea.Cmd {
	sess := dashboardSessionTemplate(m.cfg, m.targetSession)
	sess = sessionWithProfile(sess, acc.Name)
	return m.launchBatchCmd([]launch.Queued{{
		Session: sess,
		Title:   configSessionDisplayTitle(sess),
	}}, false)
}

// dispatchPickerLaunch starts a multi-session launch and returns a batch of
// per-session commands. Each command emits a launchProgressMsg the model
// uses to drive the progress toast (1/N, 2/N, …). Returns nil when no session
// is selected so the caller doesn't dispatch a dud cmd. Running picker rows
// are explicit requests for another same-key tab.
func (m *Model) dispatchPickerLaunch() tea.Cmd {
	return m.launchBatchCmd(m.pickerInput().LaunchQueue(), true)
}

// handleLaunchProgress updates the progress toast for a finished
// per-session launch and triggers a session list refresh so the newly
// running session appears.
func (m Model) handleLaunchProgress(msg launchProgressMsg) (tea.Model, tea.Cmd) {
	if msg.batchID != m.launchBatchID {
		// Stale launch from a prior batch; ignore so we don't clobber a
		// fresh batch's toast.
		return m, fetchCmd(m.client, m.cfg, m.sortMode)
	}
	m.launchBatchDone++
	var cmds []tea.Cmd
	if msg.err != nil {
		m.launchBatchErrors++
		if c := m.pushToast(Toast{
			Severity: toastError,
			Text:     launch.FailureText(msg.title, msg.err),
		}); c != nil {
			cmds = append(cmds, c)
		}
	}
	batchDone := m.launchBatchDone >= msg.total
	severity := toastInfo
	if batchDone {
		severity = toastSuccess
	}
	text := launch.ProgressText(m.launchBatchDone, msg.total, msg.title)
	expires := time.Now().Add(progressToastTTL)
	if batchDone {
		expires = time.Now().Add(defaultToastTTL)
	}
	if c := m.replaceToastByID(msg.batchID, Toast{
		Severity: severity,
		Text:     text,
		Expires:  expires,
	}); c != nil {
		cmds = append(cmds, c)
	}
	if batchDone && m.launchBatchErrors == 0 && m.launchBatchQuitOnSuccess {
		cmds = append(cmds, tea.Quit)
	} else {
		cmds = append(cmds, fetchCmd(m.client, m.cfg, m.sortMode))
	}
	return m, tea.Batch(cmds...)
}

// pickerOpenCmd opens every target directory in a plain shell tab. All
// targets are attempted even when one fails; project-labelled errors are
// joined into the single completion message consumed by the existing open
// handler.
func pickerOpenCmd(m Model, targets []config.Session) tea.Cmd {
	if len(targets) == 0 {
		return nil
	}
	return func() tea.Msg {
		errs := make([]error, 0)
		for i := range targets {
			sess := targets[i]
			_, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{Kitty: m.client, Config: m.cfg}, actions.OpenDirectory, actions.Request{Project: &sess})
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", configSessionDisplayTitle(sess), err))
			}
		}
		return pickerOpenDoneMsg{err: errors.Join(errs...)}
	}
}

// savePickerOverrides persists an updated one-time profile choice while
// retaining all project pins.
func (m Model) savePickerOverrides() {
	if err := m.savePickerState(m.pickerProfileOverride, m.pickerPinned); err != nil {
		slog.Debug("overrides: save error", "err", err)
	}
}
