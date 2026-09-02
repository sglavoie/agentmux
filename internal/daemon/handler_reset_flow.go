package daemon

import (
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

type resetFlowState uint8

const (
	resetFlowNone resetFlowState = iota
	resetFlowRequested
	resetFlowClearing
)

func isResetCleanupAction(action string) bool {
	switch action {
	case "stop", "idle", "clear", "user_clear":
		return true
	default:
		return false
	}
}

func shouldClearPendingReset(req protocol.Request, state resetFlowState) bool {
	if state == resetFlowNone || req.ResetFlow {
		return false
	}
	if req.IsPrompt {
		return true
	}
	if isResetCleanupAction(req.Action) {
		return false
	}
	return state != resetFlowRequested || req.Action != "active"
}

// applyResetFlowLocked maintains the short-lived per-window reset marker and
// rewrites reset-related cleanup into user_clear so stop icons/notifications do
// not surface during /clear, /reset, or /new flows. Caller must hold stateMu.
func (h *Handler) applyResetFlowLocked(req protocol.Request) protocol.Request {
	st := h.ensureWindowStateLocked(req.WindowID)
	state := st.PendingReset
	if req.ResetFlow {
		if req.IsPrompt || state == resetFlowNone {
			state = resetFlowRequested
		}
		st.PendingReset = state
	} else if shouldClearPendingReset(req, state) {
		state = resetFlowNone
		st.PendingReset = resetFlowNone
	}

	if state == resetFlowNone || !isResetCleanupAction(req.Action) {
		return req
	}

	originalAction := req.Action
	req.Action = "user_clear"
	req.ResetFlow = true
	switch originalAction {
	case "user_clear":
		st.PendingReset = resetFlowNone
	default:
		// Keep the clearing marker through later idle/sessionStart/notification
		// noise from the same reset flow; the next non-reset prompt or activity
		// will clear it via shouldClearPendingReset.
		st.PendingReset = resetFlowClearing
	}
	return req
}
