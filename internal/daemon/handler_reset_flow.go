package daemon

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

func shouldClearPendingReset(req Request, state resetFlowState) bool {
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
func (h *Handler) applyResetFlowLocked(req Request) Request {
	state := h.pendingReset[req.WindowID]
	if req.ResetFlow {
		if req.IsPrompt || state == resetFlowNone {
			state = resetFlowRequested
		}
		h.pendingReset[req.WindowID] = state
	} else if shouldClearPendingReset(req, state) {
		delete(h.pendingReset, req.WindowID)
		state = resetFlowNone
	}

	if state == resetFlowNone || !isResetCleanupAction(req.Action) {
		return req
	}

	originalAction := req.Action
	req.Action = "user_clear"
	req.ResetFlow = true
	switch originalAction {
	case "user_clear":
		delete(h.pendingReset, req.WindowID)
	default:
		// Keep the clearing marker through later idle/sessionStart/notification
		// noise from the same reset flow; the next non-reset prompt or activity
		// will clear it via shouldClearPendingReset.
		h.pendingReset[req.WindowID] = resetFlowClearing
	}
	return req
}
