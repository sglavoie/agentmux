package tui

import (
	"fmt"
	"strings"

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
	return title + styleDim.Render(filters) + "\n"
}

// renderList renders the session list for the current model state.
func renderList(m Model) string {
	if len(m.filtered) == 0 {
		return styleDim.Render("  (no sessions)") + "\n"
	}

	acctColor := buildAccountColorMap(m)

	groupCounts := countGroups(m.filtered)
	multiGroup := groupCounts > 1

	var sb strings.Builder
	prevGroup := -1
	for i, s := range m.filtered {
		g := stateGroup(s)
		if multiGroup && g != prevGroup {
			sb.WriteString(renderDivider(g, countInGroup(m.filtered, g)))
			sb.WriteByte('\n')
			prevGroup = g
		}
		row := formatRow(s, acctColor, m.width)
		if i == m.cursor {
			sb.WriteString(styleSelected.Render(row))
		} else {
			sb.WriteString(row)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func stateGroup(s session.RunningSession) int {
	switch s.State {
	case "key", "stop":
		return 0
	case "bolt":
		return 1
	default:
		return 2
	}
}

func countGroups(sessions []session.RunningSession) int {
	seen := make(map[int]bool)
	for _, s := range sessions {
		seen[stateGroup(s)] = true
	}
	return len(seen)
}

func countInGroup(sessions []session.RunningSession, group int) int {
	n := 0
	for _, s := range sessions {
		if stateGroup(s) == group {
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
		iconW := runewidth.StringWidth(icon)
		icon += strings.Repeat(" ", 2-iconW)
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

// buildAccountColorMap maps account name → palette color name from config.
func buildAccountColorMap(m Model) map[string]string {
	result := make(map[string]string)
	for _, acc := range m.cfg.Accounts {
		result[acc.Name] = acc.Color
	}
	return result
}
