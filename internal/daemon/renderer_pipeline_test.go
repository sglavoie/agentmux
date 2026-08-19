package daemon

// Contract tests for the split render pipeline (renderqueue.go): ordered
// persist-before-title writes, generation revalidation and supersession,
// crash-point behavior (a failed persist withholds the title), slow-window
// isolation from event ingestion, batched activity-only persistence, and the
// render-generation diagnostics.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestRendererWritesStateBeforeTitle(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	fake.windows = fakeOSWindowsWithTab(42, "W:api")

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.DesiredIcon = "bolt"
	st.StateSet = true
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()

	h.drainRenderQueueWith(fake.windows)

	ops := fake.opsLog()
	if len(ops) != 2 {
		t.Fatalf("kitty write ops = %v, want exactly [uservars, title]", ops)
	}
	if !strings.HasPrefix(ops[0], "uservars ") || !strings.HasPrefix(ops[1], "title ") {
		t.Fatalf("write order = %v, want amx_state persisted before the title", ops)
	}
}

func TestRendererPersistFailureWithholdsTitle(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{setUserVarsErr: errors.New("kitty unavailable")}
	h.SetKittyClient(fake)
	fake.windows = fakeOSWindowsWithTab(42, "W:api")

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.DesiredIcon = "bolt"
	st.StateSet = true
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()

	h.drainRenderQueueWith(fake.windows)

	if calls := fake.titleCalls(); len(calls) != 0 {
		t.Fatalf("title published despite failed persistence: %v", calls)
	}
	h.stateMu.Lock()
	st = h.store[42]
	dirty, failures, failedGen, persistedGen := st.Dirty, st.RenderFailures, st.FailedGen, st.PersistedGen
	h.stateMu.Unlock()
	if !dirty || failures != 1 {
		t.Fatalf("after persist failure dirty=%v failures=%d, want dirty=true failures=1", dirty, failures)
	}
	if failedGen != 1 || persistedGen != 0 {
		t.Fatalf("failedGen=%d persistedGen=%d, want 1 and 0", failedGen, persistedGen)
	}

	// Recovery: the next pass persists first, then publishes the title.
	fake.mu.Lock()
	fake.setUserVarsErr = nil
	fake.mu.Unlock()
	h.renderSnapshot(fake.windows)

	h.stateMu.Lock()
	st = h.store[42]
	dirty, failures = st.Dirty, st.RenderFailures
	persistedGen, renderedGen := st.PersistedGen, st.RenderedGen
	h.stateMu.Unlock()
	if dirty || failures != 0 || persistedGen != 1 || renderedGen != 1 {
		t.Fatalf("after recovery dirty=%v failures=%d persisted=%d rendered=%d, want clean converged gen 1",
			dirty, failures, persistedGen, renderedGen)
	}
	calls := fake.titleCalls()
	if len(calls) != 1 || palette.ParseTitleParts(calls[0].title).State != "bolt" {
		t.Fatalf("title calls after recovery = %v, want one bolt title", calls)
	}
}

func TestRendererSupersededGenerationNeverLandsAfterNewerState(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	fake.windows = fakeOSWindowsWithTab(42, "W:api")

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.LastAction = "active"
	st.DesiredIcon = "bolt"
	st.StateSet = true
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()

	// While generation 1's amx_state write is in flight, a newer state lands.
	var once sync.Once
	fake.beforeSetUserVars = func(string, map[string]string) {
		once.Do(func() {
			h.stateMu.Lock()
			st := h.ensureWindowStateLocked(42)
			st.LastAction = "stop_failure"
			st.DesiredIcon = "error"
			h.markWindowDirtyLocked(42)
			h.stateMu.Unlock()
		})
	}

	h.drainRenderQueueWith(fake.windows)

	// The superseded generation must never publish its title; only the newer
	// state may become visible, and the stale persist must not clear it.
	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("title calls = %d (%v), want exactly one (the newer generation)", len(calls), calls)
	}
	if icon := palette.ParseTitleParts(calls[0].title).State; icon != "error" {
		t.Fatalf("published icon = %q, want the newer generation's %q", icon, "error")
	}
	uv := fake.userVarsCalls()
	if len(uv) != 2 {
		t.Fatalf("SetUserVars calls = %d, want 2 (stale persist + newer persist)", len(uv))
	}
	if !strings.Contains(uv[1].vars["amx_state"], "stop_failure") {
		t.Fatalf("final persisted state %q does not carry the newer action", uv[1].vars["amx_state"])
	}
	h.stateMu.Lock()
	st = h.store[42]
	dirty, persistedGen, renderedGen := st.Dirty, st.PersistedGen, st.RenderedGen
	h.stateMu.Unlock()
	if dirty || persistedGen != 2 || renderedGen != 2 {
		t.Fatalf("after supersession dirty=%v persisted=%d rendered=%d, want clean at gen 2",
			dirty, persistedGen, renderedGen)
	}
}

func TestRendererGenerationRevalidationRequeuesSupersededWindow(t *testing.T) {
	h := newTestHandler()
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.Dirty = true
	st.Seq = 2
	h.stateMu.Unlock()

	if h.renderGenerationCurrent(42, 1) {
		t.Fatalf("superseded generation passed revalidation")
	}
	if h.renderQueue.empty() {
		t.Fatalf("superseded revalidation did not requeue the window")
	}
	if !h.renderGenerationCurrent(42, 2) {
		t.Fatalf("current generation failed revalidation")
	}
}

func TestRendererSlowWindowDoesNotDelayEventIngestion(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{}
	fake.windows = []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:    100,
					Title: "W:slow",
					Windows: []kitty.Window{
						{ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: "codex"}},
					},
				},
				{
					ID:    101,
					Title: "W:fast",
					Windows: []kitty.Window{
						{ID: 43, UserVars: kitty.UserVars{Profile: "work", Harness: "codex"}},
					},
				},
			},
		},
	}

	started := make(chan struct{}, 16)
	release := make(chan struct{})
	fake.beforeSetTitle = func(match, _ string) {
		if match == kitty.MatchTabByWindowID(42) {
			started <- struct{}{}
			<-release // blocks until released; a closed channel passes retries through
		}
	}
	h.SetKittyClient(fake)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.runRenderWorker(ctx)

	// Window 42's visual change reaches the worker, which wedges on the title
	// write.
	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop", Event: "Stop"})
	if !resp.OK {
		t.Fatalf("state update for slow window failed: %v", resp.Error)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("render worker never reached the slow title write")
	}

	// Event ingestion must complete while the Kitty write is wedged.
	done := make(chan Response, 1)
	go func() {
		done <- h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 43, Action: "stop", Event: "Stop"})
	}()
	select {
	case resp := <-done:
		if !resp.OK {
			t.Fatalf("state update during wedged render failed: %v", resp.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("a wedged Kitty write delayed event ingestion")
	}

	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		rendered := false
		for _, call := range fake.titleCalls() {
			if call.match == kitty.MatchTabByWindowID(43) {
				rendered = true
			}
		}
		if rendered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("window enqueued during the wedge never rendered after release")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRendererActivityOnlyEventsBatchPersistence(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	fake.windows = fakeOSWindowsWithTab(42, "W:api")

	// First event surfaces a new icon: a visual change queues immediately.
	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Event: "UserPromptSubmit"})
	if !resp.OK {
		t.Fatalf("first state update failed: %v", resp.Error)
	}
	if h.renderQueue.empty() {
		t.Fatalf("visual change was not queued for immediate render")
	}
	h.renderSnapshot(fake.windows)
	if !h.renderQueue.empty() {
		t.Fatalf("render pass left the queue non-empty")
	}
	persistsAfterFirst := len(fake.userVarsCalls())

	// A follow-up event that keeps the same icon is activity-only bookkeeping:
	// durable state is marked dirty but no immediate render is queued.
	resp = h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Event: "PostToolUse"})
	if !resp.OK {
		t.Fatalf("second state update failed: %v", resp.Error)
	}
	h.stateMu.Lock()
	dirty := h.store[42].Dirty
	h.stateMu.Unlock()
	if !dirty {
		t.Fatalf("activity-only event did not mark durable state dirty")
	}
	if !h.renderQueue.empty() {
		t.Fatalf("activity-only event queued an immediate render")
	}
	if got := len(fake.userVarsCalls()); got != persistsAfterFirst {
		t.Fatalf("activity-only event wrote to kitty immediately (%d persists, want %d)", got, persistsAfterFirst)
	}

	// The maintenance flush batches the pending persistence into the queue.
	h.enqueueSnapshotCandidates(fake.windows)
	if h.renderQueue.empty() {
		t.Fatalf("maintenance flush did not queue the batched persistence")
	}
	h.drainRenderQueueWith(fake.windows)
	h.stateMu.Lock()
	dirty = h.store[42].Dirty
	h.stateMu.Unlock()
	if dirty {
		t.Fatalf("batched persistence did not converge on flush")
	}
	if got := len(fake.userVarsCalls()); got != persistsAfterFirst+1 {
		t.Fatalf("persists after flush = %d, want %d", got, persistsAfterFirst+1)
	}
}
