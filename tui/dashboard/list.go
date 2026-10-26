package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

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
	// Setup is guidance from existing configuration and observed health.
	Setup EmptyGuidance
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
	// Live is the full live session set, used for one question only: whether a
	// row's name collides with another session's and therefore needs a
	// disambiguating qualifier beside it. It is the LIVE set and not Items.Rows on
	// purpose — see NewNameDisambiguator — so a filter never renames the rows it
	// keeps. Nil means the caller has no separate live set and the filtered rows
	// are all there is.
	Live []session.RunningSession

	Cursor int
	Offset int
	Width  int
	Height int
	// Blurred reports that the list is showing a selection the keyboard is not
	// currently driving. Its zero value is "focused", which is what every caller
	// with no pane focus of its own means, so the seam costs nothing until
	// something has a second pane to give the keys to.
	Blurred bool

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
}

// groupAt is the item-indexed form of group, used everywhere the walk is
// driven by a line rather than by a session.
func (in ListInput) groupAt(i int) int {
	return in.Items.GroupAt(i, in.LastActivity, in.IdleThreshold)
}

// bandAt is the heading the line at i is listed under; its border stays
// groupAt's, so a pinned row still shows how urgent it is.
func (in ListInput) bandAt(i int) int {
	return in.Items.BandAt(i, in.LastActivity, in.IdleThreshold)
}

func (in ListInput) pulsing(windowID int) bool {
	return in.ActivePulse != nil && in.ActivePulse(windowID)
}

// RenderList renders the session list with explicit width and height budgets.
// The width budget controls per-row column truncation; the height budget caps
// the number of lines emitted by the windowed walk.
func RenderList(in ListInput) string {
	return RenderListWithHits(in).Text
}

// RenderListWithHits renders the list and returns semantic row regions from
// the very walk that emitted them. Callers that only need text keep using
// RenderList; mouse-aware callers avoid reconstructing group and indicator
// offsets from the finished ANSI string.
func RenderListWithHits(in ListInput) ListRender {
	if in.Items.Len() == 0 {
		return ListRender{Text: renderEmptyHero(in)}
	}

	// The two questions the urgency bands answer are separate here. Borders
	// qualify one row and stay in both views; headings introduce a run of
	// same-group rows, which is a claim only the urgency ordering makes good on.
	multiGroup := in.Items.CountGroups(in.LastActivity, in.IdleThreshold) > 1
	headings := ShowsGroupHeadings(in.View, in.Items, in.LastActivity, in.IdleThreshold)
	noticeActive := anyItemHasNotice(in)
	now := in.Now

	// What every row SAYS is decided once, here, for the whole projection rather
	// than for the visible window — the disambiguator has to see every colliding
	// name and the column scan has to see every cell, or the columns would shift
	// as the operator paged. Queue entry i describes Items.Rows[i], which is what
	// lets an item's Row index reach its facts with no second lookup.
	queue := BuildQueue(QueueInput{
		Rows:          in.Items.Rows,
		Live:          in.Live,
		LastActivity:  in.LastActivity,
		IdleThreshold: in.IdleThreshold,
		Now:           now,
	})
	columns := ScanQueueColumns(queue)

	total := in.Items.Len()
	// One implementation of the windowed walk, asked here and asked again by the
	// footer's position indicator. A second copy of the fit is what let the two
	// disagree about how many items a height actually shows.
	startIdx, endIdx := in.scrollInput(headings).VisibleRange(in.Offset, in.Height)
	showTop := startIdx > 0
	showBottom := endIdx < total

	var sb strings.Builder
	var hits []ListHit
	y := 0
	if showTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
		y++
	}

	// The heading and the rows under it start on the same cell, so the heading is
	// indented by exactly what the walk prefixes its rows with: the notice gutter
	// and the group border, each only when this pass reserves it.
	headingIndent := 0
	if noticeActive {
		headingIndent += noticeColumnWidth
	}
	if multiGroup {
		headingIndent += groupBorderWidth
	}

	prevGroup := -1
	for i := startIdx; i < endIdx; i++ {
		item, _ := in.Items.At(i)
		s, isSession := in.Items.Session(item)
		g := in.groupAt(i)
		if band := in.bandAt(i); headings && band != prevGroup {
			if prevGroup != -1 {
				sb.WriteByte('\n')
				y++
			}
			sb.WriteString(renderGroupLabel(band, in.Items.countInGroup(band, in.LastActivity, in.IdleThreshold), headingIndent))
			sb.WriteByte('\n')
			y++
			prevGroup = band
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
			// item.Row is in range: ItemList.Session bounds-checks it against the same
			// row set BuildQueue produced one entry per, in order.
			row = FormatRow(RowInput{
				Queue:   queue[item.Row],
				Width:   rowWidth,
				Columns: columns,
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
			// The one place the two selections are told apart. Everything below it is
			// a claim about the ROW; this is a claim about where the keyboard is, and
			// it outranks them because a selection the operator cannot find is worse
			// than a row whose urgency they have to read off its status instead.
			styledRow = renderSelectedRow(row, in.Blurred)
		case isSession && s.Locked:
			styledRow = styleDim.Render(row)
		default:
			// The attention accent is gone from the whole row on purpose. The status
			// cell already wears it, by way of the emphasis BuildQueue assigned, and
			// painting the name amber as well made every needs-you row shout its
			// identity when only its status had anything to say.
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
		hitWidth := layout.StyledWidth(styledRow)
		if in.Width > 0 {
			hitWidth = in.Width
		}
		hit := ListHit{
			Rect:     HitRect{Y: y, Width: hitWidth, Height: 1},
			Identity: CaptureItemIdentity(in.Items, i),
		}
		if item.Kind == ItemFamilySummary {
			hit.HasChevron = true
			hit.Collapsed = item.Collapsed
			if noticeActive {
				hit.ChevronX += noticeColumnWidth
			}
			if multiGroup {
				hit.ChevronX += groupBorderWidth
			}
		}
		hits = append(hits, hit)
		sb.WriteString(styledRow)
		sb.WriteByte('\n')
		y++
	}

	if showBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}

	return ListRender{Text: sb.String(), Hits: hits}
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
// are deliberately NOT the group labels: a heading labels one section and can
// address the operator ("Needs you"), while a summary reports all three buckets
// on one line and has to say each in a couple of cells. Keeping the two
// vocabularies apart is also what keeps a family summary from reading as a
// session row that happens to carry counts.
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

// heroHintSegment is one run of the hero's hint sentence, marked as prose or as
// a key to press. The split exists because the two read differently: the prose
// recedes and the glyph stands out, which is the same contract the footer keeps.
// An empty dashboard is the one screen with no rows to look at, so the two keys
// that lead out of it must be findable at a glance rather than buried in a dim
// sentence that scans as a caption.
type heroHintSegment struct {
	text string
	key  bool
}

// heroHint is a hint sentence as its segments. The double spaces around each key
// are part of the copy rather than the styling, so the sentence reads the same
// on a terminal that drops color as it does on one that keeps it.
type heroHint []heroHintSegment

// render paints the sentence, key segments in the key style and everything else
// dim.
func (h heroHint) render() string {
	var sb strings.Builder
	for _, segment := range h {
		if segment.key {
			sb.WriteString(styleHeroKey.Render(segment.text))
			continue
		}
		sb.WriteString(styleDim.Render(segment.text))
	}
	return sb.String()
}

// heroAllKeysHint is the trailing half of every hero hint: whatever the empty
// state is, `?` is how the operator finds everything that is not on the screen.
func heroAllKeysHint() heroHint {
	return heroHint{{text: "press  "}, {text: "?", key: true}, {text: "  for all keys"}}
}

// renderEmptyHero renders a centered hero block when there are no rows to
// show. The hint copy distinguishes "no sessions running" from "all rows
// filtered out".
func renderEmptyHero(in ListInput) string {
	hasSessions := in.SessionCount > 0
	hasFilters := !in.Filter.IsEmpty()

	title := layout.Sentence(layout.EmptyText("running sessions", ""))
	detail := in.Setup.Detail
	var hint heroHint
	if in.Loading {
		title = layout.Sentence(layout.LoadingText("sessions"))
		hint = heroAllKeysHint()
	} else if hasSessions && hasFilters {
		title = layout.Sentence(layout.NoMatchText("sessions", "")) + " the active filter"
		hint = append(heroHint{{text: "press  "}, {text: "esc", key: true}, {text: "  to clear filters     "}}, heroAllKeysHint()...)
	} else if in.Setup.Blocked {
		if in.Setup.Title != "" {
			title = in.Setup.Title
		}
		hint = append(heroHint{{text: "press  "}, {text: ":", key: true}, {text: "  for commands     "}}, heroAllKeysHint()...)
	} else {
		hint = append(heroHint{{text: "press  "}, {text: "l", key: true}, {text: "  to launch     "}}, heroAllKeysHint()...)
	}

	hintLine := hint.render()
	width := in.Width
	if width <= 0 {
		width = layout.StyledWidth(title)
		if w := layout.StyledWidth(hintLine); w > width {
			width = w
		}
	}
	body := ""
	for _, line := range layout.WrapWords(title, width) {
		body += layout.Center(styleEmptyHero.Render(line), width) + "\n"
	}
	body += "\n"
	for _, line := range strings.Split(ansi.Wrap(hintLine, width, ""), "\n") {
		body += layout.Center(line, width) + "\n"
	}
	if !in.Loading && !hasSessions && !hasFilters && detail != "" {
		body += "\n"
		for _, paragraph := range strings.Split(detail, "\n") {
			for _, line := range layout.WrapWords(paragraph, max(1, width-4)) {
				body += layout.Center(styleDim.Render(line), width) + "\n"
			}
		}
	}
	return body
}
