// This file is the seam between the root Bubble Tea model and the
// tui/dashboard leaf, plus the preview pane's own root-side plumbing: the tick
// that schedules a refresh, the command that fetches scrollback, and the fit
// test that charges the root's status-bar chrome.

package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/kitty"
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

func dashboardRailRows(m Model) []dashboard.RailRow {
	contextual := dashboard.ApplyFilter(m.sessions, m.activeFilter.WithoutProject(), m.lastActivity, m.idleThreshold)
	return dashboard.BuildRailRows(contextual, m.lastActivity, m.idleThreshold)
}

func renderProjectRail(m Model, width, height int) string {
	return dashboard.RenderRail(dashboard.RailInput{
		Rows: dashboardRailRows(m), Cursor: m.railCursor, Offset: m.railOffset,
		Width: width, Height: height, AppliedProject: m.activeFilter.Project,
		Focused: m.railFocused,
	})
}

// detailStripBudgetLines returns the number of lines reserved below the list
// for the compact band's selected-item strip: the whole strip when the list
// column is compact, and nothing otherwise.
//
// The band is read from the list column's width, not the terminal's, because
// the strip exists to carry the fields a compact ROW dropped — a wide terminal
// whose list column is squeezed by a side-by-side preview dropped them too.
// Pre-resize (width or height still zero) nothing is reserved: there is no
// geometry yet to be compact.
func detailStripBudgetLines(m Model) int {
	if m.width <= 0 || m.height <= 0 {
		return 0
	}
	if dashboard.BandFor(dashboardListPaneWidth(m)) != dashboard.BandCompact {
		return 0
	}
	return dashboard.DetailStripLines
}

// renderDetailStripBlock renders the strip detailStripBudgetLines reserved, or
// "" when it reserved nothing. It is drawn at the full terminal width below the
// list/preview area rather than inside the list column: it describes the
// selection, and a preview beside a compact list has no more claim on those two
// rows than the list does.
func renderDetailStripBlock(m Model) string {
	n := detailStripBudgetLines(m)
	if n == 0 {
		return ""
	}
	item, ok := m.selectedItem()
	lines := dashboard.RenderDetailStrip(dashboard.DetailStripInput{
		Width:         m.width,
		Items:         m.items,
		Item:          item,
		Selected:      ok,
		Cfg:           m.cfg,
		LastActivity:  m.lastActivity,
		IdleThreshold: m.idleThreshold,
		Watchdog:      m.watchdog,
		Now:           modelNow(m),
	})
	return strings.Join(lines[:n], "\n") + "\n"
}

// The dashboard cursor indexes items, not sessions. m.filtered stays the
// filtered and urgency-sorted source of truth; m.items is the flattened
// projection of it that the cursor and the scroll offset address, and every
// question about "the current row" is asked through the three accessors below
// rather than by indexing m.filtered directly. That is what lets a kind other
// than a session leaf appear in the list without any of the callers having to
// learn about it: a non-session selection simply has no session to give back,
// and the session-targeted paths already handle "no session".

// listItems returns the item projection the cursor and scroll offset index.
func (m Model) listItems() dashboard.ItemList {
	return m.items
}

// rebuildItems re-derives the item projection from the current filtered rows
// under the current view. Every write to m.filtered must be followed by one of
// these, which is what keeps the projection from describing a row set a
// refresh, a filter change, or a resort has already replaced — and every change
// of view or of collapse state is a rebuild for the same reason.
func (m *Model) rebuildItems() {
	m.items = dashboard.BuildItemsFor(m.listView, m.filtered, m.collapsedFamilies)
}

// listShowsGroupHeadings reports whether the list renderer will draw urgency
// group headings for the current projection. The scroll math charges the same
// chrome the renderer draws, so both ask the leaf rather than each deciding.
func listShowsGroupHeadings(m *Model) bool {
	return dashboard.ShowsGroupHeadings(m.listView, m.items, m.lastActivity, m.idleThreshold)
}

// listItemCount is the number of selectable lines in the dashboard list. This
// is the bound every cursor clamp and scroll calculation charges against — not
// len(m.filtered), which counts sessions and stops agreeing with the list the
// moment a line stands for more than one of them.
func (m Model) listItemCount() int {
	return m.items.Len()
}

// selectedItem returns the item under the cursor, reporting false for an empty
// list and for a transiently out-of-range cursor.
func (m Model) selectedItem() (dashboard.Item, bool) {
	return m.items.At(m.cursor)
}

// selectedSession returns the running session under the cursor. It reports
// false when the list is empty, when the cursor is out of range, and when the
// selected item is not a session leaf. Every row-targeted action, availability
// check, command-context capture, and preview refresh resolves its target
// through here, so a synthetic row cannot become the subject of a Kitty or
// daemon mutation.
func (m Model) selectedSession() (session.RunningSession, bool) {
	return m.items.SessionAt(m.cursor)
}

// hasSelectedSession reports whether a row-targeted action has a session to act
// on at all.
func (m Model) hasSelectedSession() bool {
	_, ok := m.selectedSession()
	return ok
}

// dashboardListInput projects the model onto the renderer's input. The ASL
// marker lookup and the pulse predicate stay behind closures because both read
// model state the dashboard has no business knowing about.
func dashboardListInput(m Model, width, height int) dashboard.ListInput {
	now := modelNow(m)
	return dashboard.ListInput{
		Items:         m.items,
		View:          m.listView,
		Loading:       topologyLoading(m),
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

// topologyLoading is true only until the first usable running-session snapshot
// lands. Periodic refreshes after that keep showing the last confirmed state.
func topologyLoading(m Model) bool {
	return m.topologyObservedAt.IsZero() && m.topologyRefreshInFlight
}

// dashboardScrollInput projects the model onto the pagination math.
// groupHeadings is passed in rather than derived so the caller charges the same
// group chrome the renderer will actually draw.
func dashboardScrollInput(m *Model, groupHeadings bool) dashboard.ScrollInput {
	return dashboard.ScrollInput{
		Items:         m.items,
		LastActivity:  m.lastActivity,
		IdleThreshold: m.idleThreshold,
		GroupHeadings: groupHeadings,
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
	m.completion.Recompute(text, m.cfg, m.sessions, m.lastActivity, m.idleThreshold)
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

// previewTickMsg is sent by the preview ticker while preview is enabled.
type previewTickMsg struct{}

// previewContentMsg carries the result of a scrollback fetch, stamped with the
// target generation the fetch was issued for. The pane accepts a response only
// when both the generation and the window still match what it is pointed at, so
// a read in flight when the selection moves is dropped instead of painting over
// the newer target — including the case the window ID alone cannot tell apart,
// where the selection leaves a session for a family summary and comes back.
type previewContentMsg struct {
	seq      int
	windowID int
	text     string
	err      error
}

// previewTickCmd schedules a single previewTickMsg after d.
func previewTickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return previewTickMsg{}
	})
}

// fetchPreviewCmd asynchronously fetches the full scrollback for a window.
// windowID == 0 (no running window) short-circuits with an empty text and
// no error so callers can render a "(session not running)" placeholder.
// A family summary owns no window, so it tracks window 0 and takes the
// short-circuit: previewing one costs no Kitty call at all.
func fetchPreviewCmd(client kitty.KittyClient, windowID, seq int) tea.Cmd {
	return func() tea.Msg {
		if windowID == 0 {
			return previewContentMsg{seq: seq, windowID: 0, text: "", err: nil}
		}
		text, err := client.GetText(kitty.MatchWindowByID(windowID), "all")
		return previewContentMsg{seq: seq, windowID: windowID, text: text, err: err}
	}
}

// previewFits reports whether the current terminal dimensions allow the
// configured preview direction. footerLines plus the sticky status row are
// reserved at the bottom.
func previewFits(width, height, footerLines, minCols, minRows int, direction string) bool {
	switch direction {
	case "vertical":
		return height-footerLines-statusBarLines >= minRows
	default:
		return width >= minCols
	}
}
