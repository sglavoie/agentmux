package daemon

import (
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func isResetCleanupAction(action string) bool {
	switch action {
	case "stop", "idle", "clear", "user_clear":
		return true
	default:
		return false
	}
}

func shouldClearPendingReset(req protocol.Request, flow state.ResetFlowState) bool {
	if flow == state.ResetFlowNone || req.ResetFlow {
		return false
	}
	if req.IsPrompt {
		return true
	}
	if isResetCleanupAction(req.Action) {
		return false
	}
	return flow != state.ResetFlowRequested || req.Action != "active"
}

// applyResetFlowLocked maintains the short-lived per-window reset marker and
// rewrites reset-related cleanup into user_clear so stop icons/notifications do
// not surface during /clear, /reset, or /new flows. Caller must hold stateMu.
func (h *Handler) applyResetFlowLocked(req protocol.Request) protocol.Request {
	st := h.ensureWindowStateLocked(req.WindowID)
	flow := st.PendingReset
	if req.ResetFlow {
		if req.IsPrompt || flow == state.ResetFlowNone {
			flow = state.ResetFlowRequested
		}
		st.PendingReset = flow
	} else if shouldClearPendingReset(req, flow) {
		flow = state.ResetFlowNone
		st.PendingReset = state.ResetFlowNone
	}

	if flow == state.ResetFlowNone || !isResetCleanupAction(req.Action) {
		return req
	}

	originalAction := req.Action
	req.Action = "user_clear"
	req.ResetFlow = true
	switch originalAction {
	case "user_clear":
		st.PendingReset = state.ResetFlowNone
	default:
		// Keep the clearing marker through later idle/sessionStart/notification
		// noise from the same reset flow; the next non-reset prompt or activity
		// will clear it via shouldClearPendingReset.
		st.PendingReset = state.ResetFlowClearing
	}
	return req
}
