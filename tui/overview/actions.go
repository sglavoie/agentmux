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
//     sequences can park an action and deliver its completion with no Kitty, no
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
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
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

// undoWindowText is how the recovery window is spelled on the feedback row. It
// is derived from the window the shared stack enforces rather than typed beside
// it, so the offer cannot go on promising a number the stack stopped granting.
var undoWindowText = fmt.Sprintf("%ds", int(sessionactions.RecoveryWindow/time.Second))

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
	// reportOwnsOffer marks the one report that is ABOUT the recovery offer and
	// therefore takes its place on the row instead of sitting under it. Set only
	// through withRecoveryReport, which explains why.
	reportOwnsOffer bool
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

// openActionsMenu captures the row the menu will act on and puts the cursor on
// the entry the shared vocabulary says a fresh menu opens with.
func (m *model) openActionsMenu() {
	target, ok := m.selectedSession()
	if !ok {
		return
	}
	m.acts.target = sessionactions.Capture(target)
	m.acts.surface = surfaceMenu
	m.acts.menuOffset = 0
	m.acts.menuCursor = sessionactions.MenuIndexOf(sessionactions.InitialSelection())
	m.clampMenuSelection()
}

// closeActionsMenu leaves the menu without running anything.
func (m *model) closeActionsMenu() {
	m.acts.surface = surfaceGrid
	m.acts.menuCursor, m.acts.menuOffset = 0, 0
}

// menuActions is the ordered menu, in the shared vocabulary's band order. Every
// fact but the runner comes from tui/sessionactions; availability is re-derived
// against the LIVE model at render time rather than frozen when the menu opened.
//
// A definition with no runner is dropped rather than drawn as a dead row. That
// is a programming error rather than a reachable state, and
// TestOverviewMenuOffersEveryDefinition fails on it.
func menuActions(m model) []commands.Action[model] {
	runners := actionRunners()
	definitions := sessionactions.MenuDefinitions()
	list := make([]commands.Action[model], 0, len(definitions))
	for _, definition := range definitions {
		if _, ok := runners[definition.ID]; !ok {
			continue
		}
		action := definition.ID
		list = append(list, commands.Action[model]{
			ID:          definition.ID,
			Band:        definition.MenuGroup.String(),
			KeyDisplay:  definition.Mnemonic.Display,
			KeyMatch:    definition.Mnemonic.Key,
			Label:       definition.MenuLabel,
			Description: definition.MenuDescription,
			Availability: func(m model) commands.Availability {
				return definition.AvailabilityFor(actionEnv(m), m.acts.target)
			},
			Run: func(m model) (model, tea.Cmd) {
				return m.runAction(action, m.acts.target, menuOrigin(m))
			},
		})
	}
	return list
}

// overlayBodyHeight is the height a modal may occupy: the surface minus the
// three rows the chrome reserves. It is charged here rather than in the shared
// widget, which knows nothing about this surface's title, feedback, and hint
// rows.
func overlayBodyHeight(m model) int {
	return max(m.height-3, 1)
}

// menuInput projects the model onto the shared menu widget's input.
//
// The qualifier is measured across the WHOLE live set rather than the page,
// because a name collision is a property of the set: two rows called `api` offer
// the same twelve actions, and the menu header is the only place it says which
// of them is about to be closed.
func menuInput(m model) commands.ActionsInput[model] {
	return commands.ActionsInput[model]{
		Model:      m,
		Actions:    menuActions(m),
		Title:      dashboard.SessionDisplayTitle(m.acts.target.Session),
		Qualifier:  dashboard.NewNameDisambiguator(m.sessions).Qualifier(m.acts.target.Session),
		Context:    dashboard.SessionContextLine(m.acts.target.Session),
		Cursor:     m.acts.menuCursor,
		Offset:     m.acts.menuOffset,
		Width:      m.width,
		BodyHeight: overlayBodyHeight(m),
	}
}

// clampMenuSelection confines the cursor to the entries that exist and then
// scrolls the window to hold it. The two happen together, in that order, because
// a cursor the window has not been told about is a selection that has vanished:
// every path that moves the cursor or resizes the terminal ends here.
func (m *model) clampMenuSelection() {
	if m.acts.surface != surfaceMenu {
		m.acts.menuCursor, m.acts.menuOffset = 0, 0
		return
	}
	in := menuInput(*m)
	m.acts.menuCursor = in.ClampCursor()
	in.Cursor = m.acts.menuCursor
	m.acts.menuOffset = in.ScrollToCursor()
}

// profileAccounts is the configured profile list, or none when no configuration
// has been loaded.
func (m model) profileAccounts() []config.Account {
	if m.cfg == nil {
		return nil
	}
	return m.cfg.Accounts
}

// openProfilePicker raises the shared picker for the two actions that need a
// profile. The purpose is set from the action identity rather than carried on
// the step, because the controller does not know this program has a picker.
func (m *model) openProfilePicker(step sessionactions.Step) {
	m.acts.surface = surfaceProfile
	m.acts.profilePurpose = launch.ConfigPickerRestartProfile
	if step.Action == commands.LaunchAnother {
		m.acts.profilePurpose = launch.ConfigPickerDashboardLaunch
	}
	m.acts.profileCursor = launch.AccountIndex(m.profileAccounts(), step.Target.Session.EffectiveProfile())
}

// profileInput projects the model onto the shared picker's input. Its target is
// the controller's pending capture, which is the row the operator chose rather
// than whatever the cursor has since moved to.
func (m model) profileInput() launch.ProfileInput {
	target := m.acts.controller.Pending().Target
	return launch.ProfileInput{
		Accounts:       m.profileAccounts(),
		Cursor:         m.acts.profileCursor,
		Current:        target.Session.EffectiveProfile(),
		CurrentHarness: target.Session.Harness,
		Purpose:        m.acts.profilePurpose,
		Title:          dashboard.SessionDisplayTitle(target.Session),
		Width:          m.width,
		Cfg:            m.cfg,
	}
}

// confirmInput projects the model onto the shared prompt renderer.
func (m model) confirmInput() sessionactions.PromptInput {
	verb := m.acts.confirmVerb
	if text, ok := sessionactions.ConfirmVerbText(verb); ok {
		verb = text
	}
	return sessionactions.PromptInput{
		Verb:        verb,
		Subject:     dashboard.SessionDisplayTitle(m.acts.controller.Pending().Target.Session),
		Consequence: m.acts.confirmConsequence,
		Width:       m.width,
	}
}

// overlayView is the modal the grid is drawn under, or "" for no modal.
//
// Help is checked first for the reason handleKeyPress checks it first: it is the
// topmost surface, and naming it here keeps the render order and the routing
// order readable as the same stack.
func (m model) overlayView() string {
	if m.help.open {
		return m.renderHelpBox()
	}
	switch m.acts.surface {
	case surfaceMenu:
		return commands.RenderActionsBox(menuInput(m))
	case surfaceProfile:
		return m.profileInput().RenderBox()
	case surfaceConfirm:
		return sessionactions.RenderPromptBox(m.confirmInput())
	}
	return ""
}

// overlayHints is what the hint row says while a modal is up.
//
// Three of the four say nothing here because they say it inside their own box:
// the menu, the profile picker, and the keymap each end with their own key line.
// The confirmation does not, so its answer keys are the row — which is also the
// only place on this surface that says a destructive prompt can be backed out
// of.
func (m model) overlayHints() string {
	if !m.help.open && m.acts.surface == surfaceConfirm {
		return confirmHints()
	}
	return ""
}

// sessionLabel is what to call a target in a sentence.
func sessionLabel(target sessionactions.Target) string {
	return dashboard.SessionDisplayTitle(target.Session)
}

// withReport puts one sentence on the feedback row for a bounded time.
//
// It clears the focus error, which is the other thing that row reports about
// something the operator asked for. Exactly one of the two can be true at a
// time by construction: a new operation is newer news than a focus that failed
// before it, and leaving both would make the row's ranking decide which of two
// current facts is hidden.
func (m model) withReport(text string, ttl time.Duration) model {
	m.acts.report = text
	m.acts.reportUntil = m.deps.now().Add(ttl)
	m.acts.reportOwnsOffer = false
	m.focusErr = nil
	return m
}

// withRecoveryReport is withReport for the one sentence that must be shown
// INSTEAD of the recovery offer rather than beneath it.
//
// The offer normally outranks every report, because it is the only thing on that
// row which expires into something unrecoverable. A relaunch that FAILED is the
// exception, and it is the exception precisely because the stack releases the
// entry back onto the offer: without this, pressing `u` and having the launch
// fail would put the row back to the same "closed X · u undo" it showed before
// the keystroke, which is what a dropped keypress looks like. The failure and the
// retry are one sentence about one entry, so they take the row together.
func (m model) withRecoveryReport(text string, ttl time.Duration) model {
	m = m.withReport(text, ttl)
	m.acts.reportOwnsOffer = true
	return m
}

func (m model) clearReport() model {
	m.acts.report = ""
	m.acts.reportUntil = time.Time{}
	m.acts.reportOwnsOffer = false
	return m
}

// offerOwningReport is the recovery report, if one is current. It is the only
// report the feedback row places above the offer; see withRecoveryReport.
func (m model) offerOwningReport() string {
	if !m.acts.reportOwnsOffer {
		return ""
	}
	return m.activeReport()
}

// activeReport is the report if it has not expired. Expiry is evaluated on read
// rather than swept by a timer of its own: the interval and capture tickers
// already repaint this surface several times a second, so a third ticker would
// buy nothing but a third thing to stop on shutdown.
func (m model) activeReport() string {
	if m.acts.report == "" || !m.deps.now().Before(m.acts.reportUntil) {
		return ""
	}
	return m.acts.report
}

// beginActionReport says that a dispatched action is in flight, for the four
// actions the shared vocabulary has wording for. The rest announce nothing on
// the way out: focus, peek, copy, lock, mute and the two open-somewhere actions
// are their own feedback, and a sentence for each would be noise over a surface
// that shows the result.
func (m model) beginActionReport(d sessionactions.Dispatch) model {
	wording, ok := sessionactions.FeedbackFor(d.Action, sessionLabel(d.Target), d.Profile)
	if !ok {
		return m
	}
	return m.withReport(wording.Pending, reportPendingWindow)
}

// reportActionOutcome replaces the in-flight sentence with what became of it.
//
// An action with no wording normally says nothing, which is right for one that
// never announced itself — except when it FAILED. A failure nobody reports is
// the grid silently declining to do what was asked, so the menu's own label is
// borrowed to name it rather than adding a second wording table beside the
// shared one.
func (m model) reportActionOutcome(result sessionactions.Result) model {
	label := sessionLabel(result.Target)
	if wording, ok := sessionactions.FeedbackFor(result.Action, label, result.Profile); ok {
		if text, said := wording.Outcome(result.Outcome, result.Detail); said {
			return m.withReport(text, reportWindow)
		}
		return m.clearReport()
	}
	if result.Outcome != sessionactions.OutcomeFailed {
		return m.clearReport()
	}
	verb := string(result.Action)
	if definition, found := sessionactions.Lookup(result.Action); found && definition.MenuLabel != "" {
		verb = definition.MenuLabel
	}
	return m.withReport(
		sessionactions.FailureText(fmt.Sprintf("%s failed for %s", verb, label), result.Detail),
		reportWindow)
}

// handleActionDone settles one correlated completion and refreshes the topology,
// because most of these actions changed the window tree the grid is drawn from.
func (m model) handleActionDone(msg commands.ActionDoneMsg) (tea.Model, tea.Cmd) {
	updated, result, correlated := actionController(m).Complete(
		m,
		sessionactions.RequestID(msg.Request),
		sessionactions.Completion{Err: msg.Err, Unconfirmed: msg.Unconfirmed},
	)
	if !correlated {
		return m, nil
	}
	m = updated
	if result.Action == commands.CloseSession {
		m = m.applyCloseOutcome(result)
	} else {
		m = m.reportActionOutcome(result)
	}
	return m, m.requestRefresh()
}

// handlePickerOpenDone is the destination contract for the two open-somewhere
// actions: a shell or an editor that opened is where the operator now is, so the
// overview leaves. One that failed keeps them here with the reason, which is the
// only state from which retrying makes sense.
func (m model) handlePickerOpenDone(msg commands.PickerOpenDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m.withReport("open failed: "+msg.Err.Error(), reportWindow), nil
	}
	return m.dismiss()
}

// handlePeekDone stays put either way. The pager is an overlay over the session's
// own window and the operator returns from it to this grid.
func (m model) handlePeekDone(msg commands.PeekDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m.withReport("peek failed: "+msg.Err.Error(), reportWindow), nil
	}
	return m, nil
}

// actionCommand is the default effect seam: what each action identity actually
// runs against the real world.
//
// It is a package function over the configuration and the client rather than a
// method, because it is bound once in defaultDeps and must not be able to read
// the model — every fact it needs is on the dispatch, which is the capture the
// operator chose rather than whatever the cursor has since moved to.
func actionCommand(cfg *config.Config, client kitty.KittyClient, d sessionactions.Dispatch) tea.Cmd {
	target := d.Target.Session
	switch d.Action {
	case commands.PeekScrollback:
		return commands.Peek(client, d.Target.WindowID())
	case commands.RestartSession:
		return commands.Restart(client, target, cfg)
	case commands.OverrideProfile:
		return restartWithProfileCmd(cfg, client, target, d.Profile)
	case commands.LaunchAnother:
		return launchWithProfileCmd(cfg, client, target, d.Profile)
	case commands.ClearStatusIcon:
		return commands.ClearStatus(client, target)
	case commands.CloseSession:
		return commands.Close(client, d.Target.TabID())
	case commands.OpenDirectoryID:
		if cs, ok := commands.FindConfigSession(cfg, target.EffectiveProfile(), target.EffectiveProject()); ok {
			return commands.OpenDirectory(client, cfg, cs)
		}
		return nil
	case commands.OpenEditorID:
		if cs, ok := commands.FindConfigSession(cfg, target.EffectiveProfile(), target.EffectiveProject()); ok {
			return commands.OpenInEditor(client, cfg, cs)
		}
		return nil
	case commands.CopyTitleID:
		return commands.CopyTitle(dashboard.SessionDisplayTitle(target))
	case commands.ToggleLockID:
		return commands.ToggleLock(client, target)
	case commands.ToggleMuteID:
		return commands.ToggleMute(client, target)
	}
	return nil
}

// restartWithProfileCmd restarts the captured session under another profile.
//
// The registry call is recovered because it reaches a harness launcher this
// program does not own: a panic there would take the overview down over a
// session the operator can still see, and reporting it as a failed action leaves
// them on the grid with the reason.
func restartWithProfileCmd(cfg *config.Config, client kitty.KittyClient, rs session.RunningSession, profile string) tea.Cmd {
	template := commands.DashboardSessionTemplate(cfg, rs)
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = commands.ActionDoneMsg{Err: fmt.Errorf("restart panicked: %v", r)}
			}
		}()
		_, err := commands.Registry.Execute(
			context.Background(),
			actions.Dependencies{Kitty: client, Config: cfg},
			actions.Restart,
			actions.Request{
				Session:    &rs,
				Project:    &template,
				Parameters: map[string]string{"profile": profile},
				Confirmed:  true,
			},
		)
		return commands.ActionDoneMsg{Err: err}
	}
}

// launchWithProfileCmd launches a SECOND session for the captured row's project
// under the chosen profile, leaving the original running.
//
// It deliberately does not reuse the dashboard's launcher: that path drives a
// batch with progress reporting, a queue, and a launcher view, none of which
// exist here. One session, one completion, reported on the feedback row.
func launchWithProfileCmd(cfg *config.Config, client kitty.KittyClient, rs session.RunningSession, profile string) tea.Cmd {
	template := commands.SessionWithProfile(commands.DashboardSessionTemplate(cfg, rs), profile)
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = commands.ActionDoneMsg{Err: fmt.Errorf("launch panicked: %v", r)}
			}
		}()
		_, err := commands.Registry.Execute(
			context.Background(),
			actions.Dependencies{Kitty: client, Config: cfg},
			actions.Launch,
			actions.Request{Project: &template},
		)
		return commands.ActionDoneMsg{Err: err}
	}
}
