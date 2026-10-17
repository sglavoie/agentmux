package plans

import (
	"fmt"
	"time"

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

// formatWhen returns a short human-readable representation of a timestamp.
// Entries newer than 24h show as "Xh", older up to 7d as "Xd", and anything
// older falls back to YYYY-MM-DD.
func formatWhen(t time.Time) string {
	if t.IsZero() {
		return "          "
	}
	delta := time.Since(t)
	if delta < 0 {
		delta = 0
	}
	switch {
	case delta < time.Hour:
		mins := int(delta.Minutes())
		if mins < 1 && delta > 0 {
			mins = 1
		}
		return fmt.Sprintf("%2dm   ago", mins)
	case delta < 24*time.Hour:
		return fmt.Sprintf("%2dh   ago", int(delta.Hours()))
	case delta < 7*24*time.Hour:
		return fmt.Sprintf("%2dd   ago", int(delta.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
