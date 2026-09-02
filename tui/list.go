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
