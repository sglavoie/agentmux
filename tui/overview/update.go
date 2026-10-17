// What a message and a keypress do.
//
// Update is a dispatch table and nothing more: each case hands off to the
// handler that owns the state it touches, so the set of messages this surface
// understands is readable in one screen, which is the point of a standalone
// overview — the dashboard's loop consumes several dozen.
//
// The keymap itself is declared in bindings.go, which the footer and the help
// overlay project the same table from; what is here is only the routing. It
// separates navigation, presentation, focus, and dismissal:
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
//     A left click on a tile is the same thing said with the pointer, and the
//     wheel is PgUp and PgDn.
//   - DISMISSAL — esc, q, ctrl+c — leaves. It leaves while a refresh is out and
//     while a focus is pending, because an operator who wants out of an overlay
//     is not asking to wait for a subprocess first.

package overview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/theme"
)

// Update routes one message.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		theme.ObserveBackground(msg.IsDark())
		return m, nil
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
	case commands.AttachDoneMsg:
		return m.handleAttachDone(msg)
	case recoveryDoneMsg:
		return m.handleRecoveryDone(msg)
	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)
	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg)
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
// The overlay checks come FIRST and the grid's own table is only reached when
// nothing is over it. That ordering is the whole safety property of this layer
// rather than a tidiness preference: `c` is the grid's compact toggle and also
// the menu's mnemonic for restarting under another profile, and the digits that
// focus a tile are ordinary characters an overlay may want. A router that tried
// the grid first would answer both from underneath the overlay, which is an
// operator acting on a surface they cannot see.
//
// Help is checked before the action surfaces even though the two can never both
// be up — `?` is a grid binding, so nothing can open help from inside a modal —
// because reading the router top-down should name the surfaces in the order they
// stack rather than leave that fact to be inferred from the keymap.
func (m model) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.help.open {
		return m.handleHelpKey(msg)
	}
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

// handleGridKey runs whichever binding claims the key, and nothing when none
// does.
//
// The lookup is a walk of gridBindings rather than a switch, which is the whole
// point of the table: the keys this answers and the keys the footer and the help
// overlay print are read from the same declaration, so the three cannot drift.
// Keys are unique across the table — TestNoKeyIsBoundTwice is what keeps them
// that way — so the walk order decides nothing.
func (m model) handleGridKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	for _, b := range gridBindings() {
		for _, bound := range b.keys {
			if bound == key {
				return b.run(m, key)
			}
		}
	}
	return m, nil
}

// gridInFront reports that nothing is over the grid, which is when the pointer
// is talking to it. View reads it to decide whether the terminal reports the
// mouse at all, and the two mouse handlers read it again because an event
// already on its way when a modal opened still arrives.
func (m model) gridInFront() bool {
	return !m.help.open && m.acts.surface == surfaceGrid
}

// handleMouseClick is the pointer's spelling of a digit: a left click on a tile
// selects that session and focuses it, through focusIndex and so through the
// same captured-target, single-flight path. A click anywhere else — the chrome,
// an empty slot, the rows under a compact grid — does nothing.
//
// A shifted click is left alone, as it is on the dashboard: shift is how a
// terminal is asked to select text rather than report the mouse.
func (m model) handleMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if !m.gridInFront() || mouse.Button != tea.MouseLeft || mouse.Mod&tea.ModShift != 0 {
		return m, nil
	}
	g := MeasureMode(m.width, m.height, m.compact)
	pos, ok := g.TileAt(mouse.X, mouse.Y)
	if !ok {
		return m, nil
	}
	return m.focusIndex(g.PageStart(g.PageOf(m.selected)) + pos)
}

// handleMouseWheel turns the page, and only the page: it is PgUp and PgDn when
// there is another page to show and nothing when there is not, so a wheel over
// a grid that already shows everything does not move the cursor to its end.
func (m model) handleMouseWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if !m.gridInFront() || mouse.Mod&tea.ModShift != 0 {
		return m, nil
	}
	var move Move
	switch mouse.Button {
	case tea.MouseWheelUp:
		move = MovePageUp
	case tea.MouseWheelDown:
		move = MovePageDown
	default:
		return m, nil
	}
	g := MeasureMode(m.width, m.height, m.compact)
	if g.PageOf(Navigate(g, len(m.sessions), m.selected, move)) == g.PageOf(m.selected) {
		return m, nil
	}
	return m.applyMove(move)
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
// handleAttachDone settles an attach to a portable session. The attached view
// ran in this terminal and has now given it back, so unlike a Kitty focus
// there is nowhere else the operator went: the overview stays up, with any
// failure on the feedback row.
func (m model) handleAttachDone(msg commands.AttachDoneMsg) (tea.Model, tea.Cmd) {
	m.focusTabID = 0
	m.focusErr = msg.Err
	// An overview that is a popup over the terminal it just switched is in
	// the way of the session it was asked to show.
	if msg.Switched {
		return m.dismiss()
	}
	return m, nil
}

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
