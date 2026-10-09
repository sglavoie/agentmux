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
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
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

// visibleWindows is what the scheduler considers on screen right now.
func visibleWindows(m model) []int {
	tiles := m.visibleTiles()
	out := make([]int, 0, len(tiles))
	for _, tile := range tiles {
		out = append(out, tile.target.KittyWindowID())
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
		if got, want := [2]int{req.Width, req.Height}, extents[req.WindowID]; got != want {
			t.Fatalf("window %d was asked for %dx%d, want its tile's %dx%d", req.WindowID, got[0], got[1], want[0], want[1])
		}
	}
}

// tileExtents is the output region each visible window is currently drawn into.
// The grid distributes its remainder, so tiles on one page are not all the same
// size and a test that assumed they were would be asserting the wrong number.
func tileExtents(m model) map[int][2]int {
	out := map[int][2]int{}
	for _, tile := range m.visibleTiles() {
		out[tile.target.KittyWindowID()] = [2]int{tile.width, tile.height}
	}
	return out
}

// TestABurstOfTicksCoalescesIntoTheQueueItAlreadyHas is the cadence rule. A
// hundred ticks behind a slow Kitty must leave the queue exactly as long as one,
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
// together would either poll Kitty's window list ten times a second or leave
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
		live[req.WindowID]++
		if live[req.WindowID] > 1 {
			t.Fatalf("window %d has %d reads outstanding, want at most 1", req.WindowID, live[req.WindowID])
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
			windowID := windowIDTarget.KittyWindowID()
			if !visible[windowID] {
				t.Fatalf("round %d retained a capture of window %d, which is not on screen", round, windowID)
			}
		}
		for _, windowIDTarget := range m.captures.queue {
			windowID := windowIDTarget.KittyWindowID()
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

// TestOffPageTilesAreNeverRead is the visibility rule stated against a list too
// long for the grid.
func TestOffPageTilesAreNeverRead(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 100, 24) // two tiles per page

	onPage := map[int]bool{}
	for _, windowID := range visibleWindows(m) {
		onPage[windowID] = true
	}
	if len(onPage) != 2 {
		t.Fatalf("%d tiles fit a 100x24 grid, want 2", len(onPage))
	}
	for i := 0; i < 5; i++ {
		m = only(t, m, commands.CaptureTickMsg{})
	}
	for _, req := range r.captures {
		if !onPage[req.WindowID] {
			t.Fatalf("window %d was read while off-page; only %v are visible", req.WindowID, visibleWindows(m))
		}
	}
	if len(r.captures) != 2 {
		t.Fatalf("a two-tile page dispatched %d reads, want 2", len(r.captures))
	}
}

// TestPagingAwayDropsTheWorkAndPagingBackRefetches is the reconcile rule across a
// page change: queued turns for tiles that left are dropped, the buffers go with
// them, and the tiles that arrived are scheduled promptly rather than at the next
// tick.
func TestPagingAwayDropsTheWorkAndPagingBackRefetches(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 100, 24)
	firstPage := visibleWindows(m)

	live := inFlightRequests(r, m)
	for _, req := range live {
		m = reply(t, m, req, []string{"page one"}, nil)
	}
	if len(m.previews) != 2 {
		t.Fatalf("%d captures landed on the first page, want 2", len(m.previews))
	}

	m = press(t, m, "pgdown")
	secondPage := visibleWindows(m)
	if sameInts(firstPage, secondPage) {
		t.Fatalf("pgdown did not change the page; it is still %v", firstPage)
	}
	if len(m.previews) != 0 {
		t.Fatalf("%d captures survived the page change, want none", len(m.previews))
	}
	if got := r.captureWindows()[2:]; !sameInts(got, secondPage) {
		t.Fatalf("the page change read %v, want the newly visible %v", got, secondPage)
	}

	// Returning refetches rather than repainting what was shown before: the
	// terminals have been running unobserved in the meantime.
	before := len(r.captures)
	m = press(t, m, "pgup")
	if !sameInts(visibleWindows(m), firstPage) {
		t.Fatalf("pgup landed on %v, want the first page %v", visibleWindows(m), firstPage)
	}
	if got := r.captureWindows()[before:]; !sameInts(got, firstPage) {
		t.Fatalf("returning to the first page read %v, want %v refetched", got, firstPage)
	}
	if len(m.previews) != 0 {
		t.Fatal("returning to a page painted a buffer that was captured before the operator left it")
	}
}

// TestAReplyForAResizedTileReleasesItsSlotAndIsNotPainted is the whole reason
// request identity and target generation are two fields. The content is wrong for
// the tile it would be drawn into, and the slot is still owed back.
func TestAReplyForAResizedTileReleasesItsSlotAndIsNotPainted(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 160, 45)
	stale := inFlightRequests(r, m)

	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30}) // the tiles grew
	if m.captures.inFlight() != maxCapturesInFlight {
		t.Fatalf("the resize changed the in-flight count to %d; the reads are still out", m.captures.inFlight())
	}
	if len(r.captures) != maxCapturesInFlight {
		t.Fatalf("the resize dispatched %d more reads while four were still out", len(r.captures)-maxCapturesInFlight)
	}

	for _, req := range stale {
		m = reply(t, m, req, []string{"captured at the old size"}, nil)
	}
	if m.captures.inFlight() != maxCapturesInFlight {
		t.Fatalf("%d reads are in flight after the stale replies, want the four refetches", m.captures.inFlight())
	}
	if len(m.previews) != 0 {
		t.Fatalf("%d stale captures were painted into resized tiles", len(m.previews))
	}

	refetch := r.captures[maxCapturesInFlight:]
	if len(refetch) != maxCapturesInFlight {
		t.Fatalf("the stale replies produced %d refetches, want %d", len(refetch), maxCapturesInFlight)
	}
	extents := tileExtents(m)
	for _, req := range refetch {
		if got, want := [2]int{req.Width, req.Height}, extents[req.WindowID]; got != want {
			t.Fatalf("the refetch of window %d asked for %dx%d, want the new tile's %dx%d",
				req.WindowID, got[0], got[1], want[0], want[1])
		}
		for _, old := range stale {
			if old.WindowID == req.WindowID && old.Generation == req.Generation {
				t.Fatalf("window %d was refetched at the same generation %d; the stale reply would have been accepted", req.WindowID, req.Generation)
			}
		}
	}
}

// TestAShrunkTileIsRecutRatherThanRefetched is the other side of the resize rule.
// The cells a shrinking tile loses were already captured, so bumping the
// generation would refetch every window on every resize event a drag produces.
func TestAShrunkTileIsRecutRatherThanRefetched(t *testing.T) {
	sessions := viewSessions(4)
	m, r := atSize(t, sessions, 0, 160, 45)
	before := tileExtents(m)
	for _, req := range inFlightRequests(r, m) {
		lines := make([]string, req.Height)
		for i := range lines {
			lines[i] = strings.Repeat("x", req.Width)
		}
		m = reply(t, m, req, lines, nil)
	}
	if len(m.previews) != 4 {
		t.Fatalf("%d captures landed, want 4", len(m.previews))
	}
	dispatched := len(r.captures)

	// One row shorter: the same 3x3 grid, every tile losing a line and none
	// gaining a cell in either direction.
	m = only(t, m, tea.WindowSizeMsg{Width: 160, Height: 42})
	after := tileExtents(m)
	for windowID, was := range before {
		now := after[windowID]
		if now[0] > was[0] || now[1] > was[1] {
			t.Fatalf("window %d grew from %v to %v; this case is not the shrink it is named for", windowID, was, now)
		}
		if now[1] == was[1] {
			t.Fatalf("window %d did not shrink at all (%v); the case proves nothing", windowID, was)
		}
	}

	if len(r.captures) != dispatched {
		t.Fatalf("a shrink dispatched %d refetches, want 0", len(r.captures)-dispatched)
	}
	if len(m.previews) != 4 {
		t.Fatalf("%d captures survived a shrink, want all 4", len(m.previews))
	}
	for windowIDTarget, p := range m.previews {
		windowID := windowIDTarget.KittyWindowID()
		extent := after[windowID]
		if len(p.Output) != extent[1] {
			t.Fatalf("window %d holds %d lines in a %d-line tile", windowID, len(p.Output), extent[1])
		}
		for _, line := range p.Output {
			if got := layout.StyledWidth(line); got > extent[0] {
				t.Fatalf("window %d retained a %d-cell line in a %d-cell tile", windowID, got, extent[0])
			}
		}
	}
}

// TestRetainedTextNeverOutgrowsItsTile is the memory bound, asserted over a walk
// of every shipped size: whatever a tile is showing fits the tile, at every
// moment, including the instant after a resize and before the refetch lands.
func TestRetainedTextNeverOutgrowsItsTile(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 160, 45)

	for _, size := range shippedSizes {
		m = only(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, req := range inFlightRequests(r, m) {
			lines := make([]string, req.Height)
			for i := range lines {
				lines[i] = strings.Repeat("y", req.Width)
			}
			m = reply(t, m, req, lines, nil)
		}

		extents := map[int][2]int{}
		for _, tile := range m.visibleTiles() {
			extents[tile.target.KittyWindowID()] = [2]int{tile.width, tile.height}
		}
		for windowIDTarget, p := range m.previews {
			windowID := windowIDTarget.KittyWindowID()
			extent, ok := extents[windowID]
			if !ok {
				t.Fatalf("at %dx%d a capture of the invisible window %d is retained", size[0], size[1], windowID)
			}
			if len(p.Output) > extent[1] {
				t.Fatalf("at %dx%d window %d holds %d lines in a %d-line tile", size[0], size[1], windowID, len(p.Output), extent[1])
			}
			for _, line := range p.Output {
				if got := layout.StyledWidth(line); got > extent[0] {
					t.Fatalf("at %dx%d window %d holds a %d-cell line in a %d-cell tile", size[0], size[1], windowID, got, extent[0])
				}
			}
		}
	}
}

// TestADuplicateReplyReleasesNothing is the double-free guard. A slot released
// twice would let a fifth read out, and the bound would decay silently under
// exactly the retries this surface exists to survive.
func TestADuplicateReplyReleasesNothing(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 160, 45)
	req := inFlightRequests(r, m)[0]

	m = reply(t, m, req, []string{"once"}, nil)
	after := len(r.captures)
	if m.captures.inFlight() != maxCapturesInFlight {
		t.Fatalf("after one reply and its refill %d reads are in flight, want %d", m.captures.inFlight(), maxCapturesInFlight)
	}

	for i := 0; i < 3; i++ {
		next, cmd := send(t, m, commands.CaptureOutputMsg{Request: req, Lines: []string{"again"}})
		m = next
		if cmd != nil {
			t.Fatal("a duplicate reply started another read")
		}
	}
	if len(r.captures) != after {
		t.Fatalf("duplicate replies dispatched %d extra reads", len(r.captures)-after)
	}
	if m.captures.inFlight() != maxCapturesInFlight {
		t.Fatalf("duplicate replies left %d reads in flight, want %d", m.captures.inFlight(), maxCapturesInFlight)
	}
	if got := m.previews[session.KittyPaneTarget(req.WindowID)].Output; len(got) != 1 || got[0] != "once" {
		t.Fatalf("window %d shows %q; a duplicate repainted a settled tile", req.WindowID, got)
	}
}

// TestAnUnsolicitedReplyIsDropped covers the request the scheduler never issued —
// the shape a late reply takes after the model has been through a dismissal and a
// restart of its bookkeeping.
func TestAnUnsolicitedReplyIsDropped(t *testing.T) {
	sessions := viewSessions(4)
	m, r := atSize(t, sessions, 0, 160, 45)
	dispatched := len(r.captures)

	m, cmd := send(t, m, commands.CaptureOutputMsg{
		Request: commands.CaptureRequest{WindowID: windows(m)[0], Generation: 1, RequestID: 9999},
		Lines:   []string{"from nowhere"},
	})
	if cmd != nil {
		t.Fatal("an unsolicited reply started a read")
	}
	if len(r.captures) != dispatched {
		t.Fatalf("an unsolicited reply dispatched %d reads", len(r.captures)-dispatched)
	}
	if len(m.previews) != 0 {
		t.Fatal("an unsolicited reply painted a tile")
	}
}

// TestAFailedReadOccupiesItsOwnTileAndIsRetriedOnTheCadence. A failure is
// content, not an outage: the other eight tiles keep updating beside it, and the
// retry is the ordinary next turn rather than a backoff of its own.
func TestAFailedReadOccupiesItsOwnTileAndIsRetriedOnTheCadence(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 160, 45)
	live := inFlightRequests(r, m)
	broken, healthy := live[0], live[1]

	m = reply(t, m, broken, nil, errors.New("no matching window"))
	m = reply(t, m, healthy, []string{"still working"}, nil)

	failed := m.previews[session.KittyPaneTarget(broken.WindowID)]
	if failed.Content != dashboard.InspectorFailed {
		t.Fatalf("the failed window shows content state %v, want InspectorFailed", failed.Content)
	}
	if !strings.Contains(failed.Error, "no matching window") {
		t.Fatalf("the failed tile says %q, want the reason", failed.Error)
	}
	if len(failed.Output) != 0 {
		t.Fatalf("the failed tile kept %q", failed.Output)
	}
	ok := m.previews[session.KittyPaneTarget(healthy.WindowID)]
	if ok.Content != dashboard.InspectorReady || len(ok.Output) != 1 {
		t.Fatalf("the healthy tile beside a failure shows %v/%q", ok.Content, ok.Output)
	}

	// The failure does not hold a slot and does not stop the queue.
	if m.captures.inFlight() != maxCapturesInFlight {
		t.Fatalf("%d reads are in flight after a failure, want %d", m.captures.inFlight(), maxCapturesInFlight)
	}

	// Drain everything, then let one ordinary tick retry the broken window.
	for len(m.captures.queue) > 0 || m.captures.inFlight() > 0 {
		for _, req := range inFlightRequests(r, m) {
			m = reply(t, m, req, []string{"output"}, nil)
		}
	}
	before := len(r.captures)
	m = only(t, m, commands.CaptureTickMsg{})
	retried, found := r.lastCaptureFor(broken.WindowID)
	if !found || len(r.captures) <= before {
		t.Fatal("the tick after a failure retried nothing")
	}
	if retried.RequestID == broken.RequestID {
		t.Fatal("the retry reused the failed request's identity")
	}
	if retried.Generation != broken.Generation {
		t.Fatalf("the retry carried generation %d, want the tile's unchanged %d", retried.Generation, broken.Generation)
	}
}

// TestAnEmptyCaptureIsNotLoading is the distinction the five content states exist
// for, carried end to end: a terminal with nothing in it says "(no output)" and a
// tile whose read has not landed says "(loading…)", and an inference from
// emptiness would collapse the pair.
func TestAnEmptyCaptureIsNotLoading(t *testing.T) {
	sessions := viewSessions(2)
	m, r := atSize(t, sessions, 0, 100, 24)
	if len(r.captures) != 2 {
		t.Fatalf("a two-tile grid dispatched %d reads, want 2", len(r.captures))
	}

	empty := r.captures[0]
	m = reply(t, m, empty, nil, nil)

	landed, ok := m.previews[session.KittyPaneTarget(empty.WindowID)]
	if !ok {
		t.Fatal("an empty capture stored nothing, so its tile is indistinguishable from one still loading")
	}
	if landed.Content != dashboard.InspectorReady {
		t.Fatalf("an empty capture stored content state %v, want InspectorReady", landed.Content)
	}
	if _, pending := m.previews[session.KittyPaneTarget(r.captures[1].WindowID)]; pending {
		t.Fatal("the tile whose read has not landed already holds a capture")
	}

	frame := frameOf(m)
	if !strings.Contains(frame, "(no output)") {
		t.Fatalf("the frame does not say the captured tile is empty:\n%s", frame)
	}
	if !strings.Contains(frame, "(loading…)") {
		t.Fatalf("the frame does not say the other tile is still loading:\n%s", frame)
	}
}

// TestAClosedSessionsReplyIsDroppedAndItsSlotReleased. The session went away while
// its read was out; the picture is of a terminal that no longer exists and the
// slot is still owed back.
func TestAClosedSessionsReplyIsDroppedAndItsSlotReleased(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 160, 45)
	doomed := inFlightRequests(r, m)[1]

	survivors := make([]session.RunningSession, 0, len(sessions))
	for _, s := range m.sessions {
		if s.WindowID != doomed.WindowID {
			survivors = append(survivors, s)
		}
	}
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, survivors))
	if _, still := m.captures.targets[session.KittyPaneTarget(doomed.WindowID)]; still {
		t.Fatalf("window %d is still a capture target after its session closed", doomed.WindowID)
	}

	before := m.captures.inFlight()
	m = reply(t, m, doomed, []string{"from a window that is gone"}, nil)
	if p, painted := m.previews[session.KittyPaneTarget(doomed.WindowID)]; painted {
		t.Fatalf("the closed window's reply was painted: %+v", p)
	}
	for _, req := range r.captures {
		if req.WindowID == doomed.WindowID && req.RequestID != doomed.RequestID {
			t.Fatalf("window %d was read again after its session closed", doomed.WindowID)
		}
	}
	if m.captures.inFlight() != before {
		t.Fatalf("%d reads are in flight, want the slot released and refilled from the queue (%d)", m.captures.inFlight(), before)
	}
}

// TestAnAnchorChangeInvalidatesTheReadThatWasOut is the anchor rule at the
// scheduler: a restarted harness keeps its tab and gets a new window, and the read
// of the retired window must neither be painted into the tile nor credited to its
// replacement.
func TestAnAnchorChangeInvalidatesTheReadThatWasOut(t *testing.T) {
	sessions := viewSessions(3)
	m, r := atSize(t, sessions, 0, 160, 45)
	outstanding := inFlightRequests(r, m)[0]
	oldWindow := outstanding.WindowID

	restarted := make([]session.RunningSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		if s.WindowID == oldWindow {
			s.WindowID = oldWindow + 1000
		}
		restarted = append(restarted, s)
	}
	newWindow := oldWindow + 1000

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, restarted))
	fresh, found := r.lastCaptureFor(newWindow)
	if !found {
		t.Fatalf("the new anchor window %d was never read", newWindow)
	}
	if fresh.Generation == outstanding.Generation {
		t.Fatalf("the new anchor reused generation %d; the retired window's reply would be accepted", fresh.Generation)
	}

	m = reply(t, m, outstanding, []string{"from the retired window"}, nil)
	if _, painted := m.previews[session.KittyPaneTarget(oldWindow)]; painted {
		t.Fatalf("the retired window %d holds a capture", oldWindow)
	}
	if _, stolen := m.previews[session.KittyPaneTarget(newWindow)]; stolen {
		t.Fatalf("the retired window's text was painted into its replacement %d", newWindow)
	}
}

// TestAnOldSnapshotCannotPaintAnotherSession is the same claim made about the
// storage key rather than about the generation: a reply is filed under the window
// its own request names, so no reply can ever reach a tile it was not issued for.
func TestAnOldSnapshotCannotPaintAnotherSession(t *testing.T) {
	sessions := viewSessions(4)
	m, r := atSize(t, sessions, 0, 160, 45)
	live := inFlightRequests(r, m)

	for i, req := range live {
		m = reply(t, m, req, []string{"belongs to window " + itoa(req.WindowID)}, nil)
		if got := len(m.previews); got != i+1 {
			t.Fatalf("%d replies produced %d captures", i+1, got)
		}
	}
	for windowIDTarget, p := range m.previews {
		windowID := windowIDTarget.KittyWindowID()
		want := "belongs to window " + itoa(windowID)
		if len(p.Output) != 1 || p.Output[0] != want {
			t.Fatalf("window %d shows %q, want %q", windowID, p.Output, want)
		}
	}
}

// TestDismissalStopsSchedulingAndIgnoresLateReplies is shutdown. There is no
// cancellation to reach for — the reads already out are bounded by their own
// subprocess deadline — so the only thing available is to start nothing further
// and accept nothing further, while still letting every late reply release the
// slot it took.
func TestDismissalStopsSchedulingAndIgnoresLateReplies(t *testing.T) {
	sessions := viewSessions(9)
	for _, name := range []string{"esc", "q", "ctrl+c"} {
		t.Run(name, func(t *testing.T) {
			m, r := atSize(t, sessions, 0, 160, 45)
			live := inFlightRequests(r, m)
			if len(live) != maxCapturesInFlight {
				t.Fatalf("%d reads are out at dismissal, want %d", len(live), maxCapturesInFlight)
			}
			dispatched := len(r.captures)
			ticks := len(r.captureTicks)

			m, cmd := send(t, m, key(t, name))
			if !quits(t, cmd) {
				t.Fatalf("%s did not dismiss the overview", name)
			}
			if len(m.captures.queue) != 0 {
				t.Fatalf("%d tiles are still queued after dismissal", len(m.captures.queue))
			}

			// Nothing that arrives afterwards may start work.
			m = only(t, m, commands.CaptureTickMsg{})
			m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
			m = only(t, m, commands.TickMsg{})
			m = only(t, m, snapshot(m.refreshActiveSeq, 0, viewSessions(6)))
			if len(r.captures) != dispatched {
				t.Fatalf("a dismissed surface dispatched %d more reads", len(r.captures)-dispatched)
			}
			if len(r.captureTicks) != ticks {
				t.Fatalf("a dismissed surface re-armed the capture ticker %d more times", len(r.captureTicks)-ticks)
			}

			for _, req := range live {
				next, cmd := send(t, m, commands.CaptureOutputMsg{Request: req, Lines: []string{"late"}})
				m = next
				if cmd != nil {
					t.Fatal("a late reply started a read on a dismissed surface")
				}
			}
			if m.captures.inFlight() != 0 {
				t.Fatalf("%d slots are still held after every late reply landed", m.captures.inFlight())
			}
			if len(m.previews) != 0 {
				t.Fatalf("%d late replies were painted onto a surface that is closing", len(m.previews))
			}
		})
	}
}

// TestAConfirmedFocusStopsTheScheduler is the other exit. Enter leaves the surface
// too, and it leaves with up to four reads out.
func TestAConfirmedFocusStopsTheScheduler(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 160, 45)
	dispatched := len(r.captures)

	m = press(t, m, "enter")
	m, cmd := send(t, m, commands.FocusDoneMsg{})
	if !quits(t, cmd) {
		t.Fatal("a confirmed focus did not exit the overview")
	}
	if !m.captures.stopped {
		t.Fatal("a confirmed focus left the capture scheduler running")
	}

	m = only(t, m, commands.CaptureTickMsg{})
	if len(r.captures) != dispatched {
		t.Fatalf("a focused-away surface dispatched %d more reads", len(r.captures)-dispatched)
	}
}

// TestAFailedFocusLeavesTheCapturesRunning is the control for the test above: the
// operator is still on the overview, so the tiles must keep updating.
func TestAFailedFocusLeavesTheCapturesRunning(t *testing.T) {
	sessions := viewSessions(9)
	m, r := atSize(t, sessions, 0, 160, 45)
	for _, req := range inFlightRequests(r, m) {
		m = reply(t, m, req, []string{"output"}, nil)
	}
	dispatched := len(r.captures)

	m = press(t, m, "enter")
	m = only(t, m, commands.FocusDoneMsg{Err: errors.New("no matching tab")})
	if m.captures.stopped {
		t.Fatal("a failed focus stopped the capture scheduler; the surface the operator is left on would freeze")
	}

	// Every slot is held by the refills the replies above released, so the tick
	// buys a place in the queue rather than an immediate read — which is the same
	// answer a running surface gives, and the opposite of a dismissed one's.
	ticks := len(r.captureTicks)
	m = only(t, m, commands.CaptureTickMsg{})
	if len(r.captureTicks) != ticks+1 {
		t.Fatal("a failed focus left the capture cadence dead")
	}
	if len(m.captures.queue) == 0 {
		t.Fatal("the tick after a failed focus queued nothing")
	}

	for _, req := range inFlightRequests(r, m) {
		m = reply(t, m, req, []string{"output"}, nil)
	}
	if len(r.captures) <= dispatched {
		t.Fatal("a failed focus left the queue unable to drain")
	}
}

// TestRepliesReleasedInArbitraryOrderUnderRealGoroutines drives the scheduler the
// way the runtime does: the commands it hands back are run concurrently, parked
// on a channel, and released in the reverse of the order they were issued.
//
// The claim is that the bounds and the bookkeeping are properties of the
// scheduler rather than of the order replies happen to arrive in, and running it
// under -race is what makes "the controller is shared by pointer across model
// copies" a checked statement rather than a comment.
func TestRepliesReleasedInArbitraryOrderUnderRealGoroutines(t *testing.T) {
	sessions := viewSessions(9)
	m, r := loadedAt(t, sessions, 0)
	r.park = make(chan commands.CaptureRequest, 64)

	m, cmd := send(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	spawn(cmd)

	delivered := 0
	for round := 0; round < 3; round++ {
		parked := make([]commands.CaptureRequest, 0, maxCapturesInFlight)
		for i := 0; i < maxCapturesInFlight && delivered+len(parked) < len(sessions); i++ {
			parked = append(parked, <-r.park)
		}
		for i := len(parked) - 1; i >= 0; i-- {
			next, cmd := send(t, m, commands.CaptureOutputMsg{
				Request: parked[i],
				Lines:   []string{"window " + itoa(parked[i].WindowID)},
			})
			m = next
			spawn(cmd)
			delivered++
			if got := m.captures.inFlight(); got > maxCapturesInFlight {
				t.Fatalf("%d reads are in flight, want at most %d", got, maxCapturesInFlight)
			}
		}
	}

	if delivered != len(sessions) {
		t.Fatalf("%d replies were delivered, want one per tile", delivered)
	}
	if len(m.previews) != len(sessions) {
		t.Fatalf("%d tiles hold a capture, want all %d", len(m.previews), len(sessions))
	}
	for windowIDTarget, p := range m.previews {
		windowID := windowIDTarget.KittyWindowID()
		if len(p.Output) != 1 || p.Output[0] != "window "+itoa(windowID) {
			t.Fatalf("window %d shows %q", windowID, p.Output)
		}
	}
	if m.captures.inFlight() != 0 || len(m.captures.queue) != 0 {
		t.Fatalf("the scheduler settled with %d in flight and %d queued, want neither",
			m.captures.inFlight(), len(m.captures.queue))
	}
}

// spawn runs a command tree the way the runtime does: each leaf in its own
// goroutine, with a batch fanned out rather than run in sequence.
func spawn(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		if batch, ok := cmd().(tea.BatchMsg); ok {
			for _, c := range batch {
				spawn(c)
			}
		}
	}()
}

func itoa(n int) string { return strconv.Itoa(n) }
