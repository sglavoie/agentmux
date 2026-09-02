// Package plans owns the TUI's plan listing and plan viewer domain: how a
// plan's logical lines become the terminal rows the viewer scrolls through,
// which of those rows are separators or search matches, how far the viewer
// may scroll, and the labels the Plans header and result rows carry — the
// active sort mode, its cycle, and a plan's project name.
//
// The package holds vocabulary and pure projections only. It knows nothing
// about the dashboard Model, performs no plan I/O of its own, and must not
// import the root tui package: deciding what a keystroke does to the viewer's
// cursor and scroll offset stays with the model that owns them.
package plans

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/sglavoie/agentmux/tui/layout"
)

// ViewerRow is one rendered terminal row mapped back to its logical source
// line in the plan's line slice.
type ViewerRow struct {
	Text       string
	SourceLine int
}

// ViewerRows wraps a plan's logical lines to width and returns the terminal
// rows they occupy, each stamped with the source line it came from. Lipgloss
// wrapping handles word boundaries; the hard wrap that follows guarantees no
// row exceeds the budget even when a single token cannot be broken on one.
func ViewerRows(lines []string, width int) []ViewerRow {
	wrapWidth := layout.WrapWidth(width)
	rows := make([]ViewerRow, 0, len(lines))
	for i, line := range lines {
		wrapped := strings.Split(lipgloss.Wrap(line, wrapWidth, ""), "\n")
		for _, text := range wrapped {
			for _, hardWrapped := range layout.WrapHard(text, wrapWidth) {
				rows = append(rows, ViewerRow{
					Text:       hardWrapped,
					SourceLine: i,
				})
			}
		}
	}
	return rows
}

// FirstRowForLine returns the index of the first rendered row belonging to
// sourceLine, or -1 when that line produced no rows.
func FirstRowForLine(rows []ViewerRow, sourceLine int) int {
	for i, row := range rows {
		if row.SourceLine == sourceLine {
			return i
		}
	}
	return -1
}

// PageStep returns how many rows a page-scroll should advance: the height of
// the currently visible window, never less than one row.
func PageStep(total, offset, height int) int {
	window := layout.ListWindowFor(total, offset, height)
	step := window.End - window.Start
	if step < 1 {
		return 1
	}
	return step
}

// MaxScrollOffset returns the largest scroll offset that still leaves content
// on screen. The viewer deliberately allows scrolling until a single tail row
// remains rather than stopping a full page from the end.
func MaxScrollOffset(total, height int) int {
	if total <= 0 || height <= 0 || total <= height {
		return 0
	}
	tailRows := height - 1
	if tailRows < 1 {
		tailRows = 1
	}
	maxOffset := total - tailRows
	if maxOffset < 0 {
		return 0
	}
	return maxOffset
}

// isSeparatorLine reports whether a logical line is one of the `--- path ---`
// file separators the viewer inserts between a plan's files.
func isSeparatorLine(line string) bool {
	return strings.HasPrefix(line, "--- ") && strings.HasSuffix(line, " ---")
}

// IsSeparator reports whether this row renders part of a file separator.
func (r ViewerRow) IsSeparator(lines []string) bool {
	return r.SourceLine >= 0 &&
		r.SourceLine < len(lines) &&
		isSeparatorLine(lines[r.SourceLine])
}

// IsMatch reports whether this row's source line is one of the search matches.
func (r ViewerRow) IsMatch(matchLines []int) bool {
	for _, idx := range matchLines {
		if r.SourceLine == idx {
			return true
		}
	}
	return false
}

// ViewerMatches scans lines for a case-insensitive substring of query and
// returns the indices of matching lines. Returns nil when query is empty so
// n/N have no effect.
func ViewerMatches(lines []string, query string) []int {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	needle := strings.ToLower(q)
	var matches []int
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), needle) {
			matches = append(matches, i)
		}
	}
	return matches
}
