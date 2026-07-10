package daemon

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/hookevent"
)

// resetFlowStopDelay gives late reset markers a brief chance to reach the
// daemon before an ordinary stop hook paints a stop icon. It mirrors the
// hook-side transition delay while TypeStateUpdate remains in use.
var resetFlowStopDelay = 500 * time.Millisecond

type pendingResetStop struct {
	timer *time.Timer
	seq   uint64
}

func (h *Handler) deferResetSensitiveStop(req Request, payload hookevent.HookPayload) bool {
	if !shouldDeferResetSensitiveStop(req, payload) {
		return false
	}
	if resetFlowStopDelay <= 0 {
		return false
	}

	h.stateMu.Lock()
	if h.pendingResetStops == nil {
		h.pendingResetStops = make(map[int]pendingResetStop)
	}
	if pending, ok := h.pendingResetStops[req.WindowID]; ok && pending.timer != nil {
		pending.timer.Stop()
	}
	h.nextResetStopSeq++
	seq := h.nextResetStopSeq
	timer := time.AfterFunc(resetFlowStopDelay, func() {
		h.commitDeferredResetStop(req, seq)
	})
	h.pendingResetStops[req.WindowID] = pendingResetStop{timer: timer, seq: seq}
	h.stateMu.Unlock()
	return true
}

func (h *Handler) commitDeferredResetStop(req Request, seq uint64) {
	h.stateMu.Lock()
	pending, ok := h.pendingResetStops[req.WindowID]
	if !ok || pending.seq != seq {
		h.stateMu.Unlock()
		return
	}
	delete(h.pendingResetStops, req.WindowID)
	h.stateMu.Unlock()

	h.handleStateUpdate(req)
}

func shouldDeferResetSensitiveStop(req Request, payload hookevent.HookPayload) bool {
	if req.Action != "stop" || req.ContextLimit || req.ResetFlow {
		return false
	}
	switch req.Event {
	case "Stop", "SessionEnd", "agentStop", "sessionEnd":
		return true
	case "Notification", "notification":
		return strings.EqualFold(strings.TrimSpace(payload.EffectiveNotificationType()), "agent_idle")
	default:
		return false
	}
}
