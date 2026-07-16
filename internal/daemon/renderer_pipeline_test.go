package daemon

// Contract tests for the split render pipeline (renderqueue.go): ordered
// persist-before-title writes, generation revalidation and supersession,
// crash-point behavior (a failed persist withholds the title), slow-window
// isolation from event ingestion, batched activity-only persistence, and the
// render-generation diagnostics.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"golang.org/x/sys/unix"
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

func TestRendererDiagnosticsTrackGenerations(t *testing.T) {
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

	h.stateMu.Lock()
	st = h.store[42]
	if st.QueuedGen != 1 || st.AttemptedGen != 1 || st.PersistedGen != 1 || st.RenderedGen != 1 || st.FailedGen != 0 {
		h.stateMu.Unlock()
		t.Fatalf("generation counters queued=%d attempted=%d persisted=%d rendered=%d failed=%d, want 1/1/1/1/0",
			st.QueuedGen, st.AttemptedGen, st.PersistedGen, st.RenderedGen, st.FailedGen)
	}
	if !st.DirtySince.IsZero() {
		h.stateMu.Unlock()
		t.Fatalf("DirtySince not cleared after successful render")
	}
	if st.LastRenderLatency <= 0 {
		h.stateMu.Unlock()
		t.Fatalf("LastRenderLatency = %v, want > 0", st.LastRenderLatency)
	}
	h.stateMu.Unlock()

	// A failed write records the failed generation and a live dirty age, both
	// visible in the export snapshot.
	fake.mu.Lock()
	fake.setUserVarsErr = errors.New("kitty unavailable")
	fake.mu.Unlock()
	h.stateMu.Lock()
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()
	h.drainRenderQueueWith(fake.windows)

	h.stateMu.Lock()
	h.store[42].DirtySince = time.Now().Add(-5 * time.Second)
	h.syncRendererMetadataLocked(42)
	h.stateMu.Unlock()

	resp := h.handleEventsExport(Request{Type: TypeEventsExport, WindowID: 42})
	meta, ok := resp.WindowMetadata[42]
	if !ok {
		t.Fatalf("export missing window metadata")
	}
	r := meta.Renderer
	if r.QueuedGen != 2 || r.AttemptedGen != 2 || r.FailedGen != 2 || r.PersistedGen != 1 || r.RenderedGen != 1 {
		t.Fatalf("exported generations queued=%d attempted=%d persisted=%d rendered=%d failed=%d, want 2/2/1/1/2",
			r.QueuedGen, r.AttemptedGen, r.PersistedGen, r.RenderedGen, r.FailedGen)
	}
	if r.DirtySince == 0 {
		t.Fatalf("exported DirtySince = 0, want the live dirty stretch start")
	}
	if r.DirtyAgeSeconds < 4 {
		t.Fatalf("exported DirtyAgeSeconds = %d, want >= 4", r.DirtyAgeSeconds)
	}
	if r.LastRenderLatencyMs < 0 {
		t.Fatalf("exported LastRenderLatencyMs = %d, want >= 0", r.LastRenderLatencyMs)
	}
}

func TestRenderGenerationJournalDoesNotBlockStateLock(t *testing.T) {
	paths := diagnostics.PathsForRoot(t.TempDir())
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{LockTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	h := newTestHandler()
	h.setDecisionJournal(journal)

	lockFile := func(path string) *os.File {
		t.Helper()
		file, openErr := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if openErr != nil {
			t.Fatal(openErr)
		}
		if flockErr := unix.Flock(int(file.Fd()), unix.LOCK_EX); flockErr != nil {
			_ = file.Close()
			t.Fatal(flockErr)
		}
		t.Cleanup(func() {
			_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
			_ = file.Close()
		})
		return file
	}
	lockFile(filepath.Join(paths.JournalDir, "events.lock"))
	lockFile(filepath.Join(paths.SpoolDir, "journal-"+fmt.Sprint(os.Getpid())+".jsonl"))

	done := make(chan struct{})
	go func() {
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.DesiredIcon = "bolt"
		st.StateSet = true
		h.markWindowDirtyLocked(42)
		h.stateMu.Unlock()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("queued render diagnostics blocked while stateMu was held")
	}
}

func TestRenderQueuePurgePersistsQueuedStageTrace(t *testing.T) {
	h := newTestHandler()
	journal, err := diagnostics.NewJournal(diagnostics.PathsForRoot(t.TempDir()), diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h.setDecisionJournal(journal)

	const traceID = "trace-render-purge"
	resp := h.Handle(Request{
		Type: TypeStateUpdate, WindowID: 42, Action: "active", Event: "UserPromptSubmit",
		TraceID: traceID, RequestID: "request-render-purge",
	})
	if !resp.OK {
		t.Fatalf("Handle: %+v", resp)
	}

	h.pruneClosedWindows(time.Now(), map[int]bool{})
	if !h.renderQueue.empty() {
		t.Fatal("purged render remained pending")
	}

	records, err := journal.Read(context.Background(), diagnostics.Selector{
		TraceID: traceID, Stages: []string{"daemon.renderer.queued"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("queued-stage records after purge = %+v, want exactly one", records)
	}
	if records[0].Outcome != "pending" || records[0].Reason != "latest_generation" {
		t.Fatalf("queued-stage record after purge = %+v", records[0])
	}
}

func TestRenderWorkerShutdownPersistsQueuedStageTraces(t *testing.T) {
	h := newTestHandler()
	journal, err := diagnostics.NewJournal(diagnostics.PathsForRoot(t.TempDir()), diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h.setDecisionJournal(journal)

	traceIDs := []string{"trace-render-shutdown-a", "trace-render-shutdown-b"}
	for i, traceID := range traceIDs {
		resp := h.Handle(Request{
			Type: TypeStateUpdate, WindowID: 42 + i, Action: "active", Event: "UserPromptSubmit",
			TraceID: traceID, RequestID: "request-" + traceID,
		})
		if !resp.OK {
			t.Fatalf("Handle(%s): %+v", traceID, resp)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.runRenderWorker(ctx)
	if !h.renderQueue.empty() {
		t.Fatal("shutdown-discarded renders remained pending")
	}

	for _, traceID := range traceIDs {
		records, readErr := journal.Read(context.Background(), diagnostics.Selector{
			TraceID: traceID, Stages: []string{"daemon.renderer.queued"},
		})
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(records) != 1 {
			t.Fatalf("queued-stage records for %s after shutdown = %+v, want exactly one", traceID, records)
		}
	}
}

func TestRendererCoalescesBoundedCausalIDsAndPersistsFailure(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	fake.windows = fakeOSWindowsWithTab(42, "W:api")
	journal, err := diagnostics.NewJournal(diagnostics.PathsForRoot(t.TempDir()), diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h.setDecisionJournal(journal)

	for _, req := range []Request{
		{Type: TypeStateUpdate, WindowID: 42, Action: "active", Event: "UserPromptSubmit", TraceID: "trace-a", RequestID: "request-a", SourceEventID: "source-a"},
		{Type: TypeStateUpdate, WindowID: 42, Action: "permission", Event: "PermissionRequest", TraceID: "trace-b", RequestID: "request-b", SourceEventID: "source-b"},
	} {
		if resp := h.Handle(req); !resp.OK {
			t.Fatalf("Handle(%s): %+v", req.TraceID, resp)
		}
	}
	h.drainRenderQueueWith(fake.windows)

	export := h.handleEventsExport(Request{Type: TypeEventsExport, WindowID: 42})
	renderer := export.WindowMetadata[42].Renderer
	if len(renderer.Generations) != 1 {
		t.Fatalf("render generations = %+v; want one coalesced generation", renderer.Generations)
	}
	generation := renderer.Generations[0]
	if generation.Stage != renderStageRendered || generation.Outcome != "success" {
		t.Fatalf("coalesced generation = %+v; want rendered success", generation)
	}
	if len(generation.TraceIDs) != 2 || len(generation.RequestIDs) != 2 ||
		len(generation.TraceIDs) > MaxRenderCausalIDs {
		t.Fatalf("coalesced causal IDs = %+v", generation)
	}
	for _, event := range export.EventsByWindow[42] {
		if event.RenderGeneration != generation.Generation || event.RenderOutcome != renderStageRendered {
			t.Fatalf("event did not reach terminal coalesced render: %+v (generation %+v)", event, generation)
		}
	}
	for _, traceID := range []string{"trace-a", "trace-b"} {
		records, readErr := journal.Read(context.Background(), diagnostics.Selector{
			TraceID: traceID, Stages: []string{"daemon.renderer.rendered"},
		})
		if readErr != nil || len(records) == 0 {
			t.Fatalf("trace %s cannot be followed to rendered: records=%+v err=%v", traceID, records, readErr)
		}
	}
	disableStopFailure := false
	h.notifier.cfg.StopFailure = &disableStopFailure
	fake.mu.Lock()
	fake.setUserVarsErr = errors.New("kitty unavailable")
	fake.mu.Unlock()
	if resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop_failure", Event: "Stop", TraceID: "trace-fail", RequestID: "request-fail"}); !resp.OK {
		t.Fatalf("failure Handle: %+v", resp)
	}
	h.drainRenderQueueWith(fake.windows)
	records, err := journal.Read(context.Background(), diagnostics.Selector{
		TraceID: "trace-fail", Stages: []string{"daemon.renderer.failed"},
	})
	if err != nil || len(records) == 0 {
		t.Fatalf("durable renderer failure records = %+v, err=%v", records, err)
	}
	if records[len(records)-1].Reason != "persist_state" || records[len(records)-1].Outcome != "failed" {
		t.Fatalf("renderer failure record = %+v", records[len(records)-1])
	}
	fake.mu.Lock()
	fake.setUserVarsErr = nil
	fake.mu.Unlock()
	h.renderSnapshot(fake.windows)
	recovered := h.handleEventsExport(Request{Type: TypeEventsExport, WindowID: 42}).WindowMetadata[42].Renderer
	matching := 0
	for _, item := range recovered.Generations {
		if item.Generation == recovered.RenderedGen {
			matching++
			if item.Stage != renderStageRendered || item.Outcome != "success" {
				t.Fatalf("retried generation did not converge: %+v", item)
			}
		}
	}
	if matching != 1 {
		t.Fatalf("retried generation appears %d times: %+v", matching, recovered.Generations)
	}
}

func TestRendererMaintenanceFlushRetriesFailedTitleFromPersistedState(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{setTitleErr: errors.New("kitty unavailable")}
	h.SetKittyClient(fake)
	fake.windows = fakeOSWindowsWithTab(42, "W:api")

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.DesiredIcon = "bolt"
	st.StateSet = true
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()

	// Two failing passes: the state persists once, the title keeps failing.
	h.drainRenderQueueWith(fake.windows)
	h.renderSnapshot(fake.windows)

	h.stateMu.Lock()
	st = h.store[42]
	dirty, failures := st.Dirty, st.RenderFailures
	h.stateMu.Unlock()
	if dirty {
		t.Fatalf("persisted state rolled back on title failure")
	}
	if failures != 2 {
		t.Fatalf("failures = %d, want 2", failures)
	}
	if got := len(fake.userVarsCalls()); got != 1 {
		t.Fatalf("SetUserVars calls = %d, want exactly 1 (title retries must not re-persist)", got)
	}

	// Once Kitty recovers, the flush converges the title from the persisted
	// desired state.
	fake.mu.Lock()
	fake.setTitleErr = nil
	fake.mu.Unlock()
	h.renderSnapshot(fake.windows)

	calls := fake.titleCalls()
	if len(calls) != 3 {
		t.Fatalf("title attempts = %d, want 3 (two failures + one success)", len(calls))
	}
	if icon := palette.ParseTitleParts(calls[2].title).State; icon != "bolt" {
		t.Fatalf("converged icon = %q, want %q", icon, "bolt")
	}
	h.stateMu.Lock()
	st = h.store[42]
	failures, renderedGen := st.RenderFailures, st.RenderedGen
	h.stateMu.Unlock()
	if failures != 0 || renderedGen != 1 {
		t.Fatalf("after convergence failures=%d rendered=%d, want 0 and 1", failures, renderedGen)
	}
}
