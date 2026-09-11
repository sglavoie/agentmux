// This file is the root's shared list machinery: the dashboard list body, the
// scroll and cursor arithmetic every scrollable view charges against its own
// row budget, and the navigation step sizes a keystroke translates into.

package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// renderHeader renders the dashboard's quiet header: the app name, the view
// the list is drawn in, the project scope it is narrowed to, and a right-edge
// health indicator. Active filters are surfaced as chips below it.
//
// Everything else this line used to carry is gone rather than moved. The
// per-check pips and their hint strip, the icon legend, and the deprecation
// banner were four separate renderings of state that the health issue model and
// the help sheet already hold; the `/ to filter` and `i: inspector` hints
// repeated two of the footer's own bindings. What remains is the frame's
// orientation — where am I, what am I looking at, is anything wrong — and the
// one control that cannot be spelled any other way, because the health overlay
// has no key of its own.
//
// The view name is here rather than in the footer because it is the one piece
// of state that changes what every line below means: the same filtered sessions
// read as a flat urgency list or as project families, and a header that did not
// say which would leave the operator to infer it from the shape of the rows.
func renderHeader(m Model) string {
	return renderHeaderWithHits(m).text
}

type headerRender struct {
	text string
	hits []mouseHitRegion
}

type headerSegment struct {
	text string
	kind mouseHitKind
}

// headerScopeLabel names the project scope the list is currently narrowed to.
//
// It names the applied selector rather than a verb, because the control is the
// one place on the frame that says which project the dashboard is showing —
// the chip row says it too, but only while a filter is active, and "all
// projects" is a scope the operator needs to be able to read as deliberately as
// any other. The label is bounded so a long project title cannot push the
// health indicator off the right edge; the switcher itself spells it in full.
func headerScopeLabel(m Model) string {
	if applied := m.activeFilter.Project; applied != "" {
		return layout.Truncate(applied, 18, "…")
	}
	return "all projects"
}

// headerHealthSegment is the header's health control: one glyph for the worst
// reading and one phrase naming it, clickable into the details overlay.
//
// It consumes dashboard.HealthOverall and dashboard.HealthCompactLabel rather
// than re-reading the model's health fields or re-deriving a phrase of its own,
// so this control and the overlay heading are two renderings of one issue set.
// The retired pip strip hid itself until the first check landed; the indicator
// keeps its column from the first frame and lets the leaf say that the checks
// are still outstanding, so the first real reading does not move the header
// under the operator.
func headerHealthSegment(m Model) headerSegment {
	issues := healthIssues(m)
	// The leaf returns "" for an empty issue set, which is a reading that was
	// never built rather than a clean one. The root always builds all four, so
	// this is the leaf's contract being honoured rather than a state to render.
	label := dashboard.HealthCompactLabel(issues)
	if label == "" {
		return headerSegment{}
	}
	state := dashboard.HealthOverall(issues)
	return headerSegment{
		text: headerHealthStyle(state).Render(state.Glyph() + " " + label),
		kind: mouseHitHealth,
	}
}

// headerHealthGlyph is the indicator's one-cell form, kept when the phrase will
// not fit beside the view and the scope. It carries the same state and the same
// click target; only the wording is deferred to the overlay it opens.
func headerHealthGlyph(m Model) headerSegment {
	issues := healthIssues(m)
	if dashboard.HealthCompactLabel(issues) == "" {
		return headerSegment{}
	}
	state := dashboard.HealthOverall(issues)
	return headerSegment{
		text: headerHealthStyle(state).Render(state.Glyph()),
		kind: mouseHitHealth,
	}
}

// headerHealthStyle colors the indicator by severity. The glyph itself comes
// from the leaf's per-state set, so the shape and the color carry the same
// reading and a monochrome terminal loses neither.
func headerHealthStyle(state dashboard.HealthState) lipgloss.Style {
	switch state {
	case dashboard.HealthOK:
		return stylePipOK
	case dashboard.HealthWarning:
		return stylePipWarn
	case dashboard.HealthFailed:
		return stylePipErr
	default:
		return styleDim
	}
}

// renderHeaderWithHits keeps the adaptive admission decisions and the mouse
// spans in one pass. A control omitted for width contributes no target.
//
// Health owns the right edge, and what it gives up under pressure is its PHRASE
// rather than its column: a narrow header keeps the glyph, which still carries
// the severity by shape as well as by color and is still the click target that
// opens the details overlay. The phrase is the redundant part — the overlay one
// click away is where the reading is spelled out — while the view and the scope
// to its left are the two things no row on the dashboard answers. Only once the
// glyph form has been taken does the left run start dropping from its tail, so
// the scope goes before the view and the app name is the last thing standing.
func renderHeaderWithHits(m Model) headerRender {
	sep := styleDim.Render(" · ")
	sepW := layout.StyledWidth(sep)

	left := []headerSegment{
		{text: styleHeader.Render("Agent Mux")},
		{text: styleDim.Render(m.listView.String()), kind: mouseHitView},
		{text: styleFooter.Render(headerScopeLabel(m)), kind: mouseHitProjectSwitcher},
	}
	join := func(parts []headerSegment) string {
		texts := make([]string, len(parts))
		for i := range parts {
			texts[i] = parts[i].text
		}
		return strings.Join(texts, sep)
	}

	if m.width <= 0 {
		return headerRender{text: join(left) + "\n", hits: headerHits(left, sepW, -1)}
	}

	fits := func(parts []headerSegment, right headerSegment) bool {
		w := layout.StyledWidth(right.text)
		if w == 0 {
			return layout.StyledWidth(join(parts)) <= m.width
		}
		return layout.StyledWidth(join(parts))+1+w <= m.width
	}
	right := headerHealthSegment(m)
	if !fits(left, right) {
		right = headerHealthGlyph(m)
	}
	kept := left
	for len(kept) > 1 && !fits(kept, right) {
		kept = kept[:len(kept)-1]
	}
	rightWidth := layout.StyledWidth(right.text)
	if rightWidth >= m.width {
		right, rightWidth = headerSegment{}, 0
	}
	leftRendered := join(kept)
	if w := layout.StyledWidth(leftRendered); rightWidth > 0 && w+rightWidth > m.width {
		leftRendered = layout.StyledTruncate(leftRendered, m.width-rightWidth, "…")
	}
	leftWidth := layout.StyledWidth(leftRendered)

	line := leftRendered
	var hits []mouseHitRegion
	if rightWidth > 0 {
		pad := m.width - leftWidth - rightWidth
		if pad < 0 {
			pad = 0
		}
		line = leftRendered + strings.Repeat(" ", pad) + right.text
		hits = append(hits, mouseHitRegion{kind: right.kind, rect: dashboard.HitRect{
			X: m.width - rightWidth, Width: rightWidth, Height: 1,
		}})
	}
	hits = append(hits, headerHits(kept, sepW, leftWidth)...)
	return headerRender{text: layout.StyledTruncate(line, m.width, "…") + "\n", hits: hits}
}

// headerHits maps the admitted left-hand segments onto the columns they were
// drawn at. limit is the width actually painted, or -1 for an unbounded line: a
// segment past it contributes no target and one straddling it contributes only
// its visible part, so a click never lands on a control the truncation ate.
func headerHits(parts []headerSegment, sepW, limit int) []mouseHitRegion {
	var hits []mouseHitRegion
	x := 0
	for i, segment := range parts {
		if i > 0 {
			x += sepW
		}
		w := layout.StyledWidth(segment.text)
		if segment.kind != mouseHitNone && w > 0 {
			visible := w
			if limit >= 0 && x+w > limit {
				visible = limit - x
			}
			if visible > 0 {
				hits = append(hits, mouseHitRegion{kind: segment.kind, rect: dashboard.HitRect{
					X: x, Width: visible, Height: 1,
				}})
			}
		}
		x += w
	}
	return hits
}

// renderHeaderSpacer keeps a blank line between the header block and the list.
// Returns "" outside ModeList so the line collapses cleanly in other modes.
func renderHeaderSpacer(m Model) string {
	if m.mode != ModeList {
		return ""
	}
	return "\n"
}

// listMinVisible is the floor on the visible row budget for the session list.
// Even on very short terminals we keep at least this many rows visible so the
// pane remains useful. Mirrors the history overlay's own floor.
const listMinVisible = 3

const statusBarLines = 1

// chromeAboveListLines returns the number of vertical lines render() writes
// between the very top of the screen and the start of the list block. This is
// the "header + everything between header and list" budget; the trailing
// newline after the list and the footer area are accounted for separately by
// callers so the preview-ON path can subtract them in the same place as the
// preview's own height allocation.
//
// Every element is counted by rendering it and measuring the result rather than
// by re-deriving its height from the same conditions the renderer reads. The
// two used to be separate statements of the same rule — a hint that wrapped, a
// banner that grew a line, a legend that reflowed at a narrower width would
// each drift the real chrome away from the charged chrome, and the drift lands
// on the cursor as a row the renderer clips. Rendering twice costs a few string
// builds per frame; the alternative costs correctness at exactly the widths
// this file exists to get right.
//
// The one thing not measured is the header spacer: it is a bare newline, which
// layout.SectionLines correctly reports as no content, and it is still a
// physical row on screen.
func chromeAboveListLines(m Model) int {
	if m.mode == ModeSearch {
		// ModeSearch replaces the whole header block with the search input and
		// draws none of the chrome below it.
		return len(layout.SectionLines(renderSearch(m)))
	}
	n := len(layout.SectionLines(renderHeader(m)))
	n += len(layout.SectionLines(renderChips(m, m.width)))
	if m.mode == ModeList {
		n++ // spacer is always one line in ModeList
	}
	return n
}

// Nothing is drawn between the bottom of the body panes and the padding row
// above the footer, so there is no chromeBelowListLines to charge for. The
// detail strip that used to live there was retired with dashboard `p`: the
// inspector carries the selected session's metadata at every width, and two
// rows restating it under the list were two sessions the list could not show.

// listChromeLines returns the total number of vertical lines the dashboard
// chrome occupies around the session list in ModeList. It is the complement of
// the pane rows dashboardGeometry lays out, read from the same dashboardChrome,
// so the two cannot disagree about what the chrome costs.
func listChromeLines(m Model) int {
	return dashboardChromeFor(m).total()
}

// listVisibleHeight returns the row-line budget for the scrollable session
// list at the model's current dimensions. The result is floored at
// listMinVisible so a degenerate terminal does not yield a zero or negative
// window. Pre-WindowSizeMsg (height==0) the budget equals the item count
// so scroll-affecting operations are no-ops until the first resize arrives,
// mirroring detailScrollBounds.
//
// It reads the same dashboardGeometry renderSplit lays the panes out from, so
// the budget the cursor is clamped against and the budget the renderer draws
// into cannot drift apart — including in the cases where the split silently
// falls back to the plain list.
func listVisibleHeight(m Model) int {
	if m.height == 0 {
		return m.listItemCount()
	}
	return dashboardGeometry(m).listHeight
}

// listVisibleHeightForFooterLines derives the non-preview list height from an
// explicit footer-row count. Supplying the footer term rather than asking for
// it is what lets the footer budget and its position indicator converge without
// recursively calling listVisibleHeight through renderFooter — the substitution
// is the loop-breaker, and dashboardChromeWithFooter is where it is named.
func listVisibleHeightForFooterLines(m Model, footerLines int) int {
	if m.height == 0 {
		return m.listItemCount()
	}
	h := dashboardChromeWithFooter(m, footerLines).paneRows(m.height)
	if h < listMinVisible {
		h = listMinVisible
	}
	return h
}

// clampListOffset pins m.listOffset to [0, max(0, listItemCount - visible)]
// at the model's current dimensions. Mirrors clampDetailScroll: a resize or a
// shrink in the item list can leave the persisted offset above maxOffset and the
// next j/k keypress would chew through dead rows before the viewport reacts.
func clampListOffset(m *Model) {
	total := m.listItemCount()
	visible := listVisibleHeight(*m)
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.listOffset > maxOffset {
		m.listOffset = maxOffset
	}
	if m.listOffset < 0 {
		m.listOffset = 0
	}
}

// scrollListToCursor adjusts m.listOffset so the cursor sits within the
// currently visible window. Mirrors color_picker_app.scrollToCursor but
// accounts for the group chrome emitted by renderListAt when the urgency
// headings are drawn: a row that starts a visible group costs one extra line
// for its label, plus a blank separator when a prior group is already visible.
// The Projects view draws no headings, so there every item costs exactly one
// line and the walk reduces to plain counting.
//
// The renderer initializes prevGroup to -1, so the first row at the window
// origin always emits a group label when the headings are drawn. The cost walk
// here mirrors that initialization.
func scrollListToCursor(m *Model) {
	if m.height == 0 {
		return
	}
	total := m.listItemCount()
	if total == 0 {
		m.listOffset = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= total {
		m.cursor = total - 1
	}

	// Cursor moved above the window: pin offset to cursor.
	if m.cursor < m.listOffset {
		m.listOffset = m.cursor
	}

	visible := listVisibleHeight(*m)
	groupHeadings := listShowsGroupHeadings(m)

	// Advance offset item-by-item until the cumulative line cost from offset
	// through cursor fits within the visible budget. Stops when cursor and
	// offset coincide so we never overshoot. We deliberately do not call
	// clampListOffset here: its upper bound is computed in items, but the
	// label-aware walk above can require an offset > (total - visible) when
	// group labels eat into the line budget. Mirrors
	// color_picker_app.scrollToCursor, which also only floors at zero.
	for m.listOffset < m.cursor {
		if listCursorFitsWindow(m, m.listOffset, visible, groupHeadings) {
			break
		}
		m.listOffset++
	}

	if m.listOffset < 0 {
		m.listOffset = 0
	}
}

// listCursorFitsWindow reports whether renderListAt can emit the cursor row
// from the given offset inside the full list pane height. The pane height is
// not all row budget: renderListAt spends one line on ▲ when offset > 0 and
// one line on ▼ when rows remain below the cursor, and the dashboard reserves
// those indicator lines so the model cannot select a row that the renderer
// drops while making space for the scroll markers.
func listCursorFitsWindow(m *Model, offset, height int, groupHeadings bool) bool {
	return dashboardScrollInput(m, groupHeadings).CursorFits(offset, m.cursor, height)
}

// listLineCost returns the number of rendered lines from offset through
// cursor inclusive, matching the renderListAt walk.
func listLineCost(m *Model, offset, cursor int, groupHeadings bool) int {
	return dashboardScrollInput(m, groupHeadings).LineCost(offset, cursor)
}

func halfPageStep(visible int) int {
	step := visible / 2
	if step < 1 {
		return 1
	}
	return step
}

func fullPageStep(visible int) int {
	step := visible - 1
	if step < 1 {
		return 1
	}
	return step
}
