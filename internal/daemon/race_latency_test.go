package daemon

// Race tests under deterministic injected latency. Instead of relying on
// ambient machine load to widen race windows, the fake Kitty client sleeps a
// fixed sub-millisecond delay inside every list/persist/title call, keeping a
// render pass in flight while hook bursts, watchdog sweeps, ASL
// reconciliation, and snapshot flushes run concurrently. Run with -race and
// -count=N; the injected latency makes the interleavings reproducibly wide.
//
// Invariants after the storm settles:
//   - the rendered generation equals the desired generation on every window;
//   - balanced subagent lifecycles leave no residual count;
//   - the visible icon equals the reducer's effective icon (single writer);
//   - every logical request applied exactly once despite concurrent retries.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// raceKittyLatency is the injected per-call Kitty latency. Small enough that
// -count=10 stays fast, large enough that renders reliably overlap ingestion.
const raceKittyLatency = 200 * time.Microsecond

func raceLatencyWindows(windowIDs []int) []kitty.OSWindow {
	out := make([]kitty.OSWindow, 0, len(windowIDs))
	for _, id := range windowIDs {
		out = append(out, kitty.OSWindow{
			ID: id,
			Tabs: []kitty.Tab{{
				ID:    100 + id,
				Title: palette.BuildTitleParts(palette.TitleParts{Base: fmt.Sprintf("proj-%d", id)}),
				Windows: []kitty.Window{{
					ID:       id,
					UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
				}},
			}},
		})
	}
	return out
}

// TestRaceInjectedLatencyHookBurstsConverge floods four windows with
// concurrent hook bursts (balanced subagent lifecycles inside active turns)
// while the render worker, maintenance flushes, stale sweeps, and ASL
// reconciliation all run against a slow Kitty. After the storm and a final
// deterministic flush, every window must have converged: rendered generation
// equals desired generation, subagent counts balanced, and the visible icon
// equal to the reducer's effective icon.
func TestRaceInjectedLatencyHookBurstsConverge(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 0
	t.Cleanup(func() { resetFlowStopDelay = oldDelay })
	stubDaemonLoopDiscover(t, nil, nil)

	h := newTestHandler()
	h.SetStopConfirm(0, 0, false) // pin the immediate-stop path, not the confirmed-idle hold
	h.SetReducerConfig(ReducerConfig{GuardDuration: 1})
	windowIDs := []int{1, 2, 3, 4}
	fake := &fakeKittyClient{windows: raceLatencyWindows(windowIDs)}
	fake.beforeListWindows = func() { time.Sleep(raceKittyLatency) }
	fake.beforeSetUserVars = func(string, map[string]string) { time.Sleep(raceKittyLatency) }
	fake.beforeSetTitle = func(string, string) { time.Sleep(raceKittyLatency) }
	h.SetKittyClient(fake)
	// Always work from listed snapshots (deep copies), mirroring production:
	// the fake mutates its canonical state on writes, and a snapshot handed to
	// routing/publishing must never alias it.
	listSnapshot := func() []kitty.OSWindow {
		windows, err := fake.ListWindows()
		if err != nil {
			t.Fatalf("list windows: %v", err)
		}
		return windows
	}
	h.publishSnapshot(listSnapshot(), time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	var workerWG sync.WaitGroup
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		h.runRenderWorker(ctx)
	}()

	const producers = 8
	const cyclesPerProducer = 12
	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			windowID := windowIDs[p%len(windowIDs)]
			for c := 0; c < cyclesPerProducer; c++ {
				agent := fmt.Sprintf("agent-%d-%d", p, c)
				turn := fmt.Sprintf(`"session_id":"sess-%d","turn_id":"turn-%d-%d"`, windowID, p, c)
				h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: windowID, Event: "UserPromptSubmit", PayloadJSON: "{" + turn + "}"})
				h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: windowID, Event: "SubagentStart",
					PayloadJSON: fmt.Sprintf(`{%s,"agent_id":%q}`, turn, agent)})
				h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: windowID, Event: "SubagentStop",
					PayloadJSON: fmt.Sprintf(`{%s,"agent_id":%q,"stop_reason":"end_turn"}`, turn, agent)})
				h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: windowID, Event: "Stop",
					PayloadJSON: "{" + turn + `,"stop_reason":"end_turn"}`})
			}
		}(p)
	}

	// Concurrent daemon-side actors: maintenance-style flushes, stale sweeps,
	// and ASL reconciliation against an empty discovery.
	stormDone := make(chan struct{})
	var actorsWG sync.WaitGroup
	actorsWG.Add(1)
	go func() {
		defer actorsWG.Done()
		for {
			select {
			case <-stormDone:
				return
			default:
			}
			h.renderSnapshot(listSnapshot())
			h.SweepStale(time.Now())
			h.ReconcileASLLeases(time.Now(), listSnapshot())
		}
	}()

	wg.Wait()
	close(stormDone)
	actorsWG.Wait()
	cancel()
	workerWG.Wait()

	// Deterministic final flushes: retry until every window converges.
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.renderSnapshot(listSnapshot())
		converged := true
		h.stateMu.Lock()
		for _, id := range windowIDs {
			st := h.store[id]
			if st == nil || st.Dirty || st.RenderedGen != st.Seq {
				converged = false
			}
		}
		h.stateMu.Unlock()
		if converged {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("windows never converged after the storm")
		}
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for _, id := range windowIDs {
		st := h.store[id]
		if st.SubagentCount != 0 {
			t.Errorf("window %d: SubagentCount = %d after balanced lifecycles; want 0", id, st.SubagentCount)
		}
		// Single-writer invariant: the visible icon is the reducer's effective
		// icon, and the loop marker carries no phantom lease.
		if st.Reducer != nil {
			eff := st.Reducer.Reduce(time.Now().Unix())
			if st.DesiredIcon != eff.Icon {
				t.Errorf("window %d: DesiredIcon %q != effective icon %q", id, st.DesiredIcon, eff.Icon)
			}
			if st.Looping != eff.Looping {
				t.Errorf("window %d: Looping %v != effective looping %v", id, st.Looping, eff.Looping)
			}
		}
		if st.RenderedGen != st.Seq || st.PersistedGen != st.Seq {
			t.Errorf("window %d: generations rendered=%d persisted=%d desired=%d; want convergence",
				id, st.RenderedGen, st.PersistedGen, st.Seq)
		}
	}
}

// TestRaceConcurrentRetriesEachLogicalRequestAppliesOnce sends many distinct
// logical requests, each delivered by several concurrent retrying clients
// carrying the same request ID, against the real socket server. Every logical
// start/stop pair must apply exactly once: the balanced subagent count is the
// observable, and the metrics must attribute exactly one accept per logical
// request.
func TestRaceConcurrentRetriesEachLogicalRequestAppliesOnce(t *testing.T) {
	sock, server := newTestServerWithRef(t)
	c := NewClientWithSocket(sock)

	const pairs = 10
	const retriesPerRequest = 4
	var wg sync.WaitGroup
	sendRetried := func(req protocol.Request) {
		var inner sync.WaitGroup
		for r := 0; r < retriesPerRequest; r++ {
			inner.Add(1)
			go func() {
				defer inner.Done()
				_, _ = c.Send(req)
			}()
		}
		inner.Wait()
	}
	for i := 0; i < pairs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// A real hook client emits the start before the stop; only the
			// retries of each logical request race each other.
			sendRetried(protocol.Request{
				Type: protocol.TypeStateUpdate, RequestID: fmt.Sprintf("start-%d", i),
				WindowID: 9, Action: "subagent", Event: "SubagentStart",
			})
			sendRetried(protocol.Request{
				Type: protocol.TypeStateUpdate, RequestID: fmt.Sprintf("stop-%d", i),
				WindowID: 9, Action: "active", Event: "SubagentStop",
			})
		}(i)
	}
	wg.Wait()

	server.handler.stateMu.Lock()
	count := server.handler.store[9].SubagentCount
	server.handler.stateMu.Unlock()
	if count != 0 {
		t.Fatalf("SubagentCount = %d after balanced retried pairs; want 0 (each applied once)", count)
	}
	snap := server.Metrics()
	if snap.Accepted != 2*pairs {
		t.Errorf("Accepted = %d; want %d (one apply per logical request)", snap.Accepted, 2*pairs)
	}
	if want := uint64(2 * pairs * (retriesPerRequest - 1)); snap.Duplicate != want {
		t.Errorf("Duplicate = %d; want %d (every retry deduplicated)", snap.Duplicate, want)
	}
}
