package tui

import "strings"

// renderFooter renders the two-line key-hint footer.
// Line 1: navigation keys (shared across modes).
// Line 2: mode-specific action keys.
func renderFooter(m Model) string {
	var sb strings.Builder

	nav := styleFooter.Render("  j/k: up/down  g/G: top/bottom  q: quit")

	var actions string
	switch m.mode {
	case ModeSearch:
		actions = styleFooter.Render("  enter: apply  esc: cancel")
	case ModeAccountFilter:
		actions = styleFooter.Render("  enter: select  esc: cancel")
	case ModeActions:
		actions = styleFooter.Render("  enter: confirm action  esc: back")
	case ModeConfirm:
		actions = styleFooter.Render("  y: yes  n/esc: cancel")
	case ModePicker:
		actions = styleFooter.Render("  space: toggle  enter: launch  o: open dir  esc: cancel")
	case ModeConfigPicker:
		actions = styleFooter.Render("  enter: select  esc: cancel")
	default:
		// ModeList — show what actions are available.
		hint := "  space: focus  e: peek  enter/a: actions  l: pick & launch  /: search  f: filter"
		if m.activeSearch != "" || m.activeAccount != "" {
			hint += "  esc: clear filters"
		}
		actions = styleFooter.Render(hint)
	}

	sb.WriteString(nav + "\n")
	sb.WriteString(actions)
	return sb.String()
}
