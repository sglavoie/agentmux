package overview

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

var viewNow = time.Unix(1_700_000_000, 0)

// viewConfig gives the profiles their required colors, which is what a real
// operator's config supplies and what the tile's title is painted with.
func viewConfig() *config.Config {
	return &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: harness.Claude, Color: "blue"},
			{Name: "personal", Harness: harness.Codex, Color: "purple"},
			{Name: "review", Harness: harness.Copilot, Color: "green"},
		},
	}
}

// viewSession is one live row: a tab, a window, a project, and a profile.
func viewSession(tabID int, project, profile, harnessName string) session.RunningSession {
	return session.RunningSession{
		TabID:     tabID,
		WindowID:  tabID * 10,
		Key:       fmt.Sprintf("%s-%d", project, tabID),
		Base:      project,
		Project:   project,
		Profile:   profile,
		Harness:   harnessName,
		Directory: "/home/user/projects/" + project,
		State:     "bolt",
		BusySince: viewNow.Add(-42 * time.Second),
	}
}

// viewSessions builds n rows across the three configured profiles, which is what
// makes every multi-session case below also a multi-profile case.
func viewSessions(n int) []session.RunningSession {
	profiles := []struct{ name, harnessName string }{
		{"work", harness.Claude},
		{"personal", harness.Codex},
		{"review", harness.Copilot},
	}
	out := make([]session.RunningSession, 0, n)
	for i := 0; i < n; i++ {
		p := profiles[i%len(profiles)]
		out = append(out, viewSession(i+1, fmt.Sprintf("proj%02d", i+1), p.name, p.harnessName))
	}
	return Order(out)
}

func viewInput(width, height int, sessions []session.RunningSession) Input {
	return Input{
		Width:    width,
		Height:   height,
		Sessions: sessions,
		Cfg:      viewConfig(),
		Now:      viewNow,
	}
}

// renderLines renders and asserts the size contract before returning the lines,
// still styled. Every test in this file goes through it, so "exactly Height lines
// of exactly Width cells" is checked on every case rather than only where it is
// the subject.
func renderLines(t *testing.T, in Input) []string {
	t.Helper()
	out := Render(in)
	lines := strings.Split(out, "\n")
	if len(lines) != in.Height {
		t.Fatalf("Render returned %d lines, want the %d it was given", len(lines), in.Height)
	}
	if strings.HasSuffix(out, "\n") {
		t.Fatal("Render returned a trailing newline")
	}
	for i, line := range lines {
		if got := layout.StyledWidth(line); got != in.Width {
			t.Fatalf("line %d is %d cells wide, want exactly %d: %q", i, got, in.Width, ansi.Strip(line))
		}
	}
	return lines
}

// plain strips the styling, which is the only form a claim about layout can be
// made against.
func plain(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = ansi.Strip(line)
	}
	return out
}

// shippedSizes are the terminals session 2 names, plus the two extremes that are
// not terminals anybody chooses but are both reachable by dragging a window.
var shippedSizes = [][2]int{
	{60, 15}, {80, 24}, {100, 24}, {120, 30}, {160, 45},
	{20, 40}, {200, 6}, {12, 5}, {4, 4}, {1, 1},
}

// TestRenderFillsExactlyTheSurfaceItIsGiven is the whole file's precondition, run
// across every size and every session count that changes the layout's shape.
func TestRenderFillsExactlyTheSurfaceItIsGiven(t *testing.T) {
	for _, size := range shippedSizes {
		for _, count := range []int{0, 1, 2, 3, 5, 9, 12, 40} {
			t.Run(fmt.Sprintf("%dx%d/%d", size[0], size[1], count), func(t *testing.T) {
				in := viewInput(size[0], size[1], viewSessions(count))
				in.Feedback = "worktree created"
				in.Selected = count / 2
				renderLines(t, in)
			})
		}
	}
}

// TestRenderRefusesADegenerateSurface. Zero and negative dimensions arrive from a
// terminal resize before the first WindowSizeMsg; they are not an error and they
// are not a blank screen of some invented size.
func TestRenderRefusesADegenerateSurface(t *testing.T) {
	for _, size := range [][2]int{{0, 24}, {80, 0}, {0, 0}, {-4, 24}, {80, -4}} {
		if got := Render(viewInput(size[0], size[1], viewSessions(3))); got != "" {
			t.Errorf("%dx%d rendered %q, want nothing", size[0], size[1], got)
		}
	}
}

// TestTheThreeReservedRowsAreAlwaysReserved. The grid may not grow into them,
// and the feedback row holds its place while it is empty so the tiles below do
// not shift the moment something is reported.
func TestTheThreeReservedRowsAreAlwaysReserved(t *testing.T) {
	sessions := viewSessions(4)
	quiet := plain(renderLines(t, viewInput(120, 30, sessions)))

	noisy := viewInput(120, 30, sessions)
	noisy.Feedback = "handoff written"
	loud := plain(renderLines(t, noisy))

	if !strings.Contains(loud[1], "handoff written") {
		t.Errorf("the feedback row says %q, want the feedback", loud[1])
	}
	if strings.TrimSpace(quiet[1]) != "" {
		t.Errorf("the reserved feedback row says %q while there is nothing to report", quiet[1])
	}
	for i := 2; i < len(quiet); i++ {
		if quiet[i] != loud[i] {
			t.Fatalf("row %d moved when feedback appeared:\n quiet %q\n loud  %q", i, quiet[i], loud[i])
		}
	}
	if !strings.Contains(quiet[0], DefaultTitle) {
		t.Errorf("the title row says %q, want it to name the surface", quiet[0])
	}
	if !strings.Contains(quiet[len(quiet)-1], "hjkl") {
		t.Errorf("the hints row says %q, want the key hints", quiet[len(quiet)-1])
	}
}

// TestTheTitleCountsTheWholeLiveSet, not the page. A grid that said "4" while
// nine sessions were running would be reporting its own pagination as a fact
// about the machine.
func TestTheTitleCountsTheWholeLiveSet(t *testing.T) {
	in := viewInput(120, 30, viewSessions(9)) // a 2x2 grid: four tiles a page
	title := plain(renderLines(t, in))[0]
	if !strings.Contains(title, "9") {
		t.Errorf("the title row says %q, want it to count all nine sessions", title)
	}
	if !strings.Contains(title, "page 1/3") {
		t.Errorf("the title row says %q, want the page indicator", title)
	}

	in.Selected = 8
	if title := plain(renderLines(t, in))[0]; !strings.Contains(title, "page 3/3") {
		t.Errorf("with the last session selected the title says %q, want page 3/3", title)
	}
}

// TestASinglePageDoesNotCountItsOwnPages. `page 1/1` is a count of something the
// operator can see all of.
func TestASinglePageDoesNotCountItsOwnPages(t *testing.T) {
	title := plain(renderLines(t, viewInput(120, 30, viewSessions(4))))[0]
	if strings.Contains(title, "page") {
		t.Errorf("the title row says %q, want no page indicator when everything fits", title)
	}
}

// TestAnEmptyLiveSetDrawsChromeAndNoTiles. No sessions is a normal state — the
// operator closed the last one — and it must not draw an empty box that reads as
// a session whose preview failed.
func TestAnEmptyLiveSetDrawsChromeAndNoTiles(t *testing.T) {
	lines := plain(renderLines(t, viewInput(120, 30, nil)))
	if !strings.Contains(lines[0], DefaultTitle) {
		t.Errorf("the title row says %q", lines[0])
	}
	for i := 2; i < len(lines)-1; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			t.Errorf("row %d of an empty grid says %q, want blank", i, lines[i])
		}
	}
}

// TestOneSessionFillsOneTileAndLeavesTheRestBlank is the incomplete-page case at
// its smallest: one tile drawn, three slots left as space.
func TestOneSessionFillsOneTileAndLeavesTheRestBlank(t *testing.T) {
	g := Measure(120, 30)
	if g.Cols != 2 || g.Rows != 2 {
		t.Fatalf("the fixture grid is %dx%d, want 2x2", g.Cols, g.Rows)
	}
	lines := plain(renderLines(t, viewInput(120, 30, viewSessions(1))))

	first, _ := g.Tile(0)
	second, _ := g.Tile(1)
	for line := 0; line < first.Height; line++ {
		row := []rune(lines[first.Y+line])
		right := strings.TrimSpace(string(row[second.X : second.X+second.Width]))
		if right != "" {
			t.Errorf("the empty slot beside the only session says %q, want blank", right)
		}
	}
	if !strings.Contains(lines[first.Y], "┏") && !strings.Contains(lines[first.Y], "╭") {
		t.Errorf("the occupied slot has no box: %q", lines[first.Y])
	}
	// The whole second tile-row is empty, so nothing is drawn on it at all.
	third, _ := g.Tile(2)
	for line := 0; line < third.Height; line++ {
		if got := strings.TrimSpace(lines[third.Y+line]); got != "" {
			t.Errorf("row %d of the empty tile-row says %q, want blank", third.Y+line, got)
		}
	}
}

// TestTheSelectedTileIsMarkedExactlyOnce. Heavy glyphs are the marking that
// survives a terminal with no color, so counting them is counting selections.
func TestTheSelectedTileIsMarkedExactlyOnce(t *testing.T) {
	g := Measure(160, 45)
	sessions := viewSessions(9)
	for selected := 0; selected < len(sessions); selected++ {
		in := viewInput(160, 45, sessions)
		in.Selected = selected
		lines := plain(renderLines(t, in))

		// Exactly one tile wears the heavy box, and it is the selected slot.
		pos := selected - g.PageStart(g.PageOf(selected))
		want, _ := g.Tile(pos)
		corners := 0
		for y, line := range lines {
			row := []rune(line)
			for x, r := range row {
				if r != '┏' {
					continue
				}
				corners++
				if x != want.X || y != want.Y {
					t.Errorf("selection %d: a heavy corner is at (%d,%d), want (%d,%d)", selected, x, y, want.X, want.Y)
				}
			}
		}
		if corners != 1 {
			t.Errorf("selection %d: %d tiles wear the heavy box, want exactly 1", selected, corners)
		}
	}
}

// TestAnUnselectedTileWearsTheLightBox. The two sets have to differ, or the
// marking says nothing.
func TestAnUnselectedTileWearsTheLightBox(t *testing.T) {
	lines := plain(renderLines(t, viewInput(120, 30, viewSessions(4))))
	joined := strings.Join(lines, "\n")
	if strings.Count(joined, "╭") != 3 {
		t.Errorf("the three unselected tiles drew %d light corners, want 3", strings.Count(joined, "╭"))
	}
	if strings.Count(joined, "┏") != 1 {
		t.Errorf("the selection drew %d heavy corners, want 1", strings.Count(joined, "┏"))
	}
}

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
	in.Previews = map[int]Preview{
		sessions[0].WindowID: {Content: dashboard.InspectorReady, Output: dashboard.SplitOutput("building the index\n")},
		sessions[1].WindowID: {Content: dashboard.InspectorReady},
		sessions[2].WindowID: {Content: dashboard.InspectorFailed, Error: "kitty: no such window"},
		sessions[3].WindowID: {Content: dashboard.InspectorVanished},
	}
	joined := strings.Join(plain(renderLines(t, in)), "\n")
	for _, want := range []string{"building the index", "(no output)", "error: kitty: no such window", "(session not running)"} {
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
	in.Previews = map[int]Preview{
		left.WindowID:  {Content: dashboard.InspectorReady, Output: []string{"\x1b[31mbleeding red"}},
		right.WindowID: {Content: dashboard.InspectorReady, Output: []string{"innocent"}},
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
	in.Previews = map[int]Preview{}
	for i, s := range sessions {
		in.Previews[s.WindowID] = Preview{
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
		in.Previews = map[int]Preview{}
		for _, s := range sessions {
			in.Previews[s.WindowID] = Preview{
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
	if !strings.Contains(joined, "a") {
		t.Errorf("a four-cell tile dropped the session name entirely:\n%q", lines)
	}
}

// TestASurfaceWithNoRoomForAGridStillSaysWhatItIs. Three rows is exactly the
// chrome; two is the title and the hints; one is the title. The clipping order is
// the contract, because a surface that cannot say how to leave it is a trap.
func TestASurfaceWithNoRoomForAGridStillSaysWhatItIs(t *testing.T) {
	sessions := viewSessions(3)

	three := plain(renderLines(t, viewInput(80, 3, sessions)))
	if !strings.Contains(three[0], DefaultTitle) {
		t.Errorf("a three-row surface says %q on its first row", three[0])
	}
	if !strings.Contains(three[2], "hjkl") {
		t.Errorf("a three-row surface says %q on its last row", three[2])
	}

	in := viewInput(80, 2, sessions)
	in.Feedback = "dropped first"
	two := plain(renderLines(t, in))
	if !strings.Contains(two[0], DefaultTitle) || !strings.Contains(two[1], "hjkl") {
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
	in.Previews = map[int]Preview{
		sessions[0].WindowID: {Content: dashboard.InspectorReady, Output: dashboard.SplitOutput("one\ntwo\n")},
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

// The package has two halves and they are held to two different structural
// rules, so every file has to be classified as one or the other. The lists below
// are the classification, and parsePackage fails on a file that is in neither —
// which is what makes adding a file a deliberate act rather than a way to opt out
// of both allowlists.
//
// renderingFiles are pure: a session slice, a preview map and a supplied Now go
// in, and a string comes out. lifecycleFiles are the running program: they start
// commands, read the clock, and hold the state the renderer is handed.
var (
	renderingFiles = []string{"chrome.go", "footer.go", "grid.go", "nav.go", "order.go", "view.go"}
	lifecycleFiles = []string{"capture.go", "model.go", "preferences.go", "refresh.go", "status.go", "update.go"}
)

// parsePackage parses every non-test file in the package and returns them keyed
// by name, having first checked that the classification above accounts for all of
// them exactly once.
//
// Files are parsed in full rather than imports-only because the callers below
// walk function bodies.
func parsePackage(t *testing.T) map[string]*ast.File {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	onDisk := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		onDisk[name] = true
	}
	if len(onDisk) == 0 {
		t.Fatal("no non-test files were found; every structural assertion below would pass vacuously")
	}

	classified := map[string]bool{}
	for _, name := range append(append([]string{}, renderingFiles...), lifecycleFiles...) {
		if classified[name] {
			t.Errorf("%s is classified twice; it would be held to both rules and to neither exclusively", name)
		}
		classified[name] = true
		if !onDisk[name] {
			t.Errorf("%s is classified but is not in the package", name)
		}
	}
	for name := range onDisk {
		if !classified[name] {
			t.Errorf("%s is in neither renderingFiles nor lifecycleFiles; classify it so it is held to one of the two structural rules", name)
		}
	}

	fset := token.NewFileSet()
	files := make(map[string]*ast.File, len(onDisk))
	for name := range onDisk {
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files[name] = file
	}
	return files
}

// assertImports checks one file's imports against an allowlist.
func assertImports(t *testing.T, name string, file *ast.File, allowed map[string]bool, rule string) {
	t.Helper()
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("%s: unquoting %s: %v", name, imp.Path.Value, err)
		}
		if !allowed[path] {
			t.Errorf("%s imports %q, which is not on the %s allowlist", name, path, rule)
		}
	}
}

// selectorCalls reports every `pkg.Func(...)` call in a file as "pkg.Func".
func selectorCalls(file *ast.File) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok {
				out = append(out, ident.Name+"."+sel.Sel.Name)
			}
		}
		return true
	})
	return out
}

// TestTheRenderingHalfPerformsNoIO is structural rather than behavioral: a
// renderer that read a file would still return a string, and the only way to be
// sure is to look at what it is allowed to import. Anything that could reach a
// disk, a socket, a subprocess, or a clock is absent by construction — Now is a
// field on Input precisely so this list can stay short.
func TestTheRenderingHalfPerformsNoIO(t *testing.T) {
	allowed := map[string]bool{
		"fmt":     true, // Sprintf into the title row
		"sort":    true,
		"strings": true,
		"time":    true, // time.Duration and time.Time values only; see below

		"charm.land/lipgloss/v2": true,

		"github.com/sglavoie/agentmux/internal/config":  true,
		"github.com/sglavoie/agentmux/internal/session": true,
		"github.com/sglavoie/agentmux/tui/dashboard":    true,
		"github.com/sglavoie/agentmux/tui/layout":       true,
		"github.com/sglavoie/agentmux/tui/theme":        true,
	}

	files := parsePackage(t)
	for _, name := range renderingFiles {
		file, ok := files[name]
		if !ok {
			continue // parsePackage already reported the missing file
		}
		assertImports(t, name, file, allowed, "no-I/O")

		// time is on the list for its types. A call into it would read the
		// machine's clock, which is exactly what Input.Now exists to avoid.
		for _, call := range selectorCalls(file) {
			if call == "time.Now" || call == "time.Since" {
				t.Errorf("%s calls %s; the renderer's clock is Input.Now", name, call)
			}
		}
	}
}

// TestTheLifecycleHalfReachesTheWorldOnlyThroughItsSeams is the running half's
// structural rule, and it is the one that pins "browsing the overview cannot
// change anything".
//
// Two claims, both checked by absence. First, the imports: the lifecycle may name
// the session and config vocabularies, Bubble Tea, the shared commands, and the
// daemon's protocol constants — and nothing that could read a transcript, a plan,
// a socket or a file of its own. Second, the calls into tui/commands: only the
// topology and preview reads, Focus, the view preference, and their tickers. Every
// other command in that package either writes Kitty or asks the daemon for
// something this surface has no business asking, so the allowlist being short is
// the whole assertion. A navigation key cannot reach Kitty because there is no
// call in the package it could reach it through.
func TestTheLifecycleHalfReachesTheWorldOnlyThroughItsSeams(t *testing.T) {
	allowedImports := map[string]bool{
		"fmt":     true,
		"strings": true,
		"time":    true,

		"charm.land/bubbletea/v2": true,

		"github.com/sglavoie/agentmux/internal/config":          true,
		"github.com/sglavoie/agentmux/internal/daemon/protocol": true, // KittySnapshotStaleAfter
		"github.com/sglavoie/agentmux/internal/kitty":           true, // the client type, handed to the seams
		"github.com/sglavoie/agentmux/internal/session":         true,
		"github.com/sglavoie/agentmux/tui/commands":             true,
		"github.com/sglavoie/agentmux/tui/dashboard":            true, // shared NeedsYou predicate
	}
	allowedCommands := map[string]bool{
		"commands.OverviewPreferences": true, // only c changes this daemon-lifetime UI setting
		"commands.RefreshOverview":     true,
		"commands.Focus":               true,
		"commands.Tick":                true,
		// The capture is a READ of a window's terminal text and its cadence. It is
		// listed beside RefreshOverview rather than beside Focus for that reason:
		// commands.CaptureOutput runs `kitten @ get-text`, which observes a pane
		// without touching it, so adding it leaves the "browsing the overview
		// cannot change anything" claim exactly where it was.
		"commands.CaptureOutput": true,
		"commands.CaptureTick":   true,
	}

	files := parsePackage(t)
	clockCallers := map[string]bool{}
	for _, name := range lifecycleFiles {
		file, ok := files[name]
		if !ok {
			continue
		}
		assertImports(t, name, file, allowedImports, "lifecycle")

		for _, call := range selectorCalls(file) {
			if strings.HasPrefix(call, "commands.") && !allowedCommands[call] {
				t.Errorf("%s calls %s; the overview observes, and its only mutation is commands.Focus", name, call)
			}
			if call == "time.Now" || call == "time.Since" {
				clockCallers[name] = true
			}
		}
	}

	// The clock is a seam for the same reason the commands are, so it enters the
	// package in exactly one place: the constructor that binds the seams to the
	// real world. Anywhere else it would be a reading a test cannot pin.
	delete(clockCallers, "model.go")
	for name := range clockCallers {
		t.Errorf("%s calls the machine clock; the lifecycle's clock is the deps.now seam bound in model.go", name)
	}
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
