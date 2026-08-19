package daemon

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// gatedStartupListKittyClient blocks its FIRST ListWindows (the bounded startup
// restore) until releaseFirst is closed, then returns the persisted windows just
// like every later call. SetUserVars closes restoreRendered the first time the
// render tail of the late startup restore persists a window, giving the test a
// deterministic join point for the late-completing restore goroutine. Only the
// late restore's renderSnapshot drains the queue in these tests (maintenance
// enqueues but never drains), so the signal is unambiguous.
type gatedStartupListKittyClient struct {
	windows []kitty.OSWindow

	firstEntered    chan struct{}
	releaseFirst    chan struct{}
	restoreRendered chan struct{}

	mu    sync.Mutex
	calls int
	once  sync.Once
}

func (g *gatedStartupListKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	g.mu.Lock()
	g.calls++
	first := g.calls == 1
	g.mu.Unlock()
	if first {
		select {
		case g.firstEntered <- struct{}{}:
		default:
		}
		<-g.releaseFirst
	}
	return g.windows, nil
}

func (g *gatedStartupListKittyClient) GetText(string, string) (string, error) { return "", nil }

func (g *gatedStartupListKittyClient) SetTabTitle(string, string) error { return nil }

func (g *gatedStartupListKittyClient) SetUserVars(string, map[string]string) error {
	g.once.Do(func() { close(g.restoreRendered) })
	return nil
}

// TestMaintenanceTick_RestoresAmxStateBeforeReadyAndLateStartupCannotRollBackHook
// pins F1-startup-ready-before-restore. It blocks the first (startup) list so the
// bounded restore times out degraded, drives one successful maintenance list that
// carries persisted amx_state, and proves (1) the daemon only reports ready after
// that maintenance restore repopulated the store, and (2) the late-completing
// startup restore cannot roll back a hook that landed during the degraded window.
func TestMaintenanceTick_RestoresAmxStateBeforeReadyAndLateStartupCannotRollBackHook(t *testing.T) {
	h := newTestHandler()
	now := time.Now()

	// Durable state for one managed window: a live harness "active" fact.
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 1, At: now.Unix()})
	encoded, err := WindowState{
		LastAction: "active",
		Reducer:    r,
		Effective:  r.Reduce(now.Unix()),
	}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	windows := []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{{
			ID:    100,
			Title: "api",
			Windows: []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: encoded},
			}},
		}},
	}}

	client := &gatedStartupListKittyClient{
		windows:         windows,
		firstEntered:    make(chan struct{}, 1),
		releaseFirst:    make(chan struct{}),
		restoreRendered: make(chan struct{}),
	}
	h.useKittyClient(client)
	t.Cleanup(func() {
		select {
		case <-client.releaseFirst:
		default:
			close(client.releaseFirst)
		}
	})

	// The bounded startup restore blocks on the first list and comes up degraded.
	ready, reason := h.restoreStateFromKittyBounded(now, 30*time.Millisecond)
	if ready || reason == "" {
		t.Fatalf("bounded restore = (%v, %q); want degraded with a reason", ready, reason)
	}
	h.MarkDegraded(reason)
	select {
	case <-client.firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("bounded restore never parked on the blocked first list")
	}

	// While degraded the store is cold: nothing was restored yet.
	h.stateMu.Lock()
	_, coldOK := h.store[42]
	h.stateMu.Unlock()
	if coldOK {
		t.Fatal("store should be empty while the startup list is still blocked")
	}

	// A maintenance list now succeeds (call #2) and must restore the persisted
	// amx_state BEFORE the daemon advertises ready.
	h.maintenanceTick(now)

	h.stateMu.Lock()
	restored, restoredOK := h.store[42]
	h.stateMu.Unlock()
	if !restoredOK || restored.Reducer == nil {
		t.Fatal("maintenance did not restore managed amx_state before marking ready")
	}
	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready only after the maintenance restore repopulated the store")
	}
	resp := h.Handle(Request{Type: TypeHealth})
	if !resp.Ready || resp.DegradedReason != "" {
		t.Fatalf("health = %+v; want ready with a restored store", resp)
	}

	// A newer hook lands during the degraded window: model an in-process state
	// change the persisted user var does not carry. Capture pointer identity so a
	// rollback (store replacement) is unambiguous.
	h.stateMu.Lock()
	restored.Reducer.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "permission", Seq: 9, At: now.Add(time.Second).Unix()})
	restored.LastAction = "permission"
	restored.DesiredIcon = "key"
	restored.StateSet = true
	restored.Dirty = true
	restored.Seq++
	h.stateMu.Unlock()

	// Release the blocked first list. Its restore completes late and must skip the
	// window that now holds the newer hook state rather than replacing it.
	close(client.releaseFirst)
	select {
	case <-client.restoreRendered:
	case <-time.After(2 * time.Second):
		t.Fatal("late startup restore never completed its render")
	}

	h.stateMu.Lock()
	after, afterOK := h.store[42]
	h.stateMu.Unlock()
	if !afterOK {
		t.Fatal("late startup restore dropped the window entirely")
	}
	if after != restored {
		t.Fatal("late startup restore replaced the in-process state, rolling back the newer hook")
	}
	if after.LastAction != "permission" || desiredIconForState(after) != "key" {
		t.Fatalf("late startup restore rolled back hook state: action=%q desired=%q, want permission/key",
			after.LastAction, desiredIconForState(after))
	}
}

// TestRestoreStateFromKittyBounded_DegradedOnImmediateListError pins
// F2-immediate-startup-list-error-false-ready. A fast ListWindows failure closes
// the bounded wrapper's done channel WELL before the timeout, but it must be
// reported degraded with a non-empty reason — never ready — so the daemon does
// not come up over a cold cache and empty store (which would stop the
// maintenance loop from ever restoring durable amx_state).
func TestRestoreStateFromKittyBounded_DegradedOnImmediateListError(t *testing.T) {
	h := newTestHandler()
	client := &errorThenListKittyClient{err: errors.New("kitty unavailable")}
	h.useKittyClient(client)

	// Use a generous timeout: the immediate list error must drive the degraded
	// result on its own, not the deadline.
	ready, reason := h.restoreStateFromKittyBounded(time.Now(), 2*time.Second)
	if ready || reason == "" {
		t.Fatalf("bounded restore = (%v, %q); want degraded on an immediate list error", ready, reason)
	}
	h.MarkDegraded(reason)

	// The immediate error left the cache cold; the daemon still comes up degraded.
	if _, _, cached := h.cachedSnapshot(); cached {
		t.Fatal("snapshot cache should stay cold after an immediate startup list error")
	}
	resp := h.Handle(Request{Type: TypeHealth})
	if resp.Ready || resp.DegradedReason == "" {
		t.Fatalf("health = %+v; want degraded with a reason", resp)
	}
}

// TestMaintenanceTick_RestoresAfterImmediateStartupListError pins the recovery
// half of F2-immediate-startup-list-error-false-ready. After a fast startup
// ListWindows error comes up degraded, the FIRST successful maintenance snapshot
// must restore the persisted multi-source reducer facts BEFORE MarkReady, and
// health must report recovered readiness only afterward.
func TestMaintenanceTick_RestoresAfterImmediateStartupListError(t *testing.T) {
	h := newTestHandler()
	now := time.Now()

	// Persisted multi-source durable state for window 42: an interactive harness
	// "active" fact plus a live between-turns ASL lease.
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 1, At: now.Unix()})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 1, At: now.Unix()})
	encoded, err := WindowState{
		LastAction: "active",
		Reducer:    r,
		Effective:  r.Reduce(now.Unix()),
		ShadowSeq:  1,
	}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	windows := []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{{
			ID:    100,
			Title: "api",
			Windows: []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: encoded},
			}},
		}},
	}}

	client := &errorThenListKittyClient{windows: windows, err: errors.New("kitty unavailable")}
	h.useKittyClient(client)

	// The bounded startup restore hits an immediate list error and comes up
	// degraded even though the error returned well before the timeout.
	ready, reason := h.restoreStateFromKittyBounded(now, 2*time.Second)
	if ready || reason == "" {
		t.Fatalf("bounded restore = (%v, %q); want degraded on an immediate list error", ready, reason)
	}
	h.MarkDegraded(reason)

	// While degraded the store is cold and the daemon is not ready.
	h.stateMu.Lock()
	_, coldOK := h.store[42]
	h.stateMu.Unlock()
	if coldOK {
		t.Fatal("store should be empty after an immediate startup list error")
	}
	if r0, _ := h.Readiness(); r0 {
		t.Fatal("daemon must not report ready after an immediate startup list error")
	}

	// The first successful maintenance list restores the persisted amx_state
	// BEFORE the daemon advertises ready.
	h.maintenanceTick(now)

	h.stateMu.Lock()
	restored, restoredOK := h.store[42]
	h.stateMu.Unlock()
	if !restoredOK || restored.Reducer == nil {
		t.Fatal("maintenance did not restore managed amx_state before marking ready")
	}
	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready only after the maintenance restore repopulated the store")
	}
	resp := h.Handle(Request{Type: TypeHealth})
	if !resp.Ready || resp.DegradedReason != "" {
		t.Fatalf("health = %+v; want ready with a restored store", resp)
	}

	// The restored multi-source state is intact: both the interactive harness
	// fact and the live ASL lease survived, and Reduce reflects both.
	haveHarness := false
	for _, f := range restored.Reducer.Facts() {
		if f.Kind == SourceHarness && f.Key == "claude:s1" {
			haveHarness = true
		}
	}
	if !haveHarness {
		t.Fatal("restored state lost the interactive harness fact")
	}
	if exists, live := restored.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("restored state lost the live ASL lease: exists=%v live=%v", exists, live)
	}
	if eff := restored.Reducer.Reduce(now.Unix()); eff.Icon != "bolt" || !eff.Looping {
		t.Fatalf("restored Reduce = %+v, want bolt with a live lease", eff)
	}
}

// TestRenderWorkerDoesNotOverwriteUnrestoredFactsAfterDegradedStartup pins
// F2-degraded-render-overwrites-unrestored-facts. After an immediate startup
// list error, a hook can create and queue source-local state before maintenance
// performs the first restore. The render worker's own fresh ListWindows result
// must be merged before SetUserVars so that first write cannot erase disjoint
// durable facts, acknowledgement cutoffs, or a live ASL lease.
func TestRenderWorkerDoesNotOverwriteUnrestoredFactsAfterDegradedStartup(t *testing.T) {
	h := newTestHandler()
	now := time.Now()

	// Persisted state contains an older fact for the hook's legacy window source,
	// plus an acknowledged interactive cutoff and acknowledged live ASL lease.
	// The clear makes the latter two facts invisible but still semantically
	// durable: the harness Seq rejects old replays and LeaseLive supplies 🔁.
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceLegacy, Key: windowKey(42)}, Action: "active", Seq: 9, At: now.Add(-time.Minute).Unix()})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 7, At: now.Add(-time.Minute).Unix()})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 3, At: now.Add(-time.Minute).Unix()})
	r.Apply(ReducerEvent{Source: userActionSource(), Action: "user_clear", At: now.Add(-time.Second).Unix()})
	windows := encodeMergeWindow(t, r, 9, now)

	client := &fakeKittyClient{
		windows: windows,
		listErr: errors.New("kitty unavailable"),
	}
	h.useKittyClient(client)

	// The bounded startup restore fails immediately, leaving the daemon degraded
	// with a cold in-process store. Make subsequent lists succeed so the queued
	// render, not maintenance, is the first consumer of the durable payload.
	ready, reason := h.restoreStateFromKittyBounded(now, 2*time.Second)
	if ready || reason == "" {
		t.Fatalf("bounded restore = (%v, %q); want degraded on an immediate list error", ready, reason)
	}
	h.MarkDegraded(reason)
	client.mu.Lock()
	client.listErr = nil
	client.mu.Unlock()

	// A real hook creates only the legacy window source and queues an immediate
	// render. Its permission fact must win the same-source conflict with the
	// older persisted active fact.
	if resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 42, Action: "permission"}); !resp.OK {
		t.Fatalf("degraded-window hook = %+v; want OK", resp)
	}
	h.stateMu.Lock()
	hookState := h.store[42]
	h.stateMu.Unlock()
	if hookState == nil || hookState.Reducer == nil {
		t.Fatal("degraded-window hook did not create in-process reducer state")
	}

	// Drain before any maintenance tick. The worker's fresh list is now the only
	// opportunity to recover the durable payload before its first SetUserVars.
	h.drainRenderQueue()
	calls := client.userVarsCalls()
	if len(calls) != 1 {
		t.Fatalf("SetUserVars calls = %d, want exactly the first degraded render", len(calls))
	}
	writtenPayload := calls[0].vars["amx_state"]
	if writtenPayload == "" {
		t.Fatal("first degraded render wrote an empty amx_state")
	}
	var written WindowState
	if err := written.UnmarshalUserVar(writtenPayload); err != nil {
		t.Fatalf("decode first degraded render: %v", err)
	}
	if written.Reducer == nil {
		t.Fatal("first degraded render omitted reducer state")
	}

	facts := written.Reducer.Facts()
	legacy := mergedFactBySource(facts, SourceID{Kind: SourceLegacy, Key: windowKey(42)})
	if legacy == nil || legacy.Action != "permission" || legacy.Icon != "key" || legacy.Acked {
		t.Fatalf("same-source hook fact lost authority in first write: %+v", legacy)
	}
	cutoff := mergedFactBySource(facts, SourceID{Kind: SourceHarness, Key: "claude:s1"})
	if cutoff == nil || !cutoff.Acked || cutoff.Seq != 7 {
		t.Fatalf("acknowledged durable cutoff missing from first write: %+v", cutoff)
	}
	lease := mergedFactBySource(facts, SourceID{Kind: SourceASL, Key: "run-1"})
	if lease == nil || !lease.Acked || !lease.LeaseLive || lease.Seq != 3 {
		t.Fatalf("acknowledged live ASL lease missing from first write: %+v", lease)
	}
	if eff := written.Reducer.Reduce(now.Unix()); eff.Icon != "key" || !eff.Looping {
		t.Fatalf("first degraded render Reduce = %+v, want key with a live lease", eff)
	}

	// Maintenance now observes the just-written payload and marks the daemon
	// ready. A repeated tick and a stale late-startup restore of the ORIGINAL
	// payload must be idempotent: neither may replace the hook state, add facts,
	// or roll back the same-source permission.
	beforeCount := len(facts)
	h.maintenanceTick(now.Add(time.Second))
	h.maintenanceTick(now.Add(2 * time.Second))
	h.restoreFromUserVars(windows, now.Add(3*time.Second))
	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready after maintenance observes the merged payload")
	}
	h.stateMu.Lock()
	after := h.store[42]
	h.stateMu.Unlock()
	if after != hookState {
		t.Fatal("maintenance or late startup restore replaced the accepted hook state")
	}
	afterFacts := after.Reducer.Facts()
	if len(afterFacts) != beforeCount {
		t.Fatalf("repeated restore changed fact count %d -> %d", beforeCount, len(afterFacts))
	}
	if got := mergedFactBySource(afterFacts, SourceID{Kind: SourceLegacy, Key: windowKey(42)}); got == nil || got.Action != "permission" {
		t.Fatalf("repeated restore rolled back same-source hook fact: %+v", got)
	}
	if got := mergedFactBySource(afterFacts, SourceID{Kind: SourceHarness, Key: "claude:s1"}); got == nil || !got.Acked || got.Seq != 7 {
		t.Fatalf("repeated restore lost durable cutoff: %+v", got)
	}
	if exists, live := after.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("repeated restore lost live ASL lease: exists=%v live=%v", exists, live)
	}
}
