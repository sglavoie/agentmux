package tui

import (
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

	// Find the account color lookup map for this render.
	acctColor := buildAccountColorMap(m)

	var sb strings.Builder
	for i, s := range m.filtered {
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

// formatRow formats a single session row.
// Format: account_prefix | title | directory | state_icon
func formatRow(s session.RunningSession, acctColor map[string]string, width int) string {
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
		// Use remaining width dynamically.
		used := 4 + titleColWidth + 3 // badge + title + separators
		available := width - used - 4  // leave space for state icon + padding
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

	// State icon.
	stateIcon := ""
	if s.State != "" {
		stateIcon = " " + palette.StateIcon(s.State)
	}

	return badge + " " + title + "  " + dir + stateIcon
}

// buildAccountColorMap maps account name → palette color name from config.
func buildAccountColorMap(m Model) map[string]string {
	result := make(map[string]string)
	for _, acc := range m.cfg.Accounts {
		result[acc.Name] = acc.Color
	}
	return result
}
