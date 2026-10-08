// Package layout is terminal geometry, and nothing else: how wide a rendered
// string actually is in terminal cells (grapheme-aware), how it is cut,
// padded, centered, clamped, and wrapped to fit a column budget, how a popup is
// spliced into a base view by the overlay compositor, and which slice of a flat
// list fits a body of a given height.
//
// It also holds the model-independent row and detail primitives a selectable
// list is built from: greedy word wrapping with a rune-safe hard break, the
// label-plus-shortcut row that concatenates to exactly its budget, the
// optionally labelled band rule, and the fixed-height detail block that marks
// what it had to clip. They live here rather than beside either caller because
// tui/commands and tui/sessionactions both draw with them and the dependency
// runs one way between those two. The test for whether something belongs here
// is that it names no model, no session, and no action.
//
// Every visible-cell calculation in the TUI should route through the helpers
// in this package. Direct production calls to runewidth.StringWidth,
// runewidth.Truncate, or lipgloss.Width outside these helpers are discouraged
// so width, truncation, padding, and centering stay consistent across
// surfaces.
//
// It is the one leaf every other TUI leaf except tui/theme imports, and it has
// no in-repo dependency whatsoever: no domain state, no Bubble Tea model, and
// never the root tui package.
package layout
