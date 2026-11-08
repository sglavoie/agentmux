package overview

import (
	"errors"
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
)

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
	m, cmd := send(t, m, commands.AttachDoneMsg{Switched: true})
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
	m = only(t, m, commands.AttachDoneMsg{Err: errors.New("no matching tab")})
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
				Lines:   []string{"window " + itoa(windowIDOfTarget(parked[i].Target))},
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
		windowID := windowIDOfTarget(windowIDTarget)
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
