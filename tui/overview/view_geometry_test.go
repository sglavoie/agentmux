package overview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// TestRenderClampsASelectionItCannotHonor. The selection is the caller's and the
// session list can shrink under it between one frame and the next.
func TestRenderClampsASelectionItCannotHonor(t *testing.T) {
	sessions := viewSessions(3)
	for _, selected := range []int{-7, 3, 1 << 20} {
		in := viewInput(160, 45, sessions)
		in.Selected = selected
		joined := strings.Join(plain(renderLines(t, in)), "\n")
		if strings.Count(joined, "┏") != 1 {
			t.Errorf("selection %d marked %d tiles, want exactly 1", selected, strings.Count(joined, "┏"))
		}
	}
	// And with no sessions at all there is nothing to mark and nothing to panic on.
	in := viewInput(160, 45, nil)
	in.Selected = 5
	renderLines(t, in)
}

// TestRenderIsPure. Same input, same bytes — and the input is not modified on
// the way through. The clock is a field, so a second render a minute later still
// says the same thing.
func TestRenderIsPure(t *testing.T) {
	sessions := viewSessions(5)
	in := viewInput(120, 30, sessions)
	in.Previews = map[session.PaneTarget]Preview{
		paneTargetForWindow(sessions[0].WindowID): {Content: dashboard.InspectorReady, Output: dashboard.SplitOutput("one\ntwo\n")},
	}
	in.Selected = 2

	first := Render(in)
	for i := 0; i < 3; i++ {
		if got := Render(in); got != first {
			t.Fatalf("render %d differs from the first", i+2)
		}
	}
	if !strings.Contains(ansi.Strip(first), sessions[0].Base) {
		t.Fatalf("the fixture rendered nothing recognizable:\n%s", ansi.Strip(first))
	}
	// The caller's slice is untouched, so rendering cannot reorder the model's
	// own live set as a side effect.
	if got := viewSessions(5); !sameOrder(got, sessions) {
		t.Error("Render reordered the session slice it was given")
	}
}

func sameOrder(a, b []session.RunningSession) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key {
			return false
		}
	}
	return true
}

// TestGeometryAndNavigationAgree is the review session 2 asks for, written down.
// The renderer draws the page the navigator moves within, and both derive it from
// one Geometry — so the property to assert is that every reachable selection is
// on the page the renderer would draw and inside a tile it would fill.
func TestGeometryAndNavigationAgree(t *testing.T) {
	moves := []Move{MoveLeft, MoveRight, MoveUp, MoveDown, MoveNext, MovePrev, MovePageUp, MovePageDown, MoveFirst, MoveLast}
	for _, size := range [][2]int{{60, 15}, {80, 24}, {100, 24}, {120, 30}, {160, 45}} {
		g := Measure(size[0], size[1])
		for _, count := range []int{1, 4, 9, 25} {
			sessions := viewSessions(count)
			selected := 0
			for step := 0; step < 40; step++ {
				selected = Navigate(g, count, selected, moves[step%len(moves)])

				in := viewInput(size[0], size[1], sessions)
				in.Selected = selected
				lines := plain(renderLines(t, in))

				pos := selected - g.PageStart(g.PageOf(selected))
				tile, ok := g.Tile(pos)
				if !ok {
					t.Fatalf("%dx%d/%d: selection %d has no tile", size[0], size[1], count, selected)
				}
				// The renderer marked exactly the tile the navigator computed.
				marked := 0
				for y, line := range lines {
					for x, r := range []rune(line) {
						if r != '┏' {
							continue
						}
						marked++
						if x != tile.X || y != tile.Y {
							t.Fatalf("%dx%d/%d: selection %d is drawn at (%d,%d), want (%d,%d)",
								size[0], size[1], count, selected, x, y, tile.X, tile.Y)
						}
					}
				}
				if marked != 1 {
					t.Fatalf("%dx%d/%d: selection %d drew %d marks, want 1", size[0], size[1], count, selected, marked)
				}
				// And the session under the cursor is the one in that tile.
				want := ansi.Strip(sessions[selected].Base)
				body := strings.Join(lines[tile.Y:tile.Y+tile.Height], "\n")
				if !strings.Contains(body, want) {
					t.Fatalf("%dx%d/%d: the marked tile does not name %q:\n%s", size[0], size[1], count, want, body)
				}
			}
		}
	}
}
