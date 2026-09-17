// What a message and a keypress do.
//
// Update is a dispatch table and nothing more: each case hands off to the
// handler that owns the state it touches, so the set of messages this surface
// understands is readable in one screen. There are five, which is the point of a
// standalone overview — the dashboard's loop consumes several dozen.
//
// The keymap separates navigation, presentation, focus, and dismissal:
// c toggles presentation and saves only the daemon-lifetime view preference.
// The remaining division is a safety
// property rather than an organizing one:
//
//   - MOVEMENT keys change one integer. They start no command, touch no Kitty
//     window, and write no metadata anywhere. Every arrow, every hjkl, every
//     page and every end is in this group, which is what makes "browsing the
//     overview is observation" true by construction instead of by review.
//   - ENTER and DIGITS act on a session. Enter captures the tab currently under the
//     cursor and dispatches the existing focus action against that captured tab
//     exactly once; 1–0 select and focus one of the first ten cards overall.
//   - DISMISSAL — esc, q, ctrl+c — leaves. It leaves while a refresh is out and
//     while a focus is pending, because an operator who wants out of an overlay
//     is not asking to wait for a subprocess first.
package overview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// Update routes one message.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case commands.OverviewPreferencesMsg:
		return m.handlePreferences(msg)
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)
	case commands.TickMsg:
		return m.handleTick(msg)
	case commands.CaptureTickMsg:
		return m.handleCaptureTick(msg)
	case commands.SessionsMsg:
		return m.handleSessions(msg)
	case commands.CaptureOutputMsg:
		return m.handleCaptureDone(msg)
	case commands.ErrMsg:
		return m.handleRefreshErr(msg)
	case commands.FocusDoneMsg:
		return m.handleFocusDone(msg)
	case commands.ActionDoneMsg:
		return m.handleActionDone(msg)
	case commands.PickerOpenDoneMsg:
		return m.handlePickerOpenDone(msg)
	case commands.PeekDoneMsg:
		return m.handlePeekDone(msg)
	case recoveryDoneMsg:
		return m.handleRecoveryDone(msg)
	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	}
	return m, nil
}

// handleWindowSize records the new surface size and re-aims the captures at the
// tiles the new size produced.
//
// There is deliberately no clamp and no re-derivation of the SELECTION here. A
// resize changes how many tiles fit and therefore which page the cursor is on,
// but the page is computed from the selection on every frame, so the selection a
// resize must preserve is preserved by not being touched.
//
// The captures are the other half and cannot be left alone, because a resize
// changes three things at once: which windows are on the page, how wide a
// captured line is drawn, and how many lines fit. Reconciling here is what makes
// a drag of the terminal's corner refetch the tiles that grew, re-cut the ones
// that shrank, and drop the ones that fell off the page.
func (m model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width, m.height = msg.Width, msg.Height
	m.reconcileCaptures()
	return m, m.pumpCaptures()
}

// handleTick re-arms the ticker and asks the controller for a refresh.
//
// Re-arming unconditionally — including while a read is already out and while a
// focus is pending — is what keeps the cadence independent of whether any given
// interval produced a read. The controller decides whether this tick costs a
// command; the ticker only decides that there will be another tick.
func (m model) handleTick(commands.TickMsg) (tea.Model, tea.Cmd) {
	// The recovery stack expires by deadline on every read, so sweeping is not
	// what keeps the offer honest — it is what keeps the stack from carrying
	// entries nobody can be shown after a run of closes nobody undid.
	m.acts.recovery = m.acts.recovery.Sweep(m.deps.now())
	cmds := []tea.Cmd{m.deps.tick(m.interval)}
	if cmd := m.requestRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// handleKeyPress routes a key to whichever surface is in front.
//
// The modal check comes FIRST and the grid's own table is only reached when
// nothing is over it. That ordering is the whole safety property of this layer
// rather than a tidiness preference: `c` is the grid's compact toggle and also
// the menu's mnemonic for restarting under another profile, and the digits that
// focus a tile are ordinary characters an overlay may want. A router that tried
// the grid first would answer both from underneath the overlay, which is an
// operator acting on a surface they cannot see.
func (m model) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.acts.surface {
	case surfaceMenu:
		return m.handleMenuKey(msg)
	case surfaceProfile:
		return m.handleProfileKey(msg)
	case surfaceConfirm:
		return m.handleConfirmKey(msg)
	}
	return m.handleGridKey(msg)
}

// handleGridKey maps a key onto dismissal, focus, an action, an undo, or a move.
func (m model) handleGridKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		return m.dismiss()
	case "enter":
		return m.requestFocus()
	case "a":
		m.openActionsMenu()
		return m, nil
	case "u":
		return m, m.undoLastClose()
	case "c":
		return m.toggleCompact()
	case "n":
		return m.jumpNeedsYou(1)
	case "N":
		return m.jumpNeedsYou(-1)
	}
	for index := range len(focusDigits) {
		if msg.String() == focusDigits[index:index+1] {
			return m.focusIndex(index)
		}
	}
	if move, ok := moveFor(msg.String()); ok {
		return m.applyMove(move)
	}
	return m, nil
}

// dismiss leaves the surface.
//
// Stopping the scheduler is not housekeeping on the way out: a dismissal arrives
// with up to four reads outstanding and a queue behind them, and every one of
// those would otherwise start or complete against a program that is quitting.
// Stopping it drops the queue, declines the next tick, and makes every late reply
// release its slot and be ignored — which is the only shutdown available, since
// the Kitty client offers no cancellation and the reads that are already out are
// bounded by their own subprocess deadline rather than by anything here.
func (m model) dismiss() (tea.Model, tea.Cmd) {
	m.captures.stop()
	if m.preferenceSaving {
		m.quitAfterPreference = true
		return m, nil
	}
	return m, tea.Quit
}

// moveFor resolves a key to a navigation intent.
//
// The table is the whole binding layer: nav.go owns what a Move MEANS and this
// owns which key asks for one, so the movement vocabulary stays assertable
// without a terminal and the bindings stay changeable without touching the
// geometry.
func moveFor(key string) (Move, bool) {
	switch key {
	case "left", "h":
		return MoveLeft, true
	case "right", "l":
		return MoveRight, true
	case "up", "k":
		return MoveUp, true
	case "down", "j":
		return MoveDown, true
	case "tab":
		return MoveNext, true
	case "shift+tab":
		return MovePrev, true
	case "pgup":
		return MovePageUp, true
	case "pgdown":
		return MovePageDown, true
	case "home":
		return MoveFirst, true
	case "end":
		return MoveLast, true
	}
	return 0, false
}

// applyMove moves the cursor, re-resolves the identity it addresses, and re-aims
// the captures if the move changed the page.
//
// The identity has to be re-resolved rather than left alone, because it is what
// the NEXT refresh looks the cursor up by: a move that changed the index without
// updating the tab would be undone by the following read, which would find the
// old tab still present and put the cursor back.
//
// Reconciling on EVERY move rather than only on a page change is deliberate: the
// scheduler already answers "did anything change" by comparing targets, so paging
// is detected where it is defined instead of by a second page computation here
// that could disagree with the renderer's. A move within a page produces no new
// generation and no new read.
func (m model) applyMove(move Move) (tea.Model, tea.Cmd) {
	if len(m.sessions) == 0 {
		return m, nil
	}
	m.selectIndex(Navigate(MeasureMode(m.width, m.height, m.compact), len(m.sessions), m.selected, move))
	m.reconcileCaptures()
	return m, m.pumpCaptures()
}

// jumpNeedsYou uses the dashboard's attention predicate in the overview's tile
// order. It wraps across the whole list without focusing or acknowledging a pane.
func (m model) jumpNeedsYou(dir int) (tea.Model, tea.Cmd) {
	count := len(m.sessions)
	for step := 1; step <= count; step++ {
		index := (m.selected + dir*step + count) % count
		if !dashboard.NeedsYou(m.sessions[index], m.lastActivity, m.idleThreshold) {
			continue
		}
		if index == m.selected {
			return m, nil
		}
		m.selectIndex(index)
		m.reconcileCaptures()
		return m, m.pumpCaptures()
	}
	return m, nil
}

// focusIndex jumps to a numbered card, even on another page, then uses the same
// captured-target and single-flight focus path as Enter. Missing cards do nothing.
func (m model) focusIndex(index int) (tea.Model, tea.Cmd) {
	if m.focusTabID != 0 || index < 0 || index >= len(m.sessions) {
		return m, nil
	}
	m.selectIndex(index)
	m.reconcileCaptures()
	captureCmd := m.pumpCaptures()
	next, focusCmd := m.requestFocus()
	return next, tea.Batch(focusCmd, captureCmd)
}

// requestFocus handles Enter and digits: focus the selected tab, and leave only once Kitty has
// confirmed it.
//
// The tab is CAPTURED here rather than read again when the answer returns. A
// refresh landing during the round trip can reorder the list under the cursor,
// and an operator who pressed Enter on a pane is owed that pane rather than
// whichever one the index came to address.
//
// A second focus key while the first is pending is ignored, not queued. Focus is
// idempotent from Kitty's side, but a queued duplicate would still be a second
// command whose completion arrived after the program had already been told to
// quit on the first.
func (m model) requestFocus() (tea.Model, tea.Cmd) {
	target, ok := m.selectedSession()
	if !ok {
		return m, nil
	}
	next, cmd := m.focusCapturedTab(target.TabID)
	return next, cmd
}

// focusCapturedTab dispatches the focus against a tab that has already been
// captured. It is the single-flight gate itself, factored out of requestFocus so
// the menu's Open entry — which captured its tab when the menu opened, possibly
// several refreshes ago — reaches exactly the same path as Enter rather than a
// second one that could drift from it.
func (m model) focusCapturedTab(tabID int) (model, tea.Cmd) {
	if m.focusTabID != 0 || tabID == 0 {
		return m, nil
	}
	m.focusTabID = tabID
	m.focusErr = nil
	return m, m.deps.focus(tabID)
}

// handleFocusDone quits on success and reports the failure otherwise.
//
// Exiting ONLY on success is the contract: a focus that failed leaves the
// operator on the overview, with the reason on the feedback row and the same tab
// still selected, which is the only state from which retrying makes sense. A
// completion arriving with nothing pending is dropped — the surface already dealt
// with that attempt.
func (m model) handleFocusDone(msg commands.FocusDoneMsg) (tea.Model, tea.Cmd) {
	if m.focusTabID == 0 {
		return m, nil
	}
	m.focusTabID = 0
	if msg.Err != nil {
		m.focusErr = msg.Err
		return m, nil
	}
	return m.dismiss()
}
