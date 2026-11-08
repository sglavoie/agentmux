package overview

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

var viewNow = time.Unix(1_700_000_000, 0)

// viewConfig gives the profiles their required colors, which is what a real
// operator's config supplies and what the tile's title is painted with. It opens
// the full preview grid, which is what most of this suite measures; the compact
// default is covered by TestTheConfiguredModeIsTheOpeningMode.
func viewConfig() *config.Config {
	expanded := false
	return &config.Config{
		Defaults: config.Defaults{Overview: config.OverviewConfig{Compact: &expanded}},
		Profiles: []config.Account{
			{Name: "work", Harness: harness.Claude, Color: "blue"},
			{Name: "personal", Harness: harness.Codex, Color: "purple"},
			{Name: "review", Harness: harness.Copilot, Color: "green"},
		},
	}
}

// viewSession is one live tmux row with a stable tile identity.
func viewSession(tabID int, project, profile, harnessName string) session.RunningSession {
	return session.RunningSession{
		TabID:     tabID,
		WindowID:  tabID * 10,
		Backend:   runtimesession.BackendTmux,
		Pane:      backend.PaneID(strconv.Itoa(tabID * 10)),
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

func TestSourceCardIsDistinctFromKeyboardSelection(t *testing.T) {
	for _, compact := range []bool{false, true} {
		for _, selected := range []int{0, 1} {
			in := viewInput(120, 30, viewSessions(2))
			in.Compact, in.Selected, in.SourceTabID = compact, selected, 2
			g := MeasureMode(in.Width, in.Height, compact)
			lines := plain(renderLines(t, in))
			for index := range in.Sessions {
				tile, _ := g.Tile(index)
				top := string([]rune(lines[tile.Y])[tile.X : tile.X+tile.Width])
				wantCorner := "╭"
				switch index {
				case selected:
					wantCorner = "┏"
				case 1:
					wantCorner = "╔"
				}
				if !strings.HasPrefix(top, wantCorner) {
					t.Fatalf("compact=%v selected=%d card=%d: border %q, want %q", compact, selected, index, top, wantCorner)
				}
				if strings.Contains(top, "Opened from here") != (index == 1) {
					t.Fatalf("compact=%v selected=%d card=%d: incorrect source marker in %q", compact, selected, index, top)
				}
			}
		}
	}
}

func TestSourceMarkerKeepsTheSizeContractOnSmallTerminals(t *testing.T) {
	for width := 1; width <= 40; width++ {
		in := viewInput(width, 8, viewSessions(1))
		in.SourceTabID = 1
		renderLines(t, in)
	}
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
