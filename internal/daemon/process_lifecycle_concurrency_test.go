package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestProcessLifecycleConcurrentHookInvalidatesObservation(t *testing.T) {
	h := newTestHandler()
	identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5601, StartToken: "Thu Jul 16 10:20:30 2026"}
	applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
	inspector := &fakeHarnessProcessInspector{
		statuses: map[protocol.HarnessProcessIdentity]probes.HarnessProcessInspection{identity: probes.HarnessProcessMissing},
		entered:  make(chan struct{}), release: make(chan struct{}),
	}
	h.SetHarnessProcessInspector(inspector)
	done := make(chan int, 1)
	go func() { done <- h.processLifecycleTick(context.Background(), time.Now()) }()
	select {
	case <-inspector.entered:
	case <-time.After(time.Second):
		t.Fatal("inspector did not start")
	}

	applyLifecycleEvent(t, h, 42, identity, "session", "stop", 2, false)
	close(inspector.release)
	if committed := <-done; committed != 0 {
		t.Fatalf("stale observation committed %d settlements", committed)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[42]
	if st.Effective.Icon != "stop" || len(lifecycleEvents(st)) != 0 {
		t.Fatalf("newer native stop was overwritten: %+v", st)
	}
}

func TestProcessLifecycleLateNativeStopWinsAndSettlementIsIdempotent(t *testing.T) {
	h := newTestHandler()
	identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5701, StartToken: "Thu Jul 16 10:20:30 2026"}
	applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
	inspector := &fakeHarnessProcessInspector{statuses: map[protocol.HarnessProcessIdentity]probes.HarnessProcessInspection{identity: probes.HarnessProcessMissing}}
	h.SetHarnessProcessInspector(inspector)
	if committed := h.processLifecycleTick(context.Background(), time.Now()); committed != 1 {
		t.Fatalf("committed = %d, want 1", committed)
	}
	applyLifecycleEvent(t, h, 42, identity, "session", "stop", 2, false)
	if committed := h.processLifecycleTick(context.Background(), time.Now()); committed != 0 {
		t.Fatalf("second committed = %d, want 0", committed)
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	fact := factSnapshotBySource(h.store[42].Reducer.Facts(), protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:session"})
	if fact == nil || fact.Seq != 2 || fact.Action != "stop" || !fact.Terminated {
		t.Fatalf("native terminal fact = %+v", fact)
	}
	if got := len(lifecycleEvents(h.store[42])); got != 1 {
		t.Fatalf("lifecycle events = %d, want 1", got)
	}
	if inspector.callCount() != 1 {
		t.Fatalf("inspector calls = %d, terminal fact should no longer be a candidate", inspector.callCount())
	}
}

func TestProcessLifecycleRestoredV4StateSettlesOnFirstPass(t *testing.T) {
	identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5801, StartToken: "Thu Jul 16 10:20:30 2026"}
	r := state.NewReducer(zeroCfg)
	r.Apply(processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:restored"}, "active", 1, time.Now().Unix(), "restored", false, identity))
	persisted := state.WindowState{
		Activity: time.Now(), LastAction: "active", LastHarness: "claude", LastAccount: "work",
		LastSessionTitle: "W:api", DesiredIcon: "bolt", StateSet: true,
		Reducer: r, Effective: r.Reduce(time.Now().Unix()), ShadowSeq: 1,
	}
	encoded, err := persisted.MarshalUserVar()
	if err != nil {
		t.Fatal(err)
	}
	h := newTestHandler()
	h.restoreFromUserVars([]kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{{ID: 2, Windows: []kitty.Window{{
		ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: encoded},
	}}}}}}, time.Now())
	h.renderQueue = rendering.NewQueue()
	h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{statuses: map[protocol.HarnessProcessIdentity]probes.HarnessProcessInspection{identity: probes.HarnessProcessMissing}})

	if committed := h.processLifecycleTick(context.Background(), time.Now()); committed != 1 {
		t.Fatalf("committed = %d, want 1", committed)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if h.store[42].Effective.Icon != "stop" || len(lifecycleEvents(h.store[42])) != 1 {
		t.Fatalf("restored state did not reconcile: %+v", h.store[42])
	}
}

func TestProcessLifecycleSlowInspectorDoesNotBlockDaemonWork(t *testing.T) {
	h := newTestHandler()
	identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5901, StartToken: "Thu Jul 16 10:20:30 2026"}
	applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
	windows := []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{{ID: 2, Windows: []kitty.Window{{ID: 42}}}}}}
	h.SetKittyClient(&fakeKittyClient{windows: windows})
	inspector := &fakeHarnessProcessInspector{
		statuses: map[protocol.HarnessProcessIdentity]probes.HarnessProcessInspection{identity: probes.HarnessProcessMissing},
		entered:  make(chan struct{}), release: make(chan struct{}),
	}
	h.SetHarnessProcessInspector(inspector)
	tickDone := make(chan struct{})
	go func() {
		h.processLifecycleTick(context.Background(), time.Now())
		close(tickDone)
	}()
	select {
	case <-inspector.entered:
	case <-time.After(time.Second):
		t.Fatal("inspector did not start")
	}

	workDone := make(chan protocol.Response, 1)
	go func() {
		normalized := protocol.NormalizedEvent{
			Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:session"}, Action: "active", Event: "LifecycleTestEvent",
			Seq: 2, At: time.Now().Unix(), SessionID: "session", HarnessProcess: identity,
		}
		resp := h.handleStateUpdate(protocol.Request{
			Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", Event: "LifecycleTestEvent",
			Harness: "claude", Account: "work", SessionTitle: "W:api", Normalized: &normalized,
		})
		h.watchdogTick(time.Now(), time.Millisecond)
		h.renderSnapshot(windows)
		workDone <- resp
	}()
	select {
	case resp := <-workDone:
		if !resp.OK {
			t.Fatalf("concurrent hook response = %+v", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("slow inspector blocked hook ingestion, watchdog, or render work")
	}
	close(inspector.release)
	select {
	case <-tickDone:
	case <-time.After(time.Second):
		t.Fatal("lifecycle tick did not finish")
	}
}

func TestRunHarnessProcessLifecycleConvergesOnPollingPass(t *testing.T) {
	h := newTestHandler()
	identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5951, StartToken: "Thu Jul 16 10:20:30 2026"}
	applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
	h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{statuses: map[protocol.HarnessProcessIdentity]probes.HarnessProcessInspection{identity: probes.HarnessProcessMissing}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.runHarnessProcessLifecycleWithInterval(ctx, 5*time.Millisecond)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		h.stateMu.Lock()
		settled := h.store[42].Effective.Icon == "stop" && len(lifecycleEvents(h.store[42])) == 1
		h.stateMu.Unlock()
		if settled {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("lifecycle polling did not converge")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lifecycle polling loop did not stop")
	}
}

func TestSetHarnessProcessInspectorWhileLifecycleLoopRuns(t *testing.T) {
	h := newTestHandler()
	identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5971, StartToken: "Thu Jul 16 10:20:30 2026"}
	applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)

	inspectors := []*fakeHarnessProcessInspector{
		{
			statuses: map[protocol.HarnessProcessIdentity]probes.HarnessProcessInspection{identity: probes.HarnessProcessAliveSameInstance},
			entered:  make(chan struct{}),
		},
		{
			statuses: map[protocol.HarnessProcessIdentity]probes.HarnessProcessInspection{identity: probes.HarnessProcessUnknown},
			entered:  make(chan struct{}),
		},
	}
	h.SetHarnessProcessInspector(inspectors[0])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		h.runHarnessProcessLifecycleWithInterval(ctx, time.Millisecond)
		close(done)
	}()

	select {
	case <-inspectors[0].entered:
	case <-time.After(time.Second):
		t.Fatal("lifecycle loop did not inspect with the initial inspector")
	}
	h.SetHarnessProcessInspector(inspectors[1])
	select {
	case <-inspectors[1].entered:
	case <-time.After(time.Second):
		t.Fatal("lifecycle loop did not inspect after live inspector replacement")
	}

	for i := 0; i < 10_000; i++ {
		h.SetHarnessProcessInspector(inspectors[i%len(inspectors)])
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lifecycle polling loop did not stop after live inspector replacement")
	}

	if calls := inspectors[0].callCount(); calls == 0 {
		t.Fatal("lifecycle loop did not use the initial inspector")
	}
	if calls := inspectors[1].callCount(); calls == 0 {
		t.Fatal("lifecycle loop did not use the replacement inspector")
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if got := h.store[42].Effective.Icon; got != "bolt" {
		t.Fatalf("effective icon = %q, want bolt", got)
	}
}

func TestRunHarnessProcessLifecycleCancellationStopsPolling(t *testing.T) {
	h := newTestHandler()
	identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 6001, StartToken: "Thu Jul 16 10:20:30 2026"}
	applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
	inspector := &fakeHarnessProcessInspector{
		statuses: map[protocol.HarnessProcessIdentity]probes.HarnessProcessInspection{identity: probes.HarnessProcessMissing},
		entered:  make(chan struct{}), release: make(chan struct{}),
	}
	h.SetHarnessProcessInspector(inspector)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.runHarnessProcessLifecycleWithInterval(ctx, time.Millisecond)
		close(done)
	}()
	select {
	case <-inspector.entered:
	case <-time.After(time.Second):
		t.Fatal("lifecycle loop did not poll")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lifecycle loop did not stop after cancellation")
	}
}

func factSnapshotBySource(facts []protocol.SourceFactSnapshot, source protocol.SourceID) *protocol.SourceFactSnapshot {
	for i := range facts {
		if facts[i].Source() == source {
			fact := facts[i]
			return &fact
		}
	}
	return nil
}
