// The overview's half of the shared session-action controller.
//
// The grid offers the same twelve actions the dashboard's per-row menu offers,
// and it must offer exactly them: what each one is CALLED, which key the menu
// answers it with, which band it sits in, and whether the captured row may run
// it are read from tui/sessionactions, the one declaration both dashboard
// surfaces already read. WHEN an action runs, whether it needs a profile or a
// confirmation first, whether the row it was aimed at is still that row, and
// where a cancellation lands are decided by that package's controller. What is
// left here is what a choice DOES in this program, which cannot leave because
// every closure reads this model's captures, its selection, and its seams.
//
// Two things make this half different from the dashboard's, and both follow from
// the overview being a standalone program rather than a mode:
//
//   - The effects are INJECTED. Every action reaches the world through the
//     deps.action seam rather than by calling commands directly, exactly as the
//     topology read and the focus already do, so the package's acceptance
//     sequences can park an action and deliver its completion with no terminal, no
//     daemon, and no clock.
//   - Four actions SETTLE AT DISPATCH. Focus, peek, and the two open-somewhere
//     actions report back with messages that carry no request identity — they
//     predate the controller and are consumed by three other surfaces — so a
//     dispatch that waited for a correlated completion would leak an in-flight
//     record that nothing could ever close. They are completed against the model
//     the controller hands the effect, which is legal precisely because the
//     controller writes the in-flight record and publishes the state BEFORE it
//     runs the effect; their own messages then decide only where the operator
//     ends up.

package overview

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/launch"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// hostTabConsequence is appended to a destructive confirmation aimed at the tab
// this overview is itself running in.
//
// The action is still offered, for the reason the dashboard offers it: an
// operator restarting the session they raised the overview beside is an ordinary
// thing to do, and refusing would be the surface protecting itself rather than
// the operator. What they are owed is the sentence, before the keystroke.
const hostTabConsequence = "This overview is running in that tab and closes with it."

// reportWindow and reportPendingWindow are how long the feedback row keeps the
// last thing that happened and how long it keeps an operation that has not
// reported back.
//
// They restate the dashboard's toast lifetimes rather than importing them: those
// constants are unexported in the root package, which this one may not import,
// so no test can assert the two agree. The numbers are the same on purpose and
// the divergence they could develop is cosmetic — a sentence that lingers a
// second longer on one surface than the other.
const (
	reportWindow        = 4 * time.Second
	reportPendingWindow = 10 * time.Second
)

// actionSurface is which modal, if any, is over the grid.
//
// It is one value rather than a set of booleans because the surfaces are
// mutually exclusive by construction — the controller has at most one pending
// action, and each stage of it raises exactly one — and because the key router
// reads it as a switch: a second boolean would be a state in which two of them
// could both be true and the router would have to pick.
type actionSurface int

const (
	// surfaceGrid is no overlay: the grid's own keys apply.
	surfaceGrid actionSurface = iota
	// surfaceMenu is the per-row actions menu.
	surfaceMenu
	// surfaceProfile is the profile picker the two profile-taking actions raise.
	surfaceProfile
	// surfaceConfirm is the destructive confirmation.
	surfaceConfirm
)

// actionState is everything the action layer holds on the model.
//
// It is one field rather than a dozen so the grid's own state and the action
// layer's stay visibly separate: a handler that writes m.acts is doing something
// to an action, and one that does not is not.
type actionState struct {
	// controller is the shared controller's own state — the pending interaction
	// and the in-flight dispatches. It is written only through the State and
	// SetState seams below.
	controller sessionactions.State
	// recovery is the close-recovery stack. It lives beside the controller state
	// because a reservation is made during a dispatch and promoted by that
	// dispatch's completion.
	recovery sessionactions.Recovery

	surface actionSurface
	// target is the row the MENU was opened against. The profile picker and the
	// confirmation read their target from the controller's pending action
	// instead, because the controller is what captured it, and a second copy
	// here would be a second thing a refresh could be wrong about.
	target sessionactions.Target

	menuCursor, menuOffset int

	profileCursor  int
	profilePurpose launch.ConfigPickerPurpose

	confirmVerb        string
	confirmConsequence string

	// report is the last thing an action said about itself, and reportUntil is
	// when it stops being said. A surface with one feedback row cannot stack
	// messages, so the newest replaces the previous one and expires on its own.
	report      string
	reportUntil time.Time
	// reportSticky marks a failure: like a dashboard error toast it stays on the
	// row until something newer replaces it, because a failure that vanishes
	// after four seconds is easy to miss and impossible to read back.
	reportSticky bool
	// reportPending marks an in-flight sentence. Past its window it is not
	// blanked, which would read as the action having finished, but reworded as
	// still waiting until the outcome replaces it.
	reportPending bool
	// retryUntil is set only on the report of a FAILED relaunch, and holds the
	// deadline the released entry kept rather than the report's own expiry. It
	// is what makes that report the one ABOUT the recovery offer — taking its
	// place on the row instead of sitting under it — and what withdraws both the
	// retry it names and that precedence once `u` would no longer take the
	// entry. Set only through withRecoveryReport, which explains why.
	retryUntil time.Time
}

// actionEnv projects the live model onto the value every shared availability
// rule reads. It is the same projection the dashboard makes, so the two surfaces
// cannot answer "can this row run this action" from different facts.
func actionEnv(m model) sessionactions.Env {
	return sessionactions.Env{
		Config:        m.cfg,
		LastActivity:  m.lastActivity,
		IdleThreshold: m.idleThreshold,
	}
}

// actionHost is the tab and window this overview is running in.
//
// It is read from the refresh rather than from the startup source: the source is
// the tab the operator came FROM and is spent on the first snapshot, while the
// host is resolved on every read and is what a destructive action has to be
// warned about.
func actionHost(m model) sessionactions.Host {
	return sessionactions.Host{TabID: m.hostTabID, WindowID: m.hostWindowID}
}

// actionController assembles the controller for one call. It is built per call
// rather than stored because it is immutable — its one field is the seam set —
// and the state it reads and writes lives on the model.
func actionController(model) sessionactions.Controller[model] {
	return sessionactions.NewController(actionEffects())
}

// actionEffects is what each action DOES here, plus the seams the controller
// navigates and correlates through.
func actionEffects() sessionactions.Effects[model] {
	return sessionactions.Effects[model]{
		Env:     actionEnv,
		Live:    liveSessionForTarget,
		Dismiss: dismissActionSurfaces,
		Restore: restoreActionOrigin,
		Stamp: func(cmd tea.Cmd, request sessionactions.RequestID) tea.Cmd {
			return commands.StampRequest(cmd, uint64(request))
		},
		State:    func(m model) sessionactions.State { return m.acts.controller },
		SetState: func(m model, state sessionactions.State) model { m.acts.controller = state; return m },
		Run:      actionRunners(),
	}
}

// actionRunners is the effect table, derived from the menu declaration rather
// than written out beside it.
//
// Deriving it is what makes "the overview offers every menu action" true by
// construction: an action added to the shared vocabulary gets an effect here
// without an edit, and the only entries spelled by hand are the two that do
// something this model has to arrange — focus, which is the grid's own
// single-flight path, and close, which reserves a recovery before it dispatches.
func actionRunners() map[commands.ID]sessionactions.Effect[model] {
	definitions := sessionactions.MenuDefinitions()
	runners := make(map[commands.ID]sessionactions.Effect[model], len(definitions))
	for _, definition := range definitions {
		switch definition.ID {
		case commands.FocusSession:
			runners[definition.ID] = runFocusAction
		case commands.CloseSession:
			runners[definition.ID] = runCloseAction
		default:
			runners[definition.ID] = runSeamAction
		}
	}
	return runners
}

// runSeamAction is every action that is simply dispatched through the seam.
func runSeamAction(m model, d sessionactions.Dispatch) (model, tea.Cmd) {
	var cmd tea.Cmd
	if m.deps.action != nil {
		cmd = m.deps.action(d)
	}
	if locallySettled(d.Action) {
		return m.settleLocally(d), cmd
	}
	return m.beginActionReport(d), cmd
}

// runFocusAction routes the menu's Open entry through the grid's own focus path,
// so Enter on a row and Open in its menu are the same single-flighted action
// with the same exit-on-success contract.
func runFocusAction(m model, d sessionactions.Dispatch) (model, tea.Cmd) {
	return m.settleLocally(d).focusCapturedTab(d.Target.TabID())
}

// runCloseAction reserves the recovery before it dispatches.
//
// The reservation is not yet an offer: it captures what a relaunch would need
// while the row still exists, and applyCloseOutcome promotes it only once the
// close is confirmed. Capturing later is not possible — the row is by then
// deliberately gone — and offering earlier would advertise an undo for closes
// that failed.
func runCloseAction(m model, d sessionactions.Dispatch) (model, tea.Cmd) {
	if snapshot, ok := m.recoverySnapshot(d.Target.Session); ok {
		m.acts.recovery = m.acts.recovery.Reserve(d.Request, snapshot)
	}
	var cmd tea.Cmd
	if m.deps.action != nil {
		cmd = m.deps.action(d)
	}
	return m.beginActionReport(d), cmd
}

// locallySettled names the four actions whose completion messages carry no
// request identity, so their dispatch is completed at once rather than waiting
// for a correlation that can never arrive. Their messages still land — they
// decide where the operator ends up — but they settle no controller state.
func locallySettled(action commands.ID) bool {
	switch action {
	case commands.FocusSession, commands.PeekScrollback, commands.OpenDirectoryID, commands.OpenEditorID:
		return true
	}
	return false
}

// settleLocally closes a dispatch's in-flight record immediately. It is safe on
// the model the controller hands an effect because the record and the published
// state are both written before the effect runs.
func (m model) settleLocally(d sessionactions.Dispatch) model {
	updated, _, correlated := actionController(m).Complete(m, d.Request, sessionactions.Completion{})
	if !correlated {
		return m
	}
	return updated
}

// liveSessionForTarget resolves the captured target against the list as the
// overview currently sees it. The window is matched before the tab because it is
// the more specific identity; the tab fallback finds a row whose window the
// daemon has not attributed yet.
func liveSessionForTarget(m model, target sessionactions.Target) (session.RunningSession, bool) {
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

// dismissActionSurfaces drops every overlay and the transient state behind it.
// The controller runs it before an effect, so an effect that leaves for a
// destination does so over a settled grid.
func dismissActionSurfaces(m model) model {
	m.acts.surface = surfaceGrid
	m.acts.menuCursor, m.acts.menuOffset = 0, 0
	m.acts.profileCursor = 0
	m.acts.confirmVerb, m.acts.confirmConsequence = "", ""
	return m
}

// restoreActionOrigin puts the operator back in the surface they chose the
// cancelled action from, with the selection they chose it with. The overview has
// one such surface — the menu — so a cancellation from anywhere else lands on
// the grid.
func restoreActionOrigin(m model, pending sessionactions.Pending) model {
	m.acts.confirmVerb, m.acts.confirmConsequence = "", ""
	if pending.Origin.Surface != sessionactions.SurfaceMenu {
		m.acts.surface = surfaceGrid
		return m
	}
	m.acts.surface = surfaceMenu
	m.acts.target = pending.Target
	m.acts.menuCursor = pending.Origin.MenuCursor
	m.acts.menuOffset = pending.Origin.MenuOffset
	m.clampMenuSelection()
	return m
}

// menuOrigin is where the menu is, as an origin to come back to.
func menuOrigin(m model) sessionactions.Origin {
	return sessionactions.Origin{
		Surface:    sessionactions.SurfaceMenu,
		MenuCursor: m.acts.menuCursor,
		MenuOffset: m.acts.menuOffset,
	}
}

// runAction is the one entry point a key handler dispatches through.
func (m model) runAction(action commands.ID, target sessionactions.Target, origin sessionactions.Origin) (model, tea.Cmd) {
	updated, step, cmd := actionController(m).Begin(m, action, target, origin)
	return updated.applyActionStep(step, cmd)
}

// applyActionStep is what this surface does about one controller decision. The
// controller has already settled its own state and run whatever navigation seam
// applied; what is left is the overlay each step raises.
func (m model) applyActionStep(step sessionactions.Step, cmd tea.Cmd) (model, tea.Cmd) {
	switch step.Kind {
	case sessionactions.StepProfile:
		m.openProfilePicker(step)
	case sessionactions.StepConfirm:
		m.acts.surface = surfaceConfirm
		m.acts.confirmVerb = step.Prompt.Verb
		m.acts.confirmConsequence = m.confirmConsequenceFor(step)
	case sessionactions.StepRefused:
		if step.Detail != "" {
			m = m.withReport(step.Detail, reportWindow)
		}
	}
	return m, cmd
}

// confirmConsequenceFor is the prompt's second line: what the shared vocabulary
// says the action does, plus the warning that this overview is standing in the
// tab it would do it to.
func (m model) confirmConsequenceFor(step sessionactions.Step) string {
	consequence := step.Prompt.Consequence
	if actionHost(m).Hosts(step.Target) {
		if consequence != "" {
			consequence += " "
		}
		consequence += hostTabConsequence
	}
	return consequence
}
