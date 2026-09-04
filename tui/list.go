// This file is the root's shared list machinery: the dashboard list body, the
// scroll and cursor arithmetic every scrollable view charges against its own
// row budget, and the navigation step sizes a keystroke translates into.

package tui

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// renderHeader renders the dashboard title line. Active filters are surfaced
// as chips below the header via renderChips, so this line carries the title,
// the name of the current view, the filter hint, the preview hint, and the
// right-aligned daemon/hooks health pips. Error details are surfaced through
// the sticky status bar.
//
// The view name is here rather than in the footer because it is the one piece
// of state that changes what every line below means: the same filtered sessions
// read as a flat urgency list or as project families, and a header that did not
// say which would leave the operator to infer it from the shape of the rows.
func renderHeader(m Model) string {
	title := styleHeader.Render("Agent Mux")
	left := title + styleDim.Render(" · "+m.listView.String())
	pips := renderHealthPips(m)
	if m.width <= 0 {
		return left + "\n"
	}

	// Required health status owns the right edge. Controls are admitted next;
	// the duplicated filter placeholder is considered last and therefore drops
	// first. This keeps the current view label stable as a future mouse target.
	sep := styleFooter.Render("   ")
	right := pips
	prependIfFits := func(part string) {
		if part == "" {
			return
		}
		candidate := part
		if right != "" {
			candidate += sep + right
		}
		if layout.StyledWidth(left)+1+layout.StyledWidth(candidate) <= m.width {
			right = candidate
		}
	}
	if m.cfg != nil {
		label := "p: preview"
		if m.previewEnabled {
			label = "p: hide preview"
		}
		prependIfFits(styleFooter.Render(label))
	}
	if m.listView == dashboard.ViewUrgency {
		switch {
		case dashboardRailWidth(m, dashboardBaseListPaneWidth(m)) > 0:
			prependIfFits(styleFooter.Render("b: hide rail"))
		case !m.railRequested:
			prependIfFits(styleFooter.Render("b: show rail"))
		}
	}
	if m.mode == ModeList && dashboard.BandFor(m.width) != dashboard.BandCompact {
		prependIfFits(styleDim.Render(inlineFilterPlaceholder))
	}

	line := left
	if right != "" {
		rightWidth := layout.StyledWidth(right)
		if rightWidth >= m.width {
			line = layout.StyledTruncate(right, m.width, "…")
		} else {
			left = layout.StyledTruncate(left, m.width-rightWidth-1, "…")
			pad := m.width - layout.StyledWidth(left) - rightWidth
			line = left + strings.Repeat(" ", pad) + right
		}
	}
	line = layout.StyledTruncate(line, m.width, "…")
	out := line + "\n"
	if hint := renderHealthHints(m); hint != "" {
		out += hint
	}
	return out
}

// renderHealthPips returns the right-aligned `● daemon   ● hooks` strip
// drawn at the end of the header. Each pip is colored to reflect liveness;
// the labels stay in the dim footer color so the eye is drawn to the pip.
// Returns "" before the first health check completes so the header line
// doesn't briefly mis-render as "degraded" at startup.
func renderHealthPips(m Model) string {
	if m.daemonHealthCheckedAt.IsZero() && m.hookHealthCheckedAt.IsZero() {
		return ""
	}
	daemonPip := stylePipErr.Render(healthPipGlyph)
	if m.daemonAlive {
		daemonPip = stylePipOK.Render(healthPipGlyph)
	}
	hookPip := stylePipErr.Render(healthPipGlyph)
	switch {
	case m.hookHealthErr != nil:
		hookPip = stylePipWarn.Render(healthPipGlyph)
	case m.hookHealth.Healthy():
		hookPip = stylePipOK.Render(healthPipGlyph)
	}
	return daemonPip + " " + styleFooter.Render("daemon") + "   " +
		hookPip + " " + styleFooter.Render("hooks")
}

// renderHealthHints returns the one-line under-pip hint(s) shown when any
// health signal is in a degraded state. Hints are stacked one per line and
// right-aligned to sit beneath the pip strip. Returns "" when both
// daemon and hooks are healthy or no health refresh has run yet.
func renderHealthHints(m Model) string {
	if m.daemonHealthCheckedAt.IsZero() && m.hookHealthCheckedAt.IsZero() {
		return ""
	}
	var hints []string
	if !m.daemonHealthCheckedAt.IsZero() && !m.daemonAlive {
		hints = append(hints, "daemon down: amx daemon start")
	}
	if m.hookHealthErr != nil {
		hints = append(hints, "hooks check failed: "+m.hookHealthErr.Error())
	} else if !m.hookHealthCheckedAt.IsZero() && !m.hookHealth.Healthy() {
		hints = append(hints, "hooks drifted: amx config hooks-sync")
	}
	if len(hints) == 0 {
		return ""
	}
	var out strings.Builder
	for _, h := range hints {
		styled := styleFooter.Render(h)
		if m.width > 0 {
			pad := m.width - layout.StyledWidth(styled)
			if pad < 0 {
				pad = 0
			}
			out.WriteString(strings.Repeat(" ", pad))
		}
		out.WriteString(styled)
		out.WriteByte('\n')
	}
	return out.String()
}

const healthPipGlyph = "●"

// renderDeprecationBanner returns a single yellow banner line warning about
// deprecated config keys. Returns "" when the user has dismissed the
// banner for this session or the config has no deprecated keys.
func renderDeprecationBanner(m Model) string {
	if m.deprecationDismissed || len(m.deprecations) == 0 {
		return ""
	}
	var pairs []string
	for _, d := range m.deprecations {
		pairs = append(pairs, d.Key+" → "+d.Replacement)
	}
	headline := "⚠ config uses deprecated keys: " + strings.Join(pairs, ", ")
	hint := "  edit amx config path and replace the keys above · press d to dismiss for this session"
	// Both lines are clamped for the same reason the header and every footer row
	// are: the banner's width is the config's, not the terminal's, and one
	// deprecated key with a long replacement is already wider than a compact
	// pane.
	return layout.ClampLine(styleBanner.Render(headline), m.width) + "\n" +
		layout.ClampLine(styleFooter.Render(hint), m.width) + "\n"
}

// inlineFilterPlaceholder is the greyed copy shown in the dashboard title row.
// Filtering is explicit-only from the dashboard: press / to open the search
// input.
const inlineFilterPlaceholder = "/ to filter"

// renderInlineFilter keeps a spacer line above the list where the persistent
// filter hint used to render. Returns "" outside ModeList so the line
// collapses cleanly in other modes.
func renderInlineFilter(m Model) string {
	if m.mode != ModeList {
		return ""
	}
	return "\n"
}

// legendMinHeight is the minimum terminal height (rows) required to render
// the state legend strip. Below this the line is omitted silently to leave
// space for sessions.
const legendMinHeight = 20

// renderLegend returns the icon→meaning legend strip drawn above the list.
// Returns "" when the terminal is too short to spare a line, when it is too
// narrow to spend one on a reference, or when no state has a gloss to render.
// Each token is styled in its state's accent so the user maps icon → meaning at
// a glance.
//
// Both gates are the same judgement made on the two axes. The legend is a
// reference, not a status: it teaches the icon column and then stops being
// read. On a short terminal it costs rows the sessions need; on a narrow one it
// wraps to several rows to say the same thing, which is worse. So it appears
// only where the terminal can afford it on both axes — a spacious width, and a
// height above the existing floor.
func renderLegend(m Model) string {
	if m.height > 0 && m.height < legendMinHeight {
		return ""
	}
	if dashboard.BandFor(m.width) != dashboard.BandSpacious {
		return ""
	}
	var tokens []footerToken
	for _, state := range dashboard.LegendStates {
		gloss := dashboard.StateGloss(state)
		if gloss == "" {
			continue
		}
		icon := palette.StateIcon(state)
		if icon == "" {
			continue
		}
		token := layout.PadIconCell(icon) + " " + gloss
		tokens = append(tokens, footerRenderedToken(stateAccentStyle(state).Render(token)))
	}
	if len(tokens) == 0 {
		return ""
	}
	sep := styleFooter.Render("   ")
	sepW := layout.StyledWidth(sep)
	for i := 0; i < len(tokens)-1; i++ {
		tokens[i].sep = sep
		tokens[i].sepWidth = sepW
	}
	lead := "  " + styleFooter.Render("icons:") + "  "
	return wrapFooterLine(lead, "  ", tokens, m.width) + "\n"
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
// The one thing not measured is the spacer where the inline filter hint used to
// sit: it is a bare newline, which layout.SectionLines correctly reports as no
// content, and it is still a physical row on screen.
func chromeAboveListLines(m Model) int {
	if m.mode == ModeSearch {
		// ModeSearch replaces the whole header block with the search input and
		// draws none of the chrome below it.
		return len(layout.SectionLines(renderSearch(m)))
	}
	n := len(layout.SectionLines(renderHeader(m)))
	n += len(layout.SectionLines(renderDeprecationBanner(m)))
	n += len(layout.SectionLines(renderLegend(m)))
	n += len(layout.SectionLines(renderChips(m, m.width)))
	if m.mode == ModeList {
		n++ // spacer is always one line in ModeList
	}
	return n
}

// chromeBelowListLines returns the vertical lines drawn between the bottom of
// the list/preview area and the padding row above the footer. Only the compact
// band's selected-item detail strip lives there.
func chromeBelowListLines(m Model) int {
	return detailStripBudgetLines(m)
}

func legendLineCount(m Model) int {
	return len(layout.SectionLines(renderLegend(m)))
}

// listChromeLines returns the total number of vertical lines the dashboard
// chrome occupies around the session list in ModeList, mirroring the
// composition in render(). Sum of chromeAboveListLines (above) + the compact
// detail strip below the list + the padding row between body and footer +
// footerBudgetLines + the sticky status bar. Used by listVisibleHeight to
// derive the row budget when the preview pane is off.
func listChromeLines(m Model) int {
	return chromeAboveListLines(m) + chromeBelowListLines(m) +
		1 /* padding row */ + footerBudgetLines(m) + statusBarLines
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
// explicit footer-row count. Keeping this small calculation separate lets the
// footer budget and position indicator converge without recursively calling
// listVisibleHeight through renderFooter.
func listVisibleHeightForFooterLines(m Model, footerLines int) int {
	if m.height == 0 {
		return m.listItemCount()
	}
	h := m.height - chromeAboveListLines(m) - chromeBelowListLines(m) -
		1 /* padding row */ - footerLines - statusBarLines
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
