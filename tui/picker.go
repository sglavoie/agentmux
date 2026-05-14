package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
	"github.com/sglavoie/cc-manager-kitty/internal/session"
)

// renderPicker renders the session picker with multi-select and running status.
func renderPicker(m Model) string {
	var sb strings.Builder
	selectedCount := 0
	for _, v := range m.pickerSelected {
		if v {
			selectedCount++
		}
	}
	header := "Launch Sessions"
	if m.pickerActiveSearch != "" {
		header += fmt.Sprintf("  [search: %s]", m.pickerActiveSearch)
	}
	if selectedCount > 0 {
		header += fmt.Sprintf("  [%d selected]", selectedCount)
	}
	sb.WriteString(styleHeader.Render(header) + "\n\n")

	if len(m.pickerSessions) == 0 {
		sb.WriteString(styleDim.Render("  (no sessions in config)") + "\n")
		return sb.String()
	}
	if len(m.pickerFilteredIdx) == 0 {
		sb.WriteString(styleDim.Render("  (no sessions match)") + "\n")
		return sb.String()
	}

	acctColor := buildAccountColorMap(m)

	for cursor, origIdx := range m.pickerFilteredIdx {
		sess := m.pickerSessions[origIdx]
		baseTitle := session.FormatTitle(sess)
		running := m.pickerRunning[baseTitle]

		prefix := "[ ] "
		if m.pickerSelected[origIdx] {
			prefix = "[*] "
		}

		coloredTitle := accountStyle(acctColor[sess.Account]).Render(baseTitle)
		line := "  " + prefix + coloredTitle
		if running {
			line += "  " + styleDim.Render("(running)")
		}

		if cursor == m.pickerCursor {
			sb.WriteString(styleSelected.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// renderPickerWithSearch renders the picker with the live search prompt at the
// top. The list below reflects the currently committed pickerActiveSearch —
// typing does not filter live; filtering happens on enter.
func renderPickerWithSearch(m Model) string {
	prompt := styleSearch.Render("/") + m.pickerSearchText + "█"
	return prompt + "\n" + renderPicker(m)
}

// sortPickerSessions returns a sorted copy: by account name, then by title.
func sortPickerSessions(sessions []config.Session) []config.Session {
	sorted := slices.Clone(sessions)
	slices.SortStableFunc(sorted, func(a, b config.Session) int {
		if c := strings.Compare(
			strings.ToLower(a.Account),
			strings.ToLower(b.Account),
		); c != 0 {
			return c
		}
		return strings.Compare(
			strings.ToLower(a.Title),
			strings.ToLower(b.Title),
		)
	})
	return sorted
}

// buildPickerRunning returns a set of Base titles for all currently running sessions.
func buildPickerRunning(sessions []session.RunningSession) map[string]bool {
	m := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		m[s.Base] = true
	}
	return m
}
