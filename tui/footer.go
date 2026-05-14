package tui

import "strings"

func renderFooter(m Model) string {
	var sb strings.Builder

	var line1, line2 string
	switch m.mode {
	case ModeSearch:
		line1 = styleFooter.Render("  enter: apply  esc: cancel")
		line2 = styleFooter.Render("  j/k: up/down")
	case ModeAccountFilter:
		line1 = styleFooter.Render("  enter: select  esc: cancel")
		line2 = styleFooter.Render("  j/k: up/down")
	case ModeActions:
		line1 = styleFooter.Render("  enter: confirm action  esc: back")
		line2 = styleFooter.Render("  j/k: up/down")
	case ModeConfirm:
		line1 = styleFooter.Render("  y: yes  n/esc: cancel")
		line2 = ""
	case ModePicker:
		line1 = styleFooter.Render("  space: toggle  enter: launch  o: open dir  esc: cancel")
		line2 = styleFooter.Render("  j/k: up/down")
	case ModeConfigPicker:
		line1 = styleFooter.Render("  enter: select  esc: cancel")
		line2 = styleFooter.Render("  j/k: up/down")
	default:
		line1 = styleFooter.Render("  x: close  r: restart  space: focus  e: peek  a: more actions  l: launch")
		nav := "  j/k: up/down  g/G: top/end  /: search  f: filter  q: quit"
		if m.activeSearch != "" || m.activeAccount != "" {
			nav += "  esc: clear filters"
		}
		line2 = styleFooter.Render(nav)
	}

	sb.WriteString(line1)
	if line2 != "" {
		sb.WriteString("\n")
		sb.WriteString(line2)
	}
	return sb.String()
}
