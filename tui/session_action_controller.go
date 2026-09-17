// This file is the root's half of the shared session-action controller: the
// effects each action identity resolves to in this model, the navigation seams
// the controller leaves and returns through, and the application of the step it
// decides on.
//
// The division is the same one the rest of the TUI tree follows. What an action
// IS — its identity, its wording, its mnemonic, whether the captured row may run
// it — is declared once in tui/sessionactions. WHEN it runs, whether it needs a
// profile or a confirmation first, whether the row it was aimed at is still that
// row, and where a cancellation lands are decided once by the controller in that
// package. What it DOES is here, because every one of those closures reads this
// model's client, configuration, toasts, and modes.
//
// Both dashboard entry points reach the same effects through runSessionAction:
// the per-row menu in actions.go and the session half of the palette catalog in
// command_catalog.go. That is the whole point of the file — the two surfaces used
// to hold two spellings of these effects, and the spellings had drifted.

package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// hostTabConsequence is appended to a destructive confirmation aimed at the tab
// the dashboard is itself running in.
//
// The action is still offered. An operator restarting the session they launched
// the dashboard beside is an ordinary thing to do, and a refusal would be the
// surface protecting itself rather than the operator; what they are owed is the
// sentence, before the keystroke rather than after it.
const hostTabConsequence = "This dashboard is running in that tab and closes with it."

// sessionActionController assembles the controller for one call. It is built per
// call rather than stored on the model because it is immutable — every field is
// a seam or the clock — and the state it reads and writes lives on the model
// itself, through the State and SetState seams below.
func sessionActionController(m Model) sessionactions.Controller[Model] {
	return sessionactions.NewController(sessionActionEffects(), m.now)
}

// sessionActionEffects is what each action DOES in this model, plus the seams
// the controller navigates and correlates through.
//
// Every effect acts on the dispatch's captured target rather than on the live
// cursor or on m.targetSession. The two agree at dispatch time — runSessionAction
// records the target on the model so the confirmation and picker overlays can
// name it — but a refresh landing between them would move the second and not the
// first, and the capture is the one the operator chose.
func sessionActionEffects() sessionactions.Effects[Model] {
	return sessionactions.Effects[Model]{
		Env:     sessionActionEnv,
		Live:    liveSessionForTarget,
		Dismiss: dismissSessionActionSurfaces,
		Restore: restoreSessionActionOrigin,
		Stamp: func(cmd tea.Cmd, request sessionactions.RequestID) tea.Cmd {
			return commands.StampRequest(cmd, uint64(request))
		},
		State:    func(m Model) sessionactions.State { return m.sessionActions },
		SetState: func(m Model, state sessionactions.State) Model { m.sessionActions = state; return m },
		Run:      sessionActionRunners(),
	}
}

// sessionActionRunners is the effect table both surfaces dispatch through.
func sessionActionRunners() map[commands.ID]sessionactions.Effect[Model] {
	return map[commands.ID]sessionactions.Effect[Model]{
		commands.FocusSession: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			return m, commands.Focus(m.client, d.Target.TabID())
		},
		commands.PeekScrollback: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			return m, commands.Peek(m.client, d.Target.WindowID())
		},
		commands.RestartSession: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			return m, m.restartWithToast(d.Target.Session)
		},
		commands.OverrideProfile: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			return m, m.restartWithProfileCmd(d.Target.Session, d.Profile)
		},
		commands.LaunchAnother: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			return m, m.dashboardLaunchWithProfileCmd(d.Target.Session, d.Profile)
		},
		commands.ClearStatusIcon: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			return m, m.clearStatusCmd(d.Target.Session)
		},
		commands.CloseSession: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			return m, m.closeWithToast(d.Target.Session)
		},
		commands.OpenDirectoryID: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			cs, ok := commands.FindConfigSession(m.cfg, d.Target.Session.EffectiveProfile(), d.Target.Session.EffectiveProject())
			if !ok {
				return m, nil
			}
			return m, commands.OpenDirectory(m.client, m.cfg, cs)
		},
		commands.OpenEditorID: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			cs, ok := commands.FindConfigSession(m.cfg, d.Target.Session.EffectiveProfile(), d.Target.Session.EffectiveProject())
			if !ok {
				return m, nil
			}
			return m, commands.OpenInEditor(m.client, m.cfg, cs)
		},
		commands.CopyTitleID: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			return m, commands.CopyTitle(dashboard.SessionDisplayTitle(d.Target.Session))
		},
		commands.ToggleLockID: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			return m, commands.ToggleLock(m.client, d.Target.Session)
		},
		commands.ToggleMuteID: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			return m, commands.ToggleMute(m.client, d.Target.Session)
		},
		commands.SearchProject: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			updated, cmd := m.enterSessionSearchForProject(d.Target.Session.EffectiveProject())
			return updated.(Model), cmd
		},
		commands.ShowDetails: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			updated, cmd := m.enterDetailMode(d.Target.WindowID(), false)
			return updated.(Model), cmd
		},
		commands.ShowTimeline: func(m Model, d sessionactions.Dispatch) (Model, tea.Cmd) {
			updated, cmd := m.enterDetailMode(d.Target.WindowID(), true)
			return updated.(Model), cmd
		},
	}
}

// liveSessionForTarget resolves the captured target against the list as the
// dashboard currently sees it.
//
// It searches the full session list rather than the filtered one: a filter the
// operator typed after choosing an action hides a row, it does not end it, and
// refusing on that basis would report "the session is no longer listed" about a
// session that is plainly still running. The window is matched before the tab
// because it is the more specific identity; the tab fallback is what finds a row
// the daemon has not yet attributed a window to.
func liveSessionForTarget(m Model, target sessionactions.Target) (session.RunningSession, bool) {
	if !target.HasSession {
		return session.RunningSession{}, false
	}
	if windowID := target.WindowID(); windowID != 0 {
		for _, candidate := range m.sessions {
			if candidate.WindowID == windowID {
				return candidate, true
			}
		}
	}
	if tabID := target.TabID(); tabID != 0 {
		for _, candidate := range m.sessions {
			if candidate.TabID == tabID {
				return candidate, true
			}
		}
	}
	return session.RunningSession{}, false
}

// dismissSessionActionSurfaces leaves every surface a session action can be
// chosen from and clears the transient state behind it. The controller runs it
// before an effect, so an effect that opens a surface of its own — the project
// history search, the event log — lands on top of a settled list rather than
// being closed by bookkeeping that ran after it.
func dismissSessionActionSurfaces(m Model) Model {
	m.closeCommandPalette()
	m.exitActionsMode()
	m.actionsOffset = 0
	m.clearSessionActionPrompt()
	m.mode = ModeList
	return m
}

// restoreSessionActionOrigin puts the operator back in the surface they chose
// the cancelled action from, with the selection they chose it with.
//
// The palette's results are recomputed rather than restored, because
// availability is re-derived at render time and a stale result set would offer
// rows against a list that has moved. The cursor and scroll position are then
// put back over them, which is what recomputeCommandPaletteResults deliberately
// does not do: it is the query-edit path, and an edit is entitled to reset the
// selection.
func restoreSessionActionOrigin(m Model, pending sessionactions.Pending) Model {
	m.clearSessionActionPrompt()
	switch pending.Origin.Surface {
	case sessionactions.SurfaceMenu:
		m.targetSession = pending.Target.Session
		m.actionsReturnMode = ModeList
		m.mode = ModeActions
		m.actionsOffset = pending.Origin.MenuOffset
		clampActionsOffset(&m)
	case sessionactions.SurfacePalette:
		m.mode = ModeCommandPalette
		m.commandContext = pending.Target.Context()
		m.commandQuery = pending.Origin.PaletteQuery
		m.commandResults = commands.PaletteResults(commandCatalog(), m, m.commandContext, m.commandQuery)
		m.commandCursor = clamp(pending.Origin.PaletteCursor, 0, len(m.commandResults)-1)
		m.commandOffset = pending.Origin.PaletteOffset
		clampCommandPaletteOffset(&m)
	default:
		m.mode = ModeList
	}
	return m
}

// clearSessionActionPrompt drops the confirmation and picker fields a pending
// action wrote. It is deliberately silent about the handoff, worktree, and
// launcher purposes those same fields also serve: those flows never enter the
// controller, and clearing their state here would break them from a distance.
func (m *Model) clearSessionActionPrompt() {
	m.confirmAction = ""
	m.confirmConsequence = ""
	m.confirmReturnMode = ModeList
}

// menuActionOrigin is where the per-row menu is, as an origin to come back to.
func menuActionOrigin(m Model) sessionactions.Origin {
	return sessionactions.Origin{Surface: sessionactions.SurfaceMenu, MenuOffset: m.actionsOffset}
}

// paletteActionOrigin is the palette's query and selection, captured before the
// palette tears them down to execute.
func paletteActionOrigin(m Model) sessionactions.Origin {
	return sessionactions.Origin{
		Surface:       sessionactions.SurfacePalette,
		PaletteQuery:  m.commandQuery,
		PaletteCursor: m.commandCursor,
		PaletteOffset: m.commandOffset,
	}
}

// runSessionAction is the one entry point both surfaces dispatch through.
//
// The origin is read from the model and cleared in the same breath. It is
// written by the surface that offered the action and belongs to exactly the
// dispatch that follows it, so a direct dashboard binding — which records no
// origin at all — cannot inherit the palette query of whatever the operator ran
// before it.
func (m Model) runSessionAction(action commands.ID, target sessionactions.Target) (Model, tea.Cmd) {
	origin := m.pendingActionOrigin
	m.pendingActionOrigin = sessionactions.Origin{}
	m.actionErr = nil
	if target.HasSession {
		m.targetSession = target.Session
	}
	updated, step, cmd := sessionActionController(m).Begin(m, action, target, origin)
	return updated.applySessionActionStep(step, cmd)
}

// confirmSessionAction answers the confirmation the controller is waiting on.
func (m Model) confirmSessionAction() (Model, tea.Cmd) {
	updated, step, cmd := sessionActionController(m).Confirm(m)
	return updated.applySessionActionStep(step, cmd)
}

// chooseSessionActionProfile answers the profile selection the controller is
// waiting on.
func (m Model) chooseSessionActionProfile(profile string) (Model, tea.Cmd) {
	updated, step, cmd := sessionActionController(m).ChooseProfile(m, profile)
	return updated.applySessionActionStep(step, cmd)
}

// cancelSessionAction backs out of the nested interaction the controller is
// waiting on. It returns the restored model and whether anything was pending, so
// a key handler can fall through to its own behavior when nothing was.
func (m Model) cancelSessionAction() (Model, tea.Cmd, bool) {
	updated, step := sessionActionController(m).Cancel(m)
	if step.Kind != sessionactions.StepCancelled {
		return m, nil, false
	}
	next, cmd := updated.applySessionActionStep(step, nil)
	return next, cmd, true
}

// applySessionActionStep is what the host does about one controller decision.
// The controller has already settled its own state and run whatever navigation
// seam applied; what is left here is the surface each step raises.
func (m Model) applySessionActionStep(step sessionactions.Step, cmd tea.Cmd) (Model, tea.Cmd) {
	switch step.Kind {
	case sessionactions.StepProfile:
		m.openSessionActionProfilePicker(step)
	case sessionactions.StepConfirm:
		m.confirmAction = step.Prompt.Verb
		m.confirmConsequence = m.confirmConsequenceFor(step)
		m.confirmReturnMode = ModeList
		m.mode = ModeConfirm
	case sessionactions.StepRefused:
		if step.Detail != "" {
			if toastCmd := m.pushToast(Toast{Severity: toastWarning, Text: step.Detail}); toastCmd != nil {
				cmd = tea.Batch(cmd, toastCmd)
			}
		}
	}
	return m, cmd
}

// confirmConsequenceFor is the prompt's second line: what the shared vocabulary
// says the action does, plus the warning that this dashboard is standing in the
// tab it would do it to.
func (m Model) confirmConsequenceFor(step sessionactions.Step) string {
	consequence := step.Prompt.Consequence
	if m.actionHost.Hosts(step.Target) {
		if consequence != "" {
			consequence += " "
		}
		consequence += hostTabConsequence
	}
	return consequence
}

// openSessionActionProfilePicker raises the shared profile picker for the two
// actions that need one. The purpose is what handleConfigPickerKey reads to tell
// a profile replacement from a launch, and it is set from the action identity
// rather than carried on the step: the controller does not know this model has a
// picker, let alone what its purposes are called.
func (m *Model) openSessionActionProfilePicker(step sessionactions.Step) {
	current := step.Target.Session.EffectiveProfile()
	m.configPickerAccounts = m.cfg.Accounts
	m.configPickerCurrent = current
	m.configPickerCursor = launch.AccountIndex(m.cfg.Accounts, current)
	m.configPickerProjectIdx = -1
	m.configPickerReturnMode = ModeList
	m.configPickerPurpose = launch.ConfigPickerRestartProfile
	if step.Action == commands.LaunchAnother {
		m.configPickerPurpose = launch.ConfigPickerDashboardLaunch
	}
	m.mode = ModeConfigPicker
}

// sessionActionAwaitingConfirm and sessionActionAwaitingProfile are the two
// questions a key handler asks before it decides whether a keypress belongs to
// the controller or to the flow that surface also serves. The confirm overlay
// and the profile picker are both multiplexed — worktrees, handoffs, launcher
// overrides, and ASL runs raise the same modes — so "is the controller waiting"
// has to be answered rather than assumed from the mode.
func (m Model) sessionActionAwaitingConfirm() bool {
	pending := m.sessionActions.Pending()
	return pending.Active && pending.Stage == sessionactions.StageConfirm
}

func (m Model) sessionActionAwaitingProfile() bool {
	pending := m.sessionActions.Pending()
	return pending.Active && pending.Stage == sessionactions.StageProfile
}
