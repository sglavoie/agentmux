package tui

import (
	"slices"
	"strings"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
	"github.com/sglavoie/cc-manager-kitty/internal/session"
)

// renderPicker renders the session picker with multi-select and running status.
func renderPicker(m Model) string {
	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Launch Sessions") + "\n\n")

	if len(m.pickerSessions) == 0 {
		sb.WriteString(styleDim.Render("  (no sessions in config)") + "\n")
		return sb.String()
	}

	acctColor := buildAccountColorMap(m)

	for i, sess := range m.pickerSessions {
		baseTitle := session.FormatTitle(sess)
		running := m.pickerRunning[baseTitle]

		prefix := "[ ] "
		if m.pickerSelected[i] {
			prefix = "[*] "
		}

		coloredTitle := accountStyle(acctColor[sess.Account]).Render(baseTitle)
		line := "  " + prefix + coloredTitle
		if running {
			line += "  " + styleDim.Render("(running)")
		}

		if i == m.pickerCursor {
			sb.WriteString(styleSelected.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
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
