package plans

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/tui/theme"
)

// Plan-view styles stay local while deriving their colors from the shared palette.
var (
	styleSelected = theme.Selected()
	styleError    = lipgloss.NewStyle().Foreground(theme.Color(theme.Red))
	styleDim      = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleSearch   = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary)).Bold(true)
	styleHeader   = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)
	styleChip     = lipgloss.NewStyle()
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

// The count and timestamp labels below are private copies of the root's
// shared helpers, kept here for the same reason the styles are: the plan
// search and plan viewer are the only remaining consumers inside this
// package and the root still needs its own copies for the status bar.

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func matchPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}

// maxRunes floors a preview budget so a very narrow terminal still leaves room
// for an ellipsis plus a few cells of content.
func maxRunes(n int) int {
	if n < 8 {
		return 8
	}
	return n
}
