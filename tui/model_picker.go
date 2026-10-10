// This file is what the root model does about the launch picker: navigating
// the configured projects and profiles it offers, and turning a chosen row
// into an actual launch, including the overrides a launch can carry.

package tui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/launch"
)

func (m Model) handlePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.pickerSearchMode {
		return m.handlePickerSearchKey(msg)
	}
	return m.handlePickerNavKey(msg)
}

// handlePickerSearchKey handles input while the picker is in search mode
// (activated by pressing `/`). Only esc, enter, and backspace have special
// meaning; every other printable character (including j, k, space, p, P, etc.)
// is appended to the filter buffer so the user can type freely.
func (m Model) handlePickerSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		// ctrl+c quits from every mode. The default branch below would
		// otherwise swallow it (msg.Text is empty for ctrl combos).
		return m, tea.Quit
	case keyEsc, keyEnter:
		m.pickerSearchMode = false
		return m, nil
	case keyBackspace:
		if r := []rune(m.pickerFilter); len(r) > 0 {
			m.pickerFilter = string(r[:len(r)-1])
			m.pickerFilteredIdx = m.pickerFilteredIndices(m.pickerFilter)
			m.pickerCursor = 0
			scrollPickerToCursor(&m)
			if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
				return m, cmd
			}
		}
		return m, nil
	default:
		if t := msg.Key().Text; t != "" {
			m.pickerFilter += t
			m.pickerFilteredIdx = m.pickerFilteredIndices(m.pickerFilter)
			m.pickerCursor = 0
			scrollPickerToCursor(&m)
			if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
				return m, cmd
			}
		}
		return m, nil
	}
}

// exitPicker returns from the launcher to the dashboard, dropping the
// launcher's transient override, pin, and profile filter.
func (m Model) exitPicker() (tea.Model, tea.Cmd) {
	m.mode = ModeList
	m.pickerProfileOverride = nil
	m.pickerPinned = nil
	m.pickerProfileFilter = ""
	// The mode is assigned before the pane is handed back, because handing it
	// back asks which window the DASHBOARD cursor is on and that question is
	// answered through the mode.
	return m, m.returnPreviewToDashboard()
}

// handlePickerNavKey handles input while the picker is in navigation mode
// (the default). Printable characters other than recognised shortcuts are
// silently ignored; press `/` to enter search mode and type a filter.
func (m Model) handlePickerNavKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keySlash:
		m.pickerSearchMode = true
		return m, nil
	case keyEsc:
		// Two-stage Esc: first clears all active filters; second exits picker.
		if m.pickerFilter != "" || m.pickerProfileFilter != "" {
			m.pickerFilter = ""
			m.pickerProfileFilter = ""
			m.pickerFilteredIdx = m.pickerFilteredIndices("")
			m.pickerCursor = 0
			m.pickerOffset = 0
			scrollPickerToCursor(&m)
			if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
				return m, cmd
			}
			return m, nil
		}
		return m.exitPicker()
	case keyQ:
		// q leaves the launcher in one step, skipping esc's clear-filters stage.
		return m.exitPicker()
	case keyQuestion:
		m.openHelp(ModePicker)
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		m.pickerCursor = clamp(m.pickerCursor+1, 0, len(m.pickerFilteredIdx)-1)
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyK, keyUp:
		m.pickerCursor = clamp(m.pickerCursor-1, 0, len(m.pickerFilteredIdx)-1)
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyG, keyHome:
		m.pickerCursor = 0
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyGG, keyEnd:
		m.pickerCursor = len(m.pickerFilteredIdx) - 1
		if m.pickerCursor < 0 {
			m.pickerCursor = 0
		}
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyCtrlD:
		step := pickerVisibleHeight(m) / 2
		if step < 1 {
			step = 1
		}
		m.pickerCursor = clamp(m.pickerCursor+step, 0, len(m.pickerFilteredIdx)-1)
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyCtrlU:
		step := pickerVisibleHeight(m) / 2
		if step < 1 {
			step = 1
		}
		m.pickerCursor = clamp(m.pickerCursor-step, 0, len(m.pickerFilteredIdx)-1)
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyPgDown:
		step := pickerVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.pickerCursor = clamp(m.pickerCursor+step, 0, len(m.pickerFilteredIdx)-1)
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyPgUp:
		step := pickerVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.pickerCursor = clamp(m.pickerCursor-step, 0, len(m.pickerFilteredIdx)-1)
		scrollPickerToCursor(&m)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyTogglePreview:
		return m.togglePickerPreview()
	case keyF:
		return m.cyclePickerProfile()
	case keyPinProject:
		return m.togglePickerPin()
	case keySpace:
		if len(m.pickerFilteredIdx) == 0 {
			return m, nil
		}
		if m.pickerSelected == nil {
			m.pickerSelected = make(map[int]bool)
		}
		origIdx := m.pickerFilteredIdx[m.pickerCursor]
		m.pickerSelected[origIdx] = !m.pickerSelected[origIdx]
		return m, nil
	case keyProjectSessionSearch:
		if len(m.pickerFilteredIdx) == 0 || m.pickerCursor < 0 || m.pickerCursor >= len(m.pickerFilteredIdx) {
			return m, nil
		}
		origIdx := m.pickerFilteredIdx[m.pickerCursor]
		if origIdx < 0 || origIdx >= len(m.pickerSessions) {
			return m, nil
		}
		updated, cmd := m.enterSessionSearchForProject(m.pickerSessions[origIdx].Title)
		next := updated.(Model)
		// Past Sessions is a third surface, not a deeper room in the launcher, so
		// this is a way OUT of the launcher and gives the pane back like the other
		// two. Leaving the borrow outstanding here would not merely delay the
		// restore: the dashboard behind Past Sessions is reachable again from that
		// surface's own esc, so the stash would still be held while the operator
		// scrolls and resumes the inspector, and the next launcher visit would put
		// the pre-visit snapshot back over what they did.
		//
		// It is returned AFTER entry, for the reason the esc path spells out — the
		// restore asks which window the DASHBOARD cursor is on and reads the mode
		// to decide, so it must not run while the mode still says ModePicker. An
		// entry that refused (an untitled project) leaves the launcher on screen
		// and is not a way out at all, which is what the guard reads.
		if next.activeRootMode() == ModePicker {
			return next, cmd
		}
		// Sequenced rather than inlined into the return, for the reason the enter
		// path states: the restore writes a new chain identity onto next, and Go
		// does not order a bare operand against a call beside it in the same list.
		resume := next.returnPreviewToDashboard()
		return next, tea.Batch(cmd, resume)
	case keyO:
		return m, pickerOpenCmd(m, m.pickerOpenTargets())
	case keyOpenEditor:
		if len(m.pickerFilteredIdx) > 0 {
			origIdx := m.pickerFilteredIdx[m.pickerCursor]
			sess := m.pickerSessionWithResolvedProfile(origIdx)
			return m, pickerOpenEditorCmd(m, sess)
		}
		return m, nil
	case keyDashboardLaunch:
		if len(m.pickerFilteredIdx) > 0 {
			origIdx := m.pickerFilteredIdx[m.pickerCursor]
			sess := m.pickerSessionWithResolvedProfile(origIdx)
			profile := m.pickerEffectiveProfile(origIdx)
			m.targetSession = session.RunningSession{
				Base:    configSessionDisplayTitle(sess),
				Project: m.pickerSessions[origIdx].Title,
				Profile: profile,
				Account: profile,
			}
			m.configPickerAccounts = m.cfg.Accounts
			m.configPickerCurrent = profile
			m.configPickerCursor = launch.AccountIndex(m.cfg.Accounts, profile)
			m.configPickerPurpose = launch.ConfigPickerLauncherOverride
			m.configPickerReturnMode = ModePicker
			m.configPickerProjectIdx = origIdx
			m.mode = ModeConfigPicker
		}
		return m, nil
	case keyEnter:
		cmd := m.dispatchPickerLaunch()
		m.mode = ModeList
		m.pickerSelected = nil
		m.pickerProfileOverride = nil
		m.pickerPinned = nil
		m.pickerProfileFilter = ""
		// Returned before the expression is built, not inside it: handing the pane
		// back draws a new chain identity onto m, and Go does not order a bare
		// operand against a call in the same expression list.
		resume := m.returnPreviewToDashboard()
		return m, tea.Batch(cmd, resume)
	case keyBackspace:
		if r := []rune(m.pickerFilter); len(r) > 0 {
			m.pickerFilter = string(r[:len(r)-1])
			m.pickerFilteredIdx = m.pickerFilteredIndices(m.pickerFilter)
			m.pickerCursor = 0
			scrollPickerToCursor(&m)
			if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
				return m, cmd
			}
		}
		return m, nil
	default:
		return m, nil
	}
}

// cyclePickerProfile advances the launcher's default-profile filter through
// the project-backed profile groups and back to the empty "all" state.
func (m Model) cyclePickerProfile() (tea.Model, tea.Cmd) {
	m.pickerProfileFilter = launch.NextProfileFilter(m.pickerSessions, m.pickerProfileFilter)
	m.pickerFilteredIdx = m.pickerFilteredIndices(m.pickerFilter)
	m.pickerCursor = 0
	m.pickerOffset = 0
	scrollPickerToCursor(&m)
	return m, m.refreshPreviewForPickerCursor()
}

// togglePickerPin persists and applies the highlighted project's next pin
// state. The visible index list is re-partitioned without reordering
// pickerSessions, so selections and profile overrides remain attached to their
// original project indices.
func (m Model) togglePickerPin() (tea.Model, tea.Cmd) {
	if len(m.pickerFilteredIdx) == 0 ||
		m.pickerCursor < 0 || m.pickerCursor >= len(m.pickerFilteredIdx) {
		return m, nil
	}
	origIdx := m.pickerFilteredIdx[m.pickerCursor]
	if origIdx < 0 || origIdx >= len(m.pickerSessions) {
		return m, nil
	}

	nextPinned := maps.Clone(m.pickerPinned)
	if nextPinned == nil {
		nextPinned = make(map[int]bool)
	}
	pinned := !nextPinned[origIdx]
	if pinned {
		nextPinned[origIdx] = true
	} else {
		delete(nextPinned, origIdx)
	}
	title := m.pickerSessions[origIdx].Title
	if err := m.savePickerState(m.pickerProfileOverride, nextPinned); err != nil {
		return m, m.pushToast(Toast{
			Severity: toastError,
			Text:     fmt.Sprintf("Could not update pin for %s: %s", title, err),
		})
	}

	m.pickerPinned = nextPinned
	m.pickerFilteredIdx = m.pickerFilteredIndices(m.pickerFilter)
	for i, idx := range m.pickerFilteredIdx {
		if idx == origIdx {
			m.pickerCursor = i
			break
		}
	}
	scrollPickerToCursor(&m)

	action := "Unpinned"
	if pinned {
		action = "Pinned"
	}
	cmds := []tea.Cmd{m.pushToast(Toast{
		Severity: toastSuccess,
		Text:     fmt.Sprintf("%s %s", action, title),
	})}
	if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// restartWithProfileCmd closes and relaunches a session under a different
// profile.
//
// It takes the row explicitly rather than reading m.targetSession, because the
// profile picker is a surface the operator can sit in: the selection behind it
// may have moved by the time a profile is chosen, and the session being
// restarted is the one that was captured when the action was chosen. Everything
// else is unchanged — the project template, the profile parameter, and the
// registry's own launch-before-close ordering.
func (m Model) restartWithProfileCmd(rs session.RunningSession, profile string) tea.Cmd {
	sess := commands.DashboardSessionTemplate(m.cfg, rs)
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = commands.ActionDoneMsg{Err: fmt.Errorf("panic in restartWithProfile: %v", r)}
			}
		}()
		_, err := commands.Registry.Execute(context.Background(), commands.TerminalDependencies(m.client, m.cfg), actions.Restart, actions.Request{
			Session: &rs, Project: &sess, Parameters: map[string]string{"profile": profile}, Confirmed: true,
		})
		return commands.ActionDoneMsg{Err: err}
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
			_, err := commands.Registry.Execute(context.Background(), commands.TerminalDependencies(m.client, m.cfg), actions.Launch, actions.Request{Project: &sess})
			return launchProgressMsg{batchID: batchID, total: total, title: title, err: err}
		})
	}
	seq := tea.Sequence(launchCmds...)
	if toastCmd != nil {
		return tea.Batch(toastCmd, seq)
	}
	return seq
}

// dashboardLaunchWithProfileCmd launches another session from a dashboard row
// under a chosen profile. Like restartWithProfileCmd it takes the row rather
// than reading the current selection, for the same reason.
func (m *Model) dashboardLaunchWithProfileCmd(rs session.RunningSession, profile string) tea.Cmd {
	sess := commands.DashboardSessionTemplate(m.cfg, rs)
	sess = commands.SessionWithProfile(sess, profile)
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
		return m, m.requestTopologyRefresh()
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
	// Quitting hands the operator to the tab Kitty just opened. A portable
	// launch opens none, so the dashboard stays and lists the new session.
	if batchDone && m.launchBatchErrors == 0 && m.launchBatchQuitOnSuccess && commands.LaunchesInKitty() {
		cmds = append(cmds, tea.Quit)
	} else if c := m.requestTopologyRefresh(); c != nil {
		cmds = append(cmds, c)
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
	if m.client == nil {
		return pickerOpenHereCmd(m, targets)
	}
	return func() tea.Msg {
		errs := make([]error, 0)
		for i := range targets {
			sess := targets[i]
			_, err := commands.Registry.Execute(context.Background(), commands.TerminalDependencies(m.client, m.cfg), actions.OpenDirectory, actions.Request{Project: &sess})
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", configSessionDisplayTitle(sess), err))
			}
		}
		return commands.PickerOpenDoneMsg{Err: errors.Join(errs...)}
	}
}

// pickerOpenHereCmd is pickerOpenCmd outside Kitty: with no tab to open, the
// first target's directory is opened in a shell in this terminal.
func pickerOpenHereCmd(m Model, targets []config.Session) tea.Cmd {
	if len(targets) == 0 {
		return nil
	}
	return commands.OpenDirectory(nil, m.cfg, targets[0])
}

func pickerOpenEditorCmd(m Model, sess config.Session) tea.Cmd {
	if m.client == nil {
		return commands.OpenInEditor(nil, m.cfg, sess)
	}
	return func() tea.Msg {
		_, err := commands.Registry.Execute(context.Background(), commands.TerminalDependencies(m.client, m.cfg), actions.OpenEditor, actions.Request{Project: &sess})
		return commands.PickerOpenDoneMsg{Err: err}
	}
}

// savePickerOverrides persists an updated one-time profile choice while
// retaining all project pins.
func (m Model) savePickerOverrides() {
	if err := m.savePickerState(m.pickerProfileOverride, m.pickerPinned); err != nil {
		slog.Debug("overrides: save error", "err", err)
	}
}
