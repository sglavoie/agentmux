package daemon

// This file is the daemon's render pipeline: a latest-generation per-window
// queue plus the single worker that performs every Kitty write. Separating the
// writer from the maintenance loop means a slow or wedged Kitty write can
// never delay snapshot publication, watchdog sweeps, or — via the serialized
// ingress consumer — event ingestion; acknowledgement already returns before
// rendering, and nothing on the ingress path performs Kitty I/O.
//
// Ordering contract per window:
//   - The queue holds at most one entry per window; rendering always reads the
//     window's CURRENT state, so whatever generation is live when the worker
//     gets there is what lands (latest-generation semantics).
//   - A window is rendered by at most one goroutine at a time (in-flight
//     exclusion), so two writers can never interleave stale content.
//   - The generation is revalidated immediately before every Kitty write; a
//     superseded candidate abandons the pass and requeues.
//   - amx_state is written before the tab title. A failed persistence
//     withholds the title (the visible state may never run ahead of the
//     durable state); a failed title write retries later from the persisted
//     desired state without rewriting amx_state.
//   - Completion bookkeeping never clears dirty state for a superseded
//     generation.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// renderQueue is a latest-generation per-window render queue. pending holds at
// most one entry per window (FIFO order across windows); inflight marks
// windows currently being rendered so concurrent drainers (the worker and a
// synchronous renderSnapshot call) never write to the same window at once.
//
// Lock ordering: Handler.stateMu may be held while taking the queue's mutex
// (enqueueRenderLocked), never the reverse — queue methods never touch
// handler state.
type renderQueue struct {
	mu           sync.Mutex
	pending      map[int]bool
	queuedTraces map[int]protocol.RenderGenerationTrace
	order        []int
	inflight     map[int]bool
	// wake is the coalesced worker signal: capacity one, non-blocking sends.
	wake chan struct{}
}

type queuedRender struct {
	windowID int
	trace    protocol.RenderGenerationTrace
}

func newRenderQueue() *renderQueue {
	return &renderQueue{
		pending:      make(map[int]bool),
		queuedTraces: make(map[int]protocol.RenderGenerationTrace),
		inflight:     make(map[int]bool),
		wake:         make(chan struct{}, 1),
	}
}

// enqueue adds a window to the queue and carries its latest queued-stage trace
// to the worker. Safe to call while holding Handler.stateMu: it performs no
// diagnostic I/O.
func (q *renderQueue) enqueue(windowID int, trace protocol.RenderGenerationTrace) {
	q.mu.Lock()
	if !q.pending[windowID] {
		q.pending[windowID] = true
		q.order = append(q.order, windowID)
	}
	q.queuedTraces[windowID] = cloneRenderGenerationTrace(trace)
	q.mu.Unlock()
	q.signal()
}

func (q *renderQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// acquire pops the oldest pending window that is not already being rendered
// and marks it in flight. ok is false when nothing is available (empty queue
// or every pending window is in flight elsewhere).
func (q *renderQueue) acquire() (windowID int, trace protocol.RenderGenerationTrace, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, id := range q.order {
		if q.inflight[id] {
			continue
		}
		q.order = append(q.order[:i:i], q.order[i+1:]...)
		trace = q.queuedTraces[id]
		delete(q.pending, id)
		delete(q.queuedTraces, id)
		q.inflight[id] = true
		return id, trace, true
	}
	return 0, protocol.RenderGenerationTrace{}, false
}

// release ends a window's in-flight render. If the window was re-enqueued
// while in flight, the pending entry survived and the worker is re-woken so
// the newer generation renders promptly.
func (q *renderQueue) release(windowID int) {
	q.mu.Lock()
	delete(q.inflight, windowID)
	requeued := q.pending[windowID]
	q.mu.Unlock()
	if requeued {
		q.signal()
	}
}

// purge removes and returns a window's pending entry, if any. Closed-window GC
// uses it so a queued render for a dead window is cancelled instead of failing
// its Kitty write, then persists the returned queued-stage trace after this
// queue lock has been released. An in-flight render cannot be cancelled; its
// completion bookkeeping already tolerates the deleted state.
func (q *renderQueue) purge(windowID int) (protocol.RenderGenerationTrace, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending[windowID] {
		trace := cloneRenderGenerationTrace(q.queuedTraces[windowID])
		delete(q.pending, windowID)
		delete(q.queuedTraces, windowID)
		for i, id := range q.order {
			if id == windowID {
				q.order = append(q.order[:i:i], q.order[i+1:]...)
				break
			}
		}
		return trace, true
	}
	return protocol.RenderGenerationTrace{}, false
}

// discardPending atomically transfers every pending queue entry to the caller
// for off-lock queued-stage persistence during worker shutdown. In-flight
// entries have already had their queued stage persisted by acquire and are not
// represented in pending.
func (q *renderQueue) discardPending() []queuedRender {
	q.mu.Lock()
	defer q.mu.Unlock()

	discarded := make([]queuedRender, 0, len(q.pending))
	for _, windowID := range q.order {
		if !q.pending[windowID] {
			continue
		}
		discarded = append(discarded, queuedRender{
			windowID: windowID,
			trace:    cloneRenderGenerationTrace(q.queuedTraces[windowID]),
		})
	}
	q.pending = make(map[int]bool)
	q.queuedTraces = make(map[int]protocol.RenderGenerationTrace)
	q.order = nil
	return discarded
}

func (q *renderQueue) empty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending) == 0
}

// enqueueRenderLocked queues a window for rendering. The queued-stage trace is
// persisted by the render drainer after stateMu has been released.
func (h *Handler) enqueueRenderLocked(windowID int, st *WindowState) {
	if st == nil {
		return
	}
	if st.Seq > st.QueuedGen {
		st.QueuedGen = st.Seq
	}
	trace := h.markRenderStageLocked(windowID, st.Seq, renderStageQueued, "pending", "latest_generation", true)
	if h.renderQueue != nil {
		h.renderQueue.enqueue(windowID, trace)
	}
}

// runRenderWorker is the daemon's single Kitty writer. It waits for a queue
// wake, coalesces the burst behind a short debounce, and drains the queue
// against one fresh Kitty snapshot per drain.
func (h *Handler) runRenderWorker(ctx context.Context) {
	defer h.persistDiscardedRenderQueue()
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.renderQueue.wake:
		}
		timer := time.NewTimer(renderDebounce)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		h.drainRenderQueue()
	}
}

// persistDiscardedRenderQueue preserves the queued-stage causal row for every
// render the worker abandons at shutdown. discardPending releases the queue
// mutex before any bounded journal I/O begins.
func (h *Handler) persistDiscardedRenderQueue() {
	if h.renderQueue == nil {
		return
	}
	for _, entry := range h.renderQueue.discardPending() {
		h.recordRenderGenerationBounded(entry.trace, entry.windowID)
	}
}

// drainRenderQueue lists windows once and renders every queued window against
// that snapshot. A list failure leaves the queue pending; the next maintenance
// flush re-signals so the drain retries within a tick.
func (h *Handler) drainRenderQueue() {
	if h.renderQueue.empty() {
		return
	}
	client := h.kittyClientForList()
	if client == nil {
		return
	}
	reducerReceipts := h.captureSnapshotReducerReceipts()
	windows, err := client.ListWindows()
	if err != nil {
		slog.Debug("render worker: list windows failed", "err", err)
		return
	}
	// Freshen the shared snapshot too: the hook path routes against it, and a
	// render-demand list is strictly newer than the last maintenance tick.
	h.publishSnapshotWithReceipts(windows, reducerReceipts, time.Now())
	h.drainRenderQueueWith(windows)
}

// drainRenderQueueWith renders every queued window against the given snapshot.
// Windows re-enqueued mid-drain (superseded generations) are picked up in the
// same loop.
func (h *Handler) drainRenderQueueWith(windows []kitty.OSWindow) {
	for {
		windowID, queuedTrace, ok := h.renderQueue.acquire()
		if !ok {
			return
		}
		h.recordRenderGenerationBounded(queuedTrace, windowID)
		h.renderQueuedWindow(windowID, windows)
		h.renderQueue.release(windowID)
	}
}

// renderQueuedWindow performs one ordered render pass for a window: persist
// amx_state first, then publish the tab title (only when this window owns the
// tab's title). The generation captured at candidate build is revalidated
// immediately before each Kitty write.
func (h *Handler) renderQueuedWindow(windowID int, windows []kitty.OSWindow) {
	tab, liveWindow, found := kitty.FindTabByWindowID(windows, windowID)
	if !found {
		tab = nil
		liveWindow = nil
	}
	ready, degradedReason := h.Readiness()

	h.stateMu.Lock()
	st, ok := h.windowStateLocked(windowID)
	if !ok {
		h.stateMu.Unlock()
		return
	}
	// A hook accepted after a degraded startup can enqueue this render before
	// maintenance performs the first successful durable restore. The fresh
	// ListWindows snapshot used by this pass still carries the pre-startup
	// amx_state, so fold its disjoint reducer facts into the accepted in-process
	// state before building the payload. Same-source hook facts remain
	// authoritative, while acknowledged cutoffs and live ASL leases survive the
	// first write. mergePersistedFactsLocked advances Seq for a real merge, so
	// the ordinary generation checks below protect the merged candidate too.
	if !ready && degradedReason != "" && liveWindow != nil {
		h.mergePersistedFactsLocked(windowID, st, *liveWindow, time.Now())
	}
	seq := st.Seq
	if seq > st.AttemptedGen {
		st.AttemptedGen = seq
	}
	attemptTrace := h.markRenderStageLocked(windowID, seq, renderStageAttempted, "pending", "candidate_built", true)
	dirty := st.Dirty
	dirtySince := st.DirtySince
	userVar := ""
	if dirty {
		var err error
		userVar, err = st.MarshalUserVar()
		if err != nil {
			if seq > st.FailedGen {
				st.FailedGen = seq
			}
			st.RenderFailures++
			st.LastRenderError = err.Error()
			failedTrace := h.markRenderStageLocked(windowID, seq, renderStageFailed, "failed", "marshal_state", false)
			h.stateMu.Unlock()
			h.recordRenderGeneration(failedTrace, windowID)
			return
		}
	}
	nextTitle := ""
	if tab != nil {
		if owner, ownerOK := h.renderTitleOwnerLocked(tab); ownerOK && owner.ID == windowID {
			if t := desiredTabTitleLocked(tab, st); t != tab.Title {
				nextTitle = t
			}
		}
	}
	h.stateMu.Unlock()
	h.recordRenderGenerationBounded(attemptTrace, windowID)

	if !dirty && nextTitle == "" {
		h.finishTitle(windowID, seq, dirtySince, nil)
		return
	}
	// A window with pending durable state but no live tab in this snapshot
	// keeps its dirty flag; the next maintenance flush retries once Kitty
	// reports the window again.
	if tab == nil {
		return
	}
	client := h.kittyClientForWindow(windowID)
	if client == nil {
		return
	}

	if dirty {
		if !h.renderGenerationCurrent(windowID, seq) {
			return
		}
		err := client.SetUserVars(kitty.MatchWindowByID(windowID), map[string]string{
			"amx_state": userVar,
		})
		if !h.finishPersist(windowID, seq, err) {
			return
		}
	}
	if nextTitle != "" {
		if !h.renderGenerationCurrent(windowID, seq) {
			return
		}
		err := client.SetTabTitle(kitty.MatchTabByWindowID(windowID), nextTitle)
		h.finishTitle(windowID, seq, dirtySince, err)
		return
	}
	// Persisted with no title change: the generation is fully rendered.
	h.finishTitle(windowID, seq, dirtySince, nil)
}

// desiredTabTitleLocked computes the tab title the renderer wants for the
// owning window's state, preserving every foreign segment (base, star, mute)
// from the live title. Callers hold stateMu.
func desiredTabTitleLocked(tab *kitty.Tab, st *WindowState) string {
	parts := palette.ParseTitleParts(tab.Title)
	parts.State = desiredIconForState(st)
	if st.LoopingSet || st.Looping {
		parts.Looping = st.Looping
	}
	return palette.BuildTitleParts(parts)
}

// renderGenerationCurrent revalidates a candidate generation immediately
// before a Kitty write. A superseded candidate is handed straight back to the
// queue so the newer generation renders promptly even when its own mark was
// batched (activity-only).
func (h *Handler) renderGenerationCurrent(windowID int, seq uint64) bool {
	h.stateMu.Lock()
	st, ok := h.windowStateLocked(windowID)
	if !ok {
		h.stateMu.Unlock()
		return false
	}
	if st.Seq == seq {
		h.stateMu.Unlock()
		return true
	}
	h.enqueueRenderLocked(windowID, st)
	h.stateMu.Unlock()
	return false
}

// finishPersist records the outcome of the amx_state write for generation seq
// and reports whether the pass may continue to the title write. Dirty state is
// never cleared for a superseded generation: the payload that landed is
// already stale and the newer generation owns the next pass.
func (h *Handler) finishPersist(windowID int, seq uint64, renderErr error) bool {
	h.stateMu.Lock()
	st, ok := h.windowStateLocked(windowID)
	if !ok {
		h.stateMu.Unlock()
		return false
	}
	if renderErr != nil {
		if seq > st.FailedGen {
			st.FailedGen = seq
		}
		st.RenderFailures++
		st.LastRenderError = renderErr.Error()
		trace := h.markRenderStageLocked(windowID, seq, renderStageFailed, "failed", "persist_state", false)
		h.warnRenderFailureLocked(windowID, st, renderErr)
		h.stateMu.Unlock()
		h.recordRenderGeneration(trace, windowID)
		return false
	}
	if seq > st.PersistedGen {
		st.PersistedGen = seq
	}
	trace := h.markRenderStageLocked(windowID, seq, renderStagePersisted, "success", "state_persisted", false)
	if st.Seq != seq {
		h.enqueueRenderLocked(windowID, st)
		h.stateMu.Unlock()
		h.recordRenderGenerationBounded(trace, windowID)
		return false
	}
	st.Dirty = false
	st.DirtySince = time.Time{}
	h.stateMu.Unlock()
	h.recordRenderGenerationBounded(trace, windowID)
	return true
}

// finishTitle records the outcome of the tab-title write (or the no-op
// completion of a persist-only pass) for generation seq. A failed title write
// leaves the persisted state authoritative: the window retries from it via the
// failure/drift flush without rewriting amx_state.
func (h *Handler) finishTitle(windowID int, seq uint64, dirtySince time.Time, renderErr error) {
	h.stateMu.Lock()
	st, ok := h.windowStateLocked(windowID)
	if !ok {
		h.stateMu.Unlock()
		return
	}
	if renderErr != nil {
		if seq > st.FailedGen {
			st.FailedGen = seq
		}
		st.RenderFailures++
		st.LastRenderError = renderErr.Error()
		trace := h.markRenderStageLocked(windowID, seq, renderStageFailed, "failed", "render_title", false)
		h.warnRenderFailureLocked(windowID, st, renderErr)
		h.stateMu.Unlock()
		h.recordRenderGeneration(trace, windowID)
		return
	}
	if seq > st.RenderedGen {
		st.RenderedGen = seq
	}
	if st.Seq == seq {
		st.RenderFailures = 0
		st.LastRenderError = ""
		if !dirtySince.IsZero() {
			st.LastRenderLatency = time.Since(dirtySince)
		}
	}
	trace := h.markRenderStageLocked(windowID, seq, renderStageRendered, "success", "render_complete", false)
	h.stateMu.Unlock()
	h.recordRenderGenerationBounded(trace, windowID)
}

func (h *Handler) warnRenderFailureLocked(windowID int, st *WindowState, renderErr error) {
	if st.RenderFailures >= renderWarnAfterFailures && st.RenderFailures%renderWarnAfterFailures == 0 {
		slog.Warn("renderer: kitty write failed",
			"window_id", windowID,
			"failures", st.RenderFailures,
			"err", renderErr,
		)
	}
}
