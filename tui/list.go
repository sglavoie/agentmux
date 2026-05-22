package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	titleColWidth = 20
	dirMaxWidth   = 40

	// groupBorderGlyph is the 1-cell left border drawn for each row of a group.
	groupBorderGlyph = "▌"
	// groupBorderWidth is the visual width consumed by the border + trailing space.
	groupBorderWidth = 2
)

// stateGlosses maps the state name (as emitted by stateGroup/StateIcon) to a
// short human-readable meaning shown in the footer when the cursor sits on a
// row of that state. Idle/empty states have no entry: their gloss is the
// absence of an icon, which is gloss enough.
var stateGlosses = map[string]string{
	"bolt":     "active",
	"stop":     "stopped",
	"key":      "needs permission",
	"warning":  "last tool failed",
	"error":    "stopped with failure",
	"subagent": "subagent running",
	"compact":  "compacting context",
	"question": "awaiting response",
}

// StateGloss returns the human-readable meaning for a state name, or "" if
// the state has no gloss (idle / unknown).
func StateGloss(state string) string {
	return stateGlosses[state]
}

// renderHeader renders the dashboard title line. Active filters are surfaced
// as chips below the header via renderChips, so this line only carries the
// title plus any error indicators (kitty / action), the preview hint, and
// the right-aligned daemon/hooks health pips.
func renderHeader(m Model) string {
	title := styleHeader.Render("Agent Mux")
	info := ""
	if m.err != nil {
		info += "  " + styleError.Render("kitty: "+m.err.Error())
	}
	if m.actionErr != nil {
		info += "  " + styleError.Render("action: "+m.actionErr.Error())
	}
	left := title + styleDim.Render(info)

	var right string
	if m.previewEnabled {
		right = styleFooter.Render("p: hide preview  ")
	}
	pips := renderHealthPips(m)
	if pips != "" {
		if right != "" {
			right = pips + "  " + right
		} else {
			right = pips
		}
	}

	if right != "" && m.width > 0 {
		pad := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if pad < 1 {
			pad = 1
		}
		out := left + strings.Repeat(" ", pad) + right + "\n"
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
			pad := m.width - lipgloss.Width(styled)
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

// inlineFilterPlaceholder is the greyed copy shown above the list when the
// ModeList type-to-filter buffer is empty. It nudges users toward both the
// implicit (type) and explicit (/) entry points without consuming a key.
const inlineFilterPlaceholder = "type to filter (or / for explicit)"

// renderInlineFilter draws the persistent type-to-filter input above the
// list. Buffer empty → greyed placeholder; buffer non-empty → caret-suffixed
// search-styled string. When the completion popup is visible it stacks
// directly beneath. Returns "" outside ModeList so the line collapses
// cleanly in other modes.
func renderInlineFilter(m Model) string {
	if m.mode != ModeList {
		return ""
	}
	var head string
	if m.typeBuffer == "" {
		head = styleDim.Render("  " + inlineFilterPlaceholder)
	} else {
		head = "  " + styleSearch.Render("/") + m.typeBuffer + "█"
	}
	out := head + "\n"
	if popup := renderCompletion(m); popup != "" {
		out += popup
	}
	return out
}

// renderChips renders the filter chip row(s) for the current activeFilter,
// or "" when the filter is empty (so the area collapses with no blank line).
// Chips wrap to a second line when the running width would exceed the
// terminal width.
func renderChips(m Model, width int) string {
	if m.activeFilter.IsEmpty() {
		return ""
	}
	comps := m.activeFilter.Components()
	acctColor := buildAccountColorMap(m)

	const chipSep = "  "
	sepW := lipgloss.Width(chipSep)

	var lines []string
	var line strings.Builder
	lineW := 0
	for i, c := range comps {
		chip := renderChip(c, acctColor, i == m.focusedChipIdx)
		chipW := lipgloss.Width(chip)
		add := chipW
		if line.Len() > 0 {
			add += sepW
		}
		if width > 0 && line.Len() > 0 && lineW+add > width {
			lines = append(lines, line.String())
			line.Reset()
			lineW = 0
			add = chipW
		}
		if line.Len() > 0 {
			line.WriteString(chipSep)
		}
		line.WriteString(chip)
		lineW += add
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return strings.Join(lines, "\n") + "\n"
}

// renderChip renders a single chip body. The leading ▌ glyph and trailing
// ✕ remove icon share the chip's color so the entire chip reads as one unit.
// Focused chips wrap the ✕ in brackets — Postel's Law: chip focus is also
// signaled by the underline overlay in chipStyle, but the bracketed glyph
// makes the affordance unmistakable.
func renderChip(c chipComponent, acctColor map[string]string, focused bool) string {
	suffix := " ✕"
	if focused {
		suffix = " [✕]"
	}
	return chipStyle(c, acctColor, focused).Render("▌" + c.label + suffix)
}

// legendMinHeight is the minimum terminal height (rows) required to render
// the state legend strip. Below this the line is omitted silently to leave
// space for sessions.
const legendMinHeight = 20

// legendStates lists the state names rendered in the legend strip, in the
// order they appear. Mirrors stateGlosses; idle/unknown states have no entry.
var legendStates = []string{"bolt", "stop", "key", "warning", "error"}

// renderLegend returns the icon→meaning legend strip drawn above the list.
// Returns "" when the terminal is too short to spare a line, when the model
// has no width set, or when no state has a gloss to render. Each token is
// styled in its state's accent so the user maps icon → meaning at a glance.
func renderLegend(m Model) string {
	if m.height > 0 && m.height < legendMinHeight {
		return ""
	}
	var tokens []string
	for _, state := range legendStates {
		gloss := StateGloss(state)
		if gloss == "" {
			continue
		}
		icon := palette.StateIcon(state)
		if icon == "" {
			continue
		}
		token := padIconCell(icon) + " " + gloss
		tokens = append(tokens, stateAccentStyle(state).Render(token))
	}
	if len(tokens) == 0 {
		return ""
	}
	sep := styleFooter.Render("   ")
	return "  " + styleFooter.Render("icons:") + "  " + strings.Join(tokens, sep) + "\n"
}
