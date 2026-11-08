package overview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

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
