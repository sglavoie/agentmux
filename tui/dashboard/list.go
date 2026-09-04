// Package dashboard owns the live-session dashboard: what a running session
// looks like as a row, which rows the active filter keeps, which state group
// each row belongs to, how many rows fit the current viewport, where the
// cursor lands after a refresh, and how the resulting list, chips, and
// completion popup paint.
//
// It is a domain leaf. It holds no Bubble Tea model and no program state of
// its own: every entry point takes the facts it needs explicitly, so the root
// package keeps ownership of the Model and adapts it at the call site. The
// package must not import the root tui package.
package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// noticeColumnWidth is the visual width consumed by the notice gutter (1-cell
// glyph + 1-cell space). The gutter carries two different pieces of news in the
// same two cells — a transient state-transition pulse and a durable unread
// completion — and is only reserved when at least one item in the current
// projection has one of them.
const noticeColumnWidth = 2

// UnreadNoticeGlyph marks a completion the operator has not looked at. It is a
// different glyph from the pulse deliberately: the pulse says "this just
// changed" and fades, while the dot says "this finished and you have not seen
// it" and stays until the daemon says otherwise. One cell wide, like the pulse,
// so the two share the gutter without either shifting the rows behind it.
//
// Exported on the same terms as GroupBorderGlyph: the root package renders this
// leaf but does not name its glyphs, so the only caller outside this package is
// a root test asserting what the composed screen says.
const UnreadNoticeGlyph = "•"

// activeLoopColumnWidth is the visual width reserved for the active
// agent-session-loop indicator (🔁 = 2 cells + 1 trailing space). It is only
// reserved when at least one rendered row has a matching active loop.
const activeLoopColumnWidth = 3

// childIndentWidth is the horizontal offset one nesting level costs a line in
// the Projects view. It buys the summary above the child a visible span to be
// wider than, which is the whole signal that the child belongs to it. It costs
// no vertical line, which is why nothing in the fit walk charges for it.
const childIndentWidth = 2

// ListInput is every fact the dashboard list renderer reads. The root builds
// one from its Model at the call site, which is what keeps this package free
// of Bubble Tea state.
//
// A non-positive Height means "unbounded": every row in Rows is rendered and
// the ▲/▼ scroll indicators are suppressed.
type ListInput struct {
	// Items is the flattened projection of the filtered, sorted rows currently
	// on screen: one entry per rendered line, indexed by Cursor and Offset. Its
	// Rows half remains the source of truth every row's facts are read from.
	Items ItemList
	// View is the arrangement Items was projected under. The renderer reads it
	// for one decision only — whether urgency group headings are drawn — because
	// everything else about the arrangement is already carried by the items
	// themselves. Its zero value is ViewUrgency, so a caller that has never heard
	// of views renders the historical list.
	View ViewMode
	// Loading is true while the initial running-session snapshot is still in
	// flight. Later background refreshes keep rendering the last usable rows (or
	// the confirmed empty state) instead of returning to the loading hero.
	Loading bool
	// SessionCount is the size of the unfiltered running-session set. It only
	// distinguishes "no sessions running" from "all rows filtered out" in the
	// empty hero.
	SessionCount int
	// Filter is the active filter, read only for the empty hero's hint copy.
	Filter Filter

	Cursor int
	Offset int
	Width  int
	Height int

	Now           time.Time
	Cfg           *config.Config
	LastActivity  map[int]time.Time
	IdleThreshold time.Duration
	Watchdog      map[int]protocol.WatchdogState

	// ActivePulse reports whether a window carries an active recency pulse. It
	// is the transient half of the notice gutter; the durable half — the unread
	// completion — is read off the rows themselves and needs no seam, because it
	// is daemon-owned state the projection already carries. Nil means no row
	// pulses.
	ActivePulse func(windowID int) bool
	// LoopMarker returns the supervision marker bound to a row, when one is
	// positively identified. A guessed association would put a wrong state
	// next to a session, so a row this cannot identify keeps the marker it
	// already had. Nil means no row has a marker.
	LoopMarker func(s session.RunningSession) (string, bool)
}

func (in ListInput) group(s session.RunningSession) int {
	return StateGroup(s, in.LastActivity, in.IdleThreshold)
}

// groupAt is the item-indexed form of group, used everywhere the walk is
// driven by a line rather than by a session.
func (in ListInput) groupAt(i int) int {
	return in.Items.GroupAt(i, in.LastActivity, in.IdleThreshold)
}

func (in ListInput) pulsing(windowID int) bool {
	return in.ActivePulse != nil && in.ActivePulse(windowID)
}

func (in ListInput) marker(s session.RunningSession) (string, bool) {
	if in.LoopMarker == nil {
		return "", false
	}
	return in.LoopMarker(s)
}

// RenderList renders the session list with explicit width and height budgets.
// The width budget controls per-row column truncation; the height budget caps
// the number of lines emitted by the windowed walk.
func RenderList(in ListInput) string {
	if in.Items.Len() == 0 {
		return renderEmptyHero(in)
	}

	acctColor := ProfileColorMap(in.Cfg)
	harnessColor := HarnessColorMap(in.Cfg)

	// The two questions the urgency bands answer are separate here. Borders
	// qualify one row and stay in both views; headings introduce a run of
	// same-group rows, which is a claim only the urgency ordering makes good on.
	multiGroup := in.Items.CountGroups(in.LastActivity, in.IdleThreshold) > 1
	headings := ShowsGroupHeadings(in.View, in.Items, in.LastActivity, in.IdleThreshold)
	noticeActive := anyItemHasNotice(in)
	activityActive := anyRowHasActivity(in)
	now := in.Now

	total := in.Items.Len()
	// One implementation of the windowed walk, asked here and asked again by the
	// footer's position indicator. A second copy of the fit is what let the two
	// disagree about how many items a height actually shows.
	startIdx, endIdx := in.scrollInput(headings).VisibleRange(in.Offset, in.Height)
	showTop := startIdx > 0
	showBottom := endIdx < total
	activeLoopActive := anyVisibleRowHasActiveLoop(in, startIdx, endIdx)
	tabNumberWidth := tabNumberColumnWidth(in.Items.Rows)
	var activityWidth int
	if activityActive {
		activityWidth = activityColumnWidth(in.Items.Rows, in.Watchdog, now)
	}
	// One scan per pass, like the activity and tab-number columns. A zero width
	// is how the column declines to be reserved at all, so there is no separate
	// "any row has a reason" predicate to keep in step with the measurement —
	// the measurement IS the predicate.
	reasonWidth := reasonColumnWidth(in.Items.Rows, in.group)
	// Same shape, same argument: a zero width is the branch column declining to
	// be reserved, so a view where no row sits in a named checkout — no daemon,
	// git_probe off, or simply no repositories — gives the whole column back to
	// the directory instead of padding every row around an empty slot.
	branchWidth := branchColumnWidth(in.Items.Rows)
	// The band is the pane's, not the row's: it is read once, from the width the
	// list was given, and every row in the pass is laid out under it. Reading it
	// per row from the already-reduced row budget would let a group border or a
	// child indent tip one row into a narrower band than its neighbors and break
	// the alignment the column scans above exist to produce.
	band := BandFor(in.Width)
	profileWidth := profileColumnWidth(in.Items.Rows)
	titleWidth := titleColumnWidth(in.Items.Rows, band)

	var sb strings.Builder
	if showTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	prevGroup := -1
	for i := startIdx; i < endIdx; i++ {
		item, _ := in.Items.At(i)
		s, isSession := in.Items.Session(item)
		g := in.groupAt(i)
		if headings && g != prevGroup {
			if prevGroup != -1 {
				sb.WriteByte('\n')
			}
			sb.WriteString(renderGroupLabel(g, in.Items.countInGroup(g, in.LastActivity, in.IdleThreshold)))
			sb.WriteByte('\n')
			prevGroup = g
		}
		rowWidth := in.Width
		if multiGroup && rowWidth > 0 {
			rowWidth -= groupBorderWidth
		}
		if noticeActive && rowWidth > 0 {
			rowWidth -= noticeColumnWidth
		}
		// A nested line pays for its indent out of the same budget the row
		// content is laid out against, so the composed line stays inside the pane
		// instead of pushing its rightmost column past the edge.
		indent := item.Depth * childIndentWidth
		if indent > 0 && rowWidth > 0 {
			rowWidth -= indent
			if rowWidth < 0 {
				rowWidth = 0
			}
		}
		var row string
		if isSession {
			reasonCell := renderReasonCell(s, g, reasonWidth)
			var activityCell string
			if activityActive {
				activityCell = renderActivityCell(s, in.Watchdog[s.WindowID], now, activityWidth)
			}
			var activeLoopCell string
			if activeLoopActive {
				activeLoopCell = renderLoopCellFor(in, s)
			}
			branchCell := renderBranchCell(s, branchWidth)
			tabNumberCell := renderTabNumberCell(s.TabNumber, tabNumberWidth)
			row = FormatRow(RowInput{
				Session:        s,
				Band:           band,
				Width:          rowWidth,
				AcctColor:      acctColor,
				HarnessColor:   harnessColor,
				Cfg:            in.Cfg,
				TabNumberCell:  tabNumberCell,
				ActiveLoopCell: activeLoopCell,
				ReasonCell:     reasonCell,
				ActivityCell:   activityCell,
				BranchCell:     branchCell,
				ProfileWidth:   profileWidth,
				TitleWidth:     titleWidth,
			})
		} else {
			row = renderSummaryRow(in, item, rowWidth)
		}
		if indent > 0 {
			// Inside row rather than around the styled result: the selection
			// highlight and the attention accent are applied to a whole line, and a
			// prefix added after them would leave an unhighlighted notch in front
			// of the selected child.
			row = strings.Repeat(" ", indent) + row
		}
		var styledRow string
		switch {
		case i == in.Cursor:
			styledRow = renderSelectedRow(row)
		case isSession && s.Locked:
			styledRow = styleDim.Render(row)
		case g == 0:
			styledRow = styleAttentionRow.Render(row)
		default:
			styledRow = row
		}
		if multiGroup {
			styledRow = groupBorder(g) + " " + styledRow
		}
		if noticeActive {
			styledRow = noticePrefix(in, item) + styledRow
		}
		// Safety clamp at the render boundary. FormatRow has a fixed-column
		// floor (MinRowWidth) it cannot shrink below, so when the pane is
		// narrower than that floor the composed row would otherwise draw past
		// the terminal edge. layout.StyledTruncate is ANSI-aware and keeps the
		// trailing style resets intact after the visible cut.
		if in.Width > 0 {
			styledRow = layout.StyledTruncate(styledRow, in.Width, "…")
		}
		sb.WriteString(styledRow)
		sb.WriteByte('\n')
	}

	if showBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}

	return sb.String()
}

// scrollInput is the subset of the renderer's facts the windowed walk reads.
// headings is passed rather than re-derived so the walk charges exactly the
// chrome this render pass is about to draw.
func (in ListInput) scrollInput(headings bool) ScrollInput {
	return ScrollInput{
		Items:         in.Items,
		LastActivity:  in.LastActivity,
		IdleThreshold: in.IdleThreshold,
		GroupHeadings: headings,
	}
}

// anyItemHasNotice reports whether any line in the current projection carries
// something the gutter would draw. Used to decide whether to reserve the notice
// column for the entire render pass so rows stay aligned.
//
// Two things about what it scans are load-bearing. It walks the whole
// projection rather than the rendered window, so scrolling a marked item off
// screen cannot shift every remaining column two cells to the left. And it walks
// the projection rather than in.Items.Rows, so a row the current arrangement
// draws no line for — a member folded away inside a collapsed family — reserves
// nothing on its own; the family above it reserves for it, because a collapsed
// summary rolls its members' unread completions up. Rows is already the filtered
// set, so a session the active filter excluded is in neither.
func anyItemHasNotice(in ListInput) bool {
	for _, it := range in.Items.Items {
		if in.noticeFor(it) != noticeNone {
			return true
		}
	}
	return false
}

// noticeKind is what the gutter has to say about one line.
type noticeKind int

const (
	// noticeNone is the ordinary line: two blank cells, so the marked lines
	// above and below it stay in one column.
	noticeNone noticeKind = iota
	// noticeUnread is a completion the operator has not looked at.
	noticeUnread
	// noticePulse is a state change recent enough to still be worth pointing at.
	noticePulse
)

// noticeFor decides which notice a line carries. Reservation and rendering both
// read this one rule, so the column cannot be reserved by one predicate and
// painted by another — the way a whole render pass acquires a two-cell gutter
// that nothing ever draws in, or worse, loses one it is about to draw in.
//
// A pulse outranks the dot. Both can be true of the same row at once, and the
// pulse is then the same news said louder and more recently. When it decays, or
// when a ModeList keypress clears the pulse layer wholesale, the dot is still
// underneath: seenness is daemon-owned and a completion stays unread until the
// operator has actually looked at the session, not until they have touched the
// dashboard.
//
// A synthetic summary has no window of its own and so never pulses, which is
// why the pulse arm asks for a session first.
func (in ListInput) noticeFor(it Item) noticeKind {
	if s, isSession := in.Items.Session(it); isSession && in.pulsing(s.WindowID) {
		return noticePulse
	}
	if in.Items.HasUnreadNotice(it) {
		return noticeUnread
	}
	return noticeNone
}

// anyVisibleRowHasActiveLoop reports whether the daemon's bulk state snapshot
// marks any row in the rendered window as looping. The column is reserved for
// the current window only, so off-screen active loops do not steal horizontal
// space.
func anyVisibleRowHasActiveLoop(in ListInput, startIdx, endIdx int) bool {
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > in.Items.Len() {
		endIdx = in.Items.Len()
	}
	for i := startIdx; i < endIdx; i++ {
		s, ok := in.Items.SessionAt(i)
		if !ok {
			continue
		}
		if s.Looping {
			return true
		}
		if _, marked := in.marker(s); marked {
			return true
		}
	}
	return false
}

// renderLoopCellFor prefers the supervision marker when the run is exactly
// bound to this session, and otherwise keeps the daemon's own loop marker.
func renderLoopCellFor(in ListInput, s session.RunningSession) string {
	if marker, ok := in.marker(s); ok {
		return layout.PadRight(marker, activeLoopColumnWidth)
	}
	return renderActiveLoopCell(s.Looping)
}

func renderActiveLoopCell(active bool) string {
	if !active {
		return strings.Repeat(" ", activeLoopColumnWidth)
	}
	return layout.PadRight("🔁", activeLoopColumnWidth)
}

// anyRowHasActivity reports whether any row is running a busy stretch.
// Mirrors anyItemHasNotice: the activity column is reserved for the
// entire render pass when at least one row carries a timer, otherwise the
// column is omitted so rows recover their full directory budget.
func anyRowHasActivity(in ListInput) bool {
	for _, s := range in.Items.Rows {
		if !s.BusySince.IsZero() {
			return true
		}
	}
	return false
}

// noticePrefix returns the gutter cell for one line (+ its trailing space): the
// state-colored pulse glyph, the attention-colored unread-completion dot, or two
// spaces for a line with no news, so column alignment is preserved across the
// render pass.
//
// Nothing here reads or writes seenness beyond projecting it. A render that
// acknowledged what it drew would clear the news by showing it, and the cursor
// passing over a row is not the operator having read it — clearing the bit stays
// with the daemon and its Kitty focus observation.
func noticePrefix(in ListInput, it Item) string {
	switch in.noticeFor(it) {
	case noticePulse:
		s, _ := in.Items.Session(it)
		return stateAccentStyle(s.State).Render(GroupBorderGlyph) + " "
	case noticeUnread:
		return styleUnreadNotice.Render(UnreadNoticeGlyph) + " "
	default:
		return strings.Repeat(" ", noticeColumnWidth)
	}
}

const (
	// summaryChevronExpanded and summaryChevronCollapsed are the collapse
	// affordance. Both are one cell, so a family folding and unfolding does not
	// shift the label beside it.
	summaryChevronExpanded  = "▾"
	summaryChevronCollapsed = "▸"

	// unnamedFamilyLabel is what a family with no readable name renders as. The
	// grouping key is built out of filesystem paths and must never be shown, so a
	// family whose label was lost to a degraded probe says nothing rather than
	// saying the key.
	unnamedFamilyLabel = "(unnamed project)"
)

// summaryCountLabels name the three urgency buckets in a summary's tally. They
// are the group labels said briefly: a summary reports several buckets on one
// line, where "Needs Attention" would spend a quarter of a compact pane.
var summaryCountLabels = [3]string{"attention", "active", "idle"}

// renderSummaryRow paints a family summary as one line, which is what keeps
// "one item costs one line" true for the fit walk and the scroll math.
//
// The line reads chevron, worst-child state icon, family label, then the tally
// of visible members per urgency bucket. The state icon is the summary's whole
// urgency claim — it is the icon of the most urgent member, so a folded family
// hiding a session that needs attention still shows that session's glyph — and
// the tally qualifies it with how many members are behind it.
//
// When the line does not fit, the tally is dropped whole rather than cut. A
// truncated "2 atten…" would read as a smaller number than it is, and the icon
// has already carried the part that must not be lost.
func renderSummaryRow(in ListInput, it Item, width int) string {
	chevron := summaryChevronExpanded
	if it.Collapsed {
		chevron = summaryChevronCollapsed
	}
	label := it.FamilyLabel
	if label == "" {
		label = unnamedFamilyLabel
	}

	group := idleGroup
	icon := ""
	if lead, ok := in.Items.SummaryLead(it, in.LastActivity, in.IdleThreshold); ok {
		group = StateGroup(lead, in.LastActivity, in.IdleThreshold)
		if lead.State != "" {
			icon = palette.StateIcon(lead.State)
		}
	}

	head := chevron + " " + layout.PadIconCell(icon) + " " + groupLabelStyle(group).Render(label)
	tally := renderSummaryCounts(in.Items.SummaryCounts(it, in.LastActivity, in.IdleThreshold))

	row := head
	if tally != "" && (width <= 0 || layout.StyledWidth(head)+2+layout.StyledWidth(tally) <= width) {
		row = head + "  " + tally
	}
	if width > 0 {
		row = layout.StyledTruncate(row, width, "…")
	}
	return row
}

// renderSummaryCounts renders the per-bucket tally, most urgent first, omitting
// the buckets no visible member sits in. Each count wears its own group's label
// style, so the tally is scannable as color before it is read as text.
func renderSummaryCounts(counts [3]int) string {
	var parts []string
	for group, n := range counts {
		if n == 0 {
			continue
		}
		parts = append(parts, groupLabelStyle(group).Render(fmt.Sprintf("%d %s", n, summaryCountLabels[group])))
	}
	return strings.Join(parts, styleDim.Render(" · "))
}

// renderEmptyHero renders a centered hero block when there are no rows to
// show. The hint copy distinguishes "no sessions running" from "all rows
// filtered out".
func renderEmptyHero(in ListInput) string {
	hasSessions := in.SessionCount > 0
	hasFilters := !in.Filter.IsEmpty()

	title := "No running sessions."
	var hint string
	if in.Loading {
		title = "Loading sessions…"
		hint = "press  ?  for all keys"
	} else if hasSessions && hasFilters {
		title = "No sessions match the active filter."
		hint = "press  esc  to clear filters     press  ?  for all keys"
	} else {
		hint = "press  l  to launch     press  ?  for all keys"
	}

	width := in.Width
	if width <= 0 {
		width = layout.StyledWidth(title)
		if w := layout.StyledWidth(hint); w > width {
			width = w
		}
	}
	titleLine := layout.Center(styleEmptyHero.Render(title), width)
	hintLine := layout.Center(styleDim.Render(hint), width)
	return titleLine + "\n\n" + hintLine + "\n"
}
