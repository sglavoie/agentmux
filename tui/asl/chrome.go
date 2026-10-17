package asl

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/tui/theme"
)

// ASL styles stay local while deriving their colors from the shared palette.
var (
	styleSelected     = theme.Selected()
	styleError        = lipgloss.NewStyle().Foreground(theme.Color(theme.Red))
	styleDim          = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleHeader       = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)
	styleStateWarning = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))
	styleBanner       = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber)).Bold(true)

	// styleOverlayBox wraps the response modal in a rounded border with the
	// de-facto accent teal. Padding adds a single column of breathing room on
	// each side; combined with the border, the box adds 2 columns + 2 rows of
	// chrome to inner content.
	styleOverlayBox = theme.Popup()
)

// renderSelectedRow normalizes a selectable row to one continuous selection
// span. Row renderers may compose semantic foreground, faint, and reset
// sequences for their normal state; retaining those sequences underneath a
// background wrapper can terminate the selection early and leave stale
// tinted cells behind when the cursor moves. The selected copy therefore keeps
// only its visible content before applying the shared selection style.
func renderSelectedRow(row string) string {
	return styleSelected.Render(ansi.Strip(row))
}
