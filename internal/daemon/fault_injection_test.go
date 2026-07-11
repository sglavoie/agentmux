package daemon

// Deterministic fault injection. Each test injects one audited failure mode —
// wedged or failing Kitty listing, flapping title writes, a crash between the
// ordered persist and title writes, degraded startup, IPC response loss, queue
// pressure, dropped ASL lifecycle events, malformed persisted state — and
// asserts the corresponding recovery invariant:
//
//   - a wedged Kitty never blocks hook acknowledgement;
//   - the rendered generation eventually equals the desired generation once
//     the fault clears;
//   - one logical request applies exactly once even when its response is lost;
//   - queue pressure degrades (inline processing, queue_full counter) but
//     never drops an event;
//   - reconciliation repairs dropped ASL lifecycle events in both directions;
//   - malformed persisted state is discarded without harming other windows.

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestFaultWedgedKittyListNeverBlocksHookIngestion parks ListWindows on a
// channel and drives hook events through the handler while a render drain is
// stuck behind the wedged list. Every Handle call must return promptly — the
// hook path routes against the cached snapshot and performs no Kitty I/O.
func TestFaultWedgedKittyListNeverBlocksHookIngestion(t *testing.T) {
	h := newTestHandler()
	release := make(chan struct{})
	fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(42, "W:api")}
	h.SetKittyClient(fake) // primes the snapshot with one unwedged list
	fake.mu.Lock()
	fake.beforeListWindows = func() { <-release } // every later list wedges
	fake.mu.Unlock()

	// Park a render drain inside the wedged ListWindows.
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.DesiredIcon = "bolt"
	st.StateSet = true
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()
	drainDone := make(chan struct{})
	go func() {
		h.drainRenderQueue()
		close(drainDone)
	}()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "PreToolUse", PayloadJSON: "{}"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("hook ingestion blocked behind a wedged Kitty list")
	}

	close(release)
	select {
	case <-drainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("render drain never returned after Kitty recovered")
	}
}

// TestFaultFailingKittyListLeavesRenderPendingThenConverges: a failing list
// leaves the window dirty (nothing is dropped), and the first successful flush
// converges the rendered generation to the desired generation.
func TestFaultFailingKittyListLeavesRenderPendingThenConverges(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(42, "W:api"), listErr: errors.New("kitty wedged")}
	h.SetKittyClient(fake)

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.DesiredIcon = "bolt"
	st.StateSet = true
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()

	h.drainRenderQueue() // fails to list; queue entry must survive

	h.stateMu.Lock()
	dirty := h.store[42].Dirty
	h.stateMu.Unlock()
	if !dirty {
		t.Fatal("dirty flag cleared by a failed list; pending render was dropped")
	}

	fake.mu.Lock()
	fake.listErr = nil
	fake.mu.Unlock()
	h.renderSnapshot(fake.windows) // the maintenance flush path

	h.stateMu.Lock()
	st = h.store[42]
	converged := !st.Dirty && st.RenderedGen == st.Seq && st.PersistedGen == st.Seq
	h.stateMu.Unlock()
	if !converged {
		t.Fatalf("render did not converge after list recovered: %+v", st)
	}
	if calls := fake.titleCalls(); len(calls) != 1 || palette.ParseTitleParts(calls[0].title).State != "bolt" {
		t.Fatalf("title calls = %+v; want one bolt title", calls)
	}
}

// TestFaultFlappingTitleWritesEventuallyConverge injects three consecutive
// title-write failures. Every maintenance flush retries from the persisted
// desired state, and once Kitty recovers the rendered generation equals the
// desired generation with the failure diagnostics cleared.
func TestFaultFlappingTitleWritesEventuallyConverge(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(42, "W:api")}
	var titleAttempts atomic.Int32
	fake.setTitleErr = errors.New("kitty flapping")
	fake.beforeSetTitle = func(string, string) {
		if titleAttempts.Add(1) >= 3 {
			fake.mu.Lock()
			fake.setTitleErr = nil
			fake.mu.Unlock()
		}
	}
	h.SetKittyClient(fake)

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.DesiredIcon = "error"
	st.StateSet = true
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()

	for i := 0; i < 5; i++ {
		h.renderSnapshot(fake.windows)
		h.stateMu.Lock()
		converged := h.store[42].RenderedGen == h.store[42].Seq
		h.stateMu.Unlock()
		if converged {
			break
		}
	}

	h.stateMu.Lock()
	st = h.store[42]
	h.stateMu.Unlock()
	if st.RenderedGen != st.Seq || st.RenderFailures != 0 || st.LastRenderError != "" {
		t.Fatalf("rendered generation never converged after flapping titles: %+v", st)
	}
	calls := fake.titleCalls()
	if len(calls) == 0 || palette.ParseTitleParts(calls[len(calls)-1].title).State != "error" {
		t.Fatalf("final title = %+v; want the persisted error icon", calls)
	}
}

// TestFaultCrashBetweenPersistAndTitleRestoresDesiredState models the render
// pipeline's crash point: amx_state lands, the daemon dies before the title
// write. A fresh handler restoring from the persisted user vars must resume
// the desired state and converge the title — the visible state may lag the
// durable state, never run ahead of it.
func TestFaultCrashBetweenPersistAndTitleRestoresDesiredState(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(42, "W:api"), setTitleErr: errors.New("daemon crashed before title")}
	h.SetKittyClient(fake)

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.DesiredIcon = "key"
	st.StateSet = true
	st.LastAction = "permission"
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()

	h.drainRenderQueueWith(fake.windows)

	// The persist landed on the (fake) Kitty window; the title did not.
	persisted := fake.windows[0].Tabs[0].Windows[0].UserVars.AMXState
	if persisted == "" {
		t.Fatal("amx_state was not persisted before the crash point")
	}
	if got := palette.ParseTitleParts(fake.windows[0].Tabs[0].Title).State; got == "key" {
		t.Fatal("title landed despite the injected crash")
	}

	// "Restart": a fresh handler restores from the durable user var.
	h2 := newTestHandler()
	fake2 := &fakeKittyClient{windows: fake.windows}
	h2.SetKittyClient(fake2)
	h2.restoreFromUserVars(fake.windows, time.Now())

	h2.stateMu.Lock()
	st2, ok := h2.store[42]
	h2.stateMu.Unlock()
	if !ok || st2.DesiredIcon != "key" {
		t.Fatalf("restored state = %+v; want desired key icon", st2)
	}

	h2.renderSnapshot(fake.windows)
	calls := fake2.titleCalls()
	if len(calls) == 0 || palette.ParseTitleParts(calls[len(calls)-1].title).State != "key" {
		t.Fatalf("title after restart = %+v; want the restored key icon", calls)
	}
}

// TestFaultDegradedStartupStillAppliesEvents: a daemon whose startup snapshot
// wedges (bounded restore times out, the daemon comes up degraded) must still
// ingest hook events; once Kitty recovers, rendering converges from the
// accumulated state.
func TestFaultDegradedStartupStillAppliesEvents(t *testing.T) {
	h := newTestHandler()
	h.SetReducerConfig(ReducerConfig{GuardDuration: 1})
	release := make(chan struct{})
	var releaseOnce sync.Once
	fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(42, "W:api")}
	fake.beforeListWindows = func() { <-release }
	h.useKittyClient(fake) // install without the priming list
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	if ok, reason := h.restoreStateFromKittyBounded(time.Now(), 30*time.Millisecond); ok || reason == "" {
		t.Fatalf("bounded restore = (%v, %q); want degraded against a wedged Kitty", ok, reason)
	}

	resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "PreToolUse", PayloadJSON: "{}"})
	if !resp.OK {
		t.Fatalf("hook event while degraded = %+v; want applied", resp)
	}
	h.stateMu.Lock()
	icon := h.store[42].DesiredIcon
	h.stateMu.Unlock()
	if icon != "bolt" {
		t.Fatalf("desired icon while degraded = %q; want bolt", icon)
	}

	releaseOnce.Do(func() { close(release) }) // Kitty recovers
	fake.mu.Lock()
	fake.beforeListWindows = nil
	fake.mu.Unlock()
	h.renderSnapshot(fake.windows)
	h.stateMu.Lock()
	st := h.store[42]
	converged := st.RenderedGen == st.Seq && !st.Dirty
	h.stateMu.Unlock()
	if !converged {
		t.Fatalf("render did not converge after startup fault cleared: %+v", st)
	}
}

// TestFaultResponseLossRetryAppliesOnce reproduces the ambiguous-timeout
// fault: the daemon applies a request but the client never sees the response
// (connection closed early). The client's retry with the same request ID must
// not re-apply the event.
func TestFaultResponseLossRetryAppliesOnce(t *testing.T) {
	sock, server := newTestServerWithRef(t)

	req := Request{
		Type:      TypeStateUpdate,
		RequestID: "lost-response-1",
		WindowID:  7,
		Action:    "subagent",
		Event:     "SubagentStart",
	}

	// First delivery: write the request, then close without reading the
	// response — the daemon processes it but the response is lost.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	data := fmt.Sprintf(`{"type":%q,"request_id":%q,"window_id":7,"action":"subagent","event":"SubagentStart"}`+"\n",
		TypeStateUpdate, req.RequestID)
	if _, err := conn.Write([]byte(data)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.Close()

	// Wait until the daemon has actually applied the first delivery.
	c := NewClientWithSocket(sock)
	deadline := time.Now().Add(3 * time.Second)
	for server.Metrics().Accepted < 1 {
		if time.Now().After(deadline) {
			t.Fatal("daemon never processed the response-lost request")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The retry (same request ID) must replay, not re-apply.
	retry, err := c.Send(req)
	if err != nil {
		t.Fatalf("retry send: %v", err)
	}
	if !retry.OK {
		t.Fatalf("retry response = %+v; want replayed OK", retry)
	}

	snap := server.Metrics()
	if snap.Accepted != 1 || snap.Duplicate < 1 {
		t.Fatalf("metrics = %+v; want exactly one accepted apply and the retry deduplicated", snap)
	}
	server.handler.stateMu.Lock()
	count := server.handler.store[7].SubagentCount
	server.handler.stateMu.Unlock()
	if count != 1 {
		t.Fatalf("SubagentCount = %d after response-loss retry; want 1 (applied once)", count)
	}
}

// TestFaultQueuePressureNeverDropsEvents saturates a two-slot ingress queue
// while the consumer is parked. Overflow requests degrade to inline processing
// and bump queue_full, but every submitted event still applies exactly once,
// and the queue-latency counters record the pressure.
func TestFaultQueuePressureNeverDropsEvents(t *testing.T) {
	var applied atomic.Int32
	gate := make(chan struct{})
	var gateOnce sync.Once
	handle := func(_ Request) Response {
		<-gate
		applied.Add(1)
		return Response{OK: true}
	}
	var metrics Metrics
	q := newIngressQueueCap(handle, &metrics, 2)
	t.Cleanup(func() {
		gateOnce.Do(func() { close(gate) })
		q.stop()
	})

	const submitters = 12
	var wg sync.WaitGroup
	wg.Add(submitters)
	for i := 0; i < submitters; i++ {
		go func() {
			defer wg.Done()
			if resp := q.submit(Request{Type: TypeStateUpdate}); !resp.OK {
				t.Errorf("submit under pressure = %+v; want ok", resp)
			}
		}()
	}

	// Wait until the queue is saturated (some submits degraded inline and are
	// parked in handle), then release everything.
	deadline := time.Now().Add(3 * time.Second)
	for metrics.queueFull.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("queue never saturated")
		}
		time.Sleep(time.Millisecond)
	}
	gateOnce.Do(func() { close(gate) })
	wg.Wait()

	if got := applied.Load(); got != submitters {
		t.Fatalf("applied = %d; want %d (queue pressure must not drop events)", got, submitters)
	}
	snap := metrics.Snapshot()
	if snap.QueueFull == 0 {
		t.Fatal("queue_full = 0; want the overflow degradation counted")
	}
	if snap.QueueMaxWaitNs == 0 {
		t.Fatal("queue latency was not recorded under pressure")
	}
}

// TestFaultDroppedASLFinishRepairedByReconciliation drops a run's finish event
// after its turn started through the real hook path. Two consecutive
// successful discoveries that cannot see the run must converge the lease to
// the state the lost finish would have produced, and the repair must be
// recorded as a synthetic watchdog intervention.
func TestFaultDroppedASLFinishRepairedByReconciliation(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)
	h.publishSnapshot(windows, time.Now())
	stubDaemonLoopDiscover(t, nil, nil)

	resp := h.Handle(Request{
		Type:     TypeHookEvent,
		WindowID: 42,
		Event:    "AgentSessionLoopTurnStarted",
		PayloadJSON: fmt.Sprintf(
			`{"source":"agent_session_loop","cwd":%q,"harness":"claude","run_id":"run-lost"}`, projectDir),
	})
	if !resp.OK {
		t.Fatalf("turn started = %+v", resp)
	}
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "bolt" {
		t.Fatalf("live run = looping=%v icon=%q; want ⚡🔁", looping, icon)
	}

	// The finish event is dropped. Reconciliation misses the run twice.
	now := time.Now()
	h.ReconcileASLLeases(now, windows)
	if looping, _ := windowLoopState(t, h, 42); !looping {
		t.Fatal("one miss released the lease; want two-miss protection")
	}
	h.ReconcileASLLeases(now.Add(aslReconcileInterval), windows)

	looping, icon := windowLoopState(t, h, 42)
	if looping || icon != "stop" {
		t.Fatalf("after two misses = looping=%v icon=%q; want the lost finish's 🛑 without 🔁", looping, icon)
	}

	export := h.Handle(Request{Type: TypeEventsExport})
	wd := export.WindowMetadata[42].Watchdog
	if wd.Reconciles == 0 || wd.LastEvent != eventASLReconciled {
		t.Fatalf("watchdog metadata = %+v; want the reconciliation repair recorded", wd)
	}
}

// TestFaultDroppedASLStartSeededByReconciliation is the opposite direction: no
// lifecycle event ever arrived (daemon restarted, hooks dropped), but the
// loop's on-disk state reports an executing turn. Reconciliation must seed the
// lease with the discovered phase.
func TestFaultDroppedASLStartSeededByReconciliation(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	stubDaemonLoopDiscover(t, []agentloop.Candidate{loopCandidate(projectDir, "run-seeded", agentloop.PhaseTurn)}, nil)

	h.ReconcileASLLeases(time.Now(), windows)

	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "bolt" {
		t.Fatalf("seeded = looping=%v icon=%q; want ⚡🔁 from discovery", looping, icon)
	}
}

// TestFaultDiscoveryFailurePreservesLeases: a failing discovery is missing
// evidence, not evidence of absence — leases survive any number of failed
// scans.
func TestFaultDiscoveryFailurePreservesLeases(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	stubDaemonLoopDiscover(t, []agentloop.Candidate{loopCandidate(projectDir, "run-A", agentloop.PhaseTurn)}, nil)
	h.ReconcileASLLeases(time.Now(), windows)

	stubDaemonLoopDiscover(t, nil, errors.New("state dir unreadable"))
	now := time.Now()
	for i := 1; i <= 3; i++ {
		h.ReconcileASLLeases(now.Add(time.Duration(i)*aslReconcileInterval), windows)
	}
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "bolt" {
		t.Fatalf("after failed discoveries = looping=%v icon=%q; want lease preserved", looping, icon)
	}
}

// TestFaultMalformedPersistedStateIsolatedPerWindow restores a snapshot where
// one window carries unparseable amx_state, one carries a v2 payload whose
// facts include corrupted (kind-less and invalid-kind) entries, and one is
// healthy. The malformed window is discarded, the corrupted facts are dropped
// while their valid siblings survive, and the healthy window is untouched.
func TestFaultMalformedPersistedStateIsolatedPerWindow(t *testing.T) {
	h := newTestHandler()
	h.SetReducerConfig(ReducerConfig{GuardDuration: 1})

	valid := NewReducer(ReducerConfig{})
	valid.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "permission", Seq: 3, At: time.Now().Unix()})
	healthy, err := WindowState{LastAction: "permission", Reducer: valid, Effective: valid.Reduce(time.Now().Unix())}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar: %v", err)
	}

	// A v2 payload carrying one valid fact and two corrupted ones.
	corrupted := fmt.Sprintf(
		`{"v":2,"a":"active","f":[{"k":"harness","y":"claude:s2","a":"active","i":"bolt","q":2,"t":%d},{"y":"orphan","a":"stop","i":"stop","q":9},{"k":"invalid_payload","y":"x","a":"stop_failure","i":"error","q":9}],"ei":"bolt","eg":1,"sq":2}`,
		time.Now().Unix())

	windows := []kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			Windows: []kitty.Window{
				{ID: 1, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: `{"v":2,"a":"active","f":[{broken`}},
				{ID: 2, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: corrupted}},
				{ID: 3, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: healthy}},
			},
		}},
	}}

	h.restoreFromUserVars(windows, time.Now())

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if _, ok := h.store[1]; ok {
		t.Fatal("window with unparseable amx_state was restored; want discarded")
	}
	st2, ok := h.store[2]
	if !ok || st2.Reducer == nil {
		t.Fatalf("window with partially corrupted facts missing: %+v", st2)
	}
	facts := st2.Reducer.Facts()
	if len(facts) != 1 || facts[0].Kind != SourceHarness || facts[0].Key != "claude:s2" {
		t.Fatalf("corrupted facts not dropped: %+v; want only the valid harness fact", facts)
	}
	st3, ok := h.store[3]
	if !ok || st3.DesiredIcon != "key" {
		t.Fatalf("healthy window = %+v; want restored key icon", st3)
	}
}
