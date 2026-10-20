package overview

import (
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/theme"
)

// The overview keeps its styles local while deriving colors from the shared palette.
//
// Only the CHROME is styled here. Nothing in this file touches a tile's
// contents, which arrive from tui/dashboard already carrying the semantic colors
// — amber for a session waiting on the operator, red for a failed turn — that
// the grid exists to let an operator find at a glance. That separation is the
// selection rule in one sentence: the overview may paint the box, never what is
// in it.
var (
	styleTitle                = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)
	styleDim                  = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleFeedback             = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))
	styleFooterKey            = chrome.FooterKey
	styleFooterUnavailableKey = chrome.FooterUnavailableKey

	// styleBorder is an unselected tile's quiet divider-colored box, so a screenful of them
	// reads as a set of panes rather than as a drawing.
	styleBorder = lipgloss.NewStyle().Foreground(theme.Color(theme.Divider))
	// styleBorderSelected is the tile the keyboard is on. It is the footer's key
	// color, which is the same teal the dashboard's hero hint uses for a binding,
	// and bold on top.
	styleBorderSelected = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary)).Bold(true)
	// The launching tab stays blue when the cursor moves elsewhere.
	styleBorderSource = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary))
)

// borderSet is the six glyphs one tile's box is drawn from.
//
// Distinct glyphs keep both keyboard selection and source identity visible
// even when the terminal has no color. Selection uses a heavy box; the source
// uses a double box and a label that remains when it is also selected.
type borderSet struct {
	topLeft, topRight, bottomLeft, bottomRight string
	horizontal, vertical                       string
}

var (
	lightBorder = borderSet{
		topLeft: "╭", topRight: "╮", bottomLeft: "╰", bottomRight: "╯",
		horizontal: "─", vertical: "│",
	}
	heavyBorder = borderSet{
		topLeft: "┏", topRight: "┓", bottomLeft: "┗", bottomRight: "┛",
		horizontal: "━", vertical: "┃",
	}
	sourceBorder = borderSet{
		topLeft: "╔", topRight: "╗", bottomLeft: "╚", bottomRight: "╝",
		horizontal: "═", vertical: "║",
	}
)

// borderFor returns the glyphs and the style one tile's box is drawn with.
func borderFor(selected, source bool) (borderSet, lipgloss.Style) {
	if selected {
		return heavyBorder, styleBorderSelected
	}
	if source {
		return sourceBorder, styleBorderSource
	}
	return lightBorder, styleBorder
}
