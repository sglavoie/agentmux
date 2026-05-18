package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
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
	"bolt":  "active",
	"stop":  "stopped",
	"key":   "needs permission",
	"error": "last tool failed",
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
var legendStates = []string{"bolt", "stop", "key", "error"}

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

// renderList renders the session list for the current model state at the
// model's terminal width.
func renderList(m Model) string {
	return renderListAt(m, m.width)
}

// pulseColumnWidth is the visual width consumed by the recency-pulse column
// (1-cell glyph + 1-cell space). Only reserved when at least one row in the
// current view has an active pulse.
const pulseColumnWidth = 2

// renderListAt renders the session list with an explicit width budget. Used
// when the preview pane consumes part of the row so only the directory column
// shrinks instead of the icon/badge/title columns.
func renderListAt(m Model, width int) string {
	if len(m.filtered) == 0 {
		return renderEmptyHero(m, width)
	}

	acctColor := buildAccountColorMap(m)

	groupCounts := countGroups(m.filtered, m.lastActivity, m.idleThreshold)
	multiGroup := groupCounts > 1
	pulseActive := anyRowHasActivePulse(m)

	var sb strings.Builder
	prevGroup := -1
	for i, s := range m.filtered {
		g := stateGroup(s, m.lastActivity, m.idleThreshold)
		if multiGroup && g != prevGroup {
			sb.WriteString(renderGroupLabel(g, countInGroup(m.filtered, g, m.lastActivity, m.idleThreshold)))
			sb.WriteByte('\n')
			prevGroup = g
		}
		rowWidth := width
		if multiGroup && rowWidth > 0 {
			rowWidth -= groupBorderWidth
		}
		if pulseActive && rowWidth > 0 {
			rowWidth -= pulseColumnWidth
		}
		row := formatRow(s, acctColor, rowWidth)
		var styledRow string
		switch {
		case i == m.cursor:
			styledRow = styleSelected.Render(row)
		case g == 0:
			styledRow = styleAttentionRow.Render(row)
		default:
			styledRow = row
		}
		if multiGroup {
			styledRow = groupBorder(g) + " " + styledRow
		}
		if pulseActive {
			styledRow = pulsePrefix(m, s) + styledRow
		}
		sb.WriteString(styledRow)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// anyRowHasActivePulse reports whether any filtered row carries an active
// recency-pulse record. Used to decide whether to reserve the pulse column
// for the entire render pass so rows stay aligned.
func anyRowHasActivePulse(m Model) bool {
	for _, s := range m.filtered {
		if m.hasActivePulse(s.WindowID) {
			return true
		}
	}
	return false
}

// pulsePrefix returns the colored glyph (+ trailing space) for a pulsing
// row, or two spaces for a non-pulsing row so column alignment is preserved
// across the render pass.
func pulsePrefix(m Model, s session.RunningSession) string {
	if !m.hasActivePulse(s.WindowID) {
		return strings.Repeat(" ", pulseColumnWidth)
	}
	return stateAccentStyle(s.State).Render(groupBorderGlyph) + " "
}

// renderEmptyHero renders a centered hero block when there are no rows to
// show. The hint copy distinguishes "no sessions running" from "all rows
// filtered out".
func renderEmptyHero(m Model, width int) string {
	hasSessions := len(m.sessions) > 0
	hasFilters := !m.activeFilter.IsEmpty()

	title := "No running sessions."
	var hint string
	if hasSessions && hasFilters {
		title = "No sessions match the active filter."
		hint = "press  esc  to clear filters     press  ?  for all keys"
	} else {
		hint = "press  l  to launch     press  ?  for all keys"
	}

	if width <= 0 {
		width = lipgloss.Width(title)
		if w := lipgloss.Width(hint); w > width {
			width = w
		}
	}
	titleLine := centerInWidth(styleEmptyHero.Render(title), width)
	hintLine := centerInWidth(styleDim.Render(hint), width)
	return titleLine + "\n\n" + hintLine + "\n"
}

func centerInWidth(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	pad := (width - w) / 2
	return strings.Repeat(" ", pad) + s
}

// groupBorder returns the colored 1-cell left border glyph for the given
// group (0 = attention, 1 = active, 2 = idle).
func groupBorder(group int) string {
	switch group {
	case 0:
		return styleGroupBorderAttention.Render(groupBorderGlyph)
	case 1:
		return styleGroupBorderActive.Render(groupBorderGlyph)
	default:
		return styleGroupBorderIdle.Render(groupBorderGlyph)
	}
}

// renderGroupLabel emits the left-aligned tag above the first row of a group.
// The label is indented to line up with the row content (border + space).
func renderGroupLabel(group, count int) string {
	label := fmt.Sprintf("%s (%d)", groupLabels[group], count)
	var styled string
	switch group {
	case 0:
		styled = styleGroupLabelAttention.Render(label)
	case 1:
		styled = styleGroupLabelActive.Render(label)
	default:
		styled = styleGroupLabelIdle.Render(label)
	}
	return strings.Repeat(" ", groupBorderWidth) + styled
}

// stateGroup classifies a session into one of three buckets:
//
//	0 → Needs Attention (key, stop)
//	1 → Active (bolt, error, or empty state within the idle threshold,
//	    or empty state with no activity record)
//	2 → Idle (empty state with stale activity, beyond the threshold)
//
// The error state is grouped under Active so a session whose last tool
// call failed (carrying the ⚠️ icon) stays visible alongside bolt rows
// instead of hiding under Idle — the icon itself remains the cue.
//
// Without a daemon-recorded activity timestamp a session is treated as
// Active rather than Idle so a fresh session does not flicker into the
// Idle bucket the moment it appears.
func stateGroup(s session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) int {
	switch s.State {
	case "key", "stop":
		return 0
	case "bolt", "error":
		return 1
	}
	if s.State != "" {
		// Unknown/legacy state names — keep the previous behavior (Idle).
		return 2
	}
	ts, ok := lastActivity[s.WindowID]
	if !ok {
		return 1
	}
	if threshold > 0 && time.Since(ts) >= threshold {
		return 2
	}
	return 1
}

func countGroups(sessions []session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) int {
	seen := make(map[int]bool)
	for _, s := range sessions {
		seen[stateGroup(s, lastActivity, threshold)] = true
	}
	return len(seen)
}

func countInGroup(sessions []session.RunningSession, group int, lastActivity map[int]time.Time, threshold time.Duration) int {
	n := 0
	for _, s := range sessions {
		if stateGroup(s, lastActivity, threshold) == group {
			n++
		}
	}
	return n
}

var groupLabels = [3]string{"Needs Attention", "Active", "Idle"}

// harnessAbbrev returns a two-letter lowercase abbreviation for the harness
// name, or "" when the harness is unknown/empty.
func harnessAbbrev(h string) string {
	switch strings.ToLower(h) {
	case "claude":
		return "cl"
	case "copilot":
		return "cp"
	}
	return ""
}

// formatRow formats a single session row.
// Format: state_icon | account_prefix | [harness] | title | directory
func formatRow(s session.RunningSession, acctColor map[string]string, width int) string {
	// State icon at the start for instant scanning.
	iconCol := "  "
	if s.State != "" {
		icon := palette.StateIcon(s.State)
		iconW := iconDisplayWidth(icon)
		if pad := 2 - iconW; pad > 0 {
			icon += strings.Repeat(" ", pad)
		}
		iconCol = icon
	}

	// Account prefix badge (first rune of account, uppercased). Use the first
	// rune rather than the first byte so a multibyte account name produces a
	// valid UTF-8 badge instead of a truncated invalid byte.
	acctBadge := " "
	if r, sz := utf8.DecodeRuneInString(s.Account); sz > 0 && r != utf8.RuneError {
		acctBadge = strings.ToUpper(string(r))
	}

	colorName := acctColor[s.Account]
	style := accountStyle(colorName)

	badge := style.Render("[" + acctBadge + "]")

	// Optional harness badge.
	harnessBadge := ""
	if abbrev := harnessAbbrev(s.Harness); abbrev != "" {
		harnessBadge = styleDim.Render("[" + abbrev + "]")
	}

	// Title: use Base (stripped of icons) padded to titleColWidth.
	title := runewidth.Truncate(s.Base, titleColWidth, "…")
	title = title + strings.Repeat(" ", titleColWidth-runewidth.StringWidth(title))

	// Directory: truncated.
	dir := s.Directory
	maxDir := dirMaxWidth
	if width > 0 {
		harnessW := 0
		if harnessBadge != "" {
			harnessW = 5 // "[cl] " = 4 chars + 1 space
		}
		used := 3 + 4 + titleColWidth + 3 + harnessW // icon+space + badge + title + separators + harness
		available := width - used
		if available > 10 {
			maxDir = available
		}
	}
	if runewidth.StringWidth(dir) > maxDir {
		runes := []rune(dir)
		for runewidth.StringWidth(string(runes))+1 > maxDir {
			runes = runes[1:]
		}
		dir = "…" + string(runes)
	}
	dir = styleDim.Render(dir)

	if harnessBadge != "" {
		return iconCol + " " + badge + " " + harnessBadge + " " + title + "  " + dir
	}
	return iconCol + " " + badge + " " + title + "  " + dir
}

// iconDisplayWidth returns the visual cell width of a state icon, with a
// fix-up for emoji-presentation sequences. go-runewidth measures rune by
// rune and treats the U+FE0F variation selector as zero-width, so glyphs
// like "⚠️" come out as 1 cell even though Kitty renders them as 2 cells.
// When we see U+FE0F and a sub-2 measurement, we treat the icon as 2 cells
// to keep the icon column aligned.
func iconDisplayWidth(icon string) int {
	w := runewidth.StringWidth(icon)
	if w < 2 && strings.ContainsRune(icon, '️') {
		return 2
	}
	return w
}

// padIconCell returns icon padded to exactly 2 visual cells. Used where
// runewidth and the terminal disagree on emoji width (notably ⚡ vs 🛑) so
// downstream layout stays stable across frame diffs.
func padIconCell(icon string) string {
	w := iconDisplayWidth(icon)
	if w >= 2 {
		return icon
	}
	return icon + strings.Repeat(" ", 2-w)
}

// buildAccountColorMap maps account name → palette color name from config.
func buildAccountColorMap(m Model) map[string]string {
	result := make(map[string]string)
	for _, acc := range m.cfg.Accounts {
		result[acc.Name] = acc.Color
	}
	return result
}
