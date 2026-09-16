// The lifecycle's tests. They drive the real model through the real update loop
// with every seam replaced, so each one is a statement about what an operator's
// keypress or a daemon's answer DOES rather than about how it is implemented.
//
// Three properties make them deterministic. The Kitty client is nil, so any
// regression that reached Kitty outside the two seams would panic rather than
// pass. The clock is a field the test advances, so freshness is asserted at exact
// ages instead of near them. And the injected commands RECORD and return nothing,
// so every message this package consumes is one the test chose to send — which is
// what lets a stale completion, a coalesced tick, and a focus answered after a
// dismissal all be written as ordinary sequences.
package overview

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
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
		out = append(out, req.WindowID)
	}
	return out
}

// lastCaptureFor is the newest request issued for one window.
func (r *recorder) lastCaptureFor(windowID int) (commands.CaptureRequest, bool) {
	for i := len(r.captures) - 1; i >= 0; i-- {
		if r.captures[i].WindowID == windowID {
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
	}
}

// frameOf is the model's frame with the styling stripped, which is the only form a
// claim about text can be made against.
func frameOf(m model) string {
	return strings.Join(plain(strings.Split(Render(overviewInput(m)), "\n")), "\n")
}

// TestTheConstructorOwnsTheFirstSequenceAndInitOnlyDispatchesIt is the review
// session 3 asks for, written down as an assertion.
//
// The failure it guards is specific: if Init drew the sequence, its value receiver
// would discard the in-flight bit along with its copy of the model, the first
// interval tick would find an idle controller, and two startup reads would race —
// the slower of which could then apply an older tree over a newer one.
func TestTheConstructorOwnsTheFirstSequenceAndInitOnlyDispatchesIt(t *testing.T) {
	m, r := newTestModel(77)
	if m.refreshActiveSeq != firstRefreshSeq || m.refreshNextSeq != firstRefreshSeq {
		t.Fatalf("the constructor armed seq %d/%d, want %d", m.refreshActiveSeq, m.refreshNextSeq, firstRefreshSeq)
	}
	if !m.refreshInFlight {
		t.Fatal("the constructor did not mark the startup read in flight")
	}
	if len(r.topology) != 0 {
		t.Fatalf("the constructor itself dispatched %d reads; the command belongs to Init", len(r.topology))
	}

	m.Init()
	if len(r.topology) != 1 || r.topology[0] != (topologyAsk{seq: firstRefreshSeq, sourceWindowID: 77}) {
		t.Fatalf("Init dispatched %v, want exactly one read of seq %d for window 77", r.topology, firstRefreshSeq)
	}
	if len(r.ticks) != 1 || r.ticks[0] != 2*time.Second {
		t.Fatalf("Init scheduled %v, want exactly one 2s interval", r.ticks)
	}

	// The tick that arrives one interval later must not become a second startup
	// read.
	m = only(t, m, commands.TickMsg{})
	if len(r.topology) != 1 {
		t.Fatalf("a tick during the startup read dispatched %d reads, want the startup one alone", len(r.topology))
	}
	if !m.refreshPending {
		t.Fatal("the tick left no pending request, so its refresh was lost rather than coalesced")
	}
}

// TestTheConfiguredIntervalDrivesTheTicker pins that the cadence is the
// operator's, with the documented fallback.
func TestTheConfiguredIntervalDrivesTheTicker(t *testing.T) {
	for _, tc := range []struct {
		configured int
		want       time.Duration
	}{
		{configured: 0, want: 2 * time.Second},
		{configured: 5, want: 5 * time.Second},
		{configured: 30, want: 30 * time.Second},
		{configured: -1, want: 2 * time.Second},
	} {
		cfg := viewConfig()
		cfg.Defaults.RefreshInterval = tc.configured
		m := newModel(cfg, nil, 0)
		r := &recorder{now: viewNow}
		r.install(&m)
		m.Init()
		if len(r.ticks) != 1 || r.ticks[0] != tc.want {
			t.Errorf("refresh_interval=%d scheduled %v, want %v", tc.configured, r.ticks, tc.want)
		}
		// Every later tick re-arms at the same interval, so the cadence is a
		// property of the model rather than of Init.
		only(t, m, commands.TickMsg{})
		if len(r.ticks) != 2 || r.ticks[1] != tc.want {
			t.Errorf("refresh_interval=%d re-armed at %v, want %v", tc.configured, r.ticks, tc.want)
		}
	}
}

// TestTicksCoalesceIntoOneCatchUpRead is the serialization rule: however many
// intervals elapse during one slow read, they cost exactly one follow-up.
func TestTicksCoalesceIntoOneCatchUpRead(t *testing.T) {
	m, r := started(t, 0)

	for i := 0; i < 4; i++ {
		m = only(t, m, commands.TickMsg{})
	}
	if len(r.topology) != 1 {
		t.Fatalf("ticks during an in-flight read dispatched %d reads, want the startup one alone", len(r.topology))
	}
	if len(r.ticks) != 5 {
		t.Fatalf("the ticker was re-armed %d times, want one per tick plus Init's", len(r.ticks))
	}

	m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(3)))
	if len(r.topology) != 2 {
		t.Fatalf("four coalesced ticks produced %d catch-up reads, want exactly 1", len(r.topology)-1)
	}
	if m.refreshPending {
		t.Fatal("the catch-up left the pending bit set; it would fire again after the next completion")
	}
	if !m.refreshInFlight || r.topology[1].seq != m.refreshActiveSeq {
		t.Fatalf("the catch-up dispatched seq %d, want the active %d", r.topology[1].seq, m.refreshActiveSeq)
	}
	if r.topology[1].seq == r.topology[0].seq {
		t.Fatalf("the catch-up reused seq %d; the settled read's own answer would then be applied twice", r.topology[1].seq)
	}
}

// TestASuccessWithNothingPendingStartsNothing is the control for the test above.
func TestASuccessWithNothingPendingStartsNothing(t *testing.T) {
	m, r := started(t, 0)
	m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(3)))
	if len(r.topology) != 1 {
		t.Fatalf("a completion with nothing pending dispatched %d reads, want 0", len(r.topology)-1)
	}
	if m.refreshInFlight {
		t.Fatal("a settled read left the in-flight bit set; every later tick would coalesce forever")
	}
}

// TestAnObsoleteCompletionChangesNothing is the stale-response rule. A read that
// was superseded before it landed must not move the list, the freshness stamp, or
// the selection backwards.
func TestAnObsoleteCompletionChangesNothing(t *testing.T) {
	m, _ := started(t, 0)
	m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(3)))
	m = press(t, m, "tab")
	before := m

	for _, msg := range []tea.Msg{
		snapshot(firstRefreshSeq, 0, viewSessions(9)),                   // already settled
		snapshot(firstRefreshSeq+5, 0, viewSessions(9)),                 // never issued
		commands.ErrMsg{Seq: firstRefreshSeq, Err: errors.New("stale")}, // already settled
	} {
		m = only(t, m, msg)
		if got := len(m.sessions); got != 3 {
			t.Fatalf("%T with an obsolete sequence installed %d sessions, want the 3 already shown", msg, got)
		}
		if m.selected != before.selected || m.selectedTabID != before.selectedTabID {
			t.Fatalf("%T with an obsolete sequence moved the selection to %d/tab %d", msg, m.selected, m.selectedTabID)
		}
		if m.err != nil {
			t.Fatalf("%T with an obsolete sequence recorded %v", msg, m.err)
		}
		if !m.topologyObservedAt.Equal(before.topologyObservedAt) {
			t.Fatalf("%T with an obsolete sequence restamped the freshness reading", msg)
		}
	}
}

// TestAnObsoleteCompletionDoesNotSettleTheReadInFlight is the other half of the
// stale rule, and the one that would otherwise hang the surface: if a superseded
// completion cleared the in-flight bit, the read still genuinely outstanding would
// be settled by nobody and its own answer would then be dropped as obsolete.
func TestAnObsoleteCompletionDoesNotSettleTheReadInFlight(t *testing.T) {
	m, r := started(t, 0)
	m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(3)))
	m = only(t, m, commands.TickMsg{})
	live := m.refreshActiveSeq

	m = only(t, m, snapshot(live-1, 0, viewSessions(9)))
	if !m.refreshInFlight {
		t.Fatal("an obsolete completion settled the read that is still out")
	}
	m = only(t, m, snapshot(live, 0, viewSessions(9)))
	if m.refreshInFlight {
		t.Fatal("the live completion did not settle its own read")
	}
	if len(m.sessions) != 9 {
		t.Fatalf("the live completion installed %d sessions, want 9", len(m.sessions))
	}
	if len(r.topology) != 2 {
		t.Fatalf("%d reads were dispatched, want the startup one and the tick's", len(r.topology))
	}
}

// TestAFailedReadRetainsTheListAndWaitsForTheNextTick is the anti-storm rule plus
// the retention rule. A daemon and a Kitty that are both down stay down for more
// than a moment, and a surface that emptied itself over a timed-out subprocess
// would be worse than one showing a slightly old list.
func TestAFailedReadRetainsTheListAndWaitsForTheNextTick(t *testing.T) {
	m, r := started(t, 0)
	m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(4)))
	m = press(t, m, "tab")
	selected := m.selectedTabID
	stamped := m.topologyObservedAt

	m = only(t, m, commands.TickMsg{})
	live := m.refreshActiveSeq
	m = only(t, m, commands.TickMsg{})
	if !m.refreshPending {
		t.Fatal("a tick during the outstanding read left no pending request")
	}

	outage := errors.New("kitten @ ls: signal: killed")
	m = only(t, m, commands.ErrMsg{Seq: live, Err: outage})
	if len(r.topology) != 2 {
		t.Fatalf("a failure dispatched %d immediate retries, want 0", len(r.topology)-2)
	}
	if m.refreshPending {
		t.Fatal("a failure left the pending bit set; the next completion would fire a surprise retry")
	}
	if len(m.sessions) != 4 || m.selectedTabID != selected {
		t.Fatalf("a failure left %d sessions selected at tab %d, want the 4 and tab %d it was showing", len(m.sessions), m.selectedTabID, selected)
	}
	if !m.topologyObservedAt.Equal(stamped) {
		t.Fatal("a failure restamped the freshness reading; the rows would stop aging out of their own last capture")
	}
	if m.err != outage {
		t.Fatalf("a failure recorded %v, want the outage", m.err)
	}

	// The next tick is the retry, and a success clears the error.
	m = only(t, m, commands.TickMsg{})
	if len(r.topology) != 3 {
		t.Fatalf("the tick after a failure dispatched %d reads, want 1", len(r.topology)-2)
	}
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, viewSessions(4)))
	if m.err != nil {
		t.Fatalf("recovery left %v behind", m.err)
	}
	if m.selectedTabID != selected {
		t.Fatalf("recovery moved the selection to tab %d, want the retained tab %d", m.selectedTabID, selected)
	}
}

// TestRepeatedFailuresCostOneReadPerInterval generalizes the anti-storm rule over
// a sustained outage, which is the shape an operator actually meets: a daemon
// restarting, or Kitty gone while the overlay window is still up.
func TestRepeatedFailuresCostOneReadPerInterval(t *testing.T) {
	m, r := started(t, 0)
	m = only(t, m, commands.ErrMsg{Seq: m.refreshActiveSeq, Err: errors.New("down")})
	for i := 0; i < 6; i++ {
		m = only(t, m, commands.TickMsg{})
		m = only(t, m, commands.ErrMsg{Seq: m.refreshActiveSeq, Err: errors.New("still down")})
	}
	if len(r.topology) != 7 {
		t.Fatalf("six intervals of outage cost %d reads, want one per interval plus the startup one", len(r.topology))
	}
	if m.refreshInFlight || m.refreshPending {
		t.Fatalf("the controller is wedged: in-flight=%t pending=%t", m.refreshInFlight, m.refreshPending)
	}
}

func TestOpeningSelectsTopLeftAndMarksTheSourcePane(t *testing.T) {
	m, r := started(t, 30)
	if r.topology[0].sourceWindowID != 30 {
		t.Fatalf("the startup read asked about window %d, want 30", r.topology[0].sourceWindowID)
	}

	m = only(t, m, snapshot(firstRefreshSeq, 3, viewSessions(5)))
	if m.selected != 0 || m.selectedTabID != m.sessions[0].TabID {
		t.Fatalf("selected index %d/tab %d, want the top-left card", m.selected, m.selectedTabID)
	}
	if got, ok := m.selectedSession(); !ok || got.TabID != m.sessions[0].TabID {
		t.Fatalf("the index addresses tab %d rather than the top-left card", got.TabID)
	}
	if m.sourceTabID != 3 || m.sourceTabID == m.selectedTabID {
		t.Fatalf("source tab = %d, want tab 3 independently of selection", m.sourceTabID)
	}
}

// TestAnUnmanagedSourceSelectsTheFirstSession covers the pane the overview was
// launched from having no managed session — a scratch shell, or the dashboard's
// own window. RefreshOverview reports a zero tab for it, and a zero tab is not a
// selection request.
func TestAnUnmanagedSourceSelectsTheFirstSession(t *testing.T) {
	for _, sourceWindowID := range []int{0, 999} {
		m, _ := started(t, sourceWindowID)
		m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(5)))
		if m.selected != 0 || m.selectedTabID != m.sessions[0].TabID {
			t.Errorf("source window %d selected index %d/tab %d, want the first session", sourceWindowID, m.selected, m.selectedTabID)
		}
		if m.sourceTabID != 0 {
			t.Fatalf("unmanaged source marked tab %d", m.sourceTabID)
		}
	}
}

// TestASourceTabMissingFromTheListSelectsTheFirstSession is the unmanaged case's
// near neighbor: the source pane IS managed, but its tab is not in the tree the
// read came back with.
func TestASourceTabMissingFromTheListSelectsTheFirstSession(t *testing.T) {
	m, _ := started(t, 4242)
	m = only(t, m, snapshot(firstRefreshSeq, 4242, viewSessions(5)))
	if m.selected != 0 || m.selectedTabID != m.sessions[0].TabID {
		t.Fatalf("selected index %d/tab %d, want the first session", m.selected, m.selectedTabID)
	}
	if m.startupSourceWindowID != 0 {
		t.Fatal("a resolved-but-absent source tab left the startup request unspent; it would reassert itself later")
	}
	if m.sourceTabID != 0 {
		t.Fatalf("absent source marked tab %d", m.sourceTabID)
	}
}

// TestTheStartupRequestSurvivesAFailedFirstLoad is why the request is consumed at
// the SUCCESS rather than at the dispatch: a first read that reached nothing
// resolved nothing, so the source marker still needs resolving.
func TestTheStartupRequestSurvivesAFailedFirstLoad(t *testing.T) {
	m, r := started(t, 30)
	m = only(t, m, commands.ErrMsg{Seq: firstRefreshSeq, Err: errors.New("daemon unreachable")})
	if m.startupSourceWindowID != 30 {
		t.Fatalf("a failed first load consumed the startup request (now %d)", m.startupSourceWindowID)
	}

	m = only(t, m, commands.TickMsg{})
	if got := r.topology[len(r.topology)-1].sourceWindowID; got != 30 {
		t.Fatalf("the retry asked about window %d, want the retained 30", got)
	}
	m = only(t, m, snapshot(m.refreshActiveSeq, 3, viewSessions(5)))
	if m.selected != 0 || m.selectedTabID != m.sessions[0].TabID || m.sourceTabID != 3 {
		t.Fatalf("retry selected index %d/tab %d and source %d, want top-left and source tab 3", m.selected, m.selectedTabID, m.sourceTabID)
	}
}

// TestTheStartupRequestIsSpentByTheFirstSuccess is the other side of the rule. The
// request is a one-shot: a later read that still reports a source tab must not drag
// the cursor back off wherever the operator has navigated to.
func TestTheStartupRequestIsSpentByTheFirstSuccess(t *testing.T) {
	m, r := started(t, 30)
	m = only(t, m, snapshot(firstRefreshSeq, 3, viewSessions(5)))
	if m.startupSourceWindowID != 0 {
		t.Fatalf("the first success left the startup request at %d, want it spent", m.startupSourceWindowID)
	}

	m = press(t, m, "tab")
	moved := m.selectedTabID
	if moved == m.sessions[0].TabID {
		t.Fatal("Tab did not move the selection off the first card; the rest of this test proves nothing")
	}

	m = only(t, m, commands.TickMsg{})
	if got := r.topology[len(r.topology)-1].sourceWindowID; got != 0 {
		t.Fatalf("a later read still asked about window %d, want the spent request to be 0", got)
	}
	m = only(t, m, snapshot(m.refreshActiveSeq, 3, viewSessions(5)))
	if m.selectedTabID != moved {
		t.Fatalf("a later read reasserted the source tab, moving the selection to %d from %d", m.selectedTabID, moved)
	}
	if m.sourceTabID != 3 {
		t.Fatalf("navigation or refresh changed the source marker to tab %d", m.sourceTabID)
	}
}

func TestSourceMarkerSurvivesNavigationAndAnAnchorChange(t *testing.T) {
	sessions := viewSessions(3)
	m, _ := started(t, sessions[2].WindowID)
	m = only(t, m, snapshot(firstRefreshSeq, sessions[2].TabID, sessions))
	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	if !strings.Contains(frameOf(m), "Opened from here") {
		t.Fatal("the opening frame does not identify the launching card")
	}
	m = press(t, m, "tab")
	selected := m.selectedTabID
	sessions[2].WindowID = 999
	m = only(t, m, commands.TickMsg{})
	// Later topology completions cannot replace the original source marker.
	m = only(t, m, snapshot(m.refreshActiveSeq, sessions[0].TabID, sessions))
	if m.sourceTabID != sessions[2].TabID || m.selectedTabID != selected {
		t.Fatalf("refresh changed source or selection: source %d, selected %d", m.sourceTabID, m.selectedTabID)
	}
	if !strings.Contains(frameOf(m), "Opened from here") {
		t.Fatal("the restarted launching card lost its marker")
	}
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, sessions[:2]))
	if strings.Contains(frameOf(m), "Opened from here") {
		t.Fatal("a closed source left a marker on another card")
	}
}

// A newly discovered tab cannot move an existing tile under the operator.
func TestTheSelectionStaysPutWhenANewTabArrives(t *testing.T) {
	m, _ := started(t, 0)
	later := viewSession(4, "zzz-later", "work", "claude")
	earlier := viewSession(5, "aaa-earlier", "personal", "codex")

	m = only(t, m, snapshot(firstRefreshSeq, 0, []session.RunningSession{later}))
	if m.selected != 0 || m.selectedTabID != 4 {
		t.Fatalf("the only session was not selected: index %d, tab %d", m.selected, m.selectedTabID)
	}

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, []session.RunningSession{later, earlier}))
	if got := tabs(m); len(got) != 2 || got[0] != 4 || got[1] != 5 {
		t.Fatalf("the ordered list is %v, want the new project after the existing tile", got)
	}
	if m.selected != 0 || m.selectedTabID != 4 {
		t.Fatalf("the selection is index %d/tab %d, want index 0 still on tab 4", m.selected, m.selectedTabID)
	}
}

func TestOpeningPutsDoneBeforeCompacting(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     string
		unseen    bool
		agentDone bool
	}{
		{name: "seen completion", state: "stop"},
		{name: "unseen completion", state: "stop", unseen: true},
		{name: "completed agent under active ASL", state: "bolt", agentDone: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compacting := viewSession(1, "alpha", "work", "codex")
			compacting.State = "compact"
			done := viewSession(2, "zeta", "work", "codex")
			done.State, done.Unseen = tc.state, tc.unseen
			if tc.agentDone {
				done.AgentTurn = &protocol.AgentTurnSnapshot{IconState: "stop"}
			}
			m, _ := started(t, 0)
			m = only(t, m, snapshot(firstRefreshSeq, 0, []session.RunningSession{compacting, done}))
			if got, want := tabs(m), []int{2, 1}; !reflect.DeepEqual(got, want) {
				t.Fatalf("opening tiles = %v, want Done top-left before Compacting: %v", got, want)
			}
		})
	}
}

func TestOpeningSurfacesAttentionAndNonWorkingThenFreezesPositions(t *testing.T) {
	working := viewSession(1, "alpha", "work", "codex")
	working.State = "bolt"
	idle := viewSession(2, "zeta", "work", "codex")
	idle.State = "stop"
	attention := viewSession(3, "omega", "work", "codex")
	attention.State = "question"
	m, _ := started(t, 0)
	m = only(t, m, snapshot(firstRefreshSeq, 0, []session.RunningSession{working, idle, attention}))
	if got, want := tabs(m), []int{3, 2, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("opening tiles = %v, want %v", got, want)
	}
	if m.selectedTabID != 3 {
		t.Fatalf("opening selection = %d, want first attention tab 3", m.selectedTabID)
	}

	// Changes in both directions do not shuffle the cards, even if another
	// session is added and Kitty returns the rows in a different order.
	working.State = "key"
	attention.State = "bolt"
	newcomer := viewSession(4, "aardvark", "work", "codex")
	newcomer.State = "error"
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, []session.RunningSession{newcomer, attention, working, idle}))
	if got, want := tabs(m), []int{3, 2, 1, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("refreshed tiles = %v, want stable survivors and appended new tab %v", got, want)
	}
	if m.sessions[0].State != "bolt" || m.sessions[2].State != "key" {
		t.Fatal("the stable positions did not receive the refreshed status")
	}

	// A new F2 program gets a fresh triage snapshot rather than the old order.
	reopened, _ := started(t, 0)
	reopened = only(t, reopened, snapshot(firstRefreshSeq, 0, []session.RunningSession{idle, attention, working, newcomer}))
	if got, want := tabs(reopened), []int{4, 1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened tiles = %v, want %v", got, want)
	}
}

// TestAMetadataUpdateLeavesTheSelectionAlone covers the ordinary case that happens
// every two seconds: the same tabs come back with new states and new busy
// timestamps.
func TestAMetadataUpdateLeavesTheSelectionAlone(t *testing.T) {
	sessions := viewSessions(6)
	m, _ := loadedAt(t, sessions, 3)
	index, tabID := m.selected, m.selectedTabID

	updated := viewSessions(6)
	for i := range updated {
		updated[i].State = "key"
		updated[i].BusySince = viewNow.Add(-9 * time.Minute)
	}
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, updated))
	if m.selected != index || m.selectedTabID != tabID {
		t.Fatalf("a metadata update moved the selection to %d/tab %d, want %d/tab %d", m.selected, m.selectedTabID, index, tabID)
	}
	if m.sessions[m.selected].State != "key" {
		t.Fatalf("the selected row still says %q; the update did not reach the list", m.sessions[m.selected].State)
	}
}

// TestOnlyTheActivityMapIsHeldFromARead pins the deliberate drop. The per-window
// state a tile paints is already overlaid onto these rows by the shared reader, and
// the watchdog is a dashboard status-bar input the grid has no slot for, so holding
// either here would be state nothing reads — and state nothing reads is state that
// goes stale silently.
func TestOnlyTheActivityMapIsHeldFromARead(t *testing.T) {
	sessions := viewSessions(3)
	msg := snapshot(firstRefreshSeq, 0, sessions)
	msg.Activity = map[int]time.Time{sessions[0].WindowID: viewNow.Add(-time.Minute)}
	msg.Watchdog = map[int]protocol.WatchdogState{sessions[0].WindowID: {}}
	msg.States = map[int]protocol.WindowStateSnapshot{sessions[0].WindowID: {}}

	m, _ := started(t, 0)
	m = only(t, m, msg)
	if len(m.lastActivity) != 1 {
		t.Fatalf("the activity map holds %d entries, want the one the read carried", len(m.lastActivity))
	}
	if got := m.lastActivity[sessions[0].WindowID]; !got.Equal(viewNow.Add(-time.Minute)) {
		t.Fatalf("the activity entry is %v, want the read's", got)
	}
}

// TestAResizeKeepsTheSelectedSessionAndBringsItsPage is the resize rule. The page
// is derived from the selection, so shrinking the terminal until the selected tile
// is on a later page must move the PAGE rather than the cursor.
func TestAResizeKeepsTheSelectedSessionAndBringsItsPage(t *testing.T) {
	sessions := viewSessions(9)
	m, _ := loadedAt(t, sessions, 8)
	m = only(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	tabID := m.selectedTabID
	if page := Measure(160, 45).PageOf(m.selected); page != 0 {
		t.Fatalf("nine sessions on a 160x45 grid put the last one on page %d, want a single page", page)
	}

	for _, size := range [][2]int{{120, 30}, {80, 24}, {60, 15}, {200, 6}, {160, 45}} {
		m = only(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if m.selectedTabID != tabID {
			t.Fatalf("resizing to %dx%d moved the selection to tab %d, want the retained %d", size[0], size[1], m.selectedTabID, tabID)
		}
		if m.width != size[0] || m.height != size[1] {
			t.Fatalf("the model is %dx%d, want %dx%d", m.width, m.height, size[0], size[1])
		}
		if got := strings.Split(Render(overviewInput(m)), "\n"); len(got) != size[1] {
			t.Fatalf("%dx%d rendered %d lines", size[0], size[1], len(got))
		}
		// The page follows the selection: the selected session is on the frame the
		// renderer produced, on whatever page it now falls.
		if Measure(size[0], size[1]).Capacity() > 0 {
			if frame := frameOf(m); !strings.Contains(frame, m.sessions[m.selected].Base) {
				t.Fatalf("the %dx%d frame does not show the selected session %q:\n%s", size[0], size[1], m.sessions[m.selected].Base, frame)
			}
		}
	}
}

// TestAnAnchorChangeKeepsTheSelectionAndDropsTheStaleCapture is the anchor rule,
// and the reason the tab and the window are two separate fields. A restarted
// harness keeps its tab and gets a new anchor window; the cursor must not move,
// and the capture of the window that is gone must not survive to be painted as if
// it were live.
//
// It runs on a real terminal size because eviction is the VISIBILITY rule: a
// surface with no dimensions has no page, so every buffer is off-page and the
// claim about the two survivors would be true for the wrong reason.
func TestAnAnchorChangeKeepsTheSelectionAndDropsTheStaleCapture(t *testing.T) {
	before := viewSessions(3)
	m, _ := loadedAt(t, before, 1)
	m = only(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	selected := m.selectedTabID
	oldWindow := m.selectedWindowID

	m.previews = map[int]Preview{}
	for _, s := range before {
		m.previews[s.WindowID] = Preview{Output: []string{"captured"}}
	}

	after := viewSessions(3)
	for i := range after {
		if after[i].TabID == selected {
			after[i].WindowID = oldWindow + 1000
		}
	}
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, after))

	if m.selectedTabID != selected {
		t.Fatalf("the anchor change moved the selection to tab %d, want the retained %d", m.selectedTabID, selected)
	}
	if m.selectedWindowID != oldWindow+1000 {
		t.Fatalf("the selection's anchor window is %d, want the new %d", m.selectedWindowID, oldWindow+1000)
	}
	if _, ok := m.previews[oldWindow]; ok {
		t.Fatalf("the capture of the retired window %d survived; it would be painted as a live terminal", oldWindow)
	}
	if len(m.previews) != 2 {
		t.Fatalf("%d captures survived, want the two whose windows are unchanged", len(m.previews))
	}
}

// TestPruningDoesNotReachBackIntoAnEarlierModel is the aliasing rule. Every handler
// takes the model by value, so a map mutated in place would be shared with the
// generation it was called on — harmless while Bubble Tea discards the old value,
// and exactly what would make two generations indistinguishable here.
func TestPruningDoesNotReachBackIntoAnEarlierModel(t *testing.T) {
	sessions := viewSessions(3)
	m, _ := loadedAt(t, sessions, 0)
	m = only(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	m.previews = map[int]Preview{}
	for _, s := range sessions {
		m.previews[s.WindowID] = Preview{Output: []string{"captured"}}
	}
	previous := m

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, sessions[:1]))
	if len(m.previews) != 1 {
		t.Fatalf("the new generation holds %d captures, want 1", len(m.previews))
	}
	if len(previous.previews) != 3 {
		t.Fatalf("the earlier generation now holds %d captures; the prune mutated a shared map", len(previous.previews))
	}
}

// TestAClosedSessionTakesItsCaptureWithIt is the same pruning rule reached by the
// other event.
func TestAClosedSessionTakesItsCaptureWithIt(t *testing.T) {
	before := viewSessions(3)
	m, _ := loadedAt(t, before, 0)
	m = only(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	m.previews = map[int]Preview{}
	for _, s := range before {
		m.previews[s.WindowID] = Preview{Output: []string{"captured"}}
	}
	gone := before[2]

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, before[:2]))
	if _, ok := m.previews[gone.WindowID]; ok {
		t.Fatalf("the closed session's capture at window %d survived", gone.WindowID)
	}
	if len(m.previews) != 2 {
		t.Fatalf("%d captures survived, want 2", len(m.previews))
	}
}

// TestTheSelectedSessionVanishingSelectsTheNearestSurvivor is the removal rule.
// The nearest survivor is the row that took its PLACE, which is what an operator
// watching one tile close expects the cursor to land on.
func TestTheSelectedSessionVanishingSelectsTheNearestSurvivor(t *testing.T) {
	all := viewSessions(5)

	t.Run("a middle session closes", func(t *testing.T) {
		m, _ := loadedAt(t, all, 2)
		survivors := append(append([]session.RunningSession{}, all[:2]...), all[3:]...)
		m = only(t, m, commands.TickMsg{})
		m = only(t, m, snapshot(m.refreshActiveSeq, 0, survivors))
		if m.selected != 2 || m.selectedTabID != all[3].TabID {
			t.Fatalf("selected index %d/tab %d, want index 2 on the session that took its place (tab %d)", m.selected, m.selectedTabID, all[3].TabID)
		}
	})

	t.Run("the last session closes", func(t *testing.T) {
		m, _ := loadedAt(t, all, 4)
		m = only(t, m, commands.TickMsg{})
		m = only(t, m, snapshot(m.refreshActiveSeq, 0, all[:3]))
		if m.selected != 2 || m.selectedTabID != all[2].TabID {
			t.Fatalf("selected index %d/tab %d, want the new last session at index 2 (tab %d)", m.selected, m.selectedTabID, all[2].TabID)
		}
	})

	t.Run("everything before the selection closes", func(t *testing.T) {
		m, _ := loadedAt(t, all, 4)
		m = only(t, m, commands.TickMsg{})
		m = only(t, m, snapshot(m.refreshActiveSeq, 0, all[4:]))
		if m.selected != 0 || m.selectedTabID != all[4].TabID {
			t.Fatalf("selected index %d/tab %d, want the surviving tab %d found by identity at index 0", m.selected, m.selectedTabID, all[4].TabID)
		}
	})
}

// TestTheEmptyTransitionsBothWays covers the list emptying and filling again. The
// cursor has nothing to be on in between, and it must not carry a dead identity
// across the gap.
func TestTheEmptyTransitionsBothWays(t *testing.T) {
	all := viewSessions(4)
	m, _ := loadedAt(t, all, 3)
	m.previews = map[int]Preview{all[3].WindowID: {Output: []string{"captured"}}}

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, nil))
	if len(m.sessions) != 0 {
		t.Fatalf("%d sessions survived an empty read", len(m.sessions))
	}
	if m.selected != 0 || m.selectedTabID != 0 || m.selectedWindowID != 0 {
		t.Fatalf("an empty list left the cursor at %d/tab %d/window %d, want it cleared", m.selected, m.selectedTabID, m.selectedWindowID)
	}
	if len(m.previews) != 0 {
		t.Fatalf("%d captures survived an empty list", len(m.previews))
	}
	if got := m.feedback(); got != "No running sessions" {
		t.Fatalf("an empty warm list says %q, want the no-sessions sentence", got)
	}
	if _, ok := m.selectedSession(); ok {
		t.Fatal("an empty list still reports a selected session")
	}

	// Navigation and Enter on an empty list are no-ops rather than panics.
	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, name := range []string{"tab", "end", "down", "pgdown", "home"} {
		m = press(t, m, name)
	}
	next, cmd := send(t, m, key(t, "enter"))
	m = next
	if cmd != nil {
		t.Fatal("Enter on an empty list started a command")
	}

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, all))
	if m.selected != 0 || m.selectedTabID != m.sessions[0].TabID {
		t.Fatalf("refilling selected index %d/tab %d, want the first session", m.selected, m.selectedTabID)
	}
}

// TestEnterFocusesTheCapturedTabAndExitsOnlyOnSuccess is the one action the surface
// performs.
func TestEnterFocusesTheCapturedTabAndExitsOnlyOnSuccess(t *testing.T) {
	sessions := viewSessions(4)
	m, r := loadedAt(t, sessions, 2)
	target := m.selectedTabID

	m, cmd := send(t, m, key(t, "enter"))
	if len(r.focus) != 1 || r.focus[0] != target {
		t.Fatalf("Enter dispatched focus %v, want exactly one against tab %d", r.focus, target)
	}
	if cmd == nil {
		t.Fatal("Enter returned no command")
	}
	if quits(t, cmd) {
		t.Fatal("Enter quit before Kitty confirmed the focus")
	}
	if m.focusTabID != target {
		t.Fatalf("the pending focus is against tab %d, want the captured %d", m.focusTabID, target)
	}

	m, cmd = send(t, m, commands.FocusDoneMsg{})
	if !quits(t, cmd) {
		t.Fatal("a confirmed focus did not exit the overview")
	}
	if m.focusTabID != 0 {
		t.Fatal("a settled focus left the pending tab behind")
	}
}

// The pending focus keeps its tab identity while a new session arrives during
// the round trip, regardless of the grid's unchanged positions.
func TestEnterActsOnTheTabItCapturedNotTheOneTheIndexEndsUpOn(t *testing.T) {
	first := viewSession(4, "zzz-last", "work", "claude")
	m, r := started(t, first.WindowID)
	m = only(t, m, snapshot(firstRefreshSeq, first.TabID, []session.RunningSession{first}))

	m = press(t, m, "enter")
	if len(r.focus) != 1 || r.focus[0] != first.TabID {
		t.Fatalf("Enter dispatched %v, want tab %d", r.focus, first.TabID)
	}

	// A read lands mid-flight with another session, leaving the existing tile put.
	earlier := viewSession(5, "aaa-first", "personal", "codex")
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, []session.RunningSession{first, earlier}))
	if m.selected != 0 {
		t.Fatalf("the refresh left the selection at index %d, want 0", m.selected)
	}
	if m.focusTabID != first.TabID {
		t.Fatalf("the pending focus became tab %d, want the captured %d", m.focusTabID, first.TabID)
	}
	if len(r.focus) != 1 {
		t.Fatalf("%d focuses were dispatched, want the one Enter asked for", len(r.focus))
	}
}

// TestAFailedFocusStaysOnTheOverviewWithTheReason is the other half: the only
// state from which retrying makes sense is the one the operator was already in.
func TestAFailedFocusStaysOnTheOverviewWithTheReason(t *testing.T) {
	sessions := viewSessions(4)
	m, r := loadedAt(t, sessions, 1)
	target := m.selectedTabID

	m = press(t, m, "enter")
	m, cmd := send(t, m, commands.FocusDoneMsg{Err: errors.New("no matching tab")})
	if quits(t, cmd) {
		t.Fatal("a failed focus exited the overview")
	}
	if m.selectedTabID != target {
		t.Fatalf("a failed focus moved the selection to tab %d", m.selectedTabID)
	}
	if got := m.feedback(); !strings.HasPrefix(got, "Focus failed: ") || !strings.Contains(got, "no matching tab") {
		t.Fatalf("the feedback row says %q, want the focus failure and its reason", got)
	}

	// Retrying is possible because nothing is pending any more.
	m = press(t, m, "enter")
	if len(r.focus) != 2 {
		t.Fatalf("the retry dispatched %d focuses in total, want 2", len(r.focus))
	}
	if m.focusErr != nil {
		t.Fatalf("the retry did not clear the previous failure: %v", m.focusErr)
	}
}

// TestADuplicateEnterWhileFocusIsPendingIsIgnored keeps one keypress from becoming
// two commands, the second of which would complete after the program was already
// told to quit.
func TestADuplicateEnterWhileFocusIsPendingIsIgnored(t *testing.T) {
	sessions := viewSessions(4)
	m, r := loadedAt(t, sessions, 0)

	m = press(t, m, "enter")
	for i := 0; i < 3; i++ {
		next, cmd := send(t, m, key(t, "enter"))
		m = next
		if cmd != nil {
			t.Fatal("a duplicate Enter returned a command")
		}
	}
	if len(r.focus) != 1 {
		t.Fatalf("four Enter presses dispatched %d focuses, want 1", len(r.focus))
	}

	// Navigating during the round trip does not retarget the pending focus.
	m = press(t, m, "tab")
	if m.focusTabID == m.selectedTabID {
		t.Fatal("the cursor did not move off the pending focus target; the case proves nothing")
	}
	m, cmd := send(t, m, commands.FocusDoneMsg{})
	if !quits(t, cmd) {
		t.Fatal("the confirmed focus did not exit")
	}
	if len(r.focus) != 1 {
		t.Fatalf("%d focuses were dispatched in total, want the single captured one", len(r.focus))
	}
}

// TestAFocusCompletionWithNothingPendingIsDropped covers the answer that arrives
// after the surface has already dealt with the attempt.
func TestAFocusCompletionWithNothingPendingIsDropped(t *testing.T) {
	sessions := viewSessions(4)
	m, _ := loadedAt(t, sessions, 0)

	m, cmd := send(t, m, commands.FocusDoneMsg{})
	if quits(t, cmd) {
		t.Fatal("an unsolicited focus completion exited the overview")
	}
	if m.focusErr != nil {
		t.Fatalf("an unsolicited completion recorded %v", m.focusErr)
	}

	m, cmd = send(t, m, commands.FocusDoneMsg{Err: errors.New("late")})
	if quits(t, cmd) {
		t.Fatal("an unsolicited failure exited the overview")
	}
	if m.focusErr != nil {
		t.Fatalf("an unsolicited failure was reported as the operator's: %v", m.focusErr)
	}
}

// TestDismissalWorksDuringPendingWork is the escape hatch. An operator asking to
// leave an overlay is not asking to wait for a subprocess first.
func TestDismissalWorksDuringPendingWork(t *testing.T) {
	sessions := viewSessions(4)
	for _, name := range []string{"esc", "q", "ctrl+c"} {
		t.Run(name+" during the startup read", func(t *testing.T) {
			m, _ := started(t, 0)
			if !m.refreshInFlight {
				t.Fatal("the startup read is not outstanding; the case is not the one named")
			}
			if _, cmd := send(t, m, key(t, name)); !quits(t, cmd) {
				t.Fatalf("%s did not dismiss the overview during a read", name)
			}
		})
		t.Run(name+" during a pending focus", func(t *testing.T) {
			m, _ := loadedAt(t, sessions, 0)
			m = press(t, m, "enter")
			if m.focusTabID == 0 {
				t.Fatal("no focus is pending; the case is not the one named")
			}
			if _, cmd := send(t, m, key(t, name)); !quits(t, cmd) {
				t.Fatalf("%s did not dismiss the overview during a pending focus", name)
			}
		})
		t.Run(name+" on a surface that never loaded", func(t *testing.T) {
			m, _ := started(t, 0)
			m = only(t, m, commands.ErrMsg{Seq: firstRefreshSeq, Err: errors.New("down")})
			if _, cmd := send(t, m, key(t, name)); !quits(t, cmd) {
				t.Fatalf("%s did not dismiss a surface that never loaded", name)
			}
		})
	}
}

// TestNavigationAndRenderingNeverActOnASession is the safety claim the whole
// surface rests on, asserted behaviorally to complement the structural check in
// view_test.go.
//
// Every movement key, a resize, and a full render are driven over a loaded model,
// and the only thing that may have happened is the read the controller asked for.
// No focus, and — because the Kitty client is nil — no Kitty call of any kind,
// which would have panicked.
func TestNavigationAndRenderingNeverActOnASession(t *testing.T) {
	sessions := viewSessions(9)
	m, r := loadedAt(t, sessions, 4)
	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})

	keys := []string{
		"left", "right", "up", "down", "h", "j", "k", "l",
		"tab", "shift+tab", "pgup", "pgdown", "home", "end",
		"x", "space", "ctrl+r", "?", "r", "d", // unbound keys are inert, not errors
	}
	for _, name := range keys {
		next, cmd := send(t, m, key(t, name))
		m = next
		if cmd != nil {
			t.Fatalf("%q returned a command; movement changes one integer and nothing else", name)
		}
		if m.focusTabID != 0 {
			t.Fatalf("%q left a focus pending against tab %d", name, m.focusTabID)
		}
		if got := Render(overviewInput(m)); got == "" {
			t.Fatalf("the frame after %q is empty", name)
		}
	}
	if len(r.focus) != 0 {
		t.Fatalf("browsing dispatched %d focus actions, want 0", len(r.focus))
	}
	if len(r.topology) != 1 {
		t.Fatalf("browsing dispatched %d reads, want only the startup one", len(r.topology))
	}
	if len(m.sessions) != 9 {
		t.Fatalf("browsing changed the live set to %d sessions", len(m.sessions))
	}
}

// TestEveryMovementKeyAgreesWithTheNavigator pins the binding table against the
// navigation vocabulary it delegates to, so a key that stopped moving the cursor
// is a failure rather than a silently inert keypress.
func TestEveryMovementKeyAgreesWithTheNavigator(t *testing.T) {
	bindings := map[string]Move{
		"left": MoveLeft, "h": MoveLeft,
		"right": MoveRight, "l": MoveRight,
		"up": MoveUp, "k": MoveUp,
		"down": MoveDown, "j": MoveDown,
		"tab": MoveNext, "shift+tab": MovePrev,
		"pgup": MovePageUp, "pgdown": MovePageDown,
		"home": MoveFirst, "end": MoveLast,
	}
	sessions := viewSessions(9)
	for name, want := range bindings {
		if got, ok := moveFor(name); !ok || got != want {
			t.Errorf("moveFor(%q) = %v/%t, want %v", name, got, ok, want)
		}

		// Start from a middle tile so no direction is already against a wall.
		m, _ := loadedAt(t, sessions, 4)
		m = only(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
		expected := Navigate(Measure(100, 24), len(m.sessions), m.selected, want)

		m = press(t, m, name)
		if m.selected != expected {
			t.Errorf("%q selected index %d, want the navigator's %d", name, m.selected, expected)
		}
		if m.selectedTabID != m.sessions[expected].TabID {
			t.Errorf("%q left the identity on tab %d rather than on the selected row's %d", name, m.selectedTabID, m.sessions[expected].TabID)
		}
		if m.selectedWindowID != m.sessions[expected].WindowID {
			t.Errorf("%q left the anchor on window %d rather than on %d", name, m.selectedWindowID, m.sessions[expected].WindowID)
		}
	}
}

// TestAMoveSurvivesTheNextRefresh is why applyMove re-resolves the identity: a
// move that changed the index without updating the tab would be undone by the
// following read, which would find the old tab still present and put the cursor
// back.
func TestAMoveSurvivesTheNextRefresh(t *testing.T) {
	sessions := viewSessions(9)
	m, _ := loadedAt(t, sessions, 0)
	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = press(t, m, "end")
	moved := m.selectedTabID

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, viewSessions(9)))
	if m.selectedTabID != moved {
		t.Fatalf("the refresh moved the cursor back to tab %d from %d", m.selectedTabID, moved)
	}
	if m.selected != len(m.sessions)-1 {
		t.Fatalf("the cursor is at index %d, want the last session at %d", m.selected, len(m.sessions)-1)
	}
}

// TestTheFeedbackRowDistinguishesTheLifecycleStates is the ranked sentence, one
// case per rank. The three the session names explicitly — still loading, no
// running sessions, and a failed FIRST load — are what stop a blank grid from
// meaning three different things at once.
func TestTheFeedbackRowDistinguishesTheLifecycleStates(t *testing.T) {
	t.Run("initial loading", func(t *testing.T) {
		m, _ := started(t, 0)
		if got := m.feedback(); got != "Loading live sessions…" {
			t.Fatalf("feedback = %q, want the loading sentence", got)
		}
		if m.loaded {
			t.Fatal("the surface reports itself loaded before any tree landed")
		}
	})

	t.Run("failed initial loading", func(t *testing.T) {
		m, _ := started(t, 0)
		m = only(t, m, commands.ErrMsg{Seq: firstRefreshSeq, Err: errors.New("daemon unreachable")})
		got := m.feedback()
		if !strings.HasPrefix(got, "Kitty error: ") || !strings.Contains(got, "daemon unreachable") {
			t.Fatalf("feedback = %q, want the cold failure and its reason", got)
		}
	})

	t.Run("no running sessions", func(t *testing.T) {
		m, _ := started(t, 0)
		m = only(t, m, snapshot(firstRefreshSeq, 0, nil))
		if got := m.feedback(); got != "No running sessions" {
			t.Fatalf("feedback = %q, want the no-sessions sentence", got)
		}
	})

	t.Run("a fresh non-empty list says nothing", func(t *testing.T) {
		m, _ := started(t, 0)
		m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(3)))
		if got := m.feedback(); got != "" {
			t.Fatalf("feedback = %q, want silence", got)
		}
	})

	t.Run("a delayed refresh outranks emptiness", func(t *testing.T) {
		m, r := started(t, 0)
		m = only(t, m, snapshot(firstRefreshSeq, 0, nil))
		r.now = r.now.Add(protocol.KittySnapshotStaleAfter)
		got := m.feedback()
		if !strings.HasPrefix(got, "Kitty refresh delayed") {
			t.Fatalf("feedback = %q, want the delay warning rather than a claim about an unrefreshable list", got)
		}
		if !strings.Contains(got, "6s old") {
			t.Fatalf("feedback = %q, want the age of the tree on screen", got)
		}
	})

	t.Run("a delayed refresh outranks a warm failure", func(t *testing.T) {
		m, r := started(t, 0)
		m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(3)))
		m = only(t, m, commands.TickMsg{})
		m = only(t, m, commands.ErrMsg{Seq: m.refreshActiveSeq, Err: errors.New("down")})
		r.now = r.now.Add(20 * time.Second)
		if got := m.feedback(); !strings.HasPrefix(got, "Kitty refresh delayed") {
			t.Fatalf("feedback = %q; with rows on screen the age is what the operator needs, not the subprocess's words", got)
		}
	})

	t.Run("a focus failure outranks everything", func(t *testing.T) {
		sessions := viewSessions(3)
		m, r := loadedAt(t, sessions, 0)
		m = press(t, m, "enter")
		m = only(t, m, commands.FocusDoneMsg{Err: errors.New("no matching tab")})
		r.now = r.now.Add(time.Minute)
		if got := m.feedback(); !strings.HasPrefix(got, "Focus failed: ") {
			t.Fatalf("feedback = %q, want the operator's own failed action first", got)
		}
	})
}

// TestTheFeedbackRowReachesTheFrame. The ranking is only worth having if the
// sentence is drawn, and the renderer reserves exactly one row for it.
func TestTheFeedbackRowReachesTheFrame(t *testing.T) {
	m, _ := started(t, 0)
	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = only(t, m, commands.ErrMsg{Seq: firstRefreshSeq, Err: errors.New("daemon unreachable")})

	lines := strings.Split(frameOf(m), "\n")
	if len(lines) != 30 {
		t.Fatalf("the frame is %d lines, want 30", len(lines))
	}
	if !strings.Contains(lines[1], "daemon unreachable") {
		t.Fatalf("the reserved feedback row says %q, want the failure", lines[1])
	}
	if !strings.Contains(lines[len(lines)-1], "Enter focus") {
		t.Fatalf("the hints row says %q, want the overview's own hints", lines[len(lines)-1])
	}
}

// TestTheDisplayedAgeIncludesWhatTheTreeAlreadyCarried is why the elapsed term is
// added to the snapshot's own age rather than replacing it: the daemon's tree was
// already some seconds old when it was published, and an overview that ignored
// that would under-report every reading it took from the daemon.
func TestTheDisplayedAgeIncludesWhatTheTreeAlreadyCarried(t *testing.T) {
	m, r := started(t, 0)
	msg := snapshot(firstRefreshSeq, 0, viewSessions(2))
	msg.Topology.Age = 4 * time.Second
	m = only(t, m, msg)

	if age, warm := m.topologyAge(); !warm || age != 4*time.Second {
		t.Fatalf("the age at the moment of application is %v/%t, want the 4s the tree carried", age, warm)
	}
	r.now = r.now.Add(3 * time.Second)
	if age, _ := m.topologyAge(); age != 7*time.Second {
		t.Fatalf("the age after 3s is %v, want 7s", age)
	}
	if got := m.feedback(); !strings.Contains(got, "7s old") {
		t.Fatalf("feedback = %q, want the combined age", got)
	}

	// A newer tree resets the reading, even one the daemon marked stale.
	m = only(t, m, commands.TickMsg{})
	fresher := snapshot(m.refreshActiveSeq, 0, viewSessions(2))
	fresher.Topology.Stale = true
	m = only(t, m, fresher)
	if age, _ := m.topologyAge(); age != 0 {
		t.Fatalf("a newer tree left the age at %v, want it reset", age)
	}
	if got := m.feedback(); got != "" {
		t.Fatalf("feedback = %q after a newer tree, want silence", got)
	}
}

// TestABackwardsClockIsClampedRatherThanReportedNegative covers the machine whose
// clock stepped back between a read and a frame.
func TestABackwardsClockIsClampedRatherThanReportedNegative(t *testing.T) {
	m, r := started(t, 0)
	m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(2)))
	r.now = r.now.Add(-time.Hour)
	age, warm := m.topologyAge()
	if !warm {
		t.Fatal("a backwards clock made a warm surface look cold")
	}
	if age != 0 {
		t.Fatalf("the age is %v, want it clamped to zero", age)
	}
}

// TestTheModelStartsNoDashboardWarmupWork is the negative half of "keep it a
// standalone sibling". A model that had also initialized the dashboard's caches
// would still render a grid, so the assertion is on the command count: Init's two
// commands are the read and the ticker, and nothing else.
func TestTheModelStartsNoDashboardWarmupWork(t *testing.T) {
	m, r := newTestModel(0)
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init returned no command")
	}
	if len(r.topology) != 1 || len(r.ticks) != 1 {
		t.Fatalf("Init produced %d reads and %d ticks, want exactly one of each", len(r.topology), len(r.ticks))
	}
	if len(r.focus) != 0 {
		t.Fatalf("Init dispatched %d focus actions, want 0", len(r.focus))
	}
}

// TestEveryTileIsLoadingUntilCapturesArrive is the intentionally pending
// capability, pinned rather than left implicit: the model holds an empty preview
// map, so every tile draws the honest loading state until session 4 fills it.
func TestEveryTileIsLoadingUntilCapturesArrive(t *testing.T) {
	sessions := viewSessions(4)
	m, _ := loadedAt(t, sessions, 0)
	m = only(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	if len(m.previews) != 0 {
		t.Fatalf("the model holds %d captures; session 3 supplies none", len(m.previews))
	}

	frame := frameOf(m)
	if got := strings.Count(frame, "(loading…)"); got != len(sessions) {
		t.Fatalf("%d tiles show the loading state, want all %d:\n%s", got, len(sessions), frame)
	}
}

// TestTheModelRendersEveryShippedTerminalSize ties the lifecycle to the renderer's
// size contract: whatever state the model is in, the frame it produces is exactly
// the surface it was given.
func TestTheModelRendersEveryShippedTerminalSize(t *testing.T) {
	for _, size := range shippedSizes {
		for _, count := range []int{0, 1, 5, 12} {
			m, _ := started(t, 0)
			m = only(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(count)))
			m = press(t, m, "end")
			lines := strings.Split(Render(overviewInput(m)), "\n")
			if len(lines) != size[1] {
				t.Fatalf("%dx%d with %d sessions rendered %d lines", size[0], size[1], count, len(lines))
			}
		}
	}
}

// TestAFrameBeforeTheFirstResizeIsEmptyRatherThanInvented. Bubble Tea delivers the
// window size as a message, so there is one frame's worth of time in which the
// surface has no dimensions. Rendering some assumed 80x24 there would flash a
// layout the terminal is not.
func TestAFrameBeforeTheFirstResizeIsEmptyRatherThanInvented(t *testing.T) {
	m, _ := started(t, 0)
	if m.width != 0 || m.height != 0 {
		t.Fatalf("the model starts at %dx%d, want an unknown surface", m.width, m.height)
	}
	if got := Render(overviewInput(m)); got != "" {
		t.Fatalf("a surface of unknown size rendered %q", got)
	}
}

// TestTheOverviewsDefaultsMatchTheConfigurationsOwn guards the two constants the
// overview restates rather than imports. A silent divergence between the two
// surfaces' cadences would be invisible on either of them.
func TestTheOverviewsDefaultsMatchTheConfigurationsOwn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("writing an empty config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("loading an empty config: %v", err)
	}
	if want := time.Duration(cfg.Defaults.RefreshInterval) * time.Second; want != defaultRefreshInterval {
		t.Errorf("the overview falls back to %v, want the configuration's own default of %v", defaultRefreshInterval, want)
	}
	if want := time.Duration(cfg.Defaults.IdleThreshold) * time.Second; want != defaultIdleThreshold {
		t.Errorf("the overview falls back to an idle threshold of %v, want the configuration's %v", defaultIdleThreshold, want)
	}

	// And an unset configuration reaches those fallbacks through the constructor.
	m := newModel(&config.Config{}, nil, 0)
	if m.interval != defaultRefreshInterval || m.idleThreshold != defaultIdleThreshold {
		t.Errorf("an unset config produced %v/%v, want %v/%v", m.interval, m.idleThreshold, defaultRefreshInterval, defaultIdleThreshold)
	}
}
