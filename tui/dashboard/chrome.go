package dashboard

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/palette"
)

// The styles below are the dashboard's private copy of the presentation
// attributes it paints with. They deliberately duplicate the corresponding
// entries in the root package's theme rather than importing it: the root
// theme is a ~50-style private surface shared by every TUI mode, and
// exporting it to serve a leaf would give every leaf a theme edge that the
// intended import graph does not draw. Only the attributes the dashboard
// itself renders are duplicated here.
var (
	styleSelected     = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleDim          = lipgloss.NewStyle().Faint(true)
	styleFooter       = lipgloss.NewStyle().Faint(true)
	styleAttentionRow = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleEmptyHero    = lipgloss.NewStyle().Bold(true)

	// styleAgeWarning colors the per-row activity cell when a sweep-eligible
	// session has been silent for >= 80% of its resolved threshold.
	styleAgeWarning = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))

	// Per-state accent styles used by the row pulse, the chips, and the
	// legend gloss.
	styleStateBolt    = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
	styleStateStop    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateKey     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateWarning = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateError   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))

	styleGroupBorderAttention = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleGroupBorderActive    = lipgloss.NewStyle()
	styleGroupBorderIdle      = lipgloss.NewStyle().Faint(true)

	styleGroupLabelAttention = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Bold(true)
	styleGroupLabelActive    = lipgloss.NewStyle().Bold(true)
	styleGroupLabelIdle      = lipgloss.NewStyle().Faint(true).Bold(true)

	// styleChip is the neutral base for substring chips. Account chips use
	// accountStyle(colorName) on top; state chips with attention semantics
	// (key/stop/warning/error) pick up their state accent.
	styleChip = lipgloss.NewStyle()

	// styleCompletionPopup keeps non-selected completion rows dim so the menu
	// reads as auxiliary UI.
	styleCompletionPopup = lipgloss.NewStyle().Faint(true)
)

// renderSelectedRow normalizes a selectable row to one continuous selection
// span. Row renderers may compose semantic foreground, faint, and reset
// sequences for their normal state; retaining those sequences underneath a
// reverse-video wrapper can terminate the selection early and leave stale
// inverse cells behind when the cursor moves. The selected copy therefore
// keeps only its visible content before applying the shared selection style.
func renderSelectedRow(row string) string {
	return styleSelected.Render(ansi.Strip(row))
}

// accountStyle returns a Lipgloss style with the account's foreground color.
// Falls back to the default style if the color name is unknown.
func accountStyle(colorName string) lipgloss.Style {
	c, err := palette.Lookup(colorName)
	if err != nil {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.ForegroundHex()))
}

// stateAccentStyle returns the per-state foreground style used to color the
// row pulse and the legend gloss. Unknown / idle states fall back to the dim
// footer style so the accent visually disappears.
func stateAccentStyle(state string) lipgloss.Style {
	switch state {
	case "bolt":
		return styleStateBolt
	case "stop":
		return styleStateStop
	case "key":
		return styleStateKey
	case "warning":
		return styleStateWarning
	case "error":
		return styleStateError
	default:
		return styleFooter
	}
}
