package tui

import (
	"time"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// This file is the seam between the root Model and the tui/dashboard leaf.
// The leaf owns the dashboard domain — rows, filters, groups, pagination, and
// the completion popup — and reads its facts from explicit input values. The
// Model stays here, and every adapter below exists to turn "what the model
// currently holds" into "what the dashboard needs to answer a question".

// renderList renders the session list for the current model state at the
// model's terminal width, bounded by listVisibleHeight so the windowed walk
// honors the current viewport. Pre-WindowSizeMsg (height==0) the budget is
// unbounded so tests and the first paint render every row.
func renderList(m Model) string {
	if m.height == 0 {
		return renderListAt(m, m.width, 0)
	}
	return renderListAt(m, m.width, listVisibleHeight(m))
}

// renderListAt renders the session list with explicit width and height
// budgets. A non-positive height means "unbounded": every row in m.filtered is
// rendered and the ▲/▼ scroll indicators are suppressed. This preserves the
// historical behavior for callers (and tests) that do not yet care about
// windowing.
func renderListAt(m Model, width, height int) string {
	return dashboard.RenderList(dashboardListInput(m, width, height))
}

// dashboardListInput projects the model onto the renderer's input. The ASL
// marker lookup and the pulse predicate stay behind closures because both read
// model state the dashboard has no business knowing about.
func dashboardListInput(m Model, width, height int) dashboard.ListInput {
	// Fall back to the wall clock for a Model built as a bare literal, which is
	// how most of this package's render tests construct their input.
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	return dashboard.ListInput{
		Rows:          m.filtered,
		SessionCount:  len(m.sessions),
		Filter:        m.activeFilter,
		Cursor:        m.cursor,
		Offset:        m.listOffset,
		Width:         width,
		Height:        height,
		Now:           now,
		Cfg:           m.cfg,
		LastActivity:  m.lastActivity,
		IdleThreshold: m.idleThreshold,
		Watchdog:      m.watchdog,
		ActivePulse:   m.hasActivePulse,
		LoopMarker: func(s session.RunningSession) (string, bool) {
			return aslMarkerFor(m, s)
		},
	}
}

// dashboardScrollInput projects the model onto the pagination math. multiGroup
// is passed in rather than derived so the caller charges the same group chrome
// the renderer will actually draw.
func dashboardScrollInput(m *Model, multiGroup bool) dashboard.ScrollInput {
	return dashboard.ScrollInput{
		Rows:          m.filtered,
		LastActivity:  m.lastActivity,
		IdleThreshold: m.idleThreshold,
		MultiGroup:    multiGroup,
	}
}

// renderChips renders the filter chip row(s) for the current activeFilter,
// or "" when the filter is empty (so the area collapses with no blank line).
func renderChips(m Model, width int) string {
	return dashboard.RenderChips(m.activeFilter, dashboard.ProfileColorMap(m.cfg), m.focusedChipIdx, width)
}

// renderCompletion returns the floating menu lines drawn below the search
// input. Returns "" when the menu is hidden.
func renderCompletion(m Model) string {
	return dashboard.RenderCompletion(m.completion)
}

// recomputeCompletion looks at m.filterText at end-caret position and updates
// m.completion.
func (m *Model) recomputeCompletion() {
	m.recomputeCompletionFor(m.filterText)
}

// recomputeCompletionFor is the buffer-agnostic core used by the ModeSearch
// input, which recomputes against a candidate buffer before committing it.
func (m *Model) recomputeCompletionFor(text string) {
	m.completion.Recompute(text, m.cfg)
}

// cycleCompletion moves the popup selection by delta with wrap-around.
func (m *Model) cycleCompletion(delta int) {
	m.completion.Cycle(delta)
}

// insertCompletion replaces the active token in m.filterText with the
// selected candidate and closes the menu. Callers must reapply the filter
// after.
func (m *Model) insertCompletion() {
	m.insertCompletionInto(&m.filterText)
}

// insertCompletionInto is the buffer-agnostic core used by insertCompletion.
func (m *Model) insertCompletionInto(buf *string) {
	m.completion.Insert(buf)
}

// closeCompletion hides the menu without changing the filter text.
func (m *Model) closeCompletion() {
	m.completion = dashboard.Completion{}
}
