package history

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/tui/layout"
)

// The styles below are the history domain's private copy of the presentation
// attributes it paints with. They deliberately duplicate the corresponding
// entries in the root package's theme rather than importing it: the root theme
// is a ~50-style private surface shared by every TUI mode, and exporting it to
// serve a leaf would give every leaf a theme edge that the intended import
// graph does not draw. Only the attributes this package itself renders are
// duplicated here; the timeline and detail-row styles below are no longer
// duplicated at all, because the history views are their only painters.
var (
	styleSelected = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleError    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleDim      = lipgloss.NewStyle().Faint(true)
	styleSearch   = lipgloss.NewStyle().Bold(true)
	styleFooter   = lipgloss.NewStyle().Faint(true)
	styleHeader   = lipgloss.NewStyle().Bold(true)
	styleChip     = lipgloss.NewStyle()
	styleBanner   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Bold(true)

	styleFooterKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4ec9b0")).
			Bold(true)

	// styleOverlayBox wraps the delete-choice prompt, the event detail pane,
	// and the timeline in a rounded border with the de-facto accent teal.
	// Padding adds a single column of breathing room on each side; combined
	// with the border, the box adds 2 columns + 2 rows of chrome to inner
	// content.
	styleOverlayBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#4ec9b0")).
			Padding(0, 1)

	// styleAssistant colors assistant transcript lines with the active-teal
	// already established for the `bolt` state.
	styleAssistant = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))

	// Detail-pane styles (ModeDetail). detailRowStyle is intentionally
	// neutral so row text inherits the terminal's default foreground;
	// keeping the call site explicit makes it easy to recolor every row
	// later without hunting through detail.go. detailToolStyle picks up the
	// active-teal already established for `bolt` so the tool-name column
	// reads as an accent without introducing a new palette color.
	detailRowStyle  = lipgloss.NewStyle()
	detailToolStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
)

// Timeline phase header and annotation styles (ModeDetail timeline sub-view).
var (
	styleTimelineActive   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ec9b0"))
	styleTimelineCompact  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffaa00"))
	styleTimelineStop     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleTimelineError    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff5555"))
	styleTimelineGap      = lipgloss.NewStyle().Faint(true)
	styleTimelineSep      = lipgloss.NewStyle().Faint(true)
	styleTimelineTree     = lipgloss.NewStyle().Faint(true)
	styleTimelineTool     = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
	styleTimelineExpanded = lipgloss.NewStyle().Faint(true).Italic(true)
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

// renderFooterBinding renders an available key separately from its dim
// description. Keeping the two segments independent lets keys carry the
// shared accent without changing the description's established styling.
func renderFooterBinding(key, description string) string {
	return renderFooterBindingWithLabel(key, " ", description, styleFooter)
}

// renderFooterBindingWithLabel is the custom-label variant used when a
// description has semantic styling — the delete prompt's destructive red
// label is the only such case in this package.
func renderFooterBindingWithLabel(key, gap, label string, labelStyle lipgloss.Style) string {
	return styleFooterKey.Render(key) + gap + labelStyle.Render(label)
}

// joinFooterBindings joins already-rendered binding segments without adding an
// indent or wrapping. The modal footers in this package are fixed width, so
// they never need the root's wrapping path.
func joinFooterBindings(separator string, rendered ...string) string {
	kept := make([]string, 0, len(rendered))
	for _, item := range rendered {
		if item != "" {
			kept = append(kept, item)
		}
	}
	return strings.Join(kept, styleFooter.Render(separator))
}

// renderPathRow renders the dim "  <label>: <path>" row shown above the
// results list and the transcript viewer. An empty path renders nothing so
// the caller can collapse the line entirely.
func renderPathRow(label, path string, maxWidth int) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if maxWidth <= 0 {
		maxWidth = 120
	}
	row := "  " + label + ": " + path
	return styleDim.Render(layout.Truncate(row, maxWidth, "…"))
}

// pathRowLines is the vertical cost of renderPathRow for the same path.
func pathRowLines(path string) int {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	return 1
}

// plural, matchPlural, and maxRunes are deliberately private duplicates of the
// same three one-liners in the root package, which the plan viewer and the
// status bar still use. Sharing them would mean either a utility package or a
// leaf-to-root edge, and both are worse than three lines of duplication.

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

func maxRunes(n int) int {
	if n < 8 {
		return 8
	}
	return n
}
