package tui

import "strings"

// renderHelp renders the keyboard-shortcut overlay. Sections are ordered by
// frequency-of-use (Pareto), and each section caps at ≤7 entries so the user
// can chunk the list without scanning it linearly. Keys that overload across
// modes (e.g. `tab`/`esc`) are listed under the section where they're most
// often reached for; the footer/actions overlay disclose mode-specific
// meanings inline.
func renderHelp(m Model) string {
	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Keyboard Shortcuts") + "\n\n")

	sections := []struct {
		name  string
		lines []string
	}{
		{"Navigate", []string{
			"  j / ↓     move down",
			"  k / ↑     move up",
			"  g         go to top",
			"  G         go to bottom",
			"  tab       focus next filter chip",
			"  esc       clear filter / back out of overlay",
		}},
		{"Act on row", []string{
			"  space     focus the session's tab",
			"  enter     open actions overlay (lists every key below)",
			"  e         peek scrollback (active rows only)",
			"  r         restart session (confirm)",
			"  x         close session (confirm)",
			"  c         override config account",
			"  C         clear session icon",
		}},
		{"Launch / open", []string{
			"  l         open session launcher (live filter + preview)",
			"  o         open session directory in a plain shell tab",
			"  y         copy session title to clipboard",
			"  p         toggle preview pane (also active in launcher)",
		}},
		{"Filter", []string{
			"  /         filter (DSL: @account, !state, word)",
			"  f         filter pre-filled with @ (account)",
			"  1 – 9     remove filter chip at that position",
		}},
		{"App", []string{
			"  ?         show / hide this help",
			"  q         quit",
			"  ctrl+c    quit",
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
