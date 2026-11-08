// The lifecycle's tests. They drive the real model through the real update loop
// with every seam replaced, so each one is a statement about what an operator's
// keypress or a daemon's answer DOES rather than about how it is implemented.
//
// Two properties make them deterministic. The clock is a field the test
// advances, so freshness is asserted at exact
// ages instead of near them. And the injected commands RECORD and return nothing,
// so every message this package consumes is one the test chose to send — which is
// what lets a stale completion, a coalesced tick, and a focus answered after a
// dismissal all be written as ordinary sequences.
package overview

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// recorder stands in for the world: it remembers what the model asked for and
// answers nothing.
//
// Answering nothing is the point. A seam that produced its own reply would decide
// the interleaving, and the interleavings are what these tests are about.
type recorder struct {
	topology []topologyAsk
	focus    []int
	ticks    []time.Duration
	now      time.Time

	// captures is every capture request the scheduler dispatched, in order, and
	// captureTicks is the separate cadence it re-armed. They are recorded when the
	// SEAM is called rather than when the command runs, which is what lets a test
	// assert on what was scheduled without ever producing a reply.
	captures     []commands.CaptureRequest
	captureTicks []time.Duration
	// park, when set, receives every dispatched request instead of answering it,
	// so a test can release replies in whatever order it likes.
	park chan commands.CaptureRequest

	// dispatches is every session action that reached the effect seam, and
	// relaunches is every close the recovery stack asked to undo. They are the
	// two mutating seams, and recording them separately from everything above is
	// what lets a test say "browsing asked for nothing" as an assertion about a
	// slice rather than about a count that some other seam also moves.
	dispatches []sessionactions.Dispatch
	relaunches []sessionactions.Snapshot
	// relaunchRequests are the recovery request identities the seam was handed,
	// which is what a test answers a relaunch with.
	relaunchRequests []sessionactions.RequestID
}

// actionIDs names the actions dispatched so far, in order, which is the form
// every claim about what a keypress ran is written in.
func (r *recorder) actionIDs() []commands.ID {
	out := make([]commands.ID, 0, len(r.dispatches))
	for _, d := range r.dispatches {
		out = append(out, d.Action)
	}
	return out
}

// lastDispatch is the newest action that reached the seam.
func (r *recorder) lastDispatch() (sessionactions.Dispatch, bool) {
	if len(r.dispatches) == 0 {
		return sessionactions.Dispatch{}, false
	}
	return r.dispatches[len(r.dispatches)-1], true
}

type topologyAsk struct {
	seq            int
	sourceWindowID int
}

// install replaces every seam. The returned commands are inert: they produce a
// nil message, which Bubble Tea would discard and which no test delivers.
func (r *recorder) install(m *model) {
	m.deps = deps{
		topology: func(seq, sourceWindowID int) tea.Cmd {
			r.topology = append(r.topology, topologyAsk{seq: seq, sourceWindowID: sourceWindowID})
			return func() tea.Msg { return nil }
		},
		focus: func(tabID int) tea.Cmd {
			r.focus = append(r.focus, tabID)
			return func() tea.Msg { return nil }
		},
		action: func(d sessionactions.Dispatch) tea.Cmd {
			r.dispatches = append(r.dispatches, d)
			return func() tea.Msg { return nil }
		},
		relaunch: func(request sessionactions.RequestID, snapshot sessionactions.Snapshot) tea.Cmd {
			r.relaunchRequests = append(r.relaunchRequests, request)
			r.relaunches = append(r.relaunches, snapshot)
			return func() tea.Msg { return nil }
		},
		tick: func(d time.Duration) tea.Cmd {
			r.ticks = append(r.ticks, d)
			return func() tea.Msg { return nil }
		},
		capture: func(req commands.CaptureRequest) tea.Cmd {
			r.captures = append(r.captures, req)
			park := r.park
			return func() tea.Msg {
				if park != nil {
					park <- req
				}
				return nil
			}
		},
		captureTick: func(d time.Duration) tea.Cmd {
			r.captureTicks = append(r.captureTicks, d)
			return func() tea.Msg { return nil }
		},
		now: func() time.Time { return r.now },
	}
}

// captureWindows names the windows the scheduler has asked about so far, which is
// the form nearly every claim about fairness and bounds is written in.
func (r *recorder) captureWindows() []int {
	out := make([]int, 0, len(r.captures))
	for _, req := range r.captures {
		out = append(out, windowIDOfTarget(req.Target))
	}
	return out
}

// lastCaptureFor is the newest request issued for one window.
func (r *recorder) lastCaptureFor(windowID int) (commands.CaptureRequest, bool) {
	for i := len(r.captures) - 1; i >= 0; i-- {
		if windowIDOfTarget(r.captures[i].Target) == windowID {
			return r.captures[i], true
		}
	}
	return commands.CaptureRequest{}, false
}

// newTestModel is the model as the constructor leaves it: a startup read armed
// and not yet dispatched, which is the state Init is about to act on.
func newTestModel(sourceWindowID int) (model, *recorder) {
	r := &recorder{now: viewNow}
	m := newModel(viewConfig(), nil, sourceWindowID)
	r.install(&m)
	return m, r
}

// started is the model after Init: the startup read is out and the first interval
// is scheduled. This is the state the overview is in for its whole first interval,
// which is where the coalescing rules matter most.
func started(t *testing.T, sourceWindowID int) (model, *recorder) {
	t.Helper()
	m, r := newTestModel(sourceWindowID)
	if !m.refreshInFlight {
		t.Fatal("the constructor left no read in flight; the first tick would start a second startup read")
	}
	m.Init()
	if len(r.topology) != 1 {
		t.Fatalf("Init dispatched %d reads, want exactly 1", len(r.topology))
	}
	return m, r
}

// loadedAt opens from one session's pane, delivers the first snapshot, then
// navigates from the top-left card to the session the test needs selected.
func loadedAt(t *testing.T, sessions []session.RunningSession, index int) (model, *recorder) {
	t.Helper()
	m, r := started(t, sessions[index].WindowID)
	m = only(t, m, snapshot(firstRefreshSeq, sessions[index].TabID, sessions))
	for steps := 0; m.selectedTabID != sessions[index].TabID && steps < len(sessions); steps++ {
		m = press(t, m, "tab")
	}
	if m.selectedTabID != sessions[index].TabID {
		t.Fatalf("the fixture selected tab %d, want %d", m.selectedTabID, sessions[index].TabID)
	}
	return m, r
}

// send runs one message through Update and returns the model and the command it
// produced.
func send(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	next, ok := updated.(model)
	if !ok {
		t.Fatalf("Update returned %T, want the overview model", updated)
	}
	return next, cmd
}

// only sends a message and discards the command, for the cases where the command
// is not the subject.
func only(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := send(t, m, msg)
	return next
}

// key builds the KeyPressMsg whose String() is the given binding, which is what
// handleKeyPress dispatches on.
func key(t *testing.T, name string) tea.KeyPressMsg {
	t.Helper()
	switch name {
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "ctrl+c":
		return tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	case "ctrl+r":
		return tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl})
	case "ctrl+d", "ctrl+u", "ctrl+f", "ctrl+b":
		return tea.KeyPressMsg(tea.Key{Code: rune(name[len(name)-1]), Mod: tea.ModCtrl})
	case "left":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft})
	case "right":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyRight})
	case "up":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyUp})
	case "down":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	case "tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})
	case "shift+tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})
	case "pgup":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp})
	case "pgdown":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown})
	case "home":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyHome})
	case "end":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd})
	case "space":
		return tea.KeyPressMsg(tea.Key{Code: ' '}) // no Text: String() returns "space"
	}
	if runes := []rune(name); len(runes) == 1 {
		return tea.KeyPressMsg(tea.Key{Code: runes[0], Text: name})
	}
	t.Fatalf("this test helper cannot spell the key %q", name)
	return tea.KeyPressMsg{}
}

// press is the common shape: send a keypress and keep the model.
func press(t *testing.T, m model, name string) model {
	t.Helper()
	return only(t, m, key(t, name))
}

// quits reports whether a command is the program exit. Running the command is safe
// because tea.Quit only produces a message.
func quits(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// snapshot is one successful read.
func snapshot(seq int, selectionTabID int, sessions []session.RunningSession) commands.SessionsMsg {
	return commands.SessionsMsg{
		Seq:            seq,
		Sessions:       sessions,
		SelectionTabID: selectionTabID,
		Topology:       commands.TopologyFreshness{Source: commands.TopologyFromDaemon},
	}
}

// tabs names the live set by tab, which is the identity every selection claim
// below is written against.
func tabs(m model) []int {
	out := make([]int, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.TabID)
	}
	return out
}

// overviewInput is the Input the model renders itself through, so a test can
// assert on the frame an operator would see without a Bubble Tea runtime. It
// mirrors View, which cannot be reused here because tea.View does not hand back
// its string.
func overviewInput(m model) Input {
	return Input{
		Compact:       m.compact,
		Width:         m.width,
		Height:        m.height,
		Sessions:      m.sessions,
		Selected:      m.selected,
		SourceTabID:   m.sourceTabID,
		Previews:      m.previews,
		Cfg:           m.cfg,
		LastActivity:  m.lastActivity,
		IdleThreshold: m.idleThreshold,
		Now:           m.deps.now(),
		Feedback:      m.feedback(),
		FocusPending:  m.focusTabID != 0,
		Closing:       m.quitAfterPreference,
		Overlay:       m.overlayView(),
		OverlayHints:  m.overlayHints(),
	}
}

// frameOf is the model's frame with the styling stripped, which is the only form a
// claim about text can be made against.
func frameOf(m model) string {
	return strings.Join(plain(strings.Split(Render(overviewInput(m)), "\n")), "\n")
}

// hintRowOf is the last line of that frame: the one row the footer is allowed.
// A claim about what the footer does NOT say has to be made against this rather
// than against the whole frame, where the feedback row and a modal's own hint
// line carry key names too.
func hintRowOf(m model) string {
	lines := strings.Split(frameOf(m), "\n")
	return strings.TrimRight(lines[len(lines)-1], " ")
}
