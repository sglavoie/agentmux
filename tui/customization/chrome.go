package customization

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The overlays in this package are standalone Bubble Tea programs: each one
// owns a whole Kitty overlay window rather than a region of the dashboard.
// They therefore need a handful of the root TUI's presentation decisions —
// the header, dim, and error styles, the selection wrapper, and the footer
// binding row — without needing the root TUI itself.
//
// These are deliberate private copies rather than a shared style dependency.
// Exporting the root theme to serve three pickers would turn a private
// vocabulary of ~50 styles into a public contract, and pulling the theme down
// into a leaf would make every other TUI leaf depend on it. Six lipgloss
// attributes and two one-line helpers are the cheaper side of that trade.

var (
	styleSelected  = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleError     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleDim       = lipgloss.NewStyle().Faint(true)
	styleHeader    = lipgloss.NewStyle().Bold(true)
	styleFooter    = lipgloss.NewStyle().Faint(true)
	styleFooterKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4ec9b0")).
			Bold(true)
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
