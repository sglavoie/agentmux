package customization

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/tui/theme"
)

// The overlays in this package are standalone Bubble Tea programs: each one
// owns a whole Kitty overlay window rather than a region of the dashboard.
// They therefore need a handful of the root TUI's presentation decisions —
// the header, dim, and error styles, the selection wrapper, and the footer
// binding row — without needing the root TUI itself.
//
// Their styles remain local, with colors derived from the shared palette.

var (
	styleSelected  = theme.Selected()
	styleError     = lipgloss.NewStyle().Foreground(theme.Color(theme.Red))
	styleDim       = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleHeader    = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)
	styleFooter    = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleFooterKey = lipgloss.NewStyle().
			Foreground(theme.Color(theme.Primary)).
			Bold(true)
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

// renderFooterBinding renders an available key separately from its dim
// description. Keeping the two segments independent lets keys carry the
// shared accent without changing the description's established styling.
func renderFooterBinding(key, description string) string {
	return styleFooterKey.Render(key) + " " + styleFooter.Render(description)
}

// joinFooterBindings joins already-rendered binding segments without adding
// an indent or wrapping. These overlays render a single fixed-width footer
// row, so they never need the root's wrapping footer machinery.
func joinFooterBindings(separator string, rendered ...string) string {
	kept := make([]string, 0, len(rendered))
	for _, item := range rendered {
		if item != "" {
			kept = append(kept, item)
		}
	}
	return strings.Join(kept, styleFooter.Render(separator))
}
