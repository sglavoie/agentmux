package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/layout"
)

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
		if !onPage[windowIDOfTarget(req.Target)] {
			t.Fatalf("window %d was read while off-page; only %v are visible", windowIDOfTarget(req.Target), visibleWindows(m))
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
		if got, want := [2]int{req.Width, req.Height}, extents[windowIDOfTarget(req.Target)]; got != want {
			t.Fatalf("the refetch of window %d asked for %dx%d, want the new tile's %dx%d",
				windowIDOfTarget(req.Target), got[0], got[1], want[0], want[1])
		}
		for _, old := range stale {
			if windowIDOfTarget(old.Target) == windowIDOfTarget(req.Target) && old.Generation == req.Generation {
				t.Fatalf("window %d was refetched at the same generation %d; the stale reply would have been accepted", windowIDOfTarget(req.Target), req.Generation)
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
		windowID := windowIDOfTarget(windowIDTarget)
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
			extents[windowIDOfTarget(tile.target)] = [2]int{tile.width, tile.height}
		}
		for windowIDTarget, p := range m.previews {
			windowID := windowIDOfTarget(windowIDTarget)
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
