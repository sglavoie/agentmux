// This file is the dashboard inspector's display state: what the operator has
// asked for, which pane the keyboard is aimed at, and the floors a split has to
// clear before the terminal can hold one.
//
// The separation between INTENT and FIT is the whole design. m.inspectorDisplay
// records what the operator wants — the automatic default, an explicit hide, or
// a full-screen inspector — and never changes because the terminal did.
// Everything about whether a pane is actually on screen is re-derived from the
// current width and height on every frame. A resize therefore cannot destroy a
// preference: shrinking past the floors takes the split away and widening back
// restores it, with the selection, the scroll scope, and the read target all
// exactly where they were, because none of them were touched.
//
// The predecessor stored "preview is on" as a single bool and turned it off
// from the resize handler. That conflated the two questions, so a terminal that
// briefly got narrow answered "the operator does not want an inspector" and the
// pane stayed gone after the width came back.

package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

// inspectorDisplay is the operator's standing intent for the inspector pane.
type inspectorDisplay int

const (
	// inspectorAutomatic is the default and the zero value: show the inspector
	// beside the queue whenever both panes clear their floors, and show the
	// queue alone when they do not. Falling out of a split is not a decision, so
	// it is not recorded as one.
	inspectorAutomatic inspectorDisplay = iota
	// inspectorHidden is an explicit refusal. It survives every resize, which is
	// the point: an operator who pressed i to get the columns back does not want
	// a widening to undo it.
	inspectorHidden
	// inspectorFullScreen is the inspector over the whole body, with no queue
	// column. It is only ever entered deliberately — a shrinking terminal drops
	// to the queue alone rather than promoting the inspector over it, because
	// losing sight of the session list is not a reasonable answer to a resize
	// nobody asked the dashboard about.
	inspectorFullScreen
)

// paneFocus names which of the dashboard's two panes the keyboard is aimed at.
// The project switcher is deliberately not a third value here: it is a modal
// with its own mode and its own key handler, because it is a filter control
// rather than a body pane and Tab cycling through it would put a scope picker
// between the queue and the output the operator is reading.
type paneFocus int

const (
	// focusQueue is the resting state and the zero value: keys move the cursor
	// through the session list.
	focusQueue paneFocus = iota
	// focusInspector aims the navigation keys at the output viewport instead.
	focusInspector
)

const (
	// inspectorMinQueueCols is the narrowest the session column may be drawn at
	// while an inspector shares the row with it.
	inspectorMinQueueCols = 48
	// inspectorMinCols is the same floor for the inspector's own column: below
	// it the metadata block wraps into unreadability and the output is too
	// narrow to carry a harness's own line.
	inspectorMinCols = 40
	// inspectorDividerCols is the single column the vertical rule between the
	// two panes costs.
	inspectorDividerCols = 1
	// inspectorMinBodyRows is the floor on the body rows the split needs. Both
	// panes are drawn to the same height, and the inspector spends
	// a compact metadata header and divider before a single output line, so
	// anything shorter buys a queue of three rows beside an inspector showing
	// nothing.
	inspectorMinBodyRows = 10
)

// inspectorConfigured reports whether an inspector is possible at all. A model
// with no configuration cannot color a profile, resolve a harness icon, or name
// a project, so it draws no inspector rather than an empty one.
func (m Model) inspectorConfigured() bool { return m.cfg != nil }

// inspectorRequested reports whether the operator's intent admits an inspector,
// before any question of whether one fits.
func (m Model) inspectorRequested() bool {
	return m.inspectorConfigured() && m.inspectorDisplay != inspectorHidden
}

// inspectorFullScreenActive reports the full-screen inspector. It reads intent
// alone: full screen needs no split floors, so the only terminal that refuses it
// is one with no geometry yet.
func (m Model) inspectorFullScreenActive() bool {
	return m.inspectorConfigured() && m.inspectorDisplay == inspectorFullScreen
}

// inspectorSplitRequested reports the automatic intent, which is the only one a
// side-by-side split is drawn under.
func (m Model) inspectorSplitRequested() bool {
	return m.inspectorConfigured() && m.inspectorDisplay == inspectorAutomatic
}

// inspectorActive reports whether the inspector is on screen right now, which is
// what the read controller polls against. It is the conjunction of intent and
// geometry and is the authoritative answer; listInspectorShown is the footer's
// cheaper, width-only reading of the same question and says why it has to be.
func (m Model) inspectorActive() bool {
	if !m.inspectorRequested() {
		return false
	}
	if m.inspectorDisplay == inspectorFullScreen {
		return true
	}
	return inspectorSplitFits(m)
}

// listInspectorShown is inspectorActive's width-only twin, and it exists so the
// footer and the list header can ask whether there is an inspector without
// asking themselves.
//
// The recursion it avoids is real and immediate. The footer's height is a term
// in the body-row budget, the body-row budget is the height half of
// inspectorSplitFits, so a footer token whose visibility called inspectorActive
// would run footer -> rows -> footer without bottom. Width has no such cycle:
// dashboardSplitWidths reads m.width and the configured ratio and nothing that
// is measured in lines.
//
// The two answers differ only in the sliver where a terminal is wide enough for
// a split but too short for one. There the footer offers `i: hide inspector` and
// tab reads `switch pane` against a queue drawn alone — one wrong word on a
// terminal that is about ten rows tall, which is the price of a footer that
// terminates.
func listInspectorShown(m Model) bool {
	if !m.inspectorRequested() {
		return false
	}
	if m.inspectorDisplay == inspectorFullScreen {
		return true
	}
	_, _, ok := dashboardSplitWidths(m)
	return ok
}

// listBothPanesShown reports that the queue and the inspector are on screen
// together, which is the only arrangement Tab has a move to make in. It is the
// footer's reading of what focusPane decides, and it carries listInspectorShown's
// width-only approximation for the same reason that function exists: the footer's
// own height is a term in the split's row budget, so it may not ask a question
// measured in rows.
//
// The full-screen half needs no approximation. It is intent alone, so the footer
// can answer it exactly: a full-screen inspector is one pane no matter how many
// rows are left.
func listBothPanesShown(m Model) bool {
	return listInspectorShown(m) && !m.inspectorFullScreenActive()
}

// inspectorSplitFits reports whether both panes clear their floors at the
// current terminal size. The width half is dashboardSplitWidths; the height half
// is measured here because dashboardSplitWidths is deliberately width-only.
func inspectorSplitFits(m Model) bool {
	if _, _, ok := dashboardSplitWidths(m); !ok {
		return false
	}
	return dashboardInspectorBodyRows(m) >= inspectorMinBodyRows
}

// dashboardInspectorBodyRows is the body-row budget the split is judged against.
//
// It is measured against the dashboard's own chrome regardless of which view or
// overlay currently owns the terminal. That normalization is what keeps the
// inspector's state intact through the actions overlay, the help box, the
// command palette, a confirmation, and the other root views: the pane is not
// torn down and re-read merely because a taller header was drawn over it for a
// moment.
func dashboardInspectorBodyRows(m Model) int {
	m.mode = ModeList
	return dashboardChromeFor(m).paneRows(m.height)
}

// inspectorOutputRows is the number of output lines the inspector's viewport can
// show at the current geometry — the pane's rows less the measured metadata block
// and its divider. Zero means there is no viewport to scroll.
func (m Model) inspectorOutputRows() int {
	g := dashboardGeometry(m)
	if !g.split {
		return 0
	}
	rows := g.previewHeight - dashboard.InspectorChromeHeight(inspectorInputFor(m, g.previewWidth, g.previewHeight))
	if rows < 0 {
		return 0
	}
	return rows
}

// toggleInspector is the `i` key: it shows the inspector when none is on screen
// and hides it when one is.
//
// Opening is where the floors are consulted, and they choose the FORM rather
// than granting or refusing. A terminal wide enough gets the split; one that is
// not gets the inspector full-screen, because the operator asked to read a
// session's output and "your terminal is too narrow" is not an answer when the
// whole body could carry it. This is the only path into full screen — nothing
// derived from a resize may take it.
//
// Opening also takes the focus. The keypress was a request to read output, and a
// pane that appeared but ignored j/k would need a second keystroke to become
// what it was opened for.
func (m Model) toggleInspector() (tea.Model, tea.Cmd) {
	if !m.inspectorConfigured() {
		return m, nil
	}
	if m.inspectorActive() {
		m.inspectorDisplay = inspectorHidden
		m.paneFocus = focusQueue
		m.retargetPreview(0)
		scrollListToCursor(&m)
		return m, nil
	}
	if inspectorSplitFitsWithDisplay(m, inspectorAutomatic) {
		m.inspectorDisplay = inspectorAutomatic
	} else {
		m.inspectorDisplay = inspectorFullScreen
	}
	m.paneFocus = focusInspector
	return m.showInspector()
}

// inspectorSplitFitsWithDisplay asks the fit question for a candidate intent
// rather than the current one. Opening from hidden has to know whether the split
// would fit BEFORE it commits to a display state, and the fit is measured
// through geometry that reads that very state.
func inspectorSplitFitsWithDisplay(m Model, display inspectorDisplay) bool {
	m.inspectorDisplay = display
	return inspectorSplitFits(m)
}

// showInspector points the pane at the current selection and starts reading.
// Both halves go through the read controller: the read may coalesce behind one
// still in flight from before the pane was hidden, and the chain bump retires any
// tick the previous chain left over, so a hide/show cycle faster than the refresh
// interval cannot end with two chains polling.
func (m Model) showInspector() (tea.Model, tea.Cmd) {
	m.retargetPreview(m.currentPreviewWindowID())
	m.clearPreviewNotice()
	scrollListToCursor(&m)
	read := m.requestPreviewRead()
	return m, tea.Batch(read, m.startPreviewPoll())
}

// queueVisible reports whether the session list is drawn at all. It is the
// mirror of inspectorActive, and the full-screen inspector is the one
// arrangement that makes it false: that form spends the whole body on the
// inspector and draws no queue column, which is also why renderSplitFrame
// publishes no clickable list region under it.
func (m Model) queueVisible() bool {
	return !m.inspectorFullScreenActive()
}

// focusPane moves the keyboard between the two body panes, and refuses to aim
// it at a pane that is not on screen. Both refusals are the same rule read from
// its two ends: there is no inspector to focus on a queue-only dashboard, and
// no queue to focus under a full-screen inspector. Either one would strand the
// keys in a pane the operator cannot see — the movement keys would answer, but
// against a list or a viewport that is not being drawn, so the press would look
// like it did nothing while it moved something out of sight.
func (m *Model) focusPane(target paneFocus) bool {
	if target == m.paneFocus {
		return false
	}
	if target == focusInspector && !m.inspectorActive() {
		return false
	}
	if target == focusQueue && !m.queueVisible() {
		return false
	}
	m.paneFocus = target
	return true
}

// toggleInspectorFocus is Tab/Shift+Tab when an inspector is on screen: the two
// panes are a cycle of length two, so both directions are the same move. It
// reports whether it moved the keyboard — nothing branches on that answer now
// that the filter-chip ring Tab used to fall through to is retired, but a focus
// move that silently declined and a focus move that happened are different
// enough that the caller is told which it got.
//
// It asks inspectorFocused rather than the raw bit so that a Tab pressed on a
// terminal too narrow to split declines instead of quietly rewriting the focus
// the operator set before it shrank.
//
// A cycle of length two over ONE visible pane is a cycle of length one, and
// focusPane is what makes that true in both directions. A full-screen inspector
// reaches the first branch with a live focus and asks for the queue, which is
// not drawn: the move is declined and the keys stay on the pane the operator is
// reading, rather than moving a hidden cursor through a list nobody can see.
func (m *Model) toggleInspectorFocus() bool {
	if m.inspectorFocused() {
		return m.focusPane(focusQueue)
	}
	return m.focusPane(focusInspector)
}

// inspectorFocused reports that the navigation keys belong to the output
// viewport. It re-checks the pane is actually there: a resize can take the split
// away while the focus bit still says inspector, and the keys have to come back
// to the queue rather than disappearing into a pane nobody can see. The bit
// itself is left alone so a widening restores the focus along with the pane.
func (m Model) inspectorFocused() bool {
	return m.paneFocus == focusInspector && m.inspectorActive()
}

// handleInspectorKey routes the movement keys to the output viewport while the
// inspector holds the focus, and reports whether it claimed the keystroke.
//
// It claims movement and nothing else. Every other dashboard key — the palette,
// help, the launcher, quit, the actions overlay — keeps working with the
// inspector focused, because focus here means "where does j/k go", not "which
// commands exist". `n`/`N` are deliberately not claimed: they move the QUEUE
// cursor from either pane, which is what "preserving pane focus" means for an
// operator reading output who wants the next waiting session in front of them.
//
// The set is exactly the set the queue answers, which is the whole point of
// there being a focused pane at all: one vocabulary, aimed by Tab, including
// the navigation aliases used in History, Plans, and Worktrees.
func (m Model) handleInspectorKey(key string) (tea.Model, tea.Cmd, bool) {
	rows := m.inspectorOutputRows()
	latest := dashboard.SplitOutput(m.previewText)
	switch key {
	case keyJ, keyDown:
		m.inspectorViewport = m.inspectorViewport.ScrollDown(rows, 1)
	case keyK, keyUp:
		m.inspectorViewport = m.inspectorViewport.ScrollUp(latest, rows, 1)
	case keyPgDown, keyCtrlF:
		m.inspectorViewport = m.inspectorViewport.ScrollDown(rows, fullPageStep(rows))
	case keyPgUp, keyCtrlB:
		m.inspectorViewport = m.inspectorViewport.ScrollUp(latest, rows, fullPageStep(rows))
	case keyCtrlD:
		m.inspectorViewport = m.inspectorViewport.ScrollDown(rows, max(1, rows/2))
	case keyCtrlU:
		m.inspectorViewport = m.inspectorViewport.ScrollUp(latest, rows, max(1, rows/2))
	case keyHome, keyG:
		m.inspectorViewport = m.inspectorViewport.Top(latest, rows)
	case keyEnd, keyGG:
		m.inspectorViewport = m.inspectorViewport.Resume()
	default:
		return m, nil, false
	}
	return m, nil, true
}

// scrollInspectorBy moves the output viewport by a signed line delta — the
// wheel's spelling of the scroll keys. It deliberately does NOT take the focus:
// the wheel acts where the pointer is, and stealing the keyboard from the queue
// because the operator brushed the wheel over the other pane would be a second
// effect they did not ask for.
func (m Model) scrollInspectorBy(delta int) Model {
	rows := m.inspectorOutputRows()
	switch {
	case delta < 0:
		m.inspectorViewport = m.inspectorViewport.ScrollUp(dashboard.SplitOutput(m.previewText), rows, -delta)
	case delta > 0:
		m.inspectorViewport = m.inspectorViewport.ScrollDown(rows, delta)
	}
	return m
}

// escapeInspectorLayer peels one layer of inspector state and reports whether it
// found one. It is the middle rung of the dashboard's escape ladder: overlays
// have already had their turn by the time this is asked, and clearing the
// filters is what happens when this declines.
//
// Leaving full screen returns to the automatic intent rather than to hidden. The
// operator asked to read output and then asked to stop reading it; that is not a
// statement about whether the split should come back on a wider terminal, and
// automatic is the state that has made no such statement.
func (m *Model) escapeInspectorLayer() bool {
	if m.inspectorFullScreenActive() {
		m.inspectorDisplay = inspectorAutomatic
		m.paneFocus = focusQueue
		scrollListToCursor(m)
		return true
	}
	// inspectorFocused rather than the raw bit: a terminal too narrow to split
	// still carries the focus the operator set before it shrank, and Escape may
	// only peel a layer that is on screen. Consuming the keystroke to clear an
	// invisible bit would look like an Escape that did nothing, and would cost
	// the operator the press that was about to clear their filters.
	if m.inspectorFocused() {
		m.paneFocus = focusQueue
		return true
	}
	return false
}
