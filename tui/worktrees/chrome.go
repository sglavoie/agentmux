package worktrees

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/tui/theme"
)

// Worktree styles stay local while deriving their colors from the shared palette.
var (
	styleSelected     = theme.Selected()
	styleError        = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Red))
	styleDim          = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
	styleSearch       = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Bold(true)
	styleHeader       = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Secondary)).Bold(true)
	styleStateWarning = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Amber))
)

// renderSelectedRow normalizes a selectable row to one continuous selection
// span. Row renderers may compose semantic foreground, faint, and reset
// sequences for their normal state; retaining those sequences underneath a
// background wrapper can terminate the selection early and leave stale
// tinted cells behind when the cursor moves.
func renderSelectedRow(row string) string {
	return styleSelected.Render(ansi.Strip(row))
}
