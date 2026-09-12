package overview

import (
	"charm.land/lipgloss/v2"
)

// The overview's private copy of the presentation attributes it paints with. It
// duplicates the corresponding entries in the root package's theme for the
// reason tui/dashboard's chrome.go does: the root theme is a large private
// surface shared by every mode, and exporting it to serve a leaf would draw an
// import edge the tree deliberately does not have.
//
// Only the CHROME is styled here. Nothing in this file touches a tile's
// contents, which arrive from tui/dashboard already carrying the semantic colors
// — amber for a session waiting on the operator, red for a failed turn — that
// the grid exists to let an operator find at a glance. That separation is the
// selection rule in one sentence: the overview may paint the box, never what is
// in it.
var (
	styleTitle    = lipgloss.NewStyle().Bold(true)
	styleDim      = lipgloss.NewStyle().Faint(true)
	styleFeedback = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))

	// styleBorder is an unselected tile's box: faint, so a screenful of them
	// reads as a set of panes rather than as a drawing.
	styleBorder = lipgloss.NewStyle().Faint(true)
	// styleBorderSelected is the tile the keyboard is on. It is the footer's key
	// color, which is the same teal the dashboard's hero hint uses for a binding,
	// and bold on top.
	styleBorderSelected = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0")).Bold(true)
)

// borderSet is the six glyphs one tile's box is drawn from.
//
// There are two sets rather than one set in two colors because the selection has
// to survive a terminal with no color at all — a monochrome session, a capture
// piped through a filter, an operator who has turned colors off. The heavy box
// is legible as "this one" with every attribute stripped, and the color is the
// second, redundant signal rather than the only one.
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
)

// borderFor returns the glyphs and the style one tile's box is drawn with.
func borderFor(selected bool) (borderSet, lipgloss.Style) {
	if selected {
		return heavyBorder, styleBorderSelected
	}
	return lightBorder, styleBorder
}
