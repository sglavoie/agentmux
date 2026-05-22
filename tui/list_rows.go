package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

// renderList renders the session list for the current model state at the
// model's terminal width.
func renderList(m Model) string {
	return renderListAt(m, m.width)
}

// pulseColumnWidth is the visual width consumed by the recency-pulse column
// (1-cell glyph + 1-cell space). Only reserved when at least one row in the
// current view has an active pulse.
const pulseColumnWidth = 2

// ageColumnWidth is the visual width consumed by the per-row age column
// (5 cells of text + 1 trailing space). Sized to fit a warning cell like
// "9m ⏳"; only reserved when at least one row carries a sweep-eligible
// LastAction in m.watchdog.
const ageColumnWidth = 6

// ageWarningGlyph is appended to the age text once the elapsed time has
// crossed the 80% warning band. Rendered in styleAgeWarning so it reads as
// "approaching stale" without competing with the row's icon column.
const ageWarningGlyph = "⏳"

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
	ageActive := anyRowHasAge(m)
	now := time.Now()

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
		if ageActive && rowWidth > 0 {
			rowWidth -= ageColumnWidth
		}
		var ageCell string
		if ageActive {
			ageCell = renderAgeCell(m.watchdog[s.WindowID], now)
		}
		row := formatRow(s, acctColor, rowWidth, ageCell)
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

// anyRowHasAge reports whether any filtered row carries a sweep-eligible
// watchdog entry. Mirrors anyRowHasActivePulse: the age column is reserved
// for the entire render pass when at least one row has a non-empty
// LastAction, otherwise the column is omitted so rows recover their full
// directory budget.
func anyRowHasAge(m Model) bool {
	if len(m.watchdog) == 0 {
		return false
	}
	for _, s := range m.filtered {
		if ws, ok := m.watchdog[s.WindowID]; ok && ws.LastAction != "" {
			return true
		}
	}
	return false
}

// formatAge returns the per-row age label and the warning flag for a single
// watchdog entry. The text is `—` for rows with no LastAction; otherwise a
// coarsely rounded duration (`<n>m` under one hour, `<n>h` beyond). warning
// is true once the elapsed time has reached 80% of the resolved threshold,
// so the caller can flip the cell to styleAgeWarning. ThresholdSec ≤ 0
// disables the warning (no threshold configured for that window).
func formatAge(ws daemon.WatchdogState, now time.Time) (text string, warning bool) {
	if ws.LastAction == "" {
		return "—", false
	}
	elapsed := now.Sub(time.Unix(ws.LastEventAt, 0))
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed < time.Hour {
		text = fmt.Sprintf("%dm", int(elapsed/time.Minute))
	} else {
		text = fmt.Sprintf("%dh", int(elapsed/time.Hour))
	}
	if ws.ThresholdSec > 0 {
		threshold := time.Duration(ws.ThresholdSec) * time.Second
		if elapsed*5 >= threshold*4 {
			warning = true
		}
	}
	return text, warning
}

// renderAgeCell builds a fixed-width age cell sized to ageColumnWidth. The
// trailing space is part of the cell so callers can concatenate it before
// the directory without an extra separator. Warning rows get the warning
// glyph appended and the whole text styled in styleAgeWarning.
func renderAgeCell(ws daemon.WatchdogState, now time.Time) string {
	text, warning := formatAge(ws, now)
	if warning {
		text = text + " " + ageWarningGlyph
	}
	pad := ageColumnWidth - 1 - runewidth.StringWidth(text)
	if pad < 0 {
		pad = 0
	}
	cell := text + strings.Repeat(" ", pad)
	if warning {
		cell = styleAgeWarning.Render(cell)
	}
	return cell + " "
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
//	0 → Needs Attention (key, stop, question)
//	1 → Active (bolt, error, subagent, compact, or empty state within the
//	    idle threshold, or empty state with no activity record)
//	2 → Idle (empty state with stale activity, beyond the threshold)
//
// The error state is grouped under Active so a session whose last tool
// call failed (carrying the ⚠️ icon) stays visible alongside bolt rows
// instead of hiding under Idle — the icon itself remains the cue.
// subagent and compact are mid-task states the daemon emits while work is
// in progress, so they classify alongside bolt; question is the elicitation
// prompt, which requires user input and so groups under Needs Attention.
//
// Without a daemon-recorded activity timestamp a session is treated as
// Active rather than Idle so a fresh session does not flicker into the
// Idle bucket the moment it appears.
func stateGroup(s session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) int {
	switch s.State {
	case "key", "stop", "question":
		return 0
	case "bolt", "error", "subagent", "compact":
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
// Format: state_icon | account_prefix | [harness] | title | [age] | directory
// ageCell is empty when no row in the current view carries a watchdog entry;
// otherwise it is a pre-rendered fixed-width cell (ageColumnWidth cells,
// trailing space included) inserted immediately before the directory column.
func formatRow(s session.RunningSession, acctColor map[string]string, width int, ageCell string) string {
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

	// Title: use the project title padded to titleColWidth.
	title := runewidth.Truncate(runningSessionDisplayTitle(s), titleColWidth, "…")
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
		return iconCol + " " + badge + " " + harnessBadge + " " + title + "  " + ageCell + dir
	}
	return iconCol + " " + badge + " " + title + "  " + ageCell + dir
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
