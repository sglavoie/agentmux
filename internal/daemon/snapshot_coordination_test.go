package daemon

// The daemon half of the per-socket list coordination introduced for
// internal/kitty. These tests drive a real *kitty.SubprocessClient through the
// daemon's own publication paths — bounded startup restore, maintenance,
// render-queue drains, and the post-gap watchdog refresh — rather than calling
// the coordinator directly, because what matters here is that all four reach
// Kitty through one client whose socket scoping shares one coordinator.

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
)

const (
	coordinationSocket = "unix:/tmp/kitty-amx-coordination-test"
	otherSocket        = "unix:/tmp/kitty-amx-other-test"
)

// gatedListRunner is a kitty.CommandRunner that can park every `kitten @ ls`
// aimed at one socket. Only list commands are gated: writes issued after the
// list completes must not deadlock behind the same gate.
type gatedListRunner struct {
	mu        sync.Mutex
	payload   []byte
	listCalls map[string]int
	gates     map[string]chan struct{}
	entered   map[string]chan struct{}
}

func newGatedListRunner(payload []byte) *gatedListRunner {
	return &gatedListRunner{
		payload:   payload,
		listCalls: make(map[string]int),
		gates:     make(map[string]chan struct{}),
		entered:   make(map[string]chan struct{}),
	}
}

// blockLists parks every list for socket until the returned release func runs.
// entered reports the first parked list.
func (r *gatedListRunner) blockLists(socket string) (release func(), entered <-chan struct{}) {
	gate := make(chan struct{})
	arrived := make(chan struct{}, 1)
	r.mu.Lock()
	r.gates[socket] = gate
	r.entered[socket] = arrived
	r.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }, arrived
}

func (r *gatedListRunner) Run(_ string, args ...string) ([]byte, []byte, error) {
	socket, isList := parseKittenList(args)
	if !isList {
		return []byte("{}"), nil, nil
	}
	r.mu.Lock()
	r.listCalls[socket]++
	gate := r.gates[socket]
	arrived := r.entered[socket]
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
	return r.payload, nil, nil
}

func (r *gatedListRunner) listCallCount(socket string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listCalls[socket]
}

// parseKittenList reports the socket a `kitten @ [--to SOCKET] ...` invocation
// targets and whether the command is `ls`.
func parseKittenList(args []string) (socket string, isList bool) {
	for i, arg := range args {
		switch {
		case arg == "--to" && i+1 < len(args):
			socket = args[i+1]
		case arg == "ls":
			isList = true
		}
	}
	return socket, isList
}

// newCoordinatedHandler wires a handler to a real SubprocessClient whose socket
// comes from the daemon's recorded listen address, so every list path is a
// WithSocket clone sharing one coordinator — exactly the production shape.
func newCoordinatedHandler(t *testing.T, runner kitty.CommandRunner) (*Handler, *kitty.SubprocessClient) {
	t.Helper()
	client := kitty.NewClientWithSocket(runner, "")
	h := newTestHandler()
	h.useKittyClient(client)
	h.stateMu.Lock()
	h.lastKittyListenOn = coordinationSocket
	h.stateMu.Unlock()
	return h, client
}

func mustMarshalWindows(t *testing.T, windows []kitty.OSWindow) []byte {
	t.Helper()
	payload, err := json.Marshal(windows)
	if err != nil {
		t.Fatalf("marshalling fake kitty topology: %v", err)
	}
	return payload
}

// waitForCoalescedWaiters blocks until want callers have joined the socket's
// in-flight read, so the test can assert on a fully assembled overlap instead of
// racing the goroutines it just started.
func waitForCoalescedWaiters(t *testing.T, client *kitty.SubprocessClient, socket string, want uint64) kitty.ListStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, ok := client.WithSocket(socket).ListStatus()
		if ok && status.CoalescedWaiters >= want {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d callers joined the in-flight read (status %+v, ok %v)",
				status.CoalescedWaiters, want, status, ok)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestBoundedStartupOverlapKeepsOneSameSocketSubprocess is the session's central
// overlap: the startup snapshot outruns its 400ms budget, the daemon comes up
// degraded, and maintenance, rendering, and the watchdog all begin while that
// read is still outstanding. All three must join the startup subprocess rather
// than spawn `kitten @ ls` a second, third, and fourth time against a Kitty that
// is already struggling to answer the first.
func TestBoundedStartupOverlapKeepsOneSameSocketSubprocess(t *testing.T) {
	runner := newGatedListRunner(mustMarshalWindows(t, fakeOSWindowsWithTab(42, "W:api")))
	release, entered := runner.blockLists(coordinationSocket)
	defer release()
	h, client := newCoordinatedHandler(t, runner)

	// A render candidate, so the drain below actually needs to list.
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.DesiredIcon = "bolt"
	st.StateSet = true
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()

	start := time.Now()
	ready, reason := h.restoreStateFromKittyBounded(start, initialSnapshotBudget)
	budgetElapsed := time.Since(start)
	if ready || reason == "" {
		t.Fatalf("bounded restore = (%v, %q); want degraded once the read outruns the budget", ready, reason)
	}
	if budgetElapsed < initialSnapshotBudget {
		t.Fatalf("bounded restore returned after %v; want at least the %v budget", budgetElapsed, initialSnapshotBudget)
	}
	h.MarkDegraded(reason)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the startup list never reached the runner")
	}

	var overlap sync.WaitGroup
	overlap.Add(3)
	go func() { defer overlap.Done(); h.maintenanceTick(time.Now()) }()
	go func() { defer overlap.Done(); h.drainRenderQueue() }()
	go func() { defer overlap.Done(); h.watchdogTick(time.Now(), watchdogGapThreshold+time.Second) }()

	status := waitForCoalescedWaiters(t, client, coordinationSocket, 3)
	if !status.InFlight {
		t.Fatalf("status %+v; want the startup read still in flight", status)
	}
	// An attempt is only counted once it settles, so mid-flight the accounting
	// shows nothing completed yet — the runner's own call count is what proves
	// a single subprocess is carrying all four callers.
	if status.Attempts != 0 {
		t.Fatalf("underlying attempts = %d mid-flight; want the blocked read still uncounted", status.Attempts)
	}
	if got := runner.listCallCount(coordinationSocket); got != 1 {
		t.Fatalf("kitten @ ls ran %d times while the startup read was blocked; want 1", got)
	}

	release()
	overlap.Wait()

	// Every joined caller publishes what the one read returned, so the four
	// paths together advance the generation four times off a single subprocess.
	waitForGeneration(t, h, 4)
	if got := runner.listCallCount(coordinationSocket); got != 1 {
		t.Fatalf("kitten @ ls ran %d times in total; want the single coalesced subprocess", got)
	}
	final, ok := client.WithSocket(coordinationSocket).ListStatus()
	if !ok {
		t.Fatal("coordinator retained no status for the daemon socket")
	}
	if final.Attempts != 1 || final.Failures != 0 || final.CoalescedWaiters != 3 {
		t.Fatalf("final status = %+v; want 1 attempt, 0 failures, 3 waiters", final)
	}
	view := h.cachedSnapshotView()
	if !view.Ready() || view.CapturedAt().IsZero() {
		t.Fatalf("view = %+v; want a published snapshot with a capture time", view)
	}
	if !view.CapturedAt().After(start) {
		t.Fatalf("capture time %v is not after the startup instant %v; the blocked read was dated by its request",
			view.CapturedAt(), start)
	}
}

// TestBlockedDaemonSocketLeavesOtherSocketsIndependent pins that coalescing is
// per socket: a daemon whose own Kitty instance is wedged must not stall a read
// aimed at a different one.
func TestBlockedDaemonSocketLeavesOtherSocketsIndependent(t *testing.T) {
	runner := newGatedListRunner(mustMarshalWindows(t, fakeOSWindowsWithTab(42, "W:api")))
	release, entered := runner.blockLists(coordinationSocket)
	defer release()
	h, client := newCoordinatedHandler(t, runner)

	maintenance := make(chan struct{})
	go func() { defer close(maintenance); h.maintenanceTick(time.Now()) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the maintenance list never reached the runner")
	}

	done := make(chan error, 1)
	go func() {
		_, err := client.WithSocket(otherSocket).ListWindows()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("independent socket list failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a read on a different socket blocked behind the wedged daemon socket")
	}
	if got := runner.listCallCount(otherSocket); got != 1 {
		t.Fatalf("other-socket ls ran %d times; want 1", got)
	}
	if got := runner.listCallCount(coordinationSocket); got != 1 {
		t.Fatalf("daemon-socket ls ran %d times; want the single blocked read", got)
	}

	release()
	<-maintenance
}

// TestConcurrentDaemonPathsShareOneSubprocessRepeatedly runs the four listing
// paths together over many rounds with no gate at all. It is the race-detector
// guard for driving the shared coordinator from every daemon subsystem at once,
// and it pins the accounting: waiters are counted, never charged as attempts.
func TestConcurrentDaemonPathsShareOneSubprocessRepeatedly(t *testing.T) {
	runner := newGatedListRunner(mustMarshalWindows(t, fakeOSWindowsWithTab(42, "W:api")))
	h, client := newCoordinatedHandler(t, runner)
	h.MarkReady()

	const rounds = 25
	var wg sync.WaitGroup
	drive := func(work func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				work()
			}
		}()
	}
	drive(func() { h.maintenanceTick(time.Now()) })
	drive(func() {
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.DesiredIcon = "bolt"
		st.StateSet = true
		h.markWindowDirtyLocked(42)
		h.stateMu.Unlock()
		h.drainRenderQueue()
	})
	drive(func() { h.watchdogTick(time.Now(), watchdogGapThreshold+time.Second) })
	drive(func() { h.primeSnapshot() })
	wg.Wait()

	status, ok := client.WithSocket(coordinationSocket).ListStatus()
	if !ok {
		t.Fatal("coordinator retained no status for the daemon socket")
	}
	if status.Failures != 0 || status.Timeouts != 0 {
		t.Fatalf("status = %+v; want no failures from a healthy runner", status)
	}
	subprocesses := runner.listCallCount(coordinationSocket)
	if uint64(subprocesses) != status.Attempts {
		t.Fatalf("runner ran %d subprocesses but the coordinator counted %d attempts",
			subprocesses, status.Attempts)
	}
	// Four subsystems each listing `rounds` times can only reach this few
	// subprocesses by sharing them.
	if uint64(subprocesses) >= 4*rounds {
		t.Fatalf("ran %d subprocesses for %d requested reads; nothing coalesced", subprocesses, 4*rounds)
	}
	if status.Attempts+status.CoalescedWaiters == 0 {
		t.Fatalf("status = %+v; want the daemon paths accounted for", status)
	}
	view := h.cachedSnapshotView()
	if !view.Ready() || view.CapturedAt().IsZero() {
		t.Fatalf("view = %+v; want a published snapshot with a capture time", view)
	}
}

// waitForGeneration blocks until the published snapshot reaches want, which is
// how a test joins the detached startup restore: its publication is the only
// observable it leaves behind.
func waitForGeneration(t *testing.T, h *Handler, want uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		view := h.cachedSnapshotView()
		if view.Generation() >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("snapshot generation reached %d; want %d", view.Generation(), want)
		}
		time.Sleep(time.Millisecond)
	}
}
