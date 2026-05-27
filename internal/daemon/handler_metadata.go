package daemon

import (
	"github.com/sglavoie/agentmux/internal/harness"
)

// appendEventLocked records an event into the per-window ring buffer. The
// caller must hold stateMu. Older entries are dropped from the front once the
// slice would exceed maxEventsPerWindow.
func (h *Handler) appendEventLocked(windowID int, event SessionEvent) {
	meta := h.windowMetadata[windowID]
	meta.Stats.EventCount++
	if meta.Stats.FirstEventAt == 0 {
		meta.Stats.FirstEventAt = event.At
	}
	meta.Stats.LastEventAt = event.At
	h.windowMetadata[windowID] = meta

	buf := h.eventLog[windowID]
	buf = append(buf, event)
	if len(buf) > maxEventsPerWindow {
		buf = buf[len(buf)-maxEventsPerWindow:]
	}
	h.eventLog[windowID] = buf
}

func (h *Handler) touchWindowMetadataLocked(req Request) {
	meta := h.windowMetadata[req.WindowID]
	if req.SessionTitle != "" {
		meta.Durable.SessionTitle = req.SessionTitle
	}
	if req.Account != "" {
		meta.Durable.Profile = req.Account
	}
	if req.Harness != "" {
		meta.Durable.Harness = harness.Normalize(req.Harness)
	}
	meta.Durable.PipeMode = req.PipeMode
	h.windowMetadata[req.WindowID] = meta
}

func (h *Handler) updateDurableSubagentCountLocked(windowID int) {
	meta := h.windowMetadata[windowID]
	meta.Durable.SubagentCount = h.subagentCount[windowID]
	h.windowMetadata[windowID] = meta
}
