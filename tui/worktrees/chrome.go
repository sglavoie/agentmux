package worktrees

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The styles below are the worktree view's private copy of the presentation
// attributes it paints with. They deliberately duplicate the corresponding
// entries in the root package's theme rather than importing it: the root theme
// is a ~50-style private surface shared by every TUI mode, and exporting it to
// serve a leaf would give every leaf a theme edge that the intended import
// graph does not draw.
var (
	styleSelected     = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleError        = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleDim          = lipgloss.NewStyle().Faint(true)
	styleSearch       = lipgloss.NewStyle().Bold(true)
	styleHeader       = lipgloss.NewStyle().Bold(true)
	styleStateWarning = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
)

// renderSelectedRow normalizes a selectable row to one continuous selection
// span. Row renderers may compose semantic foreground, faint, and reset
// sequences for their normal state; retaining those sequences underneath a
// reverse-video wrapper can terminate the selection early and leave stale
// inverse cells behind when the cursor moves.
func renderSelectedRow(row string) string {
	return styleSelected.Render(ansi.Strip(row))
}
