package customization

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/theme"
)

// The overlays in this package are standalone Bubble Tea programs: each one
// owns a whole Kitty overlay window rather than a region of the dashboard.
// They therefore need a handful of the root TUI's presentation decisions —
// the header, dim, and error styles and the selection wrapper — without
// needing the root TUI itself. Footer bindings come from tui/chrome, like
// every other footer's.
//
// Their styles remain local, with colors derived from the shared palette.

var (
	styleSelected  = theme.Selected()
	styleError     = lipgloss.NewStyle().Foreground(theme.Color(theme.Red))
	styleDim       = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleHeader    = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)
	styleFooterKey = chrome.FooterKey
)

// renderSelectedRow normalizes a selectable row to one continuous selection
// span. Row renderers may compose semantic foreground, faint, and reset
// sequences for their normal state; retaining those sequences underneath a
// background wrapper can terminate the selection early and leave stale
// tinted cells behind when the cursor moves. The selected copy therefore
// keeps only its visible content before applying the shared selection style.
func renderSelectedRow(row string) string {
	return styleSelected.Render(ansi.Strip(row))
}
