package tui

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

// settlePreviewRead drives whatever a keystroke or a controller call handed back
// the way the program loop would: batches are expanded, timers are dropped
// unrun, and every preview reply is fed back into the model — including the
// catch-up reads those replies start.
//
// Every test that dispatches a read has to answer it. The controller holds one
// in-flight slot and releases it on the reply; a test that drops the command on
// the floor leaves the slot held, and from then on every request it makes
// coalesces into the pending bit and hands back nil — which reads like the
// dashboard refusing to fetch and is only the test never having answered.
//
// It takes the model by pointer so a caller can settle the command a mutating
// method returned in one statement. Passing the model by value there would be a
// real hazard rather than a style choice: Go does not order a bare operand
// against a call in the same argument list, so `f(m, m.refresh())` may copy m
// before or after refresh mutates it.
func settlePreviewRead(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	settlePreviewReadDepth(t, m, cmd, 0)
}

func settlePreviewReadDepth(t *testing.T, m *Model, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || isUITestTimerCmd(cmd) {
		return
	}
	if depth > 16 {
		t.Fatal("preview commands never settled: catch-ups chained off one another without end")
	}
	msg := cmd()
	if parts, ok := uitestFlattenCmds(msg); ok {
		for _, part := range parts {
			settlePreviewReadDepth(t, m, part, depth+1)
		}
		return
	}
	content, ok := msg.(previewContentMsg)
	if !ok {
		return
	}
	updated, follow := m.Update(content)
	*m = updated.(Model)
	settlePreviewReadDepth(t, m, follow, depth+1)
}

// previewReadModel is an urgency dashboard with the pane open on session:api and
// its first read already answered, which is the state each case below starts its
// own story from.
func previewReadModel(t *testing.T) Model {
	t.Helper()
	m, _ := listViewModel(dashboard.ViewUrgency)
	m.previewEnabled = true
	m = selectLine(t, m, "session:api")
	settlePreviewRead(t, &m, m.refreshPreviewForCursor())
	return m
}

// TestOnlyOnePreviewReadIsEverInFlight is the concurrency bound that makes the
// pane safe to leave on. A selection moves far faster than a terminal read
// returns, and the dashboard used to answer every move with its own Kitty
// subprocess; here a burst of moves costs one read and the memory that it owes
// exactly one more.
func TestOnlyOnePreviewReadIsEverInFlight(t *testing.T) {
	m := previewReadModel(t)

	m = selectLine(t, m, "session:web")
	first := m.refreshPreviewForCursor()
	if first == nil {
		t.Fatal("the first move with nothing outstanding dispatched no read")
	}
	if !m.previewReadInFlight {
		t.Fatal("the dispatched read did not take the in-flight slot")
	}

	// Three further moves while that read is out. None of them may read.
	for _, line := range []string{"session:cli", "session:lib", "session:etl"} {
		m = selectLine(t, m, line)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			t.Fatalf("moving to %s dispatched a second read while one was in flight", line)
		}
	}
	if !m.previewReadPending {
		t.Fatal("the moves that coalesced recorded no catch-up")
	}

	// The outstanding read answers for the target it was issued for, three
	// selections ago. It releases the slot, its content is refused, and the one
	// catch-up reads the target as it stands now.
	updated, catchUp := m.Update(first())
	m = updated.(Model)
	if catchUp == nil {
		t.Fatal("the settled read started no catch-up for the selection that coalesced behind it")
	}
	if !m.previewReadInFlight {
		t.Fatal("the catch-up did not take the slot the settled read released")
	}
	if m.previewReadPending {
		t.Error("the catch-up left the pending bit set, so the same target will be read twice")
	}
	got, ok := catchUp().(previewContentMsg)
	if !ok {
		t.Fatalf("the catch-up handed back a %T, want previewContentMsg", got)
	}
	if want := m.previewWindowID; got.windowID != want {
		t.Errorf("the catch-up read window %d, want the latest target's %d", got.windowID, want)
	}
	if got.targetSeq != m.previewTargetSeq {
		t.Errorf("the catch-up carried generation %d, want the current %d", got.targetSeq, m.previewTargetSeq)
	}

	settlePreviewRead(t, &m, catchUp)
	if m.previewReadInFlight || m.previewReadPending {
		t.Errorf("a settled catch-up left work outstanding (in flight %v, pending %v)",
			m.previewReadInFlight, m.previewReadPending)
	}
}

// TestAStaleReplySettlesWithoutPainting is what the two identities exist for. A
// reply for a selection the operator has already left is worthless as content
// and is still the settlement the controller is waiting on; treating it as
// neither is how the pane starves.
func TestAStaleReplySettlesWithoutPainting(t *testing.T) {
	m := previewReadModel(t)
	m = selectLine(t, m, "session:web")
	read := m.refreshPreviewForCursor()
	if read == nil {
		t.Fatal("setup: the move dispatched no read")
	}
	issued := read().(previewContentMsg)
	issued.text = "web scrollback"

	// The operator moves on before the reply lands.
	m = selectLine(t, m, "session:cli")
	if cmd := m.refreshPreviewForCursor(); cmd != nil {
		t.Fatal("setup: the second move read while one was already out")
	}

	updated, catchUp := m.Update(issued)
	m = updated.(Model)
	if strings.Contains(m.previewText, "web scrollback") {
		t.Error("a reply for the abandoned selection painted over the new one")
	}
	if m.previewLoaded {
		t.Error("a reply for the abandoned selection marked the new target loaded")
	}
	if catchUp == nil {
		t.Fatal("the stale reply released no slot, so the pane would never read again")
	}
	settlePreviewRead(t, &m, catchUp)
	if !m.previewLoaded {
		t.Error("the catch-up's own reply did not mark the current target loaded")
	}
	if m.previewWindowID != 13 {
		t.Errorf("the pane ended on window %d, want cli's 13", m.previewWindowID)
	}
}

// TestAFailedReadSettlesLikeAnyOther keeps a broken Kitty from wedging the pane.
// The failure is content — the inspector says so — but it is first of all a
// reply, and a controller that released its slot only on success would stop
// reading the moment one read failed.
func TestAFailedReadSettlesLikeAnyOther(t *testing.T) {
	m := previewReadModel(t)
	m = selectLine(t, m, "session:web")
	read := m.refreshPreviewForCursor()
	if read == nil {
		t.Fatal("setup: the move dispatched no read")
	}
	failed := read().(previewContentMsg)
	failed.err = errors.New("kitty is not listening")
	failed.text = ""

	updated, _ := m.Update(failed)
	m = updated.(Model)
	if m.previewReadInFlight {
		t.Fatal("a failed read kept the in-flight slot")
	}
	if m.previewError == "" {
		t.Error("a failed read recorded no failure")
	}
	if !m.previewLoaded {
		t.Error("a failed read left the target reading as still loading")
	}
	if cmd := m.requestPreviewRead(); cmd == nil {
		t.Fatal("the pane refused to read again after a failure")
	}
}

// TestHidingThePaneDoesNotStrandTheSlot covers the ownership changes: the
// dashboard pane toggled shut and the resize guard turning one off. Each leaves
// a read in flight whose reply nobody wants, and each has to end with a pane
// that reads normally once it is shown again.
func TestHidingThePaneDoesNotStrandTheSlot(t *testing.T) {
	hide := map[string]func(m *Model){
		"toggled off": func(m *Model) {
			updated, _ := m.togglePreview()
			*m = updated.(Model)
		},
		"disabled by the resize guard": func(m *Model) {
			m.disablePreviewWithNotice("preview disabled: terminal too small")
		},
	}
	for name, hideIt := range hide {
		t.Run(name, func(t *testing.T) {
			m := previewReadModel(t)
			m = selectLine(t, m, "session:web")
			read := m.refreshPreviewForCursor()
			if read == nil {
				t.Fatal("setup: the move dispatched no read")
			}
			inFlight := read().(previewContentMsg)
			inFlight.text = "web scrollback"

			hideIt(&m)
			if m.previewEnabled {
				t.Fatal("setup: the pane is still shown")
			}

			updated, follow := m.Update(inFlight)
			m = updated.(Model)
			if m.previewReadInFlight {
				t.Fatal("the reply that landed under a hidden pane released no slot")
			}
			if strings.Contains(m.previewText, "web scrollback") {
				t.Error("a reply painted a pane nobody is looking at")
			}
			if follow != nil {
				t.Error("a hidden pane started a catch-up read")
			}

			// Shown again, it reads for itself.
			updated, cmd := m.togglePreview()
			m = updated.(Model)
			if !m.previewEnabled {
				t.Fatal("the pane refused to reopen")
			}
			if cmd == nil {
				t.Fatal("reopening the pane dispatched neither a read nor a tick")
			}
			if !m.previewReadInFlight {
				t.Error("reopening the pane read nothing")
			}
			settlePreviewRead(t, &m, cmd)
			if m.previewReadInFlight || m.previewReadPending {
				t.Errorf("the reopened pane left work outstanding (in flight %v, pending %v)",
					m.previewReadInFlight, m.previewReadPending)
			}
			if !m.previewLoaded {
				t.Error("the reopened pane never painted the read it dispatched")
			}
		})
	}
}

// TestARetiredTickChainNeverSchedulesAgain is the duplicate-polling guard. Two
// chains beating on one pane double its read rate with no path back to one, and
// the way to get two is to show a pane twice inside one refresh interval — which
// a hide/show, a launcher round trip, and a resize that re-fits all do.
func TestARetiredTickChainNeverSchedulesAgain(t *testing.T) {
	m := previewReadModel(t)
	if m.previewPollSeq != 0 {
		t.Fatalf("setup: the model already owns chain %d", m.previewPollSeq)
	}
	if first := m.startPreviewPoll(); first == nil {
		t.Fatal("a shown pane opened no chain")
	}
	retired := m.previewPollSeq

	if second := m.startPreviewPoll(); second == nil {
		t.Fatal("reopening the chain produced no tick")
	}
	if m.previewPollSeq == retired {
		t.Fatal("the second chain reused the first chain's identity")
	}

	updated, follow := m.Update(previewTickMsg{pollSeq: retired})
	m = updated.(Model)
	if follow != nil {
		t.Error("a tick from the retired chain scheduled a successor, so the pane now polls twice per interval")
	}
	if m.previewReadInFlight {
		t.Error("a tick from the retired chain also read")
	}

	// The live chain does beat on, and one beat is one read plus one successor.
	updated, follow = m.Update(previewTickMsg{pollSeq: m.previewPollSeq})
	m = updated.(Model)
	if follow == nil {
		t.Fatal("the live chain stopped scheduling")
	}
	if isUITestTimerCmd(follow) {
		t.Error("the live chain scheduled its successor but dispatched no read")
	}
	if !m.previewReadInFlight {
		t.Error("the live chain's beat read nothing")
	}

	// An unstamped tick belongs to no chain and is refused even against the zero
	// counter a model that has never shown a pane holds.
	fresh, _ := listViewModel(dashboard.ViewUrgency)
	fresh.previewEnabled = true
	updated, follow = fresh.Update(previewTickMsg{})
	if follow != nil {
		t.Error("an unstamped tick opened a polling chain")
	}
	if updated.(Model).previewReadInFlight {
		t.Error("an unstamped tick read the pane")
	}
}

// TestATickCoalescesWithTheReadAlreadyOut is the starvation the tick used to
// cause on its own: a read slower than the refresh interval met a tick that read
// again regardless, so the pane ran several subprocesses at once and each was
// superseded before it landed.
func TestATickCoalescesWithTheReadAlreadyOut(t *testing.T) {
	m := previewReadModel(t)
	m = selectLine(t, m, "session:web")
	read := m.refreshPreviewForCursor()
	if read == nil {
		t.Fatal("setup: the move dispatched no read")
	}
	m.previewPollSeq = 7

	for beat := 0; beat < 3; beat++ {
		updated, follow := m.Update(previewTickMsg{pollSeq: 7})
		m = updated.(Model)
		if follow == nil {
			t.Fatalf("beat %d stopped the chain", beat)
		}
		// The one command handed back is the next tick: the read coalesced
		// rather than stacking behind the one still out.
		if !isUITestTimerCmd(follow) {
			t.Fatalf("beat %d dispatched a read while one was in flight", beat)
		}
	}
	if !m.previewReadPending {
		t.Error("three beats against an outstanding read left nothing owed")
	}
	settlePreviewRead(t, &m, read)
	if m.previewReadInFlight || m.previewReadPending {
		t.Errorf("the coalesced beats cost more than one catch-up (in flight %v, pending %v)",
			m.previewReadInFlight, m.previewReadPending)
	}
}

// TestThePaneRetainsOnlyTwoSnapshots pins the retention rule: what was last
// fetched, and — while the operator has scrolled back — the copy they are
// reading. Nothing accumulates, so a session that has printed for an hour costs
// the same two captures as one that just started.
func TestThePaneRetainsOnlyTwoSnapshots(t *testing.T) {
	m := previewReadModel(t)
	m = selectLine(t, m, "session:web")
	settlePreviewRead(t, &m, m.refreshPreviewForCursor())

	deliver := func(text string) {
		updated, _ := m.Update(previewContentMsg{
			targetSeq: m.previewTargetSeq, requestSeq: m.previewRequestSeq,
			windowID: m.previewWindowID, text: text,
		})
		m = updated.(Model)
	}

	deliver(strings.Repeat("old\n", 40))
	m.inspectorViewport = m.inspectorViewport.ScrollUp(dashboard.SplitOutput(m.previewText), 4, 2)
	if !m.inspectorViewport.Paused() {
		t.Fatal("setup: scrolling back held no snapshot")
	}

	for i := 0; i < 5; i++ {
		deliver(strings.Repeat("new\n", 40))
	}
	if !strings.Contains(m.previewText, "new") || strings.Contains(m.previewText, "old") {
		t.Errorf("the model is not holding the latest snapshot alone: %q", firstPreviewLine(m.previewText))
	}
	window := strings.Join(m.inspectorViewport.Window(dashboard.SplitOutput(m.previewText), 4), "\n")
	if !strings.Contains(window, "old") {
		t.Errorf("five refreshes under a held pane lost the snapshot being read: %q", window)
	}
}

func firstPreviewLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestConcurrentPreviewReadsAreRaceFree drives the read seam the way the program
// loop does — commands on their own goroutines against a client that is slow and
// that fails, replies applied one at a time — so the race detector sees the
// arrangement the invariants above only state functionally.
//
// The bound is measured from inside the client rather than counted from outside
// it, because "one read at a time" is about overlap and not about totals: a
// steady stream of moves is allowed to cost many reads over a run and is never
// allowed to have two of them in the terminal at once.
func TestConcurrentPreviewReadsAreRaceFree(t *testing.T) {
	client := newUITestKittyClient(nil)
	var reads, concurrent, overlaps atomic.Int64
	client.SetReadBehavior(func(string) (string, error) {
		if concurrent.Add(1) > 1 {
			overlaps.Add(1)
		}
		defer concurrent.Add(-1)
		time.Sleep(time.Millisecond)
		if reads.Add(1)%3 == 0 {
			return "", errors.New("kitty is not listening")
		}
		return strings.Repeat("scrollback\n", 20), nil
	})

	m := previewReadModel(t)
	m.client = client
	m.previewPollSeq = 1

	replies := make(chan tea.Msg, 128)
	var wg sync.WaitGroup
	run := func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			replies <- cmd()
		}()
	}

	lines := []string{"session:web", "session:cli", "session:lib", "session:etl", "session:api"}
	const rounds = 12
	for round := 0; round < rounds; round++ {
		m = selectLine(t, m, lines[round%len(lines)])
		run(m.refreshPreviewForCursor())
		// The beat the chain would deliver, without the timer that carries it.
		run(m.requestPreviewRead())
		for drained := true; drained; {
			select {
			case msg := <-replies:
				updated, catchUp := m.Update(msg)
				m = updated.(Model)
				run(catchUp)
			default:
				drained = false
			}
		}
	}

	wg.Wait()
	close(replies)
	for msg := range replies {
		updated, catchUp := m.Update(msg)
		m = updated.(Model)
		settlePreviewRead(t, &m, catchUp)
	}
	if m.previewReadPending && !m.previewReadInFlight {
		t.Error("the run ended owing a catch-up nothing will start")
	}
	if n := client.ReadCount(); n == 0 {
		t.Fatal("the run never reached the fake client")
	}
	if n := overlaps.Load(); n != 0 {
		t.Errorf("%d read(s) found another already in the terminal; the pane is meant to hold one at a time", n)
	}
}

// TestASlowReadNeverBlocksTheUpdateLoop is the responsiveness half. The read is
// a command, so the loop that handles keys never waits on it: with a read parked
// mid-flight the dashboard still navigates, filters, and renders, and the pane
// keeps showing what it last had.
func TestASlowReadNeverBlocksTheUpdateLoop(t *testing.T) {
	client := newUITestKittyClient(nil)
	release := make(chan struct{})
	client.SetReadBehavior(func(string) (string, error) {
		<-release
		return "released", nil
	})

	m := previewReadModel(t)
	m.client = client
	m.previewPollSeq = 1
	m = selectLine(t, m, "session:web")
	read := m.refreshPreviewForCursor()
	if read == nil {
		t.Fatal("setup: the move dispatched no read")
	}
	parked := m.previewReadActiveSeq
	done := make(chan tea.Msg, 1)
	go func() { done <- read() }()

	// The read is parked inside the fake client. Every one of these is handled
	// on the update loop and must return without waiting for it.
	settled := make(chan Model, 1)
	go func() {
		local := m
		for _, key := range []string{"j", "k", "g", "G"} {
			updated, _ := local.Update(keyPress(key))
			local = updated.(Model)
		}
		updated, _ := local.Update(previewTickMsg{pollSeq: 1})
		local = updated.(Model)
		_ = local.render()
		settled <- local
	}()

	select {
	case m = <-settled:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the update loop blocked on a preview read that had not answered")
	}

	close(release)
	select {
	case msg := <-done:
		updated, _ := m.Update(msg)
		// The slot the parked read held is released. A catch-up may take it
		// again in the same step — the moves above coalesced into one — so the
		// question is whether this read is still the one being waited on.
		after := updated.(Model)
		if after.previewReadInFlight && after.previewReadActiveSeq == parked {
			t.Error("the released read did not settle")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the released read never answered")
	}
}
