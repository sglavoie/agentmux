package commands

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The styles below are this domain's private copy of the presentation
// attributes it paints with. They deliberately duplicate the corresponding
// entries in the root package's theme rather than importing it: the root theme
// is a ~50-style private surface shared by every TUI mode, and exporting it to
// serve a leaf would give every leaf a theme edge that the intended import
// graph does not draw. Only the attributes this package itself renders are
// duplicated here.
var (
	styleSelected  = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleDim       = lipgloss.NewStyle().Faint(true)
	styleSearch    = lipgloss.NewStyle().Bold(true)
	styleFooter    = lipgloss.NewStyle().Faint(true)
	styleHeader    = lipgloss.NewStyle().Bold(true)
	styleFooterKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4ec9b0")).
			Bold(true)

	// styleOverlayBox wraps the palette and actions popups in a rounded
	// border with the de-facto accent teal. Padding adds a single column of
	// breathing room on each side; combined with the border, the box adds 2
	// columns + 2 rows of chrome to inner content.
	styleOverlayBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#4ec9b0")).
			Padding(0, 1)

	// styleFooterUnavailable renders actions that do not apply to the current
	// cursor row. The dim grey foreground keeps the key visible while signaling
	// that pressing it would be a no-op.
	styleFooterUnavailable = lipgloss.NewStyle().Foreground(lipgloss.Color("#5a5a5a"))
)

// renderSelectedRow normalizes a selectable row to one continuous selection
// span. Row renderers may compose semantic foreground, faint, and reset
// sequences for their normal state; retaining those sequences underneath a
// reverse-video wrapper can terminate the selection early and leave stale
// inverse cells behind when the cursor moves. The selected copy therefore keeps
// only its visible content before applying the shared selection style.
func renderSelectedRow(row string) string {
	return styleSelected.Render(ansi.Strip(row))
}

// renderFooterBinding renders an available key separately from its dim
// description. Keeping the two segments independent lets keys carry the shared
// accent without changing the description's established styling.
func renderFooterBinding(key, description string) string {
	return renderFooterBindingWithLabel(key, " ", description, styleFooter)
}

// renderFooterBindingWithLabel is the custom-label variant used when a compact
// prompt needs no gap between the key and its label.
func renderFooterBindingWithLabel(key, gap, label string, labelStyle lipgloss.Style) string {
	return styleFooterKey.Render(key) + gap + labelStyle.Render(label)
}

// joinFooterBindings joins already-rendered binding segments without adding an
// indent or wrapping.
func joinFooterBindings(separator string, rendered ...string) string {
	kept := make([]string, 0, len(rendered))
	for _, item := range rendered {
		if item != "" {
			kept = append(kept, item)
		}
	}
	return strings.Join(kept, styleFooter.Render(separator))
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
