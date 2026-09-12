package overview

import (
	"fmt"
	"testing"
)

// grid3x3 is the 160x45 terminal resolved: three columns, three rows, nine tiles
// to a page. Every spatial case worth naming — an interior tile, a corner, an
// incomplete row, a second page — exists on it.
func grid3x3(t *testing.T) Geometry {
	t.Helper()
	g := Measure(160, 45)
	if g.Cols != 3 || g.Rows != 3 {
		t.Fatalf("the fixture grid is %dx%d, want 3x3", g.Cols, g.Rows)
	}
	return g
}

// TestSpatialMovementOnAFullPage walks the arrows over a full nine-tile page.
// The layout is
//
//	0 1 2
//	3 4 5
//	6 7 8
//
// and every answer below is read off it.
func TestSpatialMovementOnAFullPage(t *testing.T) {
	g := grid3x3(t)
	const count = 9
	cases := []struct {
		from int
		move Move
		want int
	}{
		{4, MoveLeft, 3}, {4, MoveRight, 5}, {4, MoveUp, 1}, {4, MoveDown, 7},
		{0, MoveLeft, 0}, {0, MoveUp, 0},
		{2, MoveRight, 2}, {8, MoveRight, 8}, {8, MoveDown, 8},
		{6, MoveLeft, 6},
		// Horizontal movement stays inside its row: the end of a row is a wall,
		// not a wrap onto the next one.
		{2, MoveRight, 2}, {3, MoveLeft, 3}, {5, MoveRight, 5},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d-%s", tc.from, tc.move), func(t *testing.T) {
			if got := Navigate(g, count, tc.from, tc.move); got != tc.want {
				t.Errorf("%s from %d = %d, want %d", tc.move, tc.from, got, tc.want)
			}
		})
	}
}

// TestHorizontalMovementNeverChangesRow is the rule stated as a property rather
// than as a table, across every page fill a three-column grid can have.
func TestHorizontalMovementNeverChangesRow(t *testing.T) {
	g := grid3x3(t)
	for count := 1; count <= 20; count++ {
		for from := 0; from < count; from++ {
			for _, m := range []Move{MoveLeft, MoveRight} {
				got := Navigate(g, count, from, m)
				if rowOf(g, got) != rowOf(g, from) || g.PageOf(got) != g.PageOf(from) {
					t.Fatalf("count %d: %s from %d landed on %d, which is row %d of page %d rather than row %d of page %d",
						count, m, from, got, rowOf(g, got), g.PageOf(got), rowOf(g, from), g.PageOf(from))
				}
			}
		}
	}
}

func rowOf(g Geometry, index int) int {
	return (index - g.PageStart(g.PageOf(index))) / g.Cols
}

// TestAnIncompleteTargetRowTakesItsLastTile. The last row of the last page is
// usually short, and `j` from the tile above its gap must reach the row rather
// than refuse to move.
func TestAnIncompleteTargetRowTakesItsLastTile(t *testing.T) {
	g := grid3x3(t)
	// Seven sessions: rows are [0 1 2] [3 4 5] [6 _ _].
	const count = 7
	for _, tc := range []struct {
		from int
		move Move
		want int
	}{
		{3, MoveDown, 6}, // straight down onto the only tile in the row
		{4, MoveDown, 6}, // the target cell is empty; take the row's last tile
		{5, MoveDown, 6},
		{6, MoveRight, 6}, // nothing to the right inside the row
		{6, MoveUp, 3},
	} {
		if got := Navigate(g, count, tc.from, tc.move); got != tc.want {
			t.Errorf("%s from %d with %d sessions = %d, want %d", tc.move, tc.from, count, got, tc.want)
		}
	}
}

// TestAnEmptyTargetRowIsNotATarget. With four sessions the third row holds
// nothing at all, and moving into it would put the cursor on a tile the renderer
// draws as blank.
func TestAnEmptyTargetRowIsNotATarget(t *testing.T) {
	g := grid3x3(t)
	const count = 4 // rows are [0 1 2] [3 _ _] [_ _ _]
	if got := Navigate(g, count, 3, MoveDown); got != 3 {
		t.Errorf("down from 3 into an empty row = %d, want it to stay on 3", got)
	}
	if got := Navigate(g, count, 1, MoveDown); got != 3 {
		t.Errorf("down from 1 = %d, want the second row's only tile", got)
	}
}

// TestSequentialMovementCrossesPages. Tab is about the list, not the grid.
func TestSequentialMovementCrossesPages(t *testing.T) {
	g := grid3x3(t)
	const count = 20
	if got := Navigate(g, count, 8, MoveNext); got != 9 {
		t.Errorf("Tab off the end of page one = %d, want 9", got)
	}
	if got := Navigate(g, count, 9, MovePrev); got != 8 {
		t.Errorf("Shift+Tab off the start of page two = %d, want 8", got)
	}
	if got := Navigate(g, count, count-1, MoveNext); got != count-1 {
		t.Errorf("Tab at the end of the list = %d, want it clamped to %d", got, count-1)
	}
	if got := Navigate(g, count, 0, MovePrev); got != 0 {
		t.Errorf("Shift+Tab at the start of the list = %d, want it clamped to 0", got)
	}
}

// TestPageMovementUsesTheGridsOwnCapacity. This is the shared-arithmetic claim:
// PgDn moves by exactly the number of tiles the renderer draws, so it never
// skips a session and never repeats one.
func TestPageMovementUsesTheGridsOwnCapacity(t *testing.T) {
	for _, size := range [][2]int{{60, 15}, {100, 24}, {120, 30}, {160, 45}} {
		g := Measure(size[0], size[1])
		capacity := g.Capacity()
		count := capacity*3 + 1
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			if got := Navigate(g, count, 0, MovePageDown); got != capacity {
				t.Errorf("PgDn from 0 = %d, want the grid's own capacity %d", got, capacity)
			}
			if got := Navigate(g, count, capacity, MovePageUp); got != 0 {
				t.Errorf("PgUp back = %d, want 0", got)
			}
			if got := Navigate(g, count, count-1, MovePageDown); got != count-1 {
				t.Errorf("PgDn at the end = %d, want it clamped to %d", got, count-1)
			}
			if got := Navigate(g, count, 0, MovePageUp); got != 0 {
				t.Errorf("PgUp at the start = %d, want it clamped to 0", got)
			}
		})
	}
}

// TestHomeAndEndAreGlobal. They name the first and last SESSION, not the first
// and last tile on the page.
func TestHomeAndEndAreGlobal(t *testing.T) {
	g := grid3x3(t)
	const count = 20
	if got := Navigate(g, count, 14, MoveFirst); got != 0 {
		t.Errorf("Home from page two = %d, want 0", got)
	}
	if got := Navigate(g, count, 2, MoveLast); got != count-1 {
		t.Errorf("End from page one = %d, want %d", got, count-1)
	}
}

// TestNavigationKeepsTheSelectionVisible. The page is derived from the
// selection, so the property to assert is that every answer is a real index with
// a real tile on the page it falls on.
func TestNavigationKeepsTheSelectionVisible(t *testing.T) {
	moves := []Move{MoveLeft, MoveRight, MoveUp, MoveDown, MoveNext, MovePrev, MovePageUp, MovePageDown, MoveFirst, MoveLast}
	for _, size := range [][2]int{{60, 15}, {80, 24}, {100, 24}, {120, 30}, {160, 45}, {40, 9}} {
		g := Measure(size[0], size[1])
		for _, count := range []int{1, 2, 5, 9, 10, 23, 100} {
			for from := 0; from < count; from++ {
				for _, m := range moves {
					got := Navigate(g, count, from, m)
					if got < 0 || got >= count {
						t.Fatalf("%dx%d count %d: %s from %d = %d, which is not a session",
							size[0], size[1], count, m, from, got)
					}
					pos := got - g.PageStart(g.PageOf(got))
					if _, ok := g.Tile(pos); !ok {
						t.Fatalf("%dx%d count %d: %s from %d = %d, which has no tile (slot %d of %d)",
							size[0], size[1], count, m, from, got, pos, g.Capacity())
					}
				}
			}
		}
	}
}

// TestNavigationIsTotal. An empty list, a selection outside it, and a terminal
// with no grid at all are each reachable by ordinary events — the last session
// closing, a session closing under the cursor, a window dragged small — and none
// of them may panic or answer with an index that does not exist.
func TestNavigationIsTotal(t *testing.T) {
	moves := []Move{MoveLeft, MoveRight, MoveUp, MoveDown, MoveNext, MovePrev, MovePageUp, MovePageDown, MoveFirst, MoveLast, Move(99)}

	t.Run("no sessions", func(t *testing.T) {
		g := grid3x3(t)
		for _, m := range moves {
			if got := Navigate(g, 0, 0, m); got != 0 {
				t.Errorf("%s over an empty list = %d, want 0", m, got)
			}
			if got := Navigate(g, -3, 5, m); got != 0 {
				t.Errorf("%s over a negative count = %d, want 0", m, got)
			}
		}
	})

	t.Run("selection out of range", func(t *testing.T) {
		g := grid3x3(t)
		for _, m := range moves {
			for _, from := range []int{-5, 12, 1 << 20} {
				got := Navigate(g, 4, from, m)
				if got < 0 || got >= 4 {
					t.Errorf("%s from an out-of-range %d = %d, want a valid index", m, from, got)
				}
			}
		}
	})

	t.Run("no grid", func(t *testing.T) {
		g := Measure(80, 3)
		if g.Capacity() != 0 {
			t.Fatalf("the fixture grid has capacity %d, want 0", g.Capacity())
		}
		for _, m := range moves {
			got := Navigate(g, 6, 2, m)
			if got < 0 || got >= 6 {
				t.Fatalf("%s on a grid with no tiles = %d, want a valid index", m, got)
			}
		}
		// Sequential moves still work — they are about the list, and the list
		// exists even when nothing can be drawn.
		if got := Navigate(g, 6, 2, MoveNext); got != 3 {
			t.Errorf("Tab with no grid = %d, want 3", got)
		}
		if got := Navigate(g, 6, 2, MoveLast); got != 5 {
			t.Errorf("End with no grid = %d, want 5", got)
		}
		// Spatial and page moves have no meaning without tiles and stay put.
		for _, m := range []Move{MoveLeft, MoveRight, MoveUp, MoveDown, MovePageUp, MovePageDown} {
			if got := Navigate(g, 6, 2, m); got != 2 {
				t.Errorf("%s with no grid = %d, want it to stay on 2", m, got)
			}
		}
	})
}

// TestSingleTileGridIsNavigableBySequentialMovesOnly. On a 60x15 terminal every
// tile is its own page, so the arrows have nowhere to go and Tab is the whole
// navigation.
func TestSingleTileGridIsNavigableBySequentialMovesOnly(t *testing.T) {
	g := Measure(60, 15)
	if g.Capacity() != 1 {
		t.Fatalf("capacity = %d, want 1", g.Capacity())
	}
	const count = 5
	for _, m := range []Move{MoveLeft, MoveRight, MoveUp, MoveDown} {
		if got := Navigate(g, count, 2, m); got != 2 {
			t.Errorf("%s on a one-tile grid = %d, want it to stay on 2", m, got)
		}
	}
	if got := Navigate(g, count, 2, MoveNext); got != 3 {
		t.Errorf("Tab = %d, want 3", got)
	}
	if got := Navigate(g, count, 2, MovePageDown); got != 3 {
		t.Errorf("PgDn with a one-tile page = %d, want 3", got)
	}
}

// TestMoveStringNamesEveryMove keeps the diagnostic vocabulary complete, since
// every failure message above is written in it.
func TestMoveStringNamesEveryMove(t *testing.T) {
	seen := map[string]bool{}
	for m := MoveLeft; m <= MoveLast; m++ {
		name := m.String()
		if name == "unknown" {
			t.Errorf("move %d has no name", int(m))
		}
		if seen[name] {
			t.Errorf("two moves are both called %q", name)
		}
		seen[name] = true
	}
	if got := Move(99).String(); got != "unknown" {
		t.Errorf("an undefined move is called %q, want \"unknown\"", got)
	}
}
