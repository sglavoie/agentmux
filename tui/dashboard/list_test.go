package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestRenderListShowsLockOnSelectedAndUnselectedRows(t *testing.T) {
	for _, width := range []int{20, 80, 160} {
		for _, cursor := range []int{0, 1} {
			rows := []session.RunningSession{
				{WindowID: 1, Base: "🦊api", Locked: true},
				{WindowID: 2, Base: "web"},
			}
			in := ListInput{Cfg: &config.Config{}, Width: width, Cursor: cursor, Items: BuildItems(rows)}
			out := stripAnsi(RenderList(in))
			if strings.Count(out, palette.LockIcon()) != 1 || !strings.Contains(out, "🦊api") {
				t.Fatalf("width=%d cursor=%d: locked render = %q", width, cursor, out)
			}
			for _, line := range strings.Split(out, "\n") {
				if layout.Width(line) > width {
					t.Fatalf("row exceeds %d cells: %q", width, line)
				}
			}
			rows[0].Locked = false
			in.Items = BuildItems(rows)
			if out := stripAnsi(RenderList(in)); strings.Contains(out, palette.LockIcon()) {
				t.Fatalf("unlocked render contains lock: %q", out)
			}
		}
	}
}

// TestRenderListDrawsTheRunTimerInTheTimeColumn verifies that a rendered list
// reports the elapsed busy stretch rather than the time since the last hook
// event. The two are different questions about different moments, and the
// watchdog entry below is deliberately fresh so a renderer that read it instead
// would print a much smaller number.
func TestRenderListDrawsTheRunTimerInTheTimeColumn(t *testing.T) {
	now := time.Now()
	in := ListInput{
		Cfg:   &config.Config{},
		Width: 80,
		Now:   now,
		Items: BuildItems([]session.RunningSession{
			{WindowID: 1, Base: "api", State: "bolt", Account: "work", BusySince: now.Add(-(3*time.Minute + 7500*time.Millisecond))},
		}),
		Watchdog: map[int]protocol.WatchdogState{
			1: {LastAction: "active", LastEventAt: now.Unix(), ThresholdSec: 600},
		},
	}
	out := stripAnsi(RenderList(in))
	if !strings.Contains(out, "3:07") {
		t.Errorf("expected the \"3:07\" run timer in the render; got %q", out)
	}
	if !strings.Contains(out, statusWorking) {
		t.Errorf("expected the status beside the timer; got %q", out)
	}
}

// TestRenderListSaysDashForAClockItCannotRead pins the honest rendering of a
// missing anchor. The Time column stays reserved — the column exists for the
// pass, not for the row — and the row that has nothing to report says so, rather
// than borrowing the other clock and printing a number that looks exactly like
// the truth.
func TestRenderListSaysDashForAClockItCannotRead(t *testing.T) {
	in := ListInput{
		Cfg:   &config.Config{},
		Width: 80,
		Now:   time.Now(),
		Items: BuildItems([]session.RunningSession{
			{WindowID: 1, Base: "api", State: "bolt", Account: "work"},
		}),
	}
	out := stripAnsi(RenderList(in))
	if !strings.Contains(out, "—") {
		t.Errorf("a row with no busy anchor should report the dash placeholder; got %q", out)
	}
}

// TestRenderListTimeColumnIsSharedAndRightAligned asserts the property the
// once-per-pass scan exists for: every row pays the same Time column, and the
// times are right-aligned inside it so `0:07` and `99d23` can be compared as
// magnitudes down the column. The third row carries the widest representation
// formatRunElapsed can produce, which pins that even a multi-month stretch cannot
// widen the column.
func TestRenderListTimeColumnIsSharedAndRightAligned(t *testing.T) {
	now := time.Now()
	rows := []session.RunningSession{
		{WindowID: 1, Base: "api", State: "bolt", Account: "work", BusySince: now.Add(-(12*time.Hour + 7*time.Minute + 30*time.Second))},
		{WindowID: 2, Base: "web", State: "bolt", Account: "work"},
		{WindowID: 3, Base: "etl", State: "bolt", Account: "work", BusySince: now.Add(-(99*24*time.Hour + 23*time.Hour))},
	}
	in := ListInput{Cfg: &config.Config{}, Width: 80, Now: now, Items: BuildItems(rows)}

	if got := ScanQueueColumns(BuildQueue(QueueInput{Rows: rows, Now: now})).Time; got != runTimerMaxCells {
		t.Fatalf("Time column = %d, want the fixed %d", got, runTimerMaxCells)
	}

	// One bucket, so one heading above three rows: the urgency heading is drawn
	// whenever the list has anything in it, because "Running (3)" is a fact about
	// the queue even when nothing else is competing with it.
	lines := strings.Split(strings.TrimRight(stripAnsi(RenderList(in)), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected a heading and 3 rendered rows, got %d: %q", len(lines), lines)
	}
	busy, idle, long := lines[1], lines[2], lines[3]
	for _, tc := range []struct{ name, line, want string }{
		{"busy", busy, "12h07"},
		{"no anchor", idle, "—"},
		{"long-running", long, "99d23"},
	} {
		if !strings.Contains(tc.line, tc.want) {
			t.Errorf("expected %q on the %s row; got %q", tc.want, tc.name, tc.line)
		}
	}

	// Right-aligned means the times END together, which is what makes the column
	// readable as magnitudes. Measuring the end rather than the start is the whole
	// difference between this column and the two beside it.
	width := layout.Width(busy)
	for _, tc := range []struct{ name, line string }{{"no anchor", idle}, {"long-running", long}} {
		if got := layout.Width(tc.line); got != width {
			t.Errorf("%s row is %d cells and the busy row is %d: the pass is not aligned",
				tc.name, got, width)
		}
		if strings.HasSuffix(tc.line, " ") {
			t.Errorf("%s row trails a blank where its time should end: %q", tc.name, tc.line)
		}
	}
}

// TestEmptyHeroPaintsItsKeysLikeKeys is a styling assertion rather than a text
// one, and it exists because the text assertions beside it cannot fail when the
// styling is wrong. The empty dashboard is the one screen with no rows to look
// at, so the two keys that lead out of it are the whole interface; painting them
// in the same dim as the sentence around them made the only actionable glyphs on
// the screen the least visible thing on it.
func TestEmptyHeroPaintsItsKeysLikeKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   ListInput
		keys []string
	}{
		{"no sessions", ListInput{Width: 60}, []string{"l", "?"}},
		{"loading", ListInput{Width: 60, Loading: true}, []string{"?"}},
		{"filtered out", ListInput{Width: 60, SessionCount: 3, Filter: Parse("nothing-matches")}, []string{"esc", "?"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := renderEmptyHero(tc.in)
			for _, key := range tc.keys {
				if want := styleHeroKey.Render(key); !strings.Contains(out, want) {
					t.Errorf("key %q is not painted as a key in %q", key, out)
				}
			}
			// The copy is unchanged by the styling: the double spaces that set a
			// key apart are in the sentence, so a terminal that drops color still
			// reads the same. Every assertion elsewhere rests on that.
			if plain := stripAnsi(out); !strings.Contains(plain, "press  "+tc.keys[0]+"  ") {
				t.Errorf("the hint copy lost its spacing around %q: %q", tc.keys[0], plain)
			}
		})
	}
}
