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

// cancelPendingResetStop drops any deferred reset stop still waiting on the
// debounce timer for windowID. Called when a newer authoritative event
// supersedes the held stop so it can no longer fire and repaint a stale icon.
func (h *Handler) cancelPendingResetStop(windowID int) {
	h.stateMu.Lock()
	if pending, ok := h.pendingResetStops[windowID]; ok {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(h.pendingResetStops, windowID)
	}
	h.stateMu.Unlock()
}

// supersedesDeferredResetStop reports whether an incoming state update should
// cancel a stop still held in the reset-defer window. A fresh prompt (a new
// turn) or an explicit wipe (user_clear/clear) makes the held stop stale, so it
// must not paint. Reset-flow cleanup (req.ResetFlow) is deliberately excluded:
// the deferred stop is kept so applyResetFlowLocked can fold it into the same
// user_clear when its timer commits.
func supersedesDeferredResetStop(req Request) bool {
	if req.ResetFlow {
		return false
	}
	if req.IsPrompt {
		return true
	}
	switch req.Action {
	case "user_clear", "clear":
		return true
	default:
		return false
	}
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
