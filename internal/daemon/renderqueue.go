package daemon

// This file is the daemon's render pipeline: the single worker that performs
// every Kitty write, driven by the latest-generation per-window queue owned by
// internal/daemon/rendering. Separating the writer from the maintenance loop
// means a slow or wedged Kitty write can never delay snapshot publication,
// watchdog sweeps, or — via the serialized ingress consumer — event ingestion;
// acknowledgement already returns before rendering, and nothing on the ingress
// path performs Kitty I/O.
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
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// enqueueRenderLocked queues a window for rendering. The queued-stage trace is
// persisted by the render drainer after stateMu has been released.
func (h *Handler) enqueueRenderLocked(windowID int, st *state.WindowState) {
	if st == nil {
		return
	}
	if st.Seq > st.QueuedGen {
		st.QueuedGen = st.Seq
	}
	trace := h.markRenderStageLocked(windowID, st.Seq, rendering.StageQueued, "pending", "latest_generation", true)
	if h.renderQueue != nil {
		h.renderQueue.Enqueue(windowID, trace)
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
		case <-h.renderQueue.Wake():
		}
		timer := time.NewTimer(rendering.Debounce)
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
	for _, entry := range h.renderQueue.DiscardPending() {
		h.recordRenderGenerationBounded(entry.Trace, entry.WindowID)
	}
}

// drainRenderQueue lists windows once and renders every queued window against
// that snapshot. A list failure leaves the queue pending; the next maintenance
// flush re-signals so the drain retries within a tick.
func (h *Handler) drainRenderQueue() {
	if h.renderQueue.Empty() {
		return
	}
	client, socket := h.kittyListTarget()
	if client == nil {
		return
	}
	capture, err := h.captureKittySnapshot(client, socket)
	if err != nil {
		// Returning before Acquire is what preserves the pending work: nothing
		// leaves the queue, and the next maintenance flush re-signals the wake
		// so this drain retries within a tick. The last successful snapshot,
		// its generation, and its capture time are equally untouched.
		slog.Debug("render worker: list windows failed", "err", err)
		return
	}
	// Freshen the shared snapshot too: the hook path routes against it, and a
	// render-demand list is strictly newer than the last maintenance tick.
	h.publishCapture(capture)
	h.drainRenderQueueWith(capture.windows)
}

// drainRenderQueueWith renders every queued window against the given snapshot.
// Windows re-enqueued mid-drain (superseded generations) are picked up in the
// same loop.
func (h *Handler) drainRenderQueueWith(windows []kitty.OSWindow) {
	for {
		windowID, queuedTrace, ok := h.renderQueue.Acquire()
		if !ok {
			return
		}
		h.recordRenderGenerationBounded(queuedTrace, windowID)
		h.renderQueuedWindow(windowID, windows)
		h.renderQueue.Release(windowID)
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
	attemptTrace := h.markRenderStageLocked(windowID, seq, rendering.StageAttempted, "pending", "candidate_built", true)
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
			failedTrace := h.markRenderStageLocked(windowID, seq, rendering.StageFailed, "failed", "marshal_state", false)
			h.stateMu.Unlock()
			h.recordRenderGeneration(failedTrace, windowID)
			return
		}
	}
	nextTitle := ""
	if tab != nil {
		if owner, ownerOK := h.renderTitleOwnerLocked(tab); ownerOK && owner.ID == windowID {
			if t := rendering.DesiredTabTitle(tab, st); t != tab.Title {
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
		trace := h.markRenderStageLocked(windowID, seq, rendering.StageFailed, "failed", "persist_state", false)
		h.warnRenderFailureLocked(windowID, st, renderErr)
		h.stateMu.Unlock()
		h.recordRenderGeneration(trace, windowID)
		return false
	}
	if seq > st.PersistedGen {
		st.PersistedGen = seq
	}
	trace := h.markRenderStageLocked(windowID, seq, rendering.StagePersisted, "success", "state_persisted", false)
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
		trace := h.markRenderStageLocked(windowID, seq, rendering.StageFailed, "failed", "render_title", false)
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
	trace := h.markRenderStageLocked(windowID, seq, rendering.StageRendered, "success", "render_complete", false)
	h.stateMu.Unlock()
	h.recordRenderGenerationBounded(trace, windowID)
}

func (h *Handler) warnRenderFailureLocked(windowID int, st *state.WindowState, renderErr error) {
	if st.RenderFailures >= rendering.WarnAfterFailures && st.RenderFailures%rendering.WarnAfterFailures == 0 {
		slog.Warn("renderer: kitty write failed",
			"window_id", windowID,
			"failures", st.RenderFailures,
			"err", renderErr,
		)
	}
}
