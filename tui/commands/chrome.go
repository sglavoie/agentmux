package commands

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/theme"
)

// Command styles stay local while deriving their colors from the shared palette.
var (
	styleSelected  = theme.Selected()
	styleDim       = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleSearch    = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary)).Bold(true)
	styleFooter    = chrome.FooterLabel
	styleHeader    = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)
	styleFooterKey = chrome.FooterKey

	// styleOverlayBox wraps the palette and actions popups in a rounded
	// border with the de-facto accent teal. Padding adds a single column of
	// breathing room on each side; combined with the border, the box adds 2
	// columns + 2 rows of chrome to inner content.
	styleOverlayBox = theme.Popup()

	// styleFooterUnavailable renders actions that do not apply to the current
	// cursor row. The dim grey foreground keeps the key visible while signaling
	// that pressing it would be a no-op.
	styleFooterUnavailable = lipgloss.NewStyle().Foreground(theme.Color(theme.Disabled))
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

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
