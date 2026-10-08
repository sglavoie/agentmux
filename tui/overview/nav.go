// What a keypress moves the overview's selection to.
//
// The selection is a single index into the ORDERED session list, and the page is
// derived from it. That is the whole reason "keep the selection visible" needs no
// code: there is no page variable to keep in step, so a move that lands on
// another page brings the page with it by construction.
//
// Two families of movement, and the distinction is the design rather than an
// implementation detail:
//
//   - SPATIAL moves — the arrows and h/j/k/l — are about the grid the operator
//     is looking at. They move inside the current page and clamp at its edges,
//     because their meaning comes from what is on screen: `l` means "the tile to
//     the right of this one", and there is no tile to the right of the last
//     column. Horizontal movement additionally stays inside its row, so the end
//     of a row is a wall rather than a wrap onto a tile the eye did not expect.
//   - SEQUENTIAL moves — Tab, Shift+Tab, PgUp, PgDn, Home, End — are about the
//     LIST, and they cross pages freely. Tab is how an operator walks every
//     session without thinking about geometry, and PgDn is how they skip a
//     screenful of them.
//
// These positional moves clamp; n/N attention jumps wrap separately. A cursor
// that jumped from the last session to
// the first would make "am I at the end" unanswerable without counting, and the
// overview has a page indicator precisely so that question has a cheap answer.

package overview

// focusDigits follows the overall row-major card order, not the current page.
const focusDigits = "1234567890"

// Move is one navigation intent. It is a vocabulary rather than a key code: the
// binding from a keypress to a Move belongs to whatever is running the surface,
// and this package must stay assertable without one.
type Move int

const (
	// MoveLeft and MoveRight are spatial and stay inside the current row.
	MoveLeft Move = iota
	MoveRight
	// MoveUp and MoveDown are spatial and stay inside the current page.
	MoveUp
	MoveDown
	// MoveNext and MovePrev are Tab and Shift+Tab: one session along the list,
	// across row and page boundaries alike.
	MoveNext
	MovePrev
	// MovePageUp and MovePageDown move by exactly one page's capacity, which
	// keeps the tile's position within the page when a full page exists to land
	// on and clamps to the list's end when one does not.
	MovePageUp
	MovePageDown
	// MoveFirst and MoveLast are Home and End: the first and last session in the
	// whole list, not on the page.
	MoveFirst
	MoveLast
)

// String names the move for diagnostics and test failure messages.
func (m Move) String() string {
	switch m {
	case MoveLeft:
		return "left"
	case MoveRight:
		return "right"
	case MoveUp:
		return "up"
	case MoveDown:
		return "down"
	case MoveNext:
		return "next"
	case MovePrev:
		return "prev"
	case MovePageUp:
		return "page-up"
	case MovePageDown:
		return "page-down"
	case MoveFirst:
		return "first"
	case MoveLast:
		return "last"
	default:
		return "unknown"
	}
}

// Navigate returns the selection a move lands on.
//
// It is total: an out-of-range selected, an empty list, and a grid with no
// capacity are all answered rather than rejected, because each of the three is
// reachable by an ordinary sequence of events — a session closing under the
// cursor, the last session closing, and a terminal shrunk below the chrome.
//
// count is the size of the whole ordered list, g is the grid that same list is
// being drawn on, and the answer is always a valid index into the list (or 0 when
// there is no list at all).
func Navigate(g Geometry, count, selected int, m Move) int {
	if count <= 0 {
		return 0
	}
	last := count - 1
	selected = clamp(selected, 0, last)

	switch m {
	case MoveFirst:
		return 0
	case MoveLast:
		return last
	case MoveNext:
		return clamp(selected+1, 0, last)
	case MovePrev:
		return clamp(selected-1, 0, last)
	}

	capacity := g.Capacity()
	if capacity <= 0 {
		// No grid, so no spatial relationships and no page to skip. Nothing moves,
		// which is the honest answer for a surface that is drawing no tiles.
		return selected
	}

	switch m {
	case MovePageUp:
		return clamp(selected-capacity, 0, last)
	case MovePageDown:
		return clamp(selected+capacity, 0, last)
	}

	pageStart := g.PageStart(g.PageOf(selected))
	pos := selected - pageStart
	row, col := pos/g.Cols, pos%g.Cols
	switch m {
	case MoveLeft:
		col--
	case MoveRight:
		col++
	case MoveUp:
		row--
	case MoveDown:
		row++
	default:
		return selected
	}
	if col < 0 || col >= g.Cols || row < 0 || row >= g.Rows {
		return selected
	}

	// The target row may be incomplete — the last row of the last page usually
	// is. Landing on the row's last OCCUPIED tile rather than refusing the move is
	// what makes `j` from the top-right corner of a grid reach the one session on
	// the row below it. A row with nothing in it at all is not a target, and the
	// selection stays where it was.
	rowStart := pageStart + row*g.Cols
	if rowStart > last {
		return selected
	}
	rowEnd := rowStart + g.Cols - 1
	if rowEnd > last {
		rowEnd = last
	}
	return clamp(rowStart+col, rowStart, rowEnd)
}

// clamp bounds v to [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
