// The scheduler's tests: how many reads exist, whose turn it is, and what a reply
// is allowed to do.
//
// They are written against the real update loop with the capture seam recording
// rather than answering, which is what makes every interleaving this file cares
// about — a reply that arrives after its tile was resized, after its session
// closed, after the operator paged away, after the surface was dismissed — an
// ordinary sequence of messages rather than a timing accident.
//
// Two claims are deliberately made twice, once here and once in
// tui/commands/capture_test.go. This file owns the bounds, the order and the
// lifetime of a request; that file owns what one read returns and what it keeps.
package overview

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// atSize is a loaded overview on a real terminal, which is the only state in
// which anything is visible and therefore the only state in which anything is
// captured. The resize is what starts the first reads, so every test below begins
// with a recorder holding exactly the first round.
func atSize(t *testing.T, sessions []session.RunningSession, index, width, height int) (model, *recorder) {
	t.Helper()
	m, r := loadedAt(t, sessions, index)
	if len(r.captures) != 0 {
		t.Fatalf("a surface with no dimensions dispatched %d captures, want none", len(r.captures))
	}
	m = only(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	return m, r
}

// windows names the live set by anchor window, in the order the grid lays them
// out, which is the order every fairness claim below is written in.
func windows(m model) []int {
	out := make([]int, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.WindowID)
	}
	return out
}

func paneTargetForWindow(id int) session.PaneTarget {
	return session.PaneTarget{Backend: runtimesession.BackendTmux, Pane: backend.PaneID(strconv.Itoa(id))}
}

func windowIDOfTarget(target session.PaneTarget) int {
	id, _ := strconv.Atoi(target.Pane.ID)
	return id
}

// visibleWindows is what the scheduler considers on screen right now.
func visibleWindows(m model) []int {
	tiles := m.visibleTiles()
	out := make([]int, 0, len(tiles))
	for _, tile := range tiles {
		out = append(out, windowIDOfTarget(tile.target))
	}
	return out
}

// reply delivers one completed read.
func reply(t *testing.T, m model, req commands.CaptureRequest, lines []string, err error) model {
	t.Helper()
	return only(t, m, commands.CaptureOutputMsg{Request: req, Lines: lines, Err: err})
}

// inFlightRequests is the set of requests the scheduler still expects answers
// for, which a test releases by hand in whatever order it wants.
func inFlightRequests(r *recorder, m model) []commands.CaptureRequest {
	out := make([]commands.CaptureRequest, 0, len(r.captures))
	for _, req := range r.captures {
		if _, live := m.captures.byRequest[req.RequestID]; live {
			out = append(out, req)
		}
	}
	return out
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNineVisibleTilesStartFourReads is the global bound, met the first time it
// can be: a full 3x3 grid wants nine pictures and is allowed four subprocesses.
func TestNineVisibleTilesStartFourReads(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 160, 45)

	if got := len(visibleWindows(m)); got != 9 {
		t.Fatalf("%d tiles are visible on a 160x45 grid, want 9", got)
	}
	if len(r.captures) != maxCapturesInFlight {
		t.Fatalf("a nine-tile grid dispatched %d reads, want %d", len(r.captures), maxCapturesInFlight)
	}
	if m.captures.inFlight() != maxCapturesInFlight {
		t.Fatalf("%d reads are in flight, want %d", m.captures.inFlight(), maxCapturesInFlight)
	}
	if want := windows(m)[:4]; !sameInts(r.captureWindows(), want) {
		t.Fatalf("the first round read %v, want the first four tiles %v", r.captureWindows(), want)
	}
	if got := len(m.captures.queue); got != 5 {
		t.Fatalf("%d tiles are waiting, want the remaining 5", got)
	}

	seen := map[int]bool{}
	extents := tileExtents(m)
	for _, req := range r.captures {
		if seen[req.Generation] {
			t.Fatalf("generation %d was issued twice; two tiles would accept each other's text", req.Generation)
		}
		seen[req.Generation] = true
		if got, want := [2]int{req.Width, req.Height}, extents[windowIDOfTarget(req.Target)]; got != want {
			t.Fatalf("window %d was asked for %dx%d, want its tile's %dx%d", windowIDOfTarget(req.Target), got[0], got[1], want[0], want[1])
		}
	}
}

// tileExtents is the output region each visible window is currently drawn into.
// The grid distributes its remainder, so tiles on one page are not all the same
// size and a test that assumed they were would be asserting the wrong number.
func tileExtents(m model) map[int][2]int {
	out := map[int][2]int{}
	for _, tile := range m.visibleTiles() {
		out[windowIDOfTarget(tile.target)] = [2]int{tile.width, tile.height}
	}
	return out
}

// TestABurstOfTicksCoalescesIntoTheQueueItAlreadyHas is the cadence rule. A
// hundred ticks behind a slow tmux capture must leave the queue exactly as long as one,
// because a tick asks for every visible tile and enqueue declines the ones that
// are already spoken for.
func TestABurstOfTicksCoalescesIntoTheQueueItAlreadyHas(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 160, 45)
	queued := len(m.captures.queue)

	for i := 0; i < 25; i++ {
		m = only(t, m, commands.CaptureTickMsg{})
	}

	if len(r.captures) != maxCapturesInFlight {
		t.Fatalf("25 ticks behind four slow reads dispatched %d in total, want %d", len(r.captures), maxCapturesInFlight)
	}
	if got := len(m.captures.queue); got != queued {
		t.Fatalf("25 ticks left %d tiles waiting, want the %d already queued", got, queued)
	}
	if got := len(r.captureTicks); got != 26 {
		t.Fatalf("the capture ticker was re-armed %d times, want one per tick plus Init's", got)
	}
}

// TestTheCadenceIsTheOperatorsAndIndependentOfTheTopologyInterval. The window
// tree is read in seconds and a terminal's last lines in milliseconds; tying them
// together would either poll session list ten times a second or leave
// every preview two seconds behind the pane beside it.
func TestTheCadenceIsTheOperatorsAndIndependentOfTheTopologyInterval(t *testing.T) {
	for _, tc := range []struct{ configured, wantMs int }{
		{configured: 0, wantMs: 500},
		{configured: 10, wantMs: 500}, // below the floor
		{configured: 250, wantMs: 250},
		{configured: 1000, wantMs: 1000},
	} {
		cfg := viewConfig()
		cfg.Defaults.RefreshInterval = 30 // seconds, and deliberately unrelated
		cfg.Defaults.Preview.RefreshMs = tc.configured
		m := newModel(cfg, nil, 0)
		r := &recorder{now: viewNow}
		r.install(&m)
		m.Init()

		want := time.Duration(tc.wantMs) * time.Millisecond
		if len(r.captureTicks) != 1 || r.captureTicks[0] != want {
			t.Errorf("preview refresh_ms=%d armed %v, want %v", tc.configured, r.captureTicks, want)
		}
		if len(r.ticks) != 1 || r.ticks[0] != 30*time.Second {
			t.Errorf("preview refresh_ms=%d changed the topology cadence to %v", tc.configured, r.ticks)
		}

		m = only(t, m, commands.CaptureTickMsg{})
		if len(r.captureTicks) != 2 || r.captureTicks[1] != want {
			t.Errorf("preview refresh_ms=%d re-armed at %v, want %v", tc.configured, r.captureTicks, want)
		}
		if len(r.ticks) != 1 {
			t.Errorf("a capture tick started a topology read")
		}
	}
}

// TestTheQueueIsServedInOrderWhateverOrderRepliesArriveIn is the fairness rule,
// and it is asserted with the replies deliberately out of order: the fifth tile
// is at the head of the queue and takes the first slot that comes free, whichever
// of the four in flight happens to free it.
func TestTheQueueIsServedInOrderWhateverOrderRepliesArriveIn(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 160, 45)
	all := windows(m)

	// Release the four in flight newest-first, then the next four likewise.
	for round := 0; round < 2; round++ {
		live := inFlightRequests(r, m)
		for i := len(live) - 1; i >= 0; i-- {
			m = reply(t, m, live[i], []string{"output"}, nil)
		}
	}

	if got := r.captureWindows(); !sameInts(got, all) {
		t.Fatalf("nine tiles were read in the order %v, want the reading order %v", got, all)
	}
	if got := m.captures.inFlight(); got != 1 {
		t.Fatalf("%d reads are in flight after eight replies, want the ninth tile's", got)
	}
	if len(m.captures.queue) != 0 {
		t.Fatalf("%d tiles are still waiting, want none", len(m.captures.queue))
	}
}

// TestNoWindowIsEverReadTwiceAtOnce is the per-window bound. A slow pane must not
// be asked again before it has answered, or the whole budget would be spent on
// the one window least able to use it.
func TestNoWindowIsEverReadTwiceAtOnce(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 160, 45)

	// Ticks, resizes and a fresh window tree all ask for the same tiles again.
	for i := 0; i < 5; i++ {
		m = only(t, m, commands.CaptureTickMsg{})
		m = only(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
		m = only(t, m, commands.TickMsg{})
		m = only(t, m, snapshot(m.refreshActiveSeq, 0, viewSessions(9)))
	}

	live := map[int]int{}
	for _, req := range r.captures {
		if _, held := m.captures.byRequest[req.RequestID]; !held {
			continue
		}
		live[windowIDOfTarget(req.Target)]++
		if live[windowIDOfTarget(req.Target)] > 1 {
			t.Fatalf("window %d has %d reads outstanding, want at most 1", windowIDOfTarget(req.Target), live[windowIDOfTarget(req.Target)])
		}
	}
	if m.captures.inFlight() > maxCapturesInFlight {
		t.Fatalf("%d reads are in flight, want at most %d", m.captures.inFlight(), maxCapturesInFlight)
	}
	if len(live) != m.captures.inFlight() {
		t.Fatalf("%d windows hold %d slots; the two indexes disagree", len(live), m.captures.inFlight())
	}
}

// TestTheBoundHoldsThroughPagesResizesAndFreshTrees pushes every event that can
// move a tile at the scheduler at once and asserts the two ceilings survive all
// of it. It is the regression net for the invariant rather than a statement about
// any one path.
func TestTheBoundHoldsThroughPagesResizesAndFreshTrees(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 160, 45)

	sizes := [][2]int{{160, 45}, {100, 24}, {120, 30}, {200, 6}, {60, 15}, {160, 45}}
	keys := []string{"pgdown", "end", "pgup", "home", "tab", "down"}
	counts := []int{9, 4, 9, 6, 9}

	for round := 0; round < 5; round++ {
		m = only(t, m, tea.WindowSizeMsg{Width: sizes[round][0], Height: sizes[round][1]})
		m = press(t, m, keys[round])
		m = only(t, m, commands.CaptureTickMsg{})
		m = only(t, m, commands.TickMsg{})
		m = only(t, m, snapshot(m.refreshActiveSeq, 0, viewSessions(counts[round])))

		// Answer roughly half of what is out, so later rounds start with the
		// scheduler part-loaded rather than idle.
		live := inFlightRequests(r, m)
		for i := 0; i < len(live); i += 2 {
			m = reply(t, m, live[i], []string{"output"}, nil)
		}

		if got := m.captures.inFlight(); got > maxCapturesInFlight {
			t.Fatalf("round %d left %d reads in flight, want at most %d", round, got, maxCapturesInFlight)
		}
		visible := map[int]bool{}
		for _, windowID := range visibleWindows(m) {
			visible[windowID] = true
		}
		for windowIDTarget := range m.previews {
			windowID := windowIDOfTarget(windowIDTarget)
			if !visible[windowID] {
				t.Fatalf("round %d retained a capture of window %d, which is not on screen", round, windowID)
			}
		}
		for _, windowIDTarget := range m.captures.queue {
			windowID := windowIDOfTarget(windowIDTarget)
			if !visible[windowID] {
				t.Fatalf("round %d left window %d queued, which is not on screen", round, windowID)
			}
		}
	}
}

// TestATileWithNoRoomForOutputIsNeverRead is the other half of "invisible tiles
// start no new reads": a tile can be on the page and still have nothing to show,
// and a read spent on a picture that is never drawn is a subprocess spent for
// nothing.
func TestATileWithNoRoomForOutputIsNeverRead(t *testing.T) {
	sessions := viewSessions(6)
	for _, size := range [][2]int{{200, 6}, {12, 5}, {4, 4}, {1, 1}} {
		m, r := atSize(t, sessions, 0, size[0], size[1])
		for i := 0; i < 3; i++ {
			m = only(t, m, commands.CaptureTickMsg{})
		}
		if len(r.captures) != 0 {
			t.Errorf("a %dx%d surface dispatched %v, want no reads", size[0], size[1], r.captureWindows())
		}
		if len(m.captures.targets) != 0 {
			t.Errorf("a %dx%d surface holds %d capture targets, want none", size[0], size[1], len(m.captures.targets))
		}
	}
}
