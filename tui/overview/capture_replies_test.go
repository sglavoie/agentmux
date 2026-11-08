package overview

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

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
	if got := m.previews[paneTargetForWindow(windowIDOfTarget(req.Target))].Output; len(got) != 1 || got[0] != "once" {
		t.Fatalf("window %d shows %q; a duplicate repainted a settled tile", windowIDOfTarget(req.Target), got)
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
		Request: commands.CaptureRequest{Target: paneTargetForWindow(windows(m)[0]), Generation: 1, RequestID: 9999},
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

	failed := m.previews[paneTargetForWindow(windowIDOfTarget(broken.Target))]
	if failed.Content != dashboard.InspectorFailed {
		t.Fatalf("the failed window shows content state %v, want InspectorFailed", failed.Content)
	}
	if !strings.Contains(failed.Error, "no matching window") {
		t.Fatalf("the failed tile says %q, want the reason", failed.Error)
	}
	if len(failed.Output) != 0 {
		t.Fatalf("the failed tile kept %q", failed.Output)
	}
	ok := m.previews[paneTargetForWindow(windowIDOfTarget(healthy.Target))]
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
	retried, found := r.lastCaptureFor(windowIDOfTarget(broken.Target))
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

	landed, ok := m.previews[paneTargetForWindow(windowIDOfTarget(empty.Target))]
	if !ok {
		t.Fatal("an empty capture stored nothing, so its tile is indistinguishable from one still loading")
	}
	if landed.Content != dashboard.InspectorReady {
		t.Fatalf("an empty capture stored content state %v, want InspectorReady", landed.Content)
	}
	if _, pending := m.previews[paneTargetForWindow(windowIDOfTarget(r.captures[1].Target))]; pending {
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
		if s.WindowID != windowIDOfTarget(doomed.Target) {
			survivors = append(survivors, s)
		}
	}
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, survivors))
	if _, still := m.captures.targets[paneTargetForWindow(windowIDOfTarget(doomed.Target))]; still {
		t.Fatalf("window %d is still a capture target after its session closed", windowIDOfTarget(doomed.Target))
	}

	before := m.captures.inFlight()
	m = reply(t, m, doomed, []string{"from a window that is gone"}, nil)
	if p, painted := m.previews[paneTargetForWindow(windowIDOfTarget(doomed.Target))]; painted {
		t.Fatalf("the closed window's reply was painted: %+v", p)
	}
	for _, req := range r.captures {
		if windowIDOfTarget(req.Target) == windowIDOfTarget(doomed.Target) && req.RequestID != doomed.RequestID {
			t.Fatalf("window %d was read again after its session closed", windowIDOfTarget(doomed.Target))
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
	oldWindow := windowIDOfTarget(outstanding.Target)

	restarted := make([]session.RunningSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		if s.WindowID == oldWindow {
			s.WindowID = oldWindow + 1000
			s.Pane = paneTargetForWindow(s.WindowID).Pane
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
	if _, painted := m.previews[paneTargetForWindow(oldWindow)]; painted {
		t.Fatalf("the retired window %d holds a capture", oldWindow)
	}
	if _, stolen := m.previews[paneTargetForWindow(newWindow)]; stolen {
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
		m = reply(t, m, req, []string{"belongs to window " + itoa(windowIDOfTarget(req.Target))}, nil)
		if got := len(m.previews); got != i+1 {
			t.Fatalf("%d replies produced %d captures", i+1, got)
		}
	}
	for windowIDTarget, p := range m.previews {
		windowID := windowIDOfTarget(windowIDTarget)
		want := "belongs to window " + itoa(windowID)
		if len(p.Output) != 1 || p.Output[0] != want {
			t.Fatalf("window %d shows %q, want %q", windowID, p.Output, want)
		}
	}
}
