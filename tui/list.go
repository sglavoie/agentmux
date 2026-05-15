package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/cc-manager-kitty/internal/palette"
	"github.com/sglavoie/cc-manager-kitty/internal/session"
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
// title plus any error indicators (kitty / action) and the preview hint.
func renderHeader(m Model) string {
	title := styleHeader.Render("CCM Dashboard")
	info := ""
	if m.err != nil {
		info += "  " + styleError.Render("kitty: "+m.err.Error())
	}
	if m.actionErr != nil {
		info += "  " + styleError.Render("action: "+m.actionErr.Error())
	}
	left := title + styleDim.Render(info)
	if m.previewEnabled && m.width > 0 {
		hint := styleFooter.Render("p: hide preview  ")
		pad := m.width - lipgloss.Width(left) - lipgloss.Width(hint)
		if pad < 1 {
			pad = 1
		}
		return left + strings.Repeat(" ", pad) + hint + "\n"
	}
	return left + "\n"
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
func renderChip(c chipComponent, acctColor map[string]string, focused bool) string {
	return chipStyle(c, acctColor, focused).Render("▌" + c.label + " ✕")
}

// renderList renders the session list for the current model state at the
// model's terminal width.
func renderList(m Model) string {
	return renderListAt(m, m.width)
}

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
		sb.WriteString(styledRow)
		sb.WriteByte('\n')
	}
	return sb.String()
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

// formatRow formats a single session row.
// Format: state_icon | account_prefix | title | directory
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

	// Account prefix badge (first char of account, uppercased).
	acctBadge := " "
	if len(s.Account) > 0 {
		acctBadge = strings.ToUpper(s.Account[:1])
	}

	colorName := acctColor[s.Account]
	style := accountStyle(colorName)

	badge := style.Render("[" + acctBadge + "]")

	// Title: use Base (stripped of icons) padded to titleColWidth.
	title := runewidth.Truncate(s.Base, titleColWidth, "…")
	title = title + strings.Repeat(" ", titleColWidth-runewidth.StringWidth(title))

	// Directory: truncated.
	dir := s.Directory
	maxDir := dirMaxWidth
	if width > 0 {
		used := 3 + 4 + titleColWidth + 3 // icon+space + badge + title + separators
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
