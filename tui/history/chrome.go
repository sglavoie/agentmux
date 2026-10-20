package history

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/theme"
)

// History keeps its styles local but derives their colors from the shared
// palette; timeline and detail-row styles remain specific to this view.
var (
	styleSelected = theme.Selected()
	styleError    = lipgloss.NewStyle().Foreground(theme.Color(theme.Red))
	styleDim      = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleSearch   = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary)).Bold(true)
	styleFooter   = chrome.FooterLabel
	styleHeader   = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)
	styleChip     = lipgloss.NewStyle()
	styleBanner   = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber)).Bold(true)

	// styleOverlayBox wraps the delete-choice prompt, the event detail pane,
	// and the timeline in a rounded blue border with a dark surface.
	// Padding adds a single column of breathing room on each side; combined
	// with the border, the box adds 2 columns + 2 rows of chrome to inner
	// content.
	styleOverlayBox = theme.Popup()

	// styleAssistant colors assistant transcript lines with the active-teal
	// already established for the `bolt` state.
	styleAssistant = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary))

	// Detail-pane styles (ModeDetail). detailRowStyle is intentionally
	// neutral so row text inherits the terminal's default foreground;
	// keeping the call site explicit makes it easy to recolor every row
	// later without hunting through detail.go. detailToolStyle picks up the
	// active-teal already established for `bolt` so the tool-name column
	// reads as an accent without introducing a new palette color.
	detailRowStyle  = lipgloss.NewStyle()
	detailToolStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary))
)

// Timeline phase header and annotation styles (ModeDetail timeline sub-view).
var (
	styleTimelineActive   = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Primary))
	styleTimelineCompact  = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Amber))
	styleTimelineStop     = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))
	styleTimelineError    = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Red))
	styleTimelineGap      = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleTimelineSep      = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleTimelineTree     = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleTimelineTool     = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary))
	styleTimelineExpanded = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted)).Italic(true)
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

// accountStyle returns a Lipgloss style with the account's foreground color.
// Falls back to the default style if the color name is unknown.
func accountStyle(colorName string) lipgloss.Style {
	c, err := palette.Lookup(colorName)
	if err != nil {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.ForegroundHex()))
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
