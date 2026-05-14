package tui

import "strings"

// actionLabels defines the available actions in order.
var actionLabels = []string{"close", "restart", "override config"}

// renderActions renders the action overlay for the currently selected session.
func renderActions(m Model) string {
	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Actions: "+m.actionTarget.Base) + "\n\n")
	for i, label := range actionLabels {
		line := "  " + label
		if i == m.actionCursor {
			sb.WriteString(styleSelected.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
