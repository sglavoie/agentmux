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
// the filter hint, the preview hint, and the right-aligned daemon/hooks
// health pips. Error details are surfaced through the sticky status bar.
func renderHeader(m Model) string {
	title := styleHeader.Render("Agent Mux")
	left := title

	var rightParts []string
	if m.mode == ModeList {
		rightParts = append(rightParts, styleDim.Render(inlineFilterPlaceholder))
	}
	if m.previewEnabled {
		rightParts = append(rightParts, styleFooter.Render("p: hide preview"))
	}
	pips := renderHealthPips(m)
	if pips != "" {
		rightParts = append(rightParts, pips)
	}
	right := strings.Join(rightParts, styleFooter.Render("   "))

	if right != "" && m.width > 0 {
		pad := m.width - layout.StyledWidth(left) - layout.StyledWidth(right)
		if pad < 1 {
			pad = 1
		}
		line := left + strings.Repeat(" ", pad) + right
		// Final hard-clamp: at very narrow widths the title plus filter hint /
		// health pips exceed the terminal even with the pad collapsed to 1, so
		// truncate the composed row to m.width. Mirrors the per-line clamp in
		// footer.go and the per-row clamp in list_rows.go.
		line = layout.StyledTruncate(line, m.width, "…")
		out := line + "\n"
		if hint := renderHealthHints(m); hint != "" {
			out += hint
		}
		return out
	}
	out := left + "\n"
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
	return styleBanner.Render(headline) + "\n" + styleFooter.Render(hint) + "\n"
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
// Returns "" when the terminal is too short to spare a line, when the model
// has no width set, or when no state has a gloss to render. Each token is
// styled in its state's accent so the user maps icon → meaning at a glance.
func renderLegend(m Model) string {
	if m.height > 0 && m.height < legendMinHeight {
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
// between the very top of the screen and the start of the list block in
// ModeList. This is the "header + everything between header and list" budget;
// the trailing newline after the list and the footer area are accounted for
// separately by callers so the preview-ON path can subtract them in the same
// place as the preview's own height allocation.
//
// Elements (each contributes its row count when visible):
//   - header + health hints                                             (1..3)
//   - deprecation banner (when undismissed and at least one entry)        (2)
//   - legend (omitted when m.height > 0 && m.height < legendMinHeight)    (1..n)
//   - chips (when activeFilter has at least one component)                (1)
//   - spacer where the inline filter hint used to sit in ModeList          (1)
//
// The renderHeader hint and chip-wrap-to-two-lines edge cases can drift the
// real chrome by one line; the listMinVisible floor in listVisibleHeight
// absorbs that drift on short terminals.
func chromeAboveListLines(m Model) int {
	n := 1 // header
	n += healthHintLines(m)
	if !m.deprecationDismissed && len(m.deprecations) > 0 {
		n += 2
	}
	n += legendLineCount(m)
	if len(m.activeFilter.Components()) > 0 {
		n++
	}
	if m.mode == ModeList {
		n++ // spacer is always one line in ModeList
	}
	return n
}

func legendLineCount(m Model) int {
	return len(layout.SectionLines(renderLegend(m)))
}

// healthHintLines mirrors renderHealthHints' line count without rendering
// styled text. renderHeader appends one line per degraded health signal below
// the title row, so list/preview height math must reserve those rows.
func healthHintLines(m Model) int {
	if m.daemonHealthCheckedAt.IsZero() && m.hookHealthCheckedAt.IsZero() {
		return 0
	}
	n := 0
	if !m.daemonHealthCheckedAt.IsZero() && !m.daemonAlive {
		n++
	}
	if m.hookHealthErr != nil {
		n++
	} else if !m.hookHealthCheckedAt.IsZero() && !m.hookHealth.Healthy() {
		n++
	}
	return n
}

// listChromeLines returns the total number of vertical lines the dashboard
// chrome occupies around the session list in ModeList, mirroring the
// composition in render(). Sum of chromeAboveListLines (above) + the padding
// row between body and footer + footerBudgetLines + the sticky status bar.
// Used by listVisibleHeight to derive the row budget when the preview pane
// is off.
func listChromeLines(m Model) int {
	return chromeAboveListLines(m) + 1 /* padding row */ + footerBudgetLines(m) + statusBarLines
}

// listVisibleHeight returns the row-line budget for the scrollable session
// list at the model's current dimensions. The result is floored at
// listMinVisible so a degenerate terminal does not yield a zero or negative
// window. Pre-WindowSizeMsg (height==0) the budget equals the filtered length
// so scroll-affecting operations are no-ops until the first resize arrives,
// mirroring detailScrollBounds.
func listVisibleHeight(m Model) int {
	if m.height == 0 {
		return len(m.filtered)
	}
	if m.previewEnabled && m.cfg != nil {
		var h int
		cfg := m.cfg.Defaults.Preview
		footerLines := footerBudgetLines(m)
		paneHeight := m.height - chromeAboveListLines(m) - 1 /* padding row */ - footerLines - statusBarLines
		if cfg.Direction == "vertical" {
			previewHeight := int(float64(paneHeight) * cfg.Ratio)
			if previewHeight < 3 {
				previewHeight = 3
			}
			if previewHeight > paneHeight-2 {
				previewHeight = paneHeight - 2
			}
			h = paneHeight - previewHeight - 1
		} else {
			h = paneHeight
		}
		if h < listMinVisible {
			h = listMinVisible
		}
		return h
	}

	return listVisibleHeightForFooterLines(m, footerBudgetLines(m))
}

// listVisibleHeightForFooterLines derives the non-preview list height from an
// explicit footer-row count. Keeping this small calculation separate lets the
// footer budget and position indicator converge without recursively calling
// listVisibleHeight through renderFooter.
func listVisibleHeightForFooterLines(m Model, footerLines int) int {
	if m.height == 0 {
		return len(m.filtered)
	}
	h := m.height - chromeAboveListLines(m) - 1 /* padding row */ - footerLines - statusBarLines
	if h < listMinVisible {
		h = listMinVisible
	}
	return h
}

// clampListOffset pins m.listOffset to [0, max(0, len(m.filtered) - visible)]
// at the model's current dimensions. Mirrors clampDetailScroll: a resize or a
// shrink in m.filtered can leave the persisted offset above maxOffset and the
// next j/k keypress would chew through dead rows before the viewport reacts.
func clampListOffset(m *Model) {
	total := len(m.filtered)
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
// accounts for the group chrome emitted by renderListAt when multiGroup is
// true: a row that starts a visible group costs one extra line for its label,
// plus a blank separator when a prior group is already visible.
//
// The renderer initializes prevGroup to -1, so the first row at the window
// origin always emits a group label when multiGroup is true. The cost walk
// here mirrors that initialization.
func scrollListToCursor(m *Model) {
	if m.height == 0 {
		return
	}
	if len(m.filtered) == 0 {
		m.listOffset = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}

	// Cursor moved above the window: pin offset to cursor.
	if m.cursor < m.listOffset {
		m.listOffset = m.cursor
	}

	visible := listVisibleHeight(*m)
	multiGroup := dashboard.CountGroups(m.filtered, m.lastActivity, m.idleThreshold) > 1

	// Advance offset row-by-row until the cumulative line cost from offset
	// through cursor fits within the visible budget. Stops when cursor and
	// offset coincide so we never overshoot. We deliberately do not call
	// clampListOffset here: its upper bound is computed in rows, but the
	// label-aware walk above can require an offset > (total - visible) when
	// group labels eat into the line budget. Mirrors
	// color_picker_app.scrollToCursor, which also only floors at zero.
	for m.listOffset < m.cursor {
		if listCursorFitsWindow(m, m.listOffset, visible, multiGroup) {
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
func listCursorFitsWindow(m *Model, offset, height int, multiGroup bool) bool {
	return dashboardScrollInput(m, multiGroup).CursorFits(offset, m.cursor, height)
}

// listLineCost returns the number of rendered lines from offset through
// cursor inclusive, matching the renderListAt walk.
func listLineCost(m *Model, offset, cursor int, multiGroup bool) int {
	return dashboardScrollInput(m, multiGroup).LineCost(offset, cursor)
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
