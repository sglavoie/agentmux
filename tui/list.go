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
)

// renderHeader renders the dashboard title line with active filters.
func renderHeader(m Model) string {
	title := styleHeader.Render("CCM Dashboard")
	filters := ""
	if m.activeAccount != "" {
		filters += "  [account:" + m.activeAccount + "]"
	}
	if m.activeSearch != "" {
		filters += "  [/" + m.activeSearch + "]"
	}
	if m.err != nil {
		filters += "  " + styleError.Render("kitty: "+m.err.Error())
	}
	if m.actionErr != nil {
		filters += "  " + styleError.Render("action: "+m.actionErr.Error())
	}
	left := title + styleDim.Render(filters)
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
		return styleDim.Render("  (no sessions)") + "\n"
	}

	acctColor := buildAccountColorMap(m)

	groupCounts := countGroups(m.filtered, m.lastActivity, m.idleThreshold)
	multiGroup := groupCounts > 1

	var sb strings.Builder
	prevGroup := -1
	for i, s := range m.filtered {
		g := stateGroup(s, m.lastActivity, m.idleThreshold)
		if multiGroup && g != prevGroup {
			sb.WriteString(renderDivider(g, countInGroup(m.filtered, g, m.lastActivity, m.idleThreshold)))
			sb.WriteByte('\n')
			prevGroup = g
		}
		row := formatRow(s, acctColor, width)
		if i == m.cursor {
			sb.WriteString(styleSelected.Render(row))
		} else if g == 0 {
			sb.WriteString(styleAttentionRow.Render(row))
		} else {
			sb.WriteString(row)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
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

func renderDivider(group, count int) string {
	label := fmt.Sprintf(" ── %s (%d) ──", groupLabels[group], count)
	switch group {
	case 0:
		return styleAttentionDivider.Render(label)
	case 1:
		return styleActiveDivider.Render(label)
	default:
		return styleIdleDivider.Render(label)
	}
}

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
// like "⚠️" come out as 1 cell even though every modern terminal renders
// them as 2 cells. When we see U+FE0F and a sub-2 measurement, we treat
// the icon as 2 cells to keep the icon column aligned.
func iconDisplayWidth(icon string) int {
	w := runewidth.StringWidth(icon)
	if w < 2 && strings.ContainsRune(icon, '️') {
		return 2
	}
	return w
}

// buildAccountColorMap maps account name → palette color name from config.
func buildAccountColorMap(m Model) map[string]string {
	result := make(map[string]string)
	for _, acc := range m.cfg.Accounts {
		result[acc.Name] = acc.Color
	}
	return result
}
