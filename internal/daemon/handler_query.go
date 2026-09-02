package daemon

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func (h *Handler) handleStateQuery() protocol.Response {
	h.stateMu.Lock()
	now := time.Now().Unix()
	states := make(map[int]protocol.WindowStateSnapshot, len(h.store))
	snapshot := make(map[int]int64, len(h.store))
	for id, st := range h.store {
		state := protocol.WindowStateSnapshot{
			IconState:   desiredIconForState(st),
			Looping:     st.Looping,
			LastAction:  st.LastAction,
			GuardAction: st.GuardAction,
		}
		if identity, ok := st.CurrentConversation(now); ok {
			identityCopy := identity
			state.Conversation = &identityCopy
		}
		if !st.BusySince.IsZero() {
			state.BusySinceAt = st.BusySince.Unix()
		}
		if st.Activity.IsZero() {
			states[id] = state
			continue
		}
		snapshot[id] = st.Activity.Unix()
		state.LastEventAt = st.Activity.Unix()
		states[id] = state
	}
	watchdog := make(map[int]protocol.WatchdogState, len(h.store))
	for id, st := range h.store {
		action := st.LastAction
		if action != "active" && action != "subagent" && action != "compact" {
			continue
		}
		entry := protocol.WatchdogState{LastAction: action}
		if !st.Activity.IsZero() {
			entry.LastEventAt = st.Activity.Unix()
		}
		if d := h.resolveThreshold(st.LastAccount, action); d > 0 {
			entry.ThresholdSec = int(d / time.Second)
		}
		watchdog[id] = entry
	}
	h.stateMu.Unlock()
	return protocol.Response{OK: true, Activity: snapshot, Watchdog: watchdog, States: states}
}

// handleSessionDetail returns the per-window event ring buffer for req.WindowID.
// An unknown window yields a non-nil empty slice rather than nil so the TUI can
// distinguish "no daemon error" from "no events yet" without a nil check.
func (h *Handler) handleSessionDetail(req protocol.Request) protocol.Response {
	h.stateMu.Lock()
	var src []protocol.SessionEvent
	if st, ok := h.windowStateLocked(req.WindowID); ok {
		src = st.EventLog
	}
	events := make([]protocol.SessionEvent, len(src))
	for i := range src {
		events[i] = h.enrichNotificationEvent(req.WindowID, cloneSessionEvent(src[i]))
	}
	h.stateMu.Unlock()
	return protocol.Response{OK: true, Events: events}
}

// handleEventsExport returns a deep copy of the requested window's event ring
// buffer plus the durable per-window export snapshot. When req.WindowID is
// zero, every exported window is returned. Mirrors handleSessionDetail's
// locking and defensive copies so callers cannot mutate the daemon's internal
// state.
func (h *Handler) handleEventsExport(req protocol.Request) protocol.Response {
	h.stateMu.Lock()
	out := make(map[int][]protocol.SessionEvent)
	meta := make(map[int]protocol.WindowDebugMetadata)
	if req.WindowID != 0 {
		if st, ok := h.windowStateLocked(req.WindowID); ok && len(st.EventLog) > 0 {
			cp := make([]protocol.SessionEvent, len(st.EventLog))
			for i := range st.EventLog {
				cp[i] = h.enrichNotificationEvent(req.WindowID, cloneSessionEvent(st.EventLog[i]))
			}
			out[req.WindowID] = cp
		}
		if snapshot, ok := h.windowMetadata[req.WindowID]; ok {
			meta[req.WindowID] = h.exportEnrichLocked(req.WindowID, cloneWindowDebugMetadata(snapshot))
		}
	} else {
		out = make(map[int][]protocol.SessionEvent, len(h.store))
		for id, st := range h.store {
			if len(st.EventLog) == 0 {
				continue
			}
			cp := make([]protocol.SessionEvent, len(st.EventLog))
			for i := range st.EventLog {
				cp[i] = h.enrichNotificationEvent(id, cloneSessionEvent(st.EventLog[i]))
			}
			out[id] = cp
		}
		meta = make(map[int]protocol.WindowDebugMetadata, len(h.windowMetadata))
		for id, snapshot := range h.windowMetadata {
			meta[id] = h.exportEnrichLocked(id, cloneWindowDebugMetadata(snapshot))
		}
	}
	h.stateMu.Unlock()
	return protocol.Response{OK: true, EventsByWindow: out, WindowMetadata: meta}
}

// exportEnrichLocked stamps live-only state onto an export snapshot: the
// renderer dirty age (DirtySince in the metadata is copied at mutation time;
// the age is computed at export time so a long-stuck window reads truthfully),
// the reducer's current source facts, so diagnostics can hold the visible icon
// accountable to the authoritative fact set, any terminal stop still being
// withheld from that fact set, and the current busy-stretch anchor. Callers
// hold stateMu.
func (h *Handler) exportEnrichLocked(windowID int, meta protocol.WindowDebugMetadata) protocol.WindowDebugMetadata {
	st, ok := h.store[windowID]
	if !ok {
		return meta
	}
	if st.Dirty && !st.DirtySince.IsZero() {
		meta.Renderer.DirtyAgeSeconds = int64(time.Since(st.DirtySince) / time.Second)
	}
	if st.Reducer != nil {
		meta.Shadow.Facts = st.Reducer.Facts()
	}
	meta.Shadow.PendingTerminalStop = h.pendingTerminalStopSnapshotLocked(windowID)
	if !st.BusySince.IsZero() {
		meta.Shadow.BusySinceAt = st.BusySince.Unix()
	}
	return meta
}

func cloneWindowDebugMetadata(in protocol.WindowDebugMetadata) protocol.WindowDebugMetadata {
	out := in
	if in.Stats.SuppressionReasons != nil {
		out.Stats.SuppressionReasons = make(map[string]int, len(in.Stats.SuppressionReasons))
		for reason, count := range in.Stats.SuppressionReasons {
			out.Stats.SuppressionReasons[reason] = count
		}
	}
	if in.Shadow.Mismatches != nil {
		out.Shadow.Mismatches = make([]protocol.ShadowMismatch, len(in.Shadow.Mismatches))
		copy(out.Shadow.Mismatches, in.Shadow.Mismatches)
	}
	if in.Watchdog.ScreenDeclines != nil {
		out.Watchdog.ScreenDeclines = make([]protocol.ScreenDeclineSnapshot, len(in.Watchdog.ScreenDeclines))
		copy(out.Watchdog.ScreenDeclines, in.Watchdog.ScreenDeclines)
	}
	out.Renderer.Generations = cloneRenderGenerationTraces(in.Renderer.Generations)
	return out
}

// SetReducerConfig installs the shadow reducer thresholds. Existing per-window
// reducers are re-bound too, so a restore that ran before configuration (unit
// tests, degraded startups) still ends up on the configured thresholds.
