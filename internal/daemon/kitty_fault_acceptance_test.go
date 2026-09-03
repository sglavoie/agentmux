package daemon

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// The daemon half of the cross-layer fault acceptance for a Kitty that stops
// answering. What is being proven is not that any single component behaves, but
// that the components hold together under the fault: the cached state query stays
// answerable while every listing subsystem is parked on one subprocess, the
// last-good topology outlives the failure that follows, the durable record stays
// one pair, and recovery clears the status rather than leaving a permanent scar.
//
// Nothing here sleeps for a threshold. Staleness comes from publishing with an
// explicit capture instant, and the outage comes from a runner that parks and
// fails on command.

// kittyFaultRunner is a kitten runner that can park every `kitten @ ls` for a
// socket and can fail them, independently. Writes are never gated: a render that
// runs after a list completed must not deadlock behind the list's gate.
type kittyFaultRunner struct {
	mu        sync.Mutex
	payload   []byte
	listCalls map[string]int
	broken    bool
	gate      chan struct{}
	entered   chan struct{}
}

func newKittyFaultRunner(payload []byte) *kittyFaultRunner {
	return &kittyFaultRunner{payload: payload, listCalls: make(map[string]int)}
}

// park makes every subsequent list block until release runs. entered reports the
// arrival of the first parked list.
func (r *kittyFaultRunner) park() (release func(), entered <-chan struct{}) {
	gate := make(chan struct{})
	arrived := make(chan struct{}, 1)
	r.mu.Lock()
	r.gate, r.entered = gate, arrived
	r.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }, arrived
}

func (r *kittyFaultRunner) setBroken(broken bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.broken = broken
}

func (r *kittyFaultRunner) listCallCount(socket string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listCalls[socket]
}

func (r *kittyFaultRunner) Run(_ string, args ...string) ([]byte, []byte, error) {
	socket, isList := parseKittenList(args)
	if !isList {
		return []byte("{}"), nil, nil
	}
	r.mu.Lock()
	r.listCalls[socket]++
	gate, arrived := r.gate, r.entered
	r.mu.Unlock()
	if arrived != nil {
		select {
		case arrived <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		<-gate
	}
	r.mu.Lock()
	broken, payload := r.broken, r.payload
	r.mu.Unlock()
	if broken {
		return nil, []byte("kitty remote control is not responding"), errors.New("kitten @ ls: connection refused")
	}
	return payload, nil, nil
}

// TestBlockedKittySurvivesAsOneOutageAcrossEveryDaemonSurface is the cross-layer
// scenario: a warm daemon meets a Kitty that first stops answering and then
// starts refusing, and every surface an operator or a dashboard reads has to
// stay coherent through it and come back clean afterwards.
func TestBlockedKittySurvivesAsOneOutageAcrossEveryDaemonSurface(t *testing.T) {
	recorder := journalRecorder(t)
	runner := newKittyFaultRunner(mustMarshalWindows(t, fakeOSWindowsWithTab(42, "W:api")))
	h, client := newCoordinatedHandler(t, runner)
	client.SetListObserver(recorder.kittySnapshotObserver())
	h.MarkReady()

	// A warm daemon whose last successful read is already past the threshold. The
	// capture instant is the clock: nothing here waits for six real seconds.
	lastGood := fakeOSWindowsWithTab(42, "W:api")
	h.publishSnapshot(lastGood, time.Now().Add(-protocol.KittySnapshotStaleAfter-time.Second))
	warm := h.cachedSnapshotView().Generation()

	// Kitty stops answering, and every listing subsystem walks into it at once.
	release, entered := runner.park()
	defer release()
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.DesiredIcon = "bolt"
	st.StateSet = true
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()

	var listing sync.WaitGroup
	listing.Add(4)
	go func() { defer listing.Done(); h.maintenanceTick(time.Now()) }()
	go func() { defer listing.Done(); h.drainRenderQueue() }()
	go func() { defer listing.Done(); h.watchdogTick(time.Now(), watchdogGapThreshold+time.Second) }()
	go func() { defer listing.Done(); h.primeSnapshot() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no list reached the runner")
	}
	waitForCoalescedWaiters(t, client, coordinationSocket, 3)

	// Proof one: the state query is served from the cache, so it answers while
	// every listing path is parked on a Kitty that will not respond.
	answered := make(chan protocol.Response, 1)
	go func() { answered <- h.Handle(protocol.Request{Type: protocol.TypeStateQuery}) }()
	var resp protocol.Response
	select {
	case resp = <-answered:
	case <-time.After(5 * time.Second):
		t.Fatal("state_query blocked behind the wedged kitten @ ls; the cache is not serving it")
	}
	block := resp.KittySnapshot
	if block == nil || !block.Ready {
		t.Fatalf("block = %+v; want the warm cache still ready during the outage", block)
	}
	if !block.Stale {
		t.Fatalf("block = %+v; want the aged cache reported stale", block)
	}
	if block.Generation != warm || len(block.Windows) != 1 || block.Windows[0].Tabs[0].Title != "W:api" {
		t.Fatalf("block = %+v; want the last-good topology served unchanged", block)
	}
	if block.Coordinator == nil || !block.Coordinator.InFlight {
		t.Fatalf("coordinator = %+v; want the parked read visible as in flight", block.Coordinator)
	}

	// Proof two: four subsystems plus the state query are one subprocess.
	if got := runner.listCallCount(coordinationSocket); got != 1 {
		t.Fatalf("kitten @ ls ran %d times during the outage; want the single coalesced read", got)
	}

	// Kitty comes back only far enough to refuse: the parked read now fails.
	runner.setBroken(true)
	release()
	listing.Wait()

	// Proof three: a failed list does not erase what the daemon already knew.
	view := h.cachedSnapshotView()
	if !view.Ready() || view.Generation() != warm {
		t.Fatalf("view = %+v; want the last-good snapshot preserved at generation %d", view, warm)
	}
	if windows := view.Windows(); len(windows) != 1 || windows[0].Tabs[0].Title != "W:api" {
		t.Fatalf("windows = %+v; want the last-good topology preserved", windows)
	}
	block = stateQuerySnapshot(t, h)
	if block.Coordinator == nil || block.Coordinator.ConsecutiveFailures == 0 {
		t.Fatalf("coordinator = %+v; want the outage visible as a failure streak", block.Coordinator)
	}
	if block.Coordinator.LastErrorClass == "" {
		t.Fatalf("coordinator = %+v; want the failure classified", block.Coordinator)
	}

	// Proof four: however many callers the failure was shared by, it is one
	// durable record.
	failures := waitForKittySnapshotRecords(t, 1)
	if len(failures) != 1 {
		t.Fatalf("%d journal records for one outage; want 1: %+v", len(failures), failures)
	}

	// Kitty recovers.
	runner.setBroken(false)
	h.primeSnapshot()

	// Proof five: recovery clears the status rather than leaving a scar.
	block = stateQuerySnapshot(t, h)
	if !block.Ready || block.Stale {
		t.Fatalf("block = %+v; want a fresh ready snapshot after recovery", block)
	}
	if block.Generation <= warm {
		t.Fatalf("generation = %d; want the recovery publication past the warm %d", block.Generation, warm)
	}
	if block.Coordinator == nil || block.Coordinator.ConsecutiveFailures != 0 || block.Coordinator.InFlight {
		t.Fatalf("coordinator = %+v; want a settled healthy socket", block.Coordinator)
	}
	// Health reports the same recovery, without the tree.
	health := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	if health.KittySnapshot == nil || health.KittySnapshot.Stale || health.KittySnapshot.Windows != nil {
		t.Fatalf("health block = %+v; want a fresh status-only block", health.KittySnapshot)
	}

	// Proof six: the outage is exactly one failure/recovery pair on the record.
	records := waitForKittySnapshotRecords(t, 2)
	if len(records) != 2 {
		t.Fatalf("%d journal records for one outage and one recovery; want 2: %+v", len(records), records)
	}
	if records[0].Reason != "list_failed" || records[1].Reason != "list_recovered" {
		t.Fatalf("record reasons = (%q, %q); want (list_failed, list_recovered)",
			records[0].Reason, records[1].Reason)
	}
}

// TestRepeatedKittyOutagesDoNotMultiplySubprocessesOrRecords is the endurance
// shape of the same fault: many rounds of failure and recovery driven by every
// listing subsystem at once. It is the race-detector guard for the observer
// running on the listing goroutine, and it pins the two bounds that make the
// fault survivable — subprocesses stay far below requested reads, and the durable
// record stays proportional to outages rather than to attempts.
func TestRepeatedKittyOutagesDoNotMultiplySubprocessesOrRecords(t *testing.T) {
	recorder := journalRecorder(t)
	runner := newKittyFaultRunner(mustMarshalWindows(t, fakeOSWindowsWithTab(42, "W:api")))
	h, client := newCoordinatedHandler(t, runner)
	client.SetListObserver(recorder.kittySnapshotObserver())
	h.MarkReady()
	h.publishSnapshot(fakeOSWindowsWithTab(42, "W:api"), time.Now())

	const rounds = 12
	for round := 0; round < rounds; round++ {
		runner.setBroken(round%2 == 0)
		var wg sync.WaitGroup
		drive := func(work func()) {
			wg.Add(1)
			go func() { defer wg.Done(); work() }()
		}
		drive(func() { h.maintenanceTick(time.Now()) })
		drive(func() { h.drainRenderQueue() })
		drive(func() { h.watchdogTick(time.Now(), watchdogGapThreshold+time.Second) })
		drive(func() { h.primeSnapshot() })
		drive(func() { h.Handle(protocol.Request{Type: protocol.TypeStateQuery}) })
		wg.Wait()
	}

	// The topology never disappears, whichever half of the cycle the run ended on.
	view := h.cachedSnapshotView()
	if !view.Ready() {
		t.Fatalf("view = %+v; want the last-good snapshot to have survived every outage", view)
	}

	status, ok := client.WithSocket(coordinationSocket).ListStatus()
	if !ok {
		t.Fatal("coordinator retained no status for the daemon socket")
	}
	subprocesses := runner.listCallCount(coordinationSocket)
	if uint64(subprocesses) != status.Attempts {
		t.Fatalf("runner ran %d subprocesses but the coordinator counted %d attempts",
			subprocesses, status.Attempts)
	}
	if subprocesses >= 4*rounds {
		t.Fatalf("ran %d subprocesses for %d rounds of four listing paths; nothing coalesced",
			subprocesses, rounds)
	}

	// Six failing rounds can produce at most six outage edges and six recoveries,
	// however many attempts each round took. That upper bound — not an exact
	// count, since a round's four paths may or may not coalesce into one attempt —
	// is what keeps a flapping Kitty from filling the journal.
	records := readKittySnapshotRecords(t)
	if len(records) > rounds {
		t.Fatalf("%d journal records for %d rounds; the record is not bounded by outages", len(records), rounds)
	}
	if len(records) == 0 {
		t.Fatal("no journal records at all; the outages left no durable evidence")
	}
	for i, record := range records {
		if record.Reason != "list_failed" && record.Reason != "list_recovered" {
			t.Errorf("record %d reason = %q; want a health edge", i, record.Reason)
		}
	}
}
