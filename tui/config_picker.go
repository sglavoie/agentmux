package tui

import "strings"

// renderConfigPicker renders the account picker overlay for config override.
func renderConfigPicker(m Model) string {
	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Override config: "+m.targetSession.Base) + "\n\n")
	for i, acc := range m.configPickerAccounts {
		line := "  " + acc.Name
		if acc.ConfigDir != "" {
			line += "  " + styleDim.Render(acc.ConfigDir)
		}
		if acc.Name == m.configPickerCurrent {
			line += styleDim.Render(" (current)")
		}
		if i == m.configPickerCursor {
			sb.WriteString(styleSelected.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
