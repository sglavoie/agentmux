package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
)

func click(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// TestTileAtAgreesWithTile holds the hit test to the layout: every cell of
// every tile resolves to that tile, and every cell outside the grid to none.
func TestTileAtAgreesWithTile(t *testing.T) {
	for _, compact := range []bool{false, true} {
		for _, size := range [][2]int{{80, 24}, {100, 30}, {160, 45}, {211, 60}, {10, 3}, {1, 1}} {
			g := MeasureMode(size[0], size[1], compact)
			owner := map[[2]int]int{}
			for pos := 0; pos < g.Capacity(); pos++ {
				tile, _ := g.Tile(pos)
				for y := tile.Y; y < tile.Y+tile.Height; y++ {
					for x := tile.X; x < tile.X+tile.Width; x++ {
						owner[[2]int{x, y}] = pos
					}
				}
			}
			for y := -1; y <= size[1]; y++ {
				for x := -1; x <= size[0]; x++ {
					want, covered := owner[[2]int{x, y}]
					got, ok := g.TileAt(x, y)
					if ok != covered || (ok && got != want) {
						t.Fatalf("compact=%v %dx%d: TileAt(%d,%d) = %d,%v, want %d,%v", compact, size[0], size[1], x, y, got, ok, want, covered)
					}
				}
			}
		}
	}
}

func TestClickingATileFocusesTheSessionDrawnThere(t *testing.T) {
	for _, compact := range []bool{false, true} {
		for _, selected := range []int{0, 19} {
			base, _ := atSize(t, viewSessions(20), selected, 160, 45)
			if compact {
				base = press(t, base, "c")
			}
			g := MeasureMode(base.width, base.height, base.compact)
			pageStart := g.PageStart(g.PageOf(base.selected))
			for pos := 0; pos < g.Capacity(); pos++ {
				m, r := base, &recorder{}
				m.deps.focus = func(tabID int) tea.Cmd {
					r.focus = append(r.focus, tabID)
					return func() tea.Msg { return nil }
				}
				tile, _ := g.Tile(pos)
				next, cmd := send(t, m, click(tile.X+tile.Width-1, tile.Y+tile.Height-1))
				index := pageStart + pos
				if index >= len(m.sessions) {
					if cmd != nil || len(r.focus) != 0 || next.selected != m.selected {
						t.Fatalf("compact=%v pos=%d: a click on an empty slot acted", compact, pos)
					}
					continue
				}
				target := m.sessions[index]
				if len(r.focus) != 1 || r.focus[0] != target.TabID || next.focusTabID != target.TabID {
					t.Fatalf("compact=%v pos=%d: focused %v, want tab %d", compact, pos, r.focus, target.TabID)
				}
				if next.selected != index || next.selectedTabID != target.TabID {
					t.Fatalf("compact=%v pos=%d: the click did not select its tile", compact, pos)
				}
				if quits(t, cmd) {
					t.Fatal("a click must wait for the focus to be confirmed before exiting")
				}
				if _, cmd = send(t, next, commands.FocusDoneMsg{}); !quits(t, cmd) {
					t.Fatal("a confirmed click focus did not exit")
				}
			}
		}
	}
}

func TestClicksThatAreNotATileDoNothing(t *testing.T) {
	m, r := atSize(t, viewSessions(20), 0, 160, 45)
	tile, _ := MeasureMode(160, 45, false).Tile(1)
	inert := []tea.Msg{
		click(5, 0),  // title
		click(5, 1),  // feedback
		click(5, 44), // hints
		tea.MouseClickMsg{X: tile.X, Y: tile.Y, Button: tea.MouseRight},
		tea.MouseClickMsg{X: tile.X, Y: tile.Y, Button: tea.MouseLeft, Mod: tea.ModShift},
	}
	for _, msg := range inert {
		next, cmd := send(t, m, msg)
		if cmd != nil || len(r.focus) != 0 || next.selected != m.selected {
			t.Fatalf("%#v acted on the grid", msg)
		}
	}

	// A pending focus is single-flight for the pointer as it is for the keys.
	m = only(t, m, click(tile.X, tile.Y))
	next, cmd := send(t, m, click(0, gridTop))
	if cmd != nil || len(r.focus) != 1 || next.selected != 1 {
		t.Fatal("a second click retargeted or duplicated a pending focus")
	}
}

// TestTheMouseBelongsToTheGridOnly is the overlay rule for the pointer: under a
// modal the terminal is not asked to report the mouse, and an event already on
// its way reaches nothing underneath.
func TestTheMouseBelongsToTheGridOnly(t *testing.T) {
	m, r := atSize(t, viewSessions(20), 0, 160, 45)
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("the grid does not ask for the mouse")
	}
	tile, _ := MeasureMode(160, 45, false).Tile(1)
	for _, opener := range []string{"?", "a"} {
		over := press(t, m, opener)
		if over.View().MouseMode != tea.MouseModeNone {
			t.Fatalf("the mouse is still reported under the %q overlay", opener)
		}
		for _, msg := range []tea.Msg{click(tile.X, tile.Y), tea.MouseWheelMsg{X: tile.X, Y: tile.Y, Button: tea.MouseWheelDown}} {
			next, cmd := send(t, over, msg)
			if cmd != nil || len(r.focus) != 0 || next.selected != over.selected {
				t.Fatalf("%#v reached the grid under the %q overlay", msg, opener)
			}
		}
	}
}

func TestTheWheelTurnsThePageAndNothingElse(t *testing.T) {
	wheel := func(button tea.MouseButton) tea.MouseWheelMsg {
		return tea.MouseWheelMsg{X: 10, Y: 10, Button: button}
	}
	m, r := atSize(t, viewSessions(20), 0, 160, 45)
	g := MeasureMode(160, 45, false)
	if m = only(t, m, wheel(tea.MouseWheelUp)); m.selected != 0 {
		t.Fatal("wheeling up from the first page moved the cursor")
	}
	for page := 1; page < g.PageCount(20); page++ {
		if m = only(t, m, wheel(tea.MouseWheelDown)); g.PageOf(m.selected) != page {
			t.Fatalf("wheel down landed on page %d, want %d", g.PageOf(m.selected), page)
		}
	}
	last := m.selected
	if m = only(t, m, wheel(tea.MouseWheelDown)); m.selected != last {
		t.Fatal("wheeling down from the last page moved the cursor")
	}
	if m = only(t, m, wheel(tea.MouseWheelUp)); g.PageOf(m.selected) != g.PageCount(20)-2 {
		t.Fatal("wheel up did not show the previous page")
	}

	// One page: there is nothing to turn, so the cursor stays where it is.
	one, _ := atSize(t, viewSessions(4), 1, 160, 45)
	if one = only(t, one, wheel(tea.MouseWheelDown)); one.selected != 1 {
		t.Fatal("a wheel over a single page moved the cursor")
	}
	if len(r.focus) != 0 {
		t.Fatal("the wheel focused a session")
	}
}
