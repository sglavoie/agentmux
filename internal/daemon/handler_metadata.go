package daemon

import (
	"github.com/sglavoie/agentmux/internal/harness"
)

// appendEventLocked records an event into the per-window ring buffer. The
// caller must hold stateMu. Older entries are dropped from the front once the
// slice would exceed maxEventsPerWindow.
func (h *Handler) appendEventLocked(windowID int, event SessionEvent) {
	st := h.ensureWindowStateLocked(windowID)

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
	if req.Harness != "" {
		st.LastHarness = harness.Normalize(req.Harness)
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
