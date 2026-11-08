package overview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// TestTheSelectedTileKeepsItsContentsSemanticColors. The selection recolors the
// BOX. Recoloring the contents would erase the amber and red the grid exists to
// let an operator find.
func TestTheSelectedTileKeepsItsContentsSemanticColors(t *testing.T) {
	sessions := viewSessions(2)
	sessions[0].State = "key" // waiting on the operator
	sessions[1].State = "key"

	body := func(selected int) string {
		in := viewInput(100, 24, sessions)
		in.Selected = selected
		g := Measure(100, 24)
		tile, _ := g.Tile(selected)
		lines := renderLines(t, in)
		var b strings.Builder
		for line := 1; line < tile.Height-1; line++ {
			row := lines[tile.Y+line]
			b.WriteString(ansi.Strip(row))
		}
		return b.String()
	}

	if got := body(0); !strings.Contains(got, "Needs permission") {
		t.Errorf("the selected tile says %q, want the permission status", got)
	}
	if got := body(1); !strings.Contains(got, "Needs permission") {
		t.Errorf("the unselected tile says %q, want the permission status", got)
	}

	// The status text carries a foreground color in both, which is what "the
	// selection does not restyle the contents" means concretely.
	for _, selected := range []int{0, 1} {
		in := viewInput(100, 24, sessions)
		in.Selected = selected
		g := Measure(100, 24)
		tile, _ := g.Tile(0)
		lines := renderLines(t, in)
		styled := false
		for line := 1; line < tile.Height-1; line++ {
			row := lines[tile.Y+line]
			if strings.Contains(ansi.Strip(row), "Needs permission") && strings.Contains(row, "\x1b[") {
				styled = true
			}
		}
		if !styled {
			t.Errorf("with selection %d the first tile's status is unstyled", selected)
		}
	}
}

// TestTheFiveOutputSituationsReachTheTiles. The grid is the surface an operator
// scans, so "no output yet", "nothing came back", "the read failed", and "the
// window is gone" must be four different things on it.
func TestTheFiveOutputSituationsReachTheTiles(t *testing.T) {
	sessions := viewSessions(4)
	in := viewInput(160, 45, sessions)
	in.Previews = map[session.PaneTarget]Preview{
		paneTargetForWindow(sessions[0].WindowID): {Content: dashboard.InspectorReady, Output: dashboard.SplitOutput("building the index\n")},
		paneTargetForWindow(sessions[1].WindowID): {Content: dashboard.InspectorReady},
		paneTargetForWindow(sessions[2].WindowID): {Content: dashboard.InspectorFailed, Error: "tmux: no such pane"},
		paneTargetForWindow(sessions[3].WindowID): {Content: dashboard.InspectorVanished},
	}
	joined := strings.Join(plain(renderLines(t, in)), "\n")
	for _, want := range []string{"building the index", "(no output)", "error: tmux: no such pane", "(session not running)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the grid does not say %q", want)
		}
	}

	// A session with no entry at all is a read that has not landed.
	bare := viewInput(160, 45, sessions)
	if got := strings.Join(plain(renderLines(t, bare)), "\n"); !strings.Contains(got, "(loading…)") {
		t.Error("a session with no preview entry does not read as loading")
	}
}

// TestNameQualifiersAreMeasuredAcrossEveryPage is the rule that cannot be
// checked one page at a time: the collision is on page two, and the tile on page
// one must already carry its qualifier or it would look like a different session
// after a PgDn and back.
func TestNameQualifiersAreMeasuredAcrossEveryPage(t *testing.T) {
	sessions := make([]session.RunningSession, 0, 9)
	for i := 0; i < 9; i++ {
		s := viewSession(i+1, "api", "work", harness.Claude)
		s.Branch = fmt.Sprintf("branch-%d", i+1)
		sessions = append(sessions, s)
	}
	sessions = Order(sessions)

	g := Measure(120, 30) // four tiles a page, three pages
	if g.Capacity() != 4 {
		t.Fatalf("the fixture grid holds %d tiles, want 4", g.Capacity())
	}
	for page := 0; page < 3; page++ {
		in := viewInput(120, 30, sessions)
		in.Selected = g.PageStart(page)
		joined := strings.Join(plain(renderLines(t, in)), "\n")
		for i := 0; i < g.Capacity() && g.PageStart(page)+i < len(sessions); i++ {
			want := sessions[g.PageStart(page)+i].Branch
			if !strings.Contains(joined, want) {
				t.Errorf("page %d does not qualify its tile with %q:\n%s", page+1, want, joined)
			}
		}
	}
}

// TestAUniqueNameIsNotQualified. The disambiguator only speaks when there is a
// collision, and a qualifier on every tile would be noise.
func TestAUniqueNameIsNotQualified(t *testing.T) {
	sessions := viewSessions(4)
	for i := range sessions {
		sessions[i].Branch = fmt.Sprintf("branch-%d", i)
	}
	joined := strings.Join(plain(renderLines(t, viewInput(160, 45, sessions))), "\n")
	if strings.Contains(joined, "branch-0") {
		t.Errorf("a uniquely named session was qualified anyway:\n%s", joined)
	}
}

// TestStylingDoesNotLeakBetweenTiles. A preview is a capture of somebody else's
// terminal, so an unterminated color is not a hypothetical: it is whatever the
// harness happened to be mid-sequence on when the snapshot was taken.
func TestStylingDoesNotLeakBetweenTiles(t *testing.T) {
	left := viewSession(1, "aaaleft", "work", harness.Claude)
	right := viewSession(2, "zzzright", "personal", harness.Codex)
	sessions := Order([]session.RunningSession{left, right})

	in := viewInput(100, 24, sessions) // two columns, one row
	in.Previews = map[session.PaneTarget]Preview{
		paneTargetForWindow(left.WindowID):  {Content: dashboard.InspectorReady, Output: []string{"\x1b[31mbleeding red"}},
		paneTargetForWindow(right.WindowID): {Content: dashboard.InspectorReady, Output: []string{"innocent"}},
	}
	lines := renderLines(t, in)

	found := false
	for i, line := range lines {
		poison := strings.Index(line, "\x1b[31m")
		if poison < 0 {
			continue
		}
		found = true
		neighbor := strings.Index(line, "innocent")
		if neighbor < 0 {
			continue
		}
		if neighbor < poison {
			t.Fatalf("row %d puts the right tile's text before the left tile's escape", i)
		}
		between := line[poison:neighbor]
		if !strings.Contains(between, "\x1b[0m") {
			t.Errorf("row %d carries an unterminated color into the next tile: %q", i, between)
		}
	}
	if !found {
		t.Fatal("the poisoned escape never reached the output; the test proves nothing")
	}

	// And the right tile's name is not drawn in the left tile's color either.
	joined := strings.Join(lines, "\n")
	for _, name := range []string{"aaaleft", "zzzright"} {
		if !strings.Contains(ansi.Strip(joined), name) {
			t.Errorf("the grid does not name %q", name)
		}
	}
}

// TestEveryTileRowIsSealed. The general form of the leak rule: no rendered row
// may end inside a style, or the row below it — and whatever the host draws
// beside the surface — inherits it.
func TestEveryTileRowIsSealed(t *testing.T) {
	sessions := viewSessions(6)
	in := viewInput(160, 45, sessions)
	in.Previews = map[session.PaneTarget]Preview{}
	for i, s := range sessions {
		in.Previews[paneTargetForWindow(s.WindowID)] = Preview{
			Content: dashboard.InspectorReady,
			Output:  []string{fmt.Sprintf("\x1b[3%dmunclosed %d", i%8, i)},
		}
	}
	for i, line := range renderLines(t, in) {
		if !strings.Contains(line, "\x1b") {
			continue
		}
		if !strings.HasSuffix(line, "\x1b[0m") {
			t.Errorf("row %d carries styling but does not end in a reset: %q", i, line)
		}
	}
}

// TestWideGraphemesDoNotBreakTheGrid. An emoji is two cells and a CJK glyph is
// two cells, and a renderer that counted runes would draw one cell too many per
// glyph and push every tile to its right out of place.
func TestWideGraphemesDoNotBreakTheGrid(t *testing.T) {
	sessions := []session.RunningSession{
		viewSession(1, "🚀 launch 🚀", "work", harness.Claude),
		viewSession(2, "日本語プロジェクト", "personal", harness.Codex),
		viewSession(3, "café—naïve", "review", harness.Copilot),
		viewSession(4, "family 👨‍👩‍👧‍👦 tree", "work", harness.Claude),
	}
	sessions = Order(sessions)

	for _, size := range [][2]int{{60, 15}, {100, 24}, {120, 30}, {160, 45}, {12, 6}} {
		in := viewInput(size[0], size[1], sessions)
		in.Previews = map[session.PaneTarget]Preview{}
		for _, s := range sessions {
			in.Previews[paneTargetForWindow(s.WindowID)] = Preview{
				Content: dashboard.InspectorReady,
				Output:  []string{"▶ 実行中… 🚀🚀🚀", "ok"},
			}
		}
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			// renderLines already asserts every row is exactly Width cells, which
			// is the whole claim.
			renderLines(t, in)
		})
	}
}

// TestATileTooSmallForABoxDropsTheBoxNotTheSession. On a terminal a few cells
// wide the outline would be the only thing drawn, and the name is what tells the
// operator a session is there at all.
func TestATileTooSmallForABoxDropsTheBoxNotTheSession(t *testing.T) {
	sessions := []session.RunningSession{viewSession(1, "api", "work", harness.Claude)}
	lines := plain(renderLines(t, viewInput(4, 8, sessions)))
	joined := strings.Join(lines, "")
	for _, glyph := range []string{"╭", "┏", "│", "┃"} {
		if strings.Contains(joined, glyph) {
			t.Errorf("a four-cell tile drew the box glyph %q", glyph)
		}
	}
	// The digit now reserves two cells on row two, so the harness is reduced to
	// "c…" there. The identity row must still retain the compact renderer's text.
	body := dashboard.RenderCompactPreview(dashboard.CompactInput{
		Width: 4, Height: 5, Session: sessions[0], Cfg: viewConfig(), Now: viewNow,
		Content: dashboard.InspectorLoading,
	})
	if lines[gridTop] != ansi.Strip(strings.Split(body, "\n")[0]) || strings.TrimSpace(lines[gridTop]) == "" {
		t.Errorf("a four-cell tile dropped the identity row:\n%q", lines)
	}
}

// TestASurfaceWithNoRoomForAGridStillSaysWhatItIs. Three rows is exactly the
// chrome; two is the title and the hints; one is the title. The clipping order is
// the contract, because a surface that cannot say how to leave it is a trap.
//
// The hints row is probed by its FIRST entry rather than by one in the middle of
// it. The footer has always been longer than eighty cells and is truncated there,
// so a probe further along was asserting the footer's length as much as its
// presence, and it broke the moment the actions and undo keys were added ahead of
// it. The leftmost entry is the one truncation can never remove.
func TestASurfaceWithNoRoomForAGridStillSaysWhatItIs(t *testing.T) {
	sessions := viewSessions(3)

	three := plain(renderLines(t, viewInput(80, 3, sessions)))
	if !strings.Contains(three[0], DefaultTitle) {
		t.Errorf("a three-row surface says %q on its first row", three[0])
	}
	if !strings.Contains(three[2], "enter focus") {
		t.Errorf("a three-row surface says %q on its last row", three[2])
	}

	in := viewInput(80, 2, sessions)
	in.Feedback = "dropped first"
	two := plain(renderLines(t, in))
	if !strings.Contains(two[0], DefaultTitle) || !strings.Contains(two[1], "enter focus") {
		t.Errorf("a two-row surface is %q / %q, want the title and the hints", two[0], two[1])
	}
	if strings.Contains(strings.Join(two, ""), "dropped first") {
		t.Error("a two-row surface kept the feedback row ahead of the hints")
	}

	one := plain(renderLines(t, viewInput(80, 1, sessions)))
	if !strings.Contains(one[0], DefaultTitle) {
		t.Errorf("a one-row surface says %q, want the title", one[0])
	}
}
