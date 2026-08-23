package daemon

// Deterministic fault injection against the render and restore pipeline. Each
// test injects one audited failure mode — wedged or failing Kitty listing,
// flapping title writes, a crash between the ordered persist and title writes,
// degraded startup, malformed persisted state — and asserts the corresponding
// recovery invariant:
//
//   - a wedged Kitty never blocks hook acknowledgement;
//   - the rendered generation eventually equals the desired generation once
//     the fault clears;
//   - the visible state may lag the durable state, never run ahead of it;
//   - malformed persisted state is discarded without harming other windows.
//
// Transport-level faults — lost IPC responses and ingress queue pressure —
// live in fault_injection_ipc_test.go; dropped ASL lifecycle events and their
// reconciliation repairs in fault_injection_asl_test.go.

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
