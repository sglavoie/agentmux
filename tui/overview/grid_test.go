package overview

import (
	"fmt"
	"testing"
)

// TestMeasureAtTheTerminalsWeShipFor pins the grid at the sizes an operator
// actually runs. They are named rather than derived so a change to the tile
// minimum shows up here as a diff about real terminals instead of as arithmetic
// that still agrees with itself.
func TestMeasureAtTheTerminalsWeShipFor(t *testing.T) {
	cases := []struct {
		w, h             int
		cols, rows       int
		capacity         int
		tileW0, tileH0   int
		lastTileW, lastH int
	}{
		{w: 60, h: 15, cols: 1, rows: 1, capacity: 1, tileW0: 60, tileH0: 12, lastTileW: 60, lastH: 12},
		{w: 80, h: 24, cols: 1, rows: 1, capacity: 1, tileW0: 80, tileH0: 21, lastTileW: 80, lastH: 21},
		{w: 100, h: 24, cols: 2, rows: 1, capacity: 2, tileW0: 50, tileH0: 21, lastTileW: 50, lastH: 21},
		{w: 120, h: 30, cols: 2, rows: 2, capacity: 4, tileW0: 60, tileH0: 14, lastTileW: 60, lastH: 13},
		{w: 160, h: 45, cols: 3, rows: 3, capacity: 9, tileW0: 54, tileH0: 14, lastTileW: 53, lastH: 14},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%dx%d", tc.w, tc.h), func(t *testing.T) {
			g := Measure(tc.w, tc.h)
			if g.Cols != tc.cols || g.Rows != tc.rows {
				t.Fatalf("grid is %dx%d tiles, want %dx%d", g.Cols, g.Rows, tc.cols, tc.rows)
			}
			if got := g.Capacity(); got != tc.capacity {
				t.Fatalf("capacity = %d, want %d", got, tc.capacity)
			}
			first, ok := g.Tile(0)
			if !ok {
				t.Fatal("the first tile does not exist")
			}
			if first.Width != tc.tileW0 || first.Height != tc.tileH0 {
				t.Errorf("first tile is %dx%d, want %dx%d", first.Width, first.Height, tc.tileW0, tc.tileH0)
			}
			if first.X != 0 || first.Y != ChromeLines-hintRows {
				t.Errorf("first tile sits at (%d,%d), want (0,%d)", first.X, first.Y, ChromeLines-hintRows)
			}
			last, ok := g.Tile(g.Capacity() - 1)
			if !ok {
				t.Fatal("the last tile does not exist")
			}
			if last.Width != tc.lastTileW || last.Height != tc.lastH {
				t.Errorf("last tile is %dx%d, want %dx%d", last.Width, last.Height, tc.lastTileW, tc.lastH)
			}
		})
	}
}

// TestTilesCoverTheGridExactly is the property the renderer's row-by-row
// composition depends on: tiles are edge to edge with no gutters and no overlap,
// so a grid row is the concatenation of one line from each tile in it.
func TestTilesCoverTheGridExactly(t *testing.T) {
	for w := 1; w <= 200; w += 7 {
		for h := 1; h <= 60; h += 3 {
			g := Measure(w, h)
			if g.Capacity() == 0 {
				continue
			}
			// Every row of tiles spans the full width.
			for row := 0; row < g.Rows; row++ {
				sum, x := 0, 0
				for col := 0; col < g.Cols; col++ {
					tile, ok := g.Tile(row*g.Cols + col)
					if !ok {
						t.Fatalf("%dx%d: tile (%d,%d) is missing", w, h, row, col)
					}
					if tile.X != x {
						t.Fatalf("%dx%d: tile (%d,%d) starts at x=%d, want %d", w, h, row, col, tile.X, x)
					}
					x += tile.Width
					sum += tile.Width
				}
				if sum != w {
					t.Fatalf("%dx%d: row %d spans %d cells, want %d", w, h, row, sum, w)
				}
			}
			// Every column of tiles spans the full grid height and starts under
			// the chrome.
			for col := 0; col < g.Cols; col++ {
				sum, y := 0, ChromeLines-hintRows
				for row := 0; row < g.Rows; row++ {
					tile, _ := g.Tile(row*g.Cols + col)
					if tile.Y != y {
						t.Fatalf("%dx%d: tile (%d,%d) starts at y=%d, want %d", w, h, row, col, tile.Y, y)
					}
					y += tile.Height
					sum += tile.Height
				}
				if want := h - ChromeLines; sum != want {
					t.Fatalf("%dx%d: column %d spans %d rows, want %d", w, h, col, sum, want)
				}
				if y > h-hintRows {
					t.Fatalf("%dx%d: column %d runs to row %d, past the hints row at %d", w, h, col, y, h-hintRows)
				}
			}
		}
	}
}

// TestSpareSpaceIsDistributedEvenly. The leftover cells go one at a time to the
// leading columns and rows, so no two tiles differ by more than a cell and no
// gutter of nothing is left at an edge.
func TestSpareSpaceIsDistributedEvenly(t *testing.T) {
	for w := 1; w <= 200; w++ {
		for _, h := range []int{15, 30, 45, 60} {
			g := Measure(w, h)
			if g.Capacity() == 0 {
				continue
			}
			minW, maxW := 1<<30, 0
			for col := 0; col < g.Cols; col++ {
				tile, _ := g.Tile(col)
				if tile.Width < minW {
					minW = tile.Width
				}
				if tile.Width > maxW {
					maxW = tile.Width
				}
			}
			if maxW-minW > 1 {
				t.Fatalf("%dx%d: tile widths range %d..%d, want a spread of at most one cell", w, h, minW, maxW)
			}
			minH, maxH := 1<<30, 0
			for row := 0; row < g.Rows; row++ {
				tile, _ := g.Tile(row * g.Cols)
				if tile.Height < minH {
					minH = tile.Height
				}
				if tile.Height > maxH {
					maxH = tile.Height
				}
			}
			if maxH-minH > 1 {
				t.Fatalf("%dx%d: tile heights range %d..%d, want a spread of at most one row", w, h, minH, maxH)
			}
		}
	}
}

// Compact cards have exactly two content rows plus their borders. A remainder
// in the terminal height belongs below the grid, never inside one card.
func TestCompactTilesKeepFixedHeight(t *testing.T) {
	for _, height := range []int{4, 6, 7, 8, 10, 11, 22, 45} {
		g := MeasureMode(120, height, true)
		if g.Capacity() == 0 {
			t.Fatalf("height %d has no compact tile", height)
		}
		wantHeight := min(height-ChromeLines, CompactTileMinHeight)
		for row := 0; row < g.Rows; row++ {
			tile, _ := g.Tile(row * g.Cols)
			if tile.Height != wantHeight || tile.Y != gridTop+row*wantHeight {
				t.Errorf("height %d row %d: tile at y=%d, height=%d; want y=%d, height=%d",
					height, row, tile.Y, tile.Height, gridTop+row*wantHeight, wantHeight)
			}
		}
		if g.Rows*wantHeight > height-ChromeLines {
			t.Fatalf("height %d: compact grid overlaps the hints", height)
		}
	}
}

// TestTileCountBoundaries walks the exact cells where a column and a row appear.
// A fit test that only sampled round numbers would not notice an off-by-one in
// the division.
func TestTileCountBoundaries(t *testing.T) {
	for _, tc := range []struct {
		width, cols int
	}{
		{1, 1}, {47, 1}, {48, 1}, {95, 1}, {96, 2}, {143, 2}, {144, 3}, {191, 3}, {192, 4},
	} {
		if got := Measure(tc.width, 60).Cols; got != tc.cols {
			t.Errorf("width %d fits %d columns, want %d", tc.width, got, tc.cols)
		}
	}
	// Height boundaries are stated in terms of the terminal, chrome included,
	// because that is the number a caller has.
	for _, tc := range []struct {
		height, rows int
	}{
		{4, 1}, {14, 1}, {15, 1}, {26, 1}, {27, 2}, {38, 2}, {39, 3},
	} {
		if got := Measure(60, tc.height).Rows; got != tc.rows {
			t.Errorf("height %d fits %d rows, want %d", tc.height, got, tc.rows)
		}
	}
}

// TestASingleTileSurvivesASmallTerminal. Reducing to one tile is the documented
// behavior, not a degenerate case: a 40-cell terminal shows one cramped preview
// rather than none.
func TestASingleTileSurvivesASmallTerminal(t *testing.T) {
	for _, tc := range [][2]int{{40, 10}, {20, 8}, {10, 5}, {5, 4}, {1, 4}} {
		g := Measure(tc[0], tc[1])
		if g.Capacity() != 1 {
			t.Errorf("%dx%d has capacity %d, want exactly one tile", tc[0], tc[1], g.Capacity())
		}
		tile, ok := g.Tile(0)
		if !ok {
			t.Fatalf("%dx%d has no tile 0", tc[0], tc[1])
		}
		if tile.Width != tc[0] || tile.Height != tc[1]-ChromeLines {
			t.Errorf("%dx%d tile is %dx%d, want %dx%d", tc[0], tc[1], tile.Width, tile.Height, tc[0], tc[1]-ChromeLines)
		}
	}
}

// TestATerminalTooSmallForTheChromeHasNoGrid. Three rows is exactly the chrome,
// and everything below it is a surface with nowhere to put a tile. Capacity zero
// is what the navigator and the renderer are both defined against.
func TestATerminalTooSmallForTheChromeHasNoGrid(t *testing.T) {
	for _, tc := range [][2]int{{80, 3}, {80, 2}, {80, 1}, {80, 0}, {0, 40}, {-4, 40}, {80, -1}} {
		g := Measure(tc[0], tc[1])
		if g.Capacity() != 0 {
			t.Errorf("%dx%d has capacity %d, want 0", tc[0], tc[1], g.Capacity())
		}
		if _, ok := g.Tile(0); ok {
			t.Errorf("%dx%d produced a tile", tc[0], tc[1])
		}
		if got := g.PageCount(9); got != 0 {
			t.Errorf("%dx%d reports %d pages for nine sessions, want 0", tc[0], tc[1], got)
		}
		if got := g.PageOf(7); got != 0 {
			t.Errorf("%dx%d puts index 7 on page %d, want 0", tc[0], tc[1], got)
		}
	}
}

// TestPagination counts pages and locates indexes on them.
func TestPagination(t *testing.T) {
	g := Measure(160, 45) // 3x3, capacity 9
	if g.Capacity() != 9 {
		t.Fatalf("capacity = %d, want 9", g.Capacity())
	}
	for _, tc := range []struct{ count, pages int }{{0, 0}, {1, 1}, {9, 1}, {10, 2}, {18, 2}, {19, 3}} {
		if got := g.PageCount(tc.count); got != tc.pages {
			t.Errorf("%d sessions need %d pages, want %d", tc.count, got, tc.pages)
		}
	}
	for _, tc := range []struct{ index, page int }{{0, 0}, {8, 0}, {9, 1}, {17, 1}, {18, 2}} {
		if got := g.PageOf(tc.index); got != tc.page {
			t.Errorf("index %d is on page %d, want %d", tc.index, got, tc.page)
		}
		if got := g.PageStart(tc.page); got != tc.page*9 {
			t.Errorf("page %d starts at %d, want %d", tc.page, got, tc.page*9)
		}
	}
}

// TestTileRejectsSlotsOutsideThePage. Row-major order is a contract the
// navigator reads too, so an out-of-range slot must answer false rather than
// wrap onto another row.
func TestTileRejectsSlotsOutsideThePage(t *testing.T) {
	g := Measure(160, 45)
	for _, pos := range []int{-1, 9, 10, 1 << 20} {
		if _, ok := g.Tile(pos); ok {
			t.Errorf("slot %d exists on a nine-tile page", pos)
		}
	}
	// Row-major: slot 3 is the first tile of the second row.
	third, _ := g.Tile(2)
	fourth, _ := g.Tile(3)
	if fourth.X != 0 || fourth.Y <= third.Y {
		t.Errorf("slot 3 sits at (%d,%d) beside slot 2 at (%d,%d); the page is not row-major",
			fourth.X, fourth.Y, third.X, third.Y)
	}
}
