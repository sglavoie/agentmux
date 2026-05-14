package tui

import "strings"

func renderHelp(m Model) string {
	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Keyboard Shortcuts") + "\n\n")

	sections := []struct {
		name  string
		lines []string
	}{
		{"Navigation", []string{
			"  j / ↓      move down",
			"  k / ↑      move up",
			"  g          go to top",
			"  G          go to bottom",
		}},
		{"Session Actions", []string{
			"  space / enter  focus tab",
			"  e              peek scrollback",
			"  x              close session (confirm)",
			"  r              restart session (confirm)",
			"  c              override config account",
			"  C              clear session icon",
		}},
		{"Modes", []string{
			"  /    search sessions",
			"  f    filter by account",
			"  l    launch sessions (picker)",
			"  ?    show / hide this help",
		}},
		{"Global", []string{
			"  q / ctrl+c  quit",
			"  esc         clear filters  /  cancel overlay",
		}},
	}

	for _, sec := range sections {
		sb.WriteString(styleHeader.Render(sec.name) + "\n")
		for _, line := range sec.lines {
			sb.WriteString(styleDim.Render(line) + "\n")
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
