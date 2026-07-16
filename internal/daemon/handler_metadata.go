package daemon

import (
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
)

// appendEventLocked records an event into the per-window ring buffer. The
// caller must hold stateMu. Older entries are dropped from the front once the
// slice would exceed maxEventsPerWindow. Every appended event is stamped with
// the daemon's global receipt sequence and, when the producer did not set one,
// a nanosecond receipt time — so exports can totally order receipts across
// windows regardless of which path appended them.
func (h *Handler) appendEventLocked(windowID int, event SessionEvent) SessionEvent {
	st := h.ensureWindowStateLocked(windowID)

	h.eventSeq++
	event.DaemonSeq = h.eventSeq
	if event.eventRef != nil {
		event.eventRef.daemonSeq = event.DaemonSeq
		event.eventRef = nil
	}
	if event.ReceivedAtNs == 0 {
		event.ReceivedAtNs = time.Now().UnixNano()
	}

	meta := h.windowMetadata[windowID]
	meta.Stats.EventCount++
	if meta.Stats.FirstEventAt == 0 {
		meta.Stats.FirstEventAt = event.At
	}
	meta.Stats.LastEventAt = event.At
	if event.Suppressed && event.SuppressionReason != "" {
		if meta.Stats.SuppressionReasons == nil {
			meta.Stats.SuppressionReasons = make(map[string]int)
		}
		meta.Stats.SuppressionReasons[event.SuppressionReason]++
	}
	h.windowMetadata[windowID] = meta

	buf := st.EventLog
	buf = append(buf, event)
	if len(buf) > maxEventsPerWindow {
		buf = buf[len(buf)-maxEventsPerWindow:]
	}
	st.EventLog = buf
	return event
}

func (h *Handler) syncRendererMetadataLocked(windowID int) {
	st, ok := h.windowStateLocked(windowID)
	if !ok {
		return
	}
	meta := h.windowMetadata[windowID]
	meta.Renderer.DesiredIcon = desiredIconForState(st)
	meta.Renderer.StateSet = st.StateSet
	meta.Renderer.Looping = st.Looping
	meta.Renderer.LoopingSet = st.LoopingSet
	meta.Renderer.RenderFailures = st.RenderFailures
	meta.Renderer.LastRenderError = st.LastRenderError
	meta.Renderer.QueuedGen = st.QueuedGen
	meta.Renderer.AttemptedGen = st.AttemptedGen
	meta.Renderer.PersistedGen = st.PersistedGen
	meta.Renderer.RenderedGen = st.RenderedGen
	meta.Renderer.FailedGen = st.FailedGen
	meta.Renderer.DirtySince = 0
	if st.Dirty && !st.DirtySince.IsZero() {
		meta.Renderer.DirtySince = st.DirtySince.Unix()
	}
	meta.Renderer.LastRenderLatencyMs = st.LastRenderLatency.Milliseconds()
	meta.Renderer.Generations = cloneRenderGenerationTraces(st.RenderGenerations)
	meta.Renderer.KittyListenOn = h.kittySockets[windowID]
	if meta.Renderer.KittyListenOn == "" {
		meta.Renderer.KittyListenOn = h.lastKittyListenOn
	}
	h.windowMetadata[windowID] = meta
}

func (h *Handler) touchWindowMetadataLocked(req Request) {
	st := h.ensureWindowStateLocked(req.WindowID)
	meta := h.windowMetadata[req.WindowID]
	if req.SessionTitle != "" {
		meta.Durable.SessionTitle = req.SessionTitle
	}
	if req.Account != "" {
		meta.Durable.Profile = req.Account
	}
	targetHarness := req.targetHarness
	if !req.targetHarnessSet {
		// Backward compatibility for direct state_update clients, which predate
		// the producer/target split and use Harness for both roles.
		targetHarness = req.Harness
	}
	if targetHarness != "" {
		st.LastHarness = harness.Normalize(targetHarness)
		meta.Durable.Harness = st.LastHarness
	}
	meta.Durable.PipeMode = req.PipeMode
	h.windowMetadata[req.WindowID] = meta
}

func (h *Handler) updateDurableSubagentCountLocked(windowID int) {
	st := h.ensureWindowStateLocked(windowID)
	meta := h.windowMetadata[windowID]
	meta.Durable.SubagentCount = st.SubagentCount
	h.windowMetadata[windowID] = meta
}
