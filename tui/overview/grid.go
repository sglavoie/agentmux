// The live overview's pure half: what order the sessions come in, how many
// tiles fit on a terminal, which page a selection is on, where each tile sits,
// and what a keypress moves the selection to.
//
// Everything here is a function of its arguments. No clock is read, no Kitty
// call is made, no daemon is asked, and no file is opened — a whole overview is
// rendered from a supplied session slice, a supplied set of previews, and a
// supplied Now, which is what lets a grid be asserted byte-for-byte at a pinned
// moment and at any terminal size.
//
// The geometry and the navigation are one package rather than two on purpose.
// They share the two numbers that must never disagree: the COLUMN COUNT, which
// decides what is to the right of a tile, and the PAGE CAPACITY, which decides
// what PgDn means. A navigator that computed its own columns would move the
// cursor to a tile the renderer did not draw, and the bug would only appear at
// the widths where the two formulas happened to differ. Both read one Geometry.
//
// The geometry here and the navigation in nav.go import nothing at all; the
// tile's contents come from tui/dashboard and its cell arithmetic from
// tui/layout, both at the painting step. Neither names the root model.

package overview

const (
	// TileMinWidth and TileMinHeight are the target minimum size of one tile,
	// INCLUDING its border. They are the size at which a tile is still worth
	// drawing: 48 by 12 leaves a 44-cell-wide interior after the border and its
	// padding, and ten interior rows, of which the compact header and its divider
	// take three and the terminal output takes the remaining seven.
	//
	// They are a target rather than a floor. A terminal too small for even one
	// such tile still gets one — see fitCount — because a single cramped preview
	// is what the operator asked for and an empty grid is not.
	TileMinWidth  = 48
	TileMinHeight = 12
	// Two content lines plus the top and bottom borders.
	CompactTileMinHeight = 4

	// The three rows the surface reserves before any tile is placed: what this is
	// and how much of it there is, the last thing that happened, and the keys.
	//
	// They are three separate reservations rather than one number so the clipping
	// order below can name which one it is dropping.
	titleRows    = 1
	feedbackRows = 1
	hintRows     = 1

	// ChromeLines is what the surface spends before a tile is placed. A caller
	// sizing the grid charges this once.
	ChromeLines = titleRows + feedbackRows + hintRows

	// gridTop is the first row a tile may occupy: under the title and the
	// feedback line, and above the hints, which sit on the last row.
	gridTop = titleRows + feedbackRows
)

// Tile is where one preview goes: an absolute origin inside the surface and the
// exact size the tile must render itself at.
type Tile struct {
	// X and Y are zero-based cell offsets from the surface's top-left corner.
	X, Y int
	// Width and Height are the tile's full extent, border included.
	Width, Height int
}

// Geometry is one terminal size resolved into a grid.
//
// It is a value rather than a set of loose helpers because every question about
// the grid has to be answered from the same arithmetic. Cols is read by the
// renderer to lay tiles out and by the navigator to decide what is to the right
// of one; Capacity is read by the renderer to slice a page out of the session
// list and by the navigator to decide what PgDn skips. There is one Measure, so
// there is one answer.
//
// The zero value is a grid with no room in it: Cols and Rows are zero, Capacity
// is zero, and no tile exists. That is the honest reading of a terminal too
// small for the chrome, and every method below is defined on it.
type Geometry struct {
	// Width and Height are the surface the grid was measured against.
	Width, Height int
	// Cols and Rows are how many tiles fit across and down. Both are zero when
	// nothing fits.
	Cols, Rows int

	// colWidths spans the full width. rowHeights spans the full height in normal
	// mode; compact tiles keep their two content rows and leave spare rows below
	// the grid. They are private and copied out
	// through Tile so a caller cannot resize one column and leave the grid unable
	// to add up.
	colWidths  []int
	rowHeights []int
}

// Measure resolves a terminal size into a grid.
//
// The two dimensions are decided independently and identically: take what is
// left after the chrome, divide by the target tile size, and never answer zero
// while there is any room at all. Reducing to a single tile on a small terminal
// is the deliberate behavior rather than a degenerate case — 80 by 24 is one
// tile, and that is correct, because two 40-cell previews side by side would
// show half a line of terminal output each.
//
// Spare cells are spread across the leading columns and rows one at a time, so a
// 160-cell grid of three columns comes out 54, 53, 53 rather than 53, 53, 53 and
// a one-cell gutter of nothing at the right edge.
func Measure(width, height int) Geometry {
	return MeasureMode(width, height, false)
}

// MeasureMode supplies the same geometry to rendering and navigation in either view.
func MeasureMode(width, height int, compact bool) Geometry {
	g := Geometry{Width: width, Height: height}
	if width <= 0 || height <= 0 {
		return g
	}
	gridHeight := height - ChromeLines
	if gridHeight <= 0 {
		return g
	}
	g.Cols = fitCount(width, TileMinWidth)
	tileHeight := TileMinHeight
	if compact {
		tileHeight = CompactTileMinHeight
	}
	g.Rows = fitCount(gridHeight, tileHeight)
	g.colWidths = distribute(width, g.Cols)
	if compact {
		g.rowHeights = make([]int, g.Rows)
		for i := range g.rowHeights {
			g.rowHeights[i] = min(gridHeight, CompactTileMinHeight)
		}
	} else {
		g.rowHeights = distribute(gridHeight, g.Rows)
	}
	return g
}

// fitCount is how many tiles of at least min cells fit in available cells, and
// never fewer than one while there is anything to fill.
//
// The clamp to one is the "reduce to one tile on smaller terminals" rule. It is
// here rather than at the two call sites because a grid that reduced its columns
// but not its rows — or the reverse — would be a grid whose two dimensions
// disagreed about whether the terminal was too small.
func fitCount(available, min int) int {
	if available <= 0 {
		return 0
	}
	if n := available / min; n > 1 {
		return n
	}
	return 1
}

// distribute splits total into n extents that sum exactly to total, giving the
// remainder to the leading extents one cell each.
func distribute(total, n int) []int {
	if n <= 0 || total <= 0 {
		return nil
	}
	base := total / n
	extra := total % n
	out := make([]int, n)
	for i := range out {
		out[i] = base
		if i < extra {
			out[i]++
		}
	}
	return out
}

// Capacity is how many tiles one page holds. Zero means the terminal has no room
// for a grid at all, and every caller has to be defined on that: it is what a
// three-row terminal answers, and dividing by it is how a navigator crashes.
func (g Geometry) Capacity() int { return g.Cols * g.Rows }

// PageCount is how many pages count sessions need.
//
// Zero sessions is zero pages, and so is any number of sessions on a grid with
// no capacity: there is no page that could show one, and reporting `page 1 of 1`
// over an empty grid would be a claim the surface cannot back up.
func (g Geometry) PageCount(count int) int {
	capacity := g.Capacity()
	if count <= 0 || capacity <= 0 {
		return 0
	}
	return (count + capacity - 1) / capacity
}

// PageOf is the zero-based page an index falls on. It is the whole of "keep the
// selection visible": the page is DERIVED from the selection on every render
// rather than held as its own state, so there is no second value that could
// drift out of agreement with the cursor.
func (g Geometry) PageOf(index int) int {
	capacity := g.Capacity()
	if capacity <= 0 || index <= 0 {
		return 0
	}
	return index / capacity
}

// PageStart is the index of the first session on a page.
func (g Geometry) PageStart(page int) int {
	if page <= 0 {
		return 0
	}
	return page * g.Capacity()
}

// Tile returns where the pos'th tile of a page sits, in row-major order, and
// false when the grid has no such slot.
func (g Geometry) Tile(pos int) (Tile, bool) {
	if pos < 0 || pos >= g.Capacity() {
		return Tile{}, false
	}
	row, col := pos/g.Cols, pos%g.Cols
	t := Tile{X: 0, Y: gridTop, Width: g.colWidths[col], Height: g.rowHeights[row]}
	for i := 0; i < col; i++ {
		t.X += g.colWidths[i]
	}
	for i := 0; i < row; i++ {
		t.Y += g.rowHeights[i]
	}
	return t, true
}

// TileAt is the page position of the tile covering a cell, and false for a cell
// no tile covers: the chrome rows, and the spare rows compact mode leaves under
// the grid.
//
// It reads the same extents Tile does, so a pointer resolves to the tile that
// was drawn under it. Whether a SESSION is in that slot is the caller's to
// answer; the last page usually has slots with nothing in them.
func (g Geometry) TileAt(x, y int) (int, bool) {
	if x < 0 || y < gridTop {
		return 0, false
	}
	col, ok := extentAt(g.colWidths, x)
	if !ok {
		return 0, false
	}
	row, ok := extentAt(g.rowHeights, y-gridTop)
	if !ok {
		return 0, false
	}
	return row*g.Cols + col, true
}

// extentAt is which of a run of adjoining extents an offset falls in.
func extentAt(extents []int, offset int) (int, bool) {
	for i, extent := range extents {
		if offset < extent {
			return i, true
		}
		offset -= extent
	}
	return 0, false
}
