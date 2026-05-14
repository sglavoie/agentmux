package tui

import (
	"strings"
	"time"
)

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
	case ModeConfirm:
		line1 = styleFooter.Render("  y: yes  n/esc: cancel")
		line2 = ""
	case ModePicker:
		line1 = styleFooter.Render("  space: toggle  enter: launch  o: open dir  esc: cancel")
		line2 = styleFooter.Render("  j/k: up/down")
	case ModeConfigPicker:
		line1 = styleFooter.Render("  enter: select  esc: cancel")
		line2 = styleFooter.Render("  j/k: up/down")
	case ModeHelp:
		line1 = styleFooter.Render("  esc/?/q: close help")
		line2 = ""
	default: // ModeList
		if m.previewEnabled {
			// In preview mode the only binding shown is `p: hide preview`,
			// which is rendered right-aligned in the title row.
			line1 = ""
			line2 = ""
		} else {
			line1 = listFooterLine1(m)
			nav := "  j/k: up/down  /: search  f: filter  q: quit  p: preview"
			if m.activeSearch != "" || m.activeAccount != "" {
				nav += "  esc: clear filters"
			}
			line2 = styleFooter.Render(nav)
		}
	}

	sb.WriteString(line1)
	if line2 != "" {
		sb.WriteString("\n")
		sb.WriteString(line2)
	}
	if m.mode == ModeList && m.previewNotice != "" &&
		(m.previewNoticeExpires.IsZero() || time.Now().Before(m.previewNoticeExpires)) {
		sb.WriteString("\n")
		sb.WriteString(styleError.Render("  " + m.previewNotice))
	}
	return sb.String()
}

func listFooterLine1(m Model) string {
	if len(m.filtered) == 0 {
		return styleFooter.Render("  l: launch  /: search  q: quit  ?: more")
	}
	s := m.filtered[m.cursor]
	switch {
	case stateGroup(s, m.lastActivity, m.idleThreshold) == 0: // needs attention (key / stop)
		return styleFooter.Render("  space: focus  x: close  r: restart  c: config  C: clear  ?: more")
	case s.State == "bolt": // active
		return styleFooter.Render("  space: focus  e: peek  r: restart  x: close  ?: more")
	default: // idle / stopped / unknown
		return styleFooter.Render("  space: focus  r: restart  x: close  l: launch  ?: more")
	}
}
