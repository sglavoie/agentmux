// The scheduler is keyed on the pane a tile reads, in whichever backend hosts
// it, not on a Kitty window ID. These tests put tmux-hosted sessions on the
// grid beside Kitty ones and hold them to the same bounds, order, and lifetime
// rules capture_test.go states for Kitty alone.
package overview

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// mixedSessions is nine sessions with every other one hosted in tmux: no Kitty
// window, a pane reference instead. Pane %N deliberately reuses the number of
// the Kitty window beside it.
func mixedSessions() []session.RunningSession {
	sessions := viewSessions(9)
	for i := range sessions {
		if i%2 == 1 {
			sessions[i].Backend = runtimesession.BackendTmux
			sessions[i].Pane = backend.PaneID("%" + strconv.Itoa(sessions[i-1].WindowID))
			sessions[i].WindowID = 0
		}
	}
	return sessions
}

func targetsOf(reqs []commands.CaptureRequest) []session.PaneTarget {
	out := make([]session.PaneTarget, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, req.Target)
	}
	return out
}

func TestTmuxTilesAreScheduledLikeKittyOnes(t *testing.T) {
	sessions := mixedSessions()
	m, r := atSize(t, sessions, 0, 160, 45)

	tiles := m.visibleTiles()
	if len(tiles) != 9 {
		t.Fatalf("%d tiles are visible, want all 9 including the tmux-hosted ones", len(tiles))
	}
	tmuxTiles := 0
	for i, tile := range tiles {
		want := m.sessions[i].PaneTarget()
		if tile.target != want || tile.target.IsZero() {
			t.Fatalf("tile %d reads %v, want %v", i, tile.target, want)
		}
		if tile.target.Backend == runtimesession.BackendTmux {
			tmuxTiles++
			if tile.target.KittyWindowID() != 0 {
				t.Fatalf("tmux tile %v claims a Kitty window", tile.target)
			}
		}
	}
	if tmuxTiles != 4 {
		t.Fatalf("%d tmux tiles, want 4", tmuxTiles)
	}

	// The global bound and the FIFO order hold across backends.
	if len(r.captures) != maxCapturesInFlight || m.captures.inFlight() != maxCapturesInFlight {
		t.Fatalf("dispatched %d reads with %d in flight, want %d", len(r.captures), m.captures.inFlight(), maxCapturesInFlight)
	}
	for i, target := range targetsOf(r.captures) {
		if target != tiles[i].target {
			t.Fatalf("read %d went to %v, want tile %d's %v", i, target, i, tiles[i].target)
		}
	}
	for _, req := range r.captures {
		if req.WindowID != req.Target.KittyWindowID() {
			t.Fatalf("request for %v carries window %d", req.Target, req.WindowID)
		}
	}

	// Draining every reply reads each tile exactly once, tmux ones included.
	seen := map[session.PaneTarget]int{}
	for delivered := 0; delivered < len(r.captures); delivered++ {
		req := r.captures[delivered]
		seen[req.Target]++
		m = reply(t, m, req, []string{"output of " + req.Target.String()}, nil)
		if m.captures.inFlight() > maxCapturesInFlight {
			t.Fatalf("%d reads in flight", m.captures.inFlight())
		}
	}
	if len(seen) != 9 {
		t.Fatalf("read %d distinct panes, want 9: %v", len(seen), seen)
	}
	for target, count := range seen {
		if count != 1 {
			t.Fatalf("%v was read %d times in one round", target, count)
		}
		preview, ok := m.previews[target]
		if !ok || len(preview.Output) != 1 || preview.Output[0] != "output of "+target.String() {
			t.Fatalf("%v shows %+v", target, preview)
		}
	}
	if m.captures.inFlight() != 0 || len(m.captures.queue) != 0 {
		t.Fatalf("settled with %d in flight and %d queued", m.captures.inFlight(), len(m.captures.queue))
	}
}

// A Kitty window and a pane of another backend that share an ID are two panes.
func TestTheSamePaneIDInTwoBackendsIsTwoTargets(t *testing.T) {
	sessions := viewSessions(2)
	sessions[1].Backend = runtimesession.BackendTmux
	sessions[1].Pane = backend.PaneID(session.KittyPaneTarget(sessions[0].WindowID).Pane.ID)
	sessions[1].WindowID = 0
	m, r := atSize(t, sessions, 0, 160, 45)

	if len(r.captures) != 2 || r.captures[0].Target == r.captures[1].Target {
		t.Fatalf("requests %v; want one per backend", targetsOf(r.captures))
	}
	if r.captures[0].Target.Pane != r.captures[1].Target.Pane {
		t.Fatal("the fixture no longer shares a pane ID across backends")
	}
	m = reply(t, m, r.captures[0], []string{"kitty text"}, nil)
	m = reply(t, m, r.captures[1], []string{"tmux text"}, nil)
	if got := m.previews[r.captures[0].Target].Output; len(got) != 1 || got[0] != "kitty text" {
		t.Fatalf("the Kitty tile shows %q", got)
	}
	if got := m.previews[r.captures[1].Target].Output; len(got) != 1 || got[0] != "tmux text" {
		t.Fatalf("the tmux tile shows %q", got)
	}
}

// A capture that could not be taken, and one that arrives for a tile that has
// since changed, never become content.
func TestUnavailableAndStaleTmuxCapturesPaintNothing(t *testing.T) {
	sessions := mixedSessions()
	m, r := atSize(t, sessions, 0, 160, 45)
	var tmuxReq commands.CaptureRequest
	for _, req := range r.captures {
		if req.Target.Backend == runtimesession.BackendTmux {
			tmuxReq = req
			break
		}
	}
	if tmuxReq.Target.IsZero() {
		t.Fatal("the first round holds no tmux read")
	}

	// Unavailable: the tile says so and holds no output.
	m = reply(t, m, tmuxReq, nil, commands.ErrCaptureUnavailable)
	failed := m.previews[tmuxReq.Target]
	if failed.Content != dashboard.InspectorFailed || len(failed.Output) != 0 ||
		!strings.Contains(failed.Error, "capture unavailable") {
		t.Fatalf("an unavailable capture shows %+v", failed)
	}

	// Stale: a 120x30 surface has fewer, wider tiles than a 160x45 one, so the
	// tile grows and is retargeted under a new generation; the reply to the old
	// request releases its slot and paints nothing.
	m = only(t, m, commands.CaptureTickMsg{})
	stale, ok := lastCaptureForTarget(r, tmuxReq.Target)
	for !ok || stale.RequestID == tmuxReq.RequestID {
		// Drain other reads until the tmux tile is asked again.
		var next commands.CaptureRequest
		for _, req := range inFlightRequests(r, m) {
			if req.Target != tmuxReq.Target {
				next = req
				break
			}
		}
		if next.RequestID == 0 {
			t.Fatal("the tmux tile was never asked again")
		}
		m = reply(t, m, next, []string{"x"}, nil)
		stale, ok = lastCaptureForTarget(r, tmuxReq.Target)
	}
	before := m.captures.targets[tmuxReq.Target].generation
	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	if after := m.captures.targets[tmuxReq.Target].generation; after == before {
		t.Fatal("growing the surface did not retarget the tile; the reply below would not be stale")
	}
	inFlight := m.captures.inFlight()
	m = reply(t, m, stale, []string{"text for the old size"}, nil)
	if got := m.previews[tmuxReq.Target]; len(got.Output) != 0 {
		t.Fatalf("a stale reply painted %q", got.Output)
	}
	if _, held := m.captures.byRequest[stale.RequestID]; held {
		t.Fatal("a stale reply kept its slot")
	}
	if m.captures.inFlight() > inFlight {
		t.Fatalf("in flight went from %d to %d", inFlight, m.captures.inFlight())
	}
	// And the same reply delivered twice is not a second release.
	if m, cmd := send(t, m, commands.CaptureOutputMsg{Request: stale, Lines: []string{"again"}}); cmd != nil ||
		len(m.previews[tmuxReq.Target].Output) != 0 {
		t.Fatal("a duplicate stale reply did something")
	}
}

func lastCaptureForTarget(r *recorder, target session.PaneTarget) (commands.CaptureRequest, bool) {
	for i := len(r.captures) - 1; i >= 0; i-- {
		if r.captures[i].Target == target {
			return r.captures[i], true
		}
	}
	return commands.CaptureRequest{}, false
}
