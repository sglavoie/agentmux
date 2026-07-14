package daemon

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// blockingListKittyClient parks ListWindows until release is closed, modelling a
// slow or wedged Kitty during startup. GetText/SetTabTitle/SetUserVars satisfy
// the interface but are never exercised by the bounded-restore tests.
type blockingListKittyClient struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingListKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return nil, nil
}
func (b *blockingListKittyClient) GetText(string, string) (string, error)      { return "", nil }
func (b *blockingListKittyClient) SetTabTitle(string, string) error            { return nil }
func (b *blockingListKittyClient) SetUserVars(string, map[string]string) error { return nil }

// errorThenListKittyClient returns an injected error on its FIRST ListWindows,
// modelling a fast startup list failure that returns well before any bounded
// timeout, and serves the persisted windows on every later call. It lets a test
// drive an immediate startup list error followed by a successful maintenance
// snapshot without any blocking or timing dependence.
type errorThenListKittyClient struct {
	windows []kitty.OSWindow
	err     error

	mu    sync.Mutex
	calls int
}

func (c *errorThenListKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	c.mu.Lock()
	c.calls++
	first := c.calls == 1
	c.mu.Unlock()
	if first {
		return nil, c.err
	}
	return c.windows, nil
}
func (c *errorThenListKittyClient) GetText(string, string) (string, error)      { return "", nil }
func (c *errorThenListKittyClient) SetTabTitle(string, string) error            { return nil }
func (c *errorThenListKittyClient) SetUserVars(string, map[string]string) error { return nil }

func TestRestoreStateFromKittyBounded_ReadyWithResponsiveKitty(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(42, "api")}
	h.useKittyClient(fake)

	ready, reason := h.restoreStateFromKittyBounded(time.Now(), initialSnapshotBudget)
	if !ready || reason != "" {
		t.Fatalf("bounded restore = (%v, %q); want ready with no reason", ready, reason)
	}
	h.MarkReady()

	if _, _, cached := h.cachedSnapshot(); !cached {
		t.Fatal("snapshot cache should be primed after a responsive restore")
	}
	resp := h.Handle(Request{Type: TypeHealth})
	if !resp.Ready || resp.DegradedReason != "" {
		t.Fatalf("health = %+v; want ready with no degraded reason", resp)
	}
}

func TestRestoreStateFromKittyBounded_DegradedWhenKittyWedged(t *testing.T) {
	h := newTestHandler()
	client := &blockingListKittyClient{entered: make(chan struct{}, 1), release: make(chan struct{})}
	h.useKittyClient(client)
	t.Cleanup(func() { close(client.release) })

	ready, reason := h.restoreStateFromKittyBounded(time.Now(), 30*time.Millisecond)
	if ready || reason == "" {
		t.Fatalf("bounded restore = (%v, %q); want degraded with a reason", ready, reason)
	}
	h.MarkDegraded(reason)

	// A wedged initial list leaves the cache cold; the daemon still comes up.
	if _, _, cached := h.cachedSnapshot(); cached {
		t.Fatal("snapshot cache should stay cold while the initial list is wedged")
	}
	resp := h.Handle(Request{Type: TypeHealth})
	if resp.Ready || resp.DegradedReason == "" {
		t.Fatalf("health = %+v; want degraded with a reason", resp)
	}

	// Confirm the restore goroutine actually reached the wedged list, proving the
	// timeout (not a missing call) drove the degraded result.
	select {
	case <-client.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("bounded restore never attempted the initial list")
	}
}

func TestMaintenanceTick_ClearsStartupDegradedAfterSuccessfulSnapshot(t *testing.T) {
	h := newTestHandler()
	h.useKittyClient(&fakeKittyClient{windows: fakeOSWindowsWithTab(42, "api")})
	h.MarkDegraded("initial kitty snapshot exceeded 400ms")

	h.maintenanceTick(time.Now())

	resp := h.Handle(Request{Type: TypeHealth})
	if !resp.Ready || resp.DegradedReason != "" {
		t.Fatalf("health = %+v; want recovered readiness after maintenance snapshot", resp)
	}
}

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
func (g *gatedStartupListKittyClient) SetTabTitle(string, string) error       { return nil }
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

// TestMaintenanceTick_MergesPersistedFactsWithHookState pins
// F1-degraded-startup-restore-fact-loss. During a degraded startup a real hook
// creates a window's in-process state (for its own source only) before the
// first successful restore runs. That restore must MERGE the window's disjoint
// still-live persisted facts — here a separate interactive harness fact and a
// live between-turns ASL lease — into the hook state instead of skipping the
// whole window, while the accepted same-source hook fact wins its own slot and
// a late-completing startup restore can neither duplicate nor roll any of it
// back.
func TestMaintenanceTick_MergesPersistedFactsWithHookState(t *testing.T) {
	h := newTestHandler()
	now := time.Now()

	// Persisted multi-source durable state for window 42: an interactive harness
	// "active" fact, a live between-turns ASL lease, and an OLDER legacy
	// window-keyed fact that collides with the source the degraded-window hook
	// will create.
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 1, At: now.Unix()})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 1, At: now.Unix()})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceLegacy, Key: windowKey(42)}, Action: "active", Seq: 1, At: now.Add(-time.Minute).Unix()})
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

	// A real hook lands FIRST, before any restore, creating the window's
	// in-process state for the legacy window source only. The disjoint harness
	// fact and ASL lease live solely in the persisted amx_state.
	if resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 42, Action: "permission"}); !resp.OK {
		t.Fatalf("degraded-window hook = %+v; want OK", resp)
	}
	h.stateMu.Lock()
	hookSt, hookOK := h.store[42]
	h.stateMu.Unlock()
	if !hookOK || hookSt.Reducer == nil {
		t.Fatal("degraded-window hook did not create in-process reducer state")
	}

	// Maintenance recovery restores the persisted amx_state BEFORE marking ready.
	// The window already holds hook state, so the restore MERGES the disjoint
	// persisted facts into it rather than skipping the window.
	h.maintenanceTick(now)

	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
	}
	h.stateMu.Lock()
	merged, mergedOK := h.store[42]
	h.stateMu.Unlock()
	if !mergedOK || merged != hookSt {
		t.Fatal("maintenance merge replaced the in-process state instead of merging into it")
	}

	legacyFact := func(facts []SourceFactSnapshot) *SourceFactSnapshot {
		for i := range facts {
			if facts[i].Kind == SourceLegacy && facts[i].Key == windowKey(42) {
				return &facts[i]
			}
		}
		return nil
	}
	haveHarness := func(facts []SourceFactSnapshot) bool {
		for i := range facts {
			if facts[i].Kind == SourceHarness && facts[i].Key == "claude:s1" {
				return true
			}
		}
		return false
	}

	// The new hook fact wins the same-source conflict: the legacy window source
	// still holds the accepted "permission", NOT the older persisted "active".
	mergedFacts := merged.Reducer.Facts()
	if lf := legacyFact(mergedFacts); lf == nil || lf.Action != "permission" || lf.Icon != "key" {
		t.Fatalf("same-source hook fact lost the conflict: %+v", lf)
	}
	// The disjoint persisted facts survived the merge.
	if !haveHarness(mergedFacts) {
		t.Fatal("disjoint persisted harness fact was not merged")
	}
	if exists, live := merged.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("durable ASL lease not merged: exists=%v live=%v", exists, live)
	}
	if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "key" || !eff.Looping {
		t.Fatalf("merged Reduce = %+v, want key with a live lease", eff)
	}

	// Release the blocked first list. Its late startup restore re-enters the
	// merge path, finds every source already present, and must neither duplicate
	// facts nor roll the hook state back — proving idempotency.
	beforeCount := len(mergedFacts)
	close(client.releaseFirst)
	select {
	case <-client.restoreRendered:
	case <-time.After(2 * time.Second):
		t.Fatal("late startup restore never completed its render")
	}

	h.stateMu.Lock()
	after, afterOK := h.store[42]
	h.stateMu.Unlock()
	if !afterOK || after != merged {
		t.Fatal("late startup restore replaced the merged in-process state")
	}
	afterFacts := after.Reducer.Facts()
	if len(afterFacts) != beforeCount {
		t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, len(afterFacts))
	}
	if lf := legacyFact(afterFacts); lf == nil || lf.Action != "permission" {
		t.Fatalf("late startup restore rolled back the hook fact: %+v", lf)
	}
	if exists, live := after.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("late startup restore dropped the merged ASL lease: exists=%v live=%v", exists, live)
	}
}

// TestMaintenanceTick_MergesAcknowledgedPersistedFactsWithHookState pins
// F1-degraded-startup-merge-drops-acknowledged-facts. When a window is cleared
// with /clear, user_clear acknowledges every visible fact but deliberately
// leaves a live ASL loop lease's LeaseLive set (Reduce still derives 🔁 from it)
// and keeps acknowledged non-terminal facts as durable source-sequence cutoffs.
// If a real hook then creates the window's in-process state during a degraded
// startup (before the first successful restore), the recovery merge must import
// those ACKNOWLEDGED disjoint facts — not skip them — so the live loop marker
// survives and pre-clear duplicate/reordered events stay no-ops. The accepted
// same-source hook fact still wins its own slot, and a late-completing startup
// restore neither duplicates nor rolls any of it back.
func TestMaintenanceTick_MergesAcknowledgedPersistedFactsWithHookState(t *testing.T) {
	h := newTestHandler()
	now := time.Now()

	// Persisted /clear state for window 42: an interactive harness "active" fact
	// and a live between-turns ASL lease, both acknowledged by a user_clear that
	// preserved the loop lease. The harness fact's Seq 5 is the persisted cutoff.
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 5, At: now.Unix()})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 1, At: now.Unix()})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceProbe, Key: "user"}, Action: "user_clear", Seq: 0, At: now.Unix()})
	if eff := r.Reduce(now.Unix()); eff.Icon != "" || !eff.Looping {
		t.Fatalf("persisted /clear Reduce = %+v; want icon-less with a live lease", eff)
	}
	encoded, err := WindowState{
		LastAction: "clear",
		Reducer:    r,
		Effective:  r.Reduce(now.Unix()),
		ShadowSeq:  5,
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

	// A real hook lands FIRST, before any restore, creating the window's
	// in-process state for the legacy window source only. The acknowledged
	// harness fact and live ASL lease live solely in the persisted amx_state.
	if resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 42, Action: "permission"}); !resp.OK {
		t.Fatalf("degraded-window hook = %+v; want OK", resp)
	}
	h.stateMu.Lock()
	hookSt, hookOK := h.store[42]
	h.stateMu.Unlock()
	if !hookOK || hookSt.Reducer == nil {
		t.Fatal("degraded-window hook did not create in-process reducer state")
	}

	// Maintenance recovery merges the ACKNOWLEDGED persisted facts into the hook
	// state before marking ready.
	h.maintenanceTick(now)

	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
	}
	h.stateMu.Lock()
	merged, mergedOK := h.store[42]
	h.stateMu.Unlock()
	if !mergedOK || merged != hookSt {
		t.Fatal("maintenance merge replaced the in-process state instead of merging into it")
	}

	aslFact := func(facts []SourceFactSnapshot) *SourceFactSnapshot {
		for i := range facts {
			if facts[i].Kind == SourceASL && facts[i].Key == "run-1" {
				return &facts[i]
			}
		}
		return nil
	}
	harnessFact := func(facts []SourceFactSnapshot) *SourceFactSnapshot {
		for i := range facts {
			if facts[i].Kind == SourceHarness && facts[i].Key == "claude:s1" {
				return &facts[i]
			}
		}
		return nil
	}

	// The acknowledged live ASL lease was merged: it stays icon-less and
	// acknowledged, but keeps LeaseLive and drives the reduced 🔁.
	if af := aslFact(merged.Reducer.Facts()); af == nil || !af.Acked || !af.LeaseLive || af.Icon != "" {
		t.Fatalf("acknowledged ASL lease not merged intact: %+v", af)
	}
	if exists, live := merged.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("durable acknowledged ASL lease not merged: exists=%v live=%v", exists, live)
	}
	// The acknowledged non-terminal harness fact was merged and keeps its Seq 5
	// cutoff.
	if hf := harnessFact(merged.Reducer.Facts()); hf == nil || !hf.Acked || hf.Seq != 5 {
		t.Fatalf("acknowledged harness cutoff fact not merged intact: %+v", hf)
	}
	// The hook fact renders 🔑 while the acked ASL lease supplies 🔁.
	if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "key" || !eff.Looping {
		t.Fatalf("merged Reduce = %+v, want key with a live lease", eff)
	}

	// The retained acknowledged cutoff keeps a pre-clear duplicate AND a reordered
	// event no-ops for the harness source.
	if dec := merged.Reducer.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 5, At: now.Unix()}); dec != DecisionDuplicate {
		t.Fatalf("pre-clear replay decision = %s, want duplicate (cutoff lost)", dec)
	}
	if dec := merged.Reducer.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 3, At: now.Unix()}); dec != DecisionReordered {
		t.Fatalf("pre-clear reorder decision = %s, want reordered (cutoff lost)", dec)
	}

	// Release the blocked first list. Its late startup restore re-enters the merge
	// path, finds every source already present, and must neither duplicate facts
	// nor roll the merged state back — proving idempotency across recovery paths.
	beforeCount := len(merged.Reducer.Facts())
	close(client.releaseFirst)
	select {
	case <-client.restoreRendered:
	case <-time.After(2 * time.Second):
		t.Fatal("late startup restore never completed its render")
	}

	h.stateMu.Lock()
	after, afterOK := h.store[42]
	h.stateMu.Unlock()
	if !afterOK || after != merged {
		t.Fatal("late startup restore replaced the merged in-process state")
	}
	if got := len(after.Reducer.Facts()); got != beforeCount {
		t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
	}
	if af := aslFact(after.Reducer.Facts()); af == nil || !af.Acked || !af.LeaseLive {
		t.Fatalf("late startup restore dropped the acknowledged ASL lease: %+v", af)
	}
	if exists, live := after.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("late startup restore dropped the merged ASL lease: exists=%v live=%v", exists, live)
	}
	if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "key" || !eff.Looping {
		t.Fatalf("post-late-restore Reduce = %+v, want key with a live lease", eff)
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

// encodeMergeWindow builds the single-window OSWindow list a degraded-startup
// merge test serves from its gated Kitty client: window 42 (Kitty ID) carries
// r's durable facts encoded as amx_state.
func encodeMergeWindow(t *testing.T, r *Reducer, shadowSeq uint64, now time.Time) []kitty.OSWindow {
	t.Helper()
	encoded, err := WindowState{Reducer: r, Effective: r.Reduce(now.Unix()), ShadowSeq: shadowSeq}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	return []kitty.OSWindow{{
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
}

// bringUpDegradedOnGatedClient wires a gatedStartupListKittyClient that blocks
// the first (startup) list, comes up degraded through the bounded restore, and
// waits until the restore goroutine has parked on the blocked list — the exact
// window in which a real hook lands before any persisted-fact restore.
func bringUpDegradedOnGatedClient(t *testing.T, h *Handler, windows []kitty.OSWindow, now time.Time) *gatedStartupListKittyClient {
	t.Helper()
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
	return client
}

// seedInProcessReducerTransition models the degraded-startup hook: it creates the
// window's in-process reducer (as a real hook would, for its own source only)
// and folds `ev` — a user_clear, a prompt, or an interactive terminal — through
// Apply so the reducer records the transition's supersession cutoff before the
// first persisted-fact restore runs.
func seedInProcessReducerTransition(t *testing.T, h *Handler, windowID int, ev ReducerEvent) *WindowState {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.ensureWindowStateLocked(windowID)
	if st.Reducer == nil {
		st.Reducer = NewReducer(h.reducerCfg)
	}
	st.Reducer.Apply(ev)
	st.StateSet = true
	return st
}

// mergedFactBySource returns the merged snapshot for a source (nil when absent),
// so a test can assert acknowledgement and presence after a merge.
func mergedFactBySource(facts []SourceFactSnapshot, id SourceID) *SourceFactSnapshot {
	for i := range facts {
		if facts[i].Source() == id {
			return &facts[i]
		}
	}
	return nil
}

// waitLateStartupRestore releases the blocked startup list and waits for the
// late-completing restore's render, so a test can assert the merge is idempotent
// across the maintenance-recovery and late-startup restore paths.
func waitLateStartupRestore(t *testing.T, client *gatedStartupListKittyClient) {
	t.Helper()
	close(client.releaseFirst)
	select {
	case <-client.restoreRendered:
	case <-time.After(2 * time.Second):
		t.Fatal("late startup restore never completed its render")
	}
}

// TestMaintenanceTick_MergeHonorsPreRestoreUserClear pins the user_clear leg of
// F2-degraded-startup-merge-bypasses-window-supersession. A /clear (user_clear)
// accepted in-process before the first persisted-fact restore acknowledges every
// fact then on screen but leaves no fact a later import could collide with. The
// recovery merge must therefore acknowledge the older persisted facts it imports
// (an interactive bolt and a terminal stop) so the tab cannot regain an icon,
// while a genuinely live ASL loop lease is preserved — a live loop, not a
// resurrected one.
func TestMaintenanceTick_MergeHonorsPreRestoreUserClear(t *testing.T) {
	h := newTestHandler()
	now := time.Now()
	old := now.Add(-time.Minute).Unix()

	// Persisted durable state for window 42, all OLDER than the clear: an
	// interactive harness bolt, a terminal probe stop, and a live between-turns
	// ASL lease.
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 1, At: old})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceProbe, Key: "screen"}, Action: "stop", Seq: 1, At: old})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 1, At: old})
	windows := encodeMergeWindow(t, r, 1, now)

	client := bringUpDegradedOnGatedClient(t, h, windows, now)

	// A /clear lands FIRST, before any restore. user_clear creates no fact; it
	// records the clear cutoff on the (otherwise empty) in-process reducer.
	seedInProcessReducerTransition(t, h, 42, ReducerEvent{
		Source: userActionSource(), Action: "user_clear", Seq: 5, At: now.Unix(),
	})

	// Maintenance recovery merges the disjoint persisted facts into the cleared
	// window before marking ready.
	h.maintenanceTick(now)
	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
	}
	h.stateMu.Lock()
	merged, mergedOK := h.store[42]
	h.stateMu.Unlock()
	if !mergedOK || merged.Reducer == nil {
		t.Fatal("maintenance merge did not populate the window")
	}

	// The clear cutoff acknowledged the merged pre-clear facts: no icon returns.
	if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "" {
		t.Fatalf("merged Reduce = %+v; want icon-less (a merged pre-clear fact regained an icon)", eff)
	}
	// The merged interactive bolt is present but acknowledged.
	if bolt := mergedFactBySource(merged.Reducer.Facts(), SourceID{Kind: SourceHarness, Key: "claude:s1"}); bolt == nil || !bolt.Acked {
		t.Fatalf("merged pre-clear interactive bolt not acknowledged: %+v", bolt)
	}
	// The merged terminal stop was acknowledged then compacted away, exactly as a
	// live user_clear + Compact retires an acknowledged terminal tombstone.
	if stop := mergedFactBySource(merged.Reducer.Facts(), SourceID{Kind: SourceProbe, Key: "screen"}); stop != nil {
		t.Fatalf("acknowledged terminal stop survived Compact: %+v", stop)
	}
	// The live ASL lease is preserved: user_clear leaves LeaseLive set, so the
	// loop the tab keeps is a genuinely live one, not a resurrected marker.
	if exists, live := merged.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("live ASL lease not preserved through the clear merge: exists=%v live=%v", exists, live)
	}
	if eff := merged.Reducer.Reduce(now.Unix()); !eff.Looping {
		t.Fatalf("merged Reduce = %+v; want the live lease's loop preserved", eff)
	}

	// The late startup restore re-enters the merge and must not resurrect an icon.
	beforeCount := len(merged.Reducer.Facts())
	waitLateStartupRestore(t, client)
	h.stateMu.Lock()
	after := h.store[42]
	h.stateMu.Unlock()
	if got := len(after.Reducer.Facts()); got != beforeCount {
		t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
	}
	if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "" {
		t.Fatalf("late startup restore regained an icon: %+v", eff)
	}
}

// TestMaintenanceTick_MergeHonorsPreRestorePromptSupersession pins the prompt leg
// of F2-degraded-startup-merge-bypasses-window-supersession. A direct user prompt
// accepted in-process before the first restore acknowledges stuck terminal facts
// on every other source and retires older interactive active facts. The recovery
// merge must apply that supersession to the older persisted facts it imports so
// the window ends on the prompt's own ⚡ — while a concurrent pipe-mode child and
// a live ASL lease are preserved.
func TestMaintenanceTick_MergeHonorsPreRestorePromptSupersession(t *testing.T) {
	h := newTestHandler()
	now := time.Now()
	old := now.Add(-time.Minute).Unix()

	// Persisted durable state for window 42, all OLDER than the prompt: a terminal
	// stop on a different session, a stale interactive bolt on a third session, a
	// concurrent pipe-mode `claude -p` child, and a live between-turns ASL lease.
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s0"}, Action: "stop", Seq: 1, At: old})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s2"}, Action: "active", Seq: 1, At: old})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:p1"}, Action: "active", Seq: 1, At: old, PipeMode: true})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 1, At: old})
	windows := encodeMergeWindow(t, r, 1, now)

	client := bringUpDegradedOnGatedClient(t, h, windows, now)

	// A prompt lands FIRST, before any restore, on a fresh interactive session.
	seedInProcessReducerTransition(t, h, 42, ReducerEvent{
		Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "active",
		Prompt: true, SessionID: "s1", Seq: 5, At: now.Unix(),
	})

	h.maintenanceTick(now)
	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
	}
	h.stateMu.Lock()
	merged := h.store[42]
	h.stateMu.Unlock()
	if merged == nil || merged.Reducer == nil {
		t.Fatal("maintenance merge did not populate the window")
	}
	facts := merged.Reducer.Facts()

	// The prompt's own ⚡ wins: the older terminal stop was acknowledged (and
	// compacted away) and the stale interactive bolt was retired.
	if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "bolt" {
		t.Fatalf("merged Reduce = %+v; want the prompt's bolt", eff)
	}
	if stop := mergedFactBySource(facts, SourceID{Kind: SourceHarness, Key: "claude:s0"}); stop != nil {
		t.Fatalf("prompt did not acknowledge the older terminal stop before merge: %+v", stop)
	}
	if stale := mergedFactBySource(facts, SourceID{Kind: SourceHarness, Key: "claude:s2"}); stale == nil || !stale.Acked {
		t.Fatalf("stale interactive bolt not retired by the pre-restore prompt: %+v", stale)
	}
	// Concurrent protected state survives: the pipe-mode child keeps its bolt and
	// the live ASL lease keeps looping.
	if pipe := mergedFactBySource(facts, SourceID{Kind: SourceHarness, Key: "claude:p1"}); pipe == nil || pipe.Acked || !pipe.PipeMode {
		t.Fatalf("concurrent pipe-mode child was wrongly retired: %+v", pipe)
	}
	if exists, live := merged.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("live ASL lease not preserved through the prompt merge: exists=%v live=%v", exists, live)
	}
	if eff := merged.Reducer.Reduce(now.Unix()); !eff.Looping {
		t.Fatalf("merged Reduce = %+v; want the live lease's loop preserved", eff)
	}

	beforeCount := len(facts)
	waitLateStartupRestore(t, client)
	h.stateMu.Lock()
	after := h.store[42]
	h.stateMu.Unlock()
	if got := len(after.Reducer.Facts()); got != beforeCount {
		t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
	}
	if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "bolt" || !eff.Looping {
		t.Fatalf("late startup restore diverged: %+v; want bolt with a live lease", eff)
	}
}

// TestMaintenanceTick_MergeHonorsPreRestoreTerminalRetirement pins the terminal
// leg of F2-degraded-startup-merge-bypasses-window-supersession. A native
// interactive terminal (stop) accepted in-process before the first restore
// retires older interactive active facts so its 🛑 is not masked. The recovery
// merge must apply that retirement to the stale interactive bolt it imports so
// the terminal result surfaces (bolt outranks stop) rather than being masked.
func TestMaintenanceTick_MergeHonorsPreRestoreTerminalRetirement(t *testing.T) {
	h := newTestHandler()
	now := time.Now()
	old := now.Add(-time.Minute).Unix()

	// Persisted durable state for window 42: a stale interactive bolt on an older
	// session that, without retirement, would mask the current stop.
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s2"}, Action: "active", Seq: 1, At: old})
	windows := encodeMergeWindow(t, r, 1, now)

	client := bringUpDegradedOnGatedClient(t, h, windows, now)

	// A native interactive terminal lands FIRST, before any restore.
	seedInProcessReducerTransition(t, h, 42, ReducerEvent{
		Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "stop",
		SessionID: "s1", Seq: 5, At: now.Unix(),
	})

	h.maintenanceTick(now)
	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
	}
	h.stateMu.Lock()
	merged := h.store[42]
	h.stateMu.Unlock()
	if merged == nil || merged.Reducer == nil {
		t.Fatal("maintenance merge did not populate the window")
	}

	// The stale bolt was retired so the current session's stop surfaces.
	if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "stop" {
		t.Fatalf("merged Reduce = %+v; want stop (a merged stale interactive bolt masked the terminal)", eff)
	}
	if stale := mergedFactBySource(merged.Reducer.Facts(), SourceID{Kind: SourceHarness, Key: "claude:s2"}); stale == nil || !stale.Acked {
		t.Fatalf("stale interactive bolt not retired by the pre-restore terminal: %+v", stale)
	}

	beforeCount := len(merged.Reducer.Facts())
	waitLateStartupRestore(t, client)
	h.stateMu.Lock()
	after := h.store[42]
	h.stateMu.Unlock()
	if got := len(after.Reducer.Facts()); got != beforeCount {
		t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
	}
	if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "stop" {
		t.Fatalf("late startup restore stopped surfacing the terminal: %+v", eff)
	}
}

// TestMaintenanceTick_MergeHonorsSameTimestampHigherPersistedSequence pins
// F2-degraded-merge-cross-source-sequence-tie. During degraded startup the
// fresh in-process reducer starts at low per-source sequences while the older
// persisted generation carries arbitrarily high unrelated ones. When the
// pre-restore transition and an older persisted fact share the same Unix
// second, the supersession replay must not read the persisted fact's higher
// cross-source sequence as "newer": the clear, prompt, and terminal cutoffs
// must still supersede it, while genuinely concurrent pipe-mode and live ASL
// lease contributions stay untouched and the late startup restore stays
// idempotent.
func TestMaintenanceTick_MergeHonorsSameTimestampHigherPersistedSequence(t *testing.T) {
	t.Run("clear", func(t *testing.T) {
		h := newTestHandler()
		now := time.Now()
		at := now.Unix()

		// Persisted durable state for window 42, SAME second as the clear but with
		// far higher per-source sequences: an interactive harness bolt, a terminal
		// probe stop, and a live between-turns ASL lease.
		r := NewReducer(ReducerConfig{})
		r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 500, At: at})
		r.Apply(ReducerEvent{Source: SourceID{Kind: SourceProbe, Key: "screen"}, Action: "stop", Seq: 600, At: at})
		r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 700, At: at})
		windows := encodeMergeWindow(t, r, 1, now)

		client := bringUpDegradedOnGatedClient(t, h, windows, now)

		// The /clear lands FIRST on the fresh reducer, at a LOW sequence — the
		// new in-process generation has just started counting.
		seedInProcessReducerTransition(t, h, 42, ReducerEvent{
			Source: userActionSource(), Action: "user_clear", Seq: 1, At: at,
		})

		h.maintenanceTick(now)
		if r2, _ := h.Readiness(); !r2 {
			t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
		}
		h.stateMu.Lock()
		merged := h.store[42]
		h.stateMu.Unlock()
		if merged == nil || merged.Reducer == nil {
			t.Fatal("maintenance merge did not populate the window")
		}

		// The clear supersedes the same-second persisted facts despite their
		// numerically higher unrelated sequences: no icon returns.
		if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "" {
			t.Fatalf("merged Reduce = %+v; want icon-less (a same-second higher-seq persisted fact escaped the clear)", eff)
		}
		if bolt := mergedFactBySource(merged.Reducer.Facts(), SourceID{Kind: SourceHarness, Key: "claude:s1"}); bolt == nil || !bolt.Acked {
			t.Fatalf("same-second higher-seq interactive bolt not acknowledged by the clear: %+v", bolt)
		}
		if stop := mergedFactBySource(merged.Reducer.Facts(), SourceID{Kind: SourceProbe, Key: "screen"}); stop != nil {
			t.Fatalf("acknowledged terminal stop survived Compact: %+v", stop)
		}
		// The live ASL lease is preserved: user_clear leaves LeaseLive set.
		if exists, live := merged.Reducer.LeaseState("run-1"); !exists || !live {
			t.Fatalf("live ASL lease not preserved through the clear merge: exists=%v live=%v", exists, live)
		}
		if eff := merged.Reducer.Reduce(now.Unix()); !eff.Looping {
			t.Fatalf("merged Reduce = %+v; want the live lease's loop preserved", eff)
		}

		beforeCount := len(merged.Reducer.Facts())
		waitLateStartupRestore(t, client)
		h.stateMu.Lock()
		after := h.store[42]
		h.stateMu.Unlock()
		if got := len(after.Reducer.Facts()); got != beforeCount {
			t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
		}
		if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "" {
			t.Fatalf("late startup restore regained an icon: %+v", eff)
		}
	})

	t.Run("prompt", func(t *testing.T) {
		h := newTestHandler()
		now := time.Now()
		at := now.Unix()

		// Persisted durable state for window 42, SAME second as the prompt but with
		// far higher per-source sequences: a terminal stop, a stale interactive
		// bolt, a concurrent pipe-mode child, and a live between-turns ASL lease.
		r := NewReducer(ReducerConfig{})
		r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s0"}, Action: "stop", Seq: 500, At: at})
		r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s2"}, Action: "active", Seq: 600, At: at})
		r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:p1"}, Action: "active", Seq: 700, At: at, PipeMode: true})
		r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 800, At: at})
		windows := encodeMergeWindow(t, r, 1, now)

		client := bringUpDegradedOnGatedClient(t, h, windows, now)

		// The prompt lands FIRST, on a fresh interactive session at a LOW sequence.
		seedInProcessReducerTransition(t, h, 42, ReducerEvent{
			Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "active",
			Prompt: true, SessionID: "s1", Seq: 1, At: at,
		})

		h.maintenanceTick(now)
		if r2, _ := h.Readiness(); !r2 {
			t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
		}
		h.stateMu.Lock()
		merged := h.store[42]
		h.stateMu.Unlock()
		if merged == nil || merged.Reducer == nil {
			t.Fatal("maintenance merge did not populate the window")
		}
		facts := merged.Reducer.Facts()

		// The prompt's own ⚡ wins despite the persisted facts' higher sequences.
		if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "bolt" {
			t.Fatalf("merged Reduce = %+v; want the prompt's bolt", eff)
		}
		if stop := mergedFactBySource(facts, SourceID{Kind: SourceHarness, Key: "claude:s0"}); stop != nil {
			t.Fatalf("prompt did not acknowledge the same-second higher-seq terminal stop: %+v", stop)
		}
		if stale := mergedFactBySource(facts, SourceID{Kind: SourceHarness, Key: "claude:s2"}); stale == nil || !stale.Acked {
			t.Fatalf("same-second higher-seq stale interactive bolt not retired by the prompt: %+v", stale)
		}
		// Concurrent protected state survives: the pipe-mode child keeps its bolt
		// and the live ASL lease keeps looping.
		if pipe := mergedFactBySource(facts, SourceID{Kind: SourceHarness, Key: "claude:p1"}); pipe == nil || pipe.Acked || !pipe.PipeMode {
			t.Fatalf("concurrent pipe-mode child was wrongly retired: %+v", pipe)
		}
		if exists, live := merged.Reducer.LeaseState("run-1"); !exists || !live {
			t.Fatalf("live ASL lease not preserved through the prompt merge: exists=%v live=%v", exists, live)
		}
		if eff := merged.Reducer.Reduce(now.Unix()); !eff.Looping {
			t.Fatalf("merged Reduce = %+v; want the live lease's loop preserved", eff)
		}

		beforeCount := len(facts)
		waitLateStartupRestore(t, client)
		h.stateMu.Lock()
		after := h.store[42]
		h.stateMu.Unlock()
		if got := len(after.Reducer.Facts()); got != beforeCount {
			t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
		}
		if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "bolt" || !eff.Looping {
			t.Fatalf("late startup restore diverged: %+v; want bolt with a live lease", eff)
		}
	})

	t.Run("terminal", func(t *testing.T) {
		h := newTestHandler()
		now := time.Now()
		at := now.Unix()

		// Persisted durable state for window 42: a stale interactive bolt in the
		// SAME second as the terminal but with a far higher per-source sequence.
		r := NewReducer(ReducerConfig{})
		r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s2"}, Action: "active", Seq: 500, At: at})
		windows := encodeMergeWindow(t, r, 1, now)

		client := bringUpDegradedOnGatedClient(t, h, windows, now)

		// A native interactive terminal lands FIRST, at a LOW sequence.
		seedInProcessReducerTransition(t, h, 42, ReducerEvent{
			Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "stop",
			SessionID: "s1", Seq: 1, At: at,
		})

		h.maintenanceTick(now)
		if r2, _ := h.Readiness(); !r2 {
			t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
		}
		h.stateMu.Lock()
		merged := h.store[42]
		h.stateMu.Unlock()
		if merged == nil || merged.Reducer == nil {
			t.Fatal("maintenance merge did not populate the window")
		}

		// The stale bolt was retired despite its higher unrelated sequence, so the
		// current session's stop surfaces (bolt would otherwise outrank stop).
		if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "stop" {
			t.Fatalf("merged Reduce = %+v; want stop (a same-second higher-seq stale bolt masked the terminal)", eff)
		}
		if stale := mergedFactBySource(merged.Reducer.Facts(), SourceID{Kind: SourceHarness, Key: "claude:s2"}); stale == nil || !stale.Acked {
			t.Fatalf("same-second higher-seq stale interactive bolt not retired by the terminal: %+v", stale)
		}

		beforeCount := len(merged.Reducer.Facts())
		waitLateStartupRestore(t, client)
		h.stateMu.Lock()
		after := h.store[42]
		h.stateMu.Unlock()
		if got := len(after.Reducer.Facts()); got != beforeCount {
			t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
		}
		if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "stop" {
			t.Fatalf("late startup restore stopped surfacing the terminal: %+v", eff)
		}
	})
}
