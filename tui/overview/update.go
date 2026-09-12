// What a message and a keypress do.
//
// Update is a dispatch table and nothing more: each case hands off to the
// handler that owns the state it touches, so the set of messages this surface
// understands is readable in one screen. There are five, which is the point of a
// standalone overview — the dashboard's loop consumes several dozen.
//
// The keymap divides into three kinds of key, and the division is a safety
// property rather than an organizing one:
//
//   - MOVEMENT keys change one integer. They start no command, touch no Kitty
//     window, and write no metadata anywhere. Every arrow, every hjkl, every
//     page and every end is in this group, which is what makes "browsing the
//     overview is observation" true by construction instead of by review.
//   - ENTER is the one key that acts. It captures the tab currently under the
//     cursor and dispatches the existing focus action against that captured tab
//     exactly once.
//   - DISMISSAL — esc, q, ctrl+c — leaves. It leaves while a refresh is out and
//     while a focus is pending, because an operator who wants out of an overlay
//     is not asking to wait for a subprocess first.
package overview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
)

// Update routes one message.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
	cmds := []tea.Cmd{m.deps.tick(m.interval)}
	if cmd := m.requestRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// handleKeyPress maps a key onto dismissal, focus, or a move.
func (m model) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		return m.dismiss()
	case "enter":
		return m.requestFocus()
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
	m.selectIndex(Navigate(Measure(m.width, m.height), len(m.sessions), m.selected, move))
	m.reconcileCaptures()
	return m, m.pumpCaptures()
}

// requestFocus is Enter: focus the selected tab, and leave only once Kitty has
// confirmed it.
//
// The tab is CAPTURED here rather than read again when the answer returns. A
// refresh landing during the round trip can reorder the list under the cursor,
// and an operator who pressed Enter on a pane is owed that pane rather than
// whichever one the index came to address.
//
// A second Enter while the first is pending is ignored, not queued. Focus is
// idempotent from Kitty's side, but a queued duplicate would still be a second
// command whose completion arrived after the program had already been told to
// quit on the first.
func (m model) requestFocus() (tea.Model, tea.Cmd) {
	if m.focusTabID != 0 {
		return m, nil
	}
	target, ok := m.selectedSession()
	if !ok {
		return m, nil
	}
	m.focusTabID = target.TabID
	m.focusErr = nil
	return m, m.deps.focus(target.TabID)
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
