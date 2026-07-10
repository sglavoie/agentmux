package daemon

import (
	"strings"
	"time"
)

const (
	suppressionReasonGuardBlocked         = "guard_blocked"
	suppressionReasonSubagentStillRunning = "subagent_still_running"
	suppressionReasonCopilotMidSubagent   = "copilot_mid_subagent"
	suppressionReasonCopilotPendingPrompt = "copilot_pending_prompt"
	suppressionReasonActiveAfterTerminal  = "active_after_terminal"
	suppressionReasonElicitationPending   = "elicitation_pending"
	suppressionReasonUnknownEvent         = "unknown_event"
	suppressionReasonAutoApprovedPerm     = "auto_approved_permission"
	suppressionReasonOwnerMismatch        = "owner_mismatch"
	suppressionReasonIgnoredEvent         = "ignored_event"
)

func (h *Handler) lastKnownState(windowID int) (action, iconState string) {
	h.stateMu.Lock()
	if st, ok := h.windowStateLocked(windowID); ok {
		action = st.LastAction
		iconState = desiredIconForState(st)
	}
	h.stateMu.Unlock()
	return action, iconState
}

func sessionEventFromRequest(req Request, action, resultIconState, currentIconState string) SessionEvent {
	return SessionEvent{
		At:               time.Now().Unix(),
		Event:            req.Event,
		Action:           action,
		ToolName:         req.ToolName,
		Source:           req.Source,
		PreservesIcon:    req.PreservesIcon,
		ResultIconState:  resultIconState,
		CurrentIconState: currentIconState,
		NotificationType: req.NotificationType,
		StopReason:       req.StopReason,
		Reason:           req.Reason,
		ContextLimit:     req.ContextLimit,
		ResetFlow:        req.ResetFlow,
	}
}

func suppressedSessionEvent(req Request, action, suppressionReason, currentIconState string) SessionEvent {
	ev := sessionEventFromRequest(req, action, IconStatePreserve, currentIconState)
	ev.Suppressed = true
	ev.SuppressionReason = suppressionReason
	return ev
}

func shouldSuppressPendingElicitationActive(req Request, prevAction string) bool {
	if req.PreservesIcon || isCopilotRequest(req) || prevAction != "elicitation" || req.Action != "active" {
		return false
	}
	return !isElicitationResolutionEvent(req)
}

func isElicitationResolutionEvent(req Request) bool {
	if req.IsPrompt {
		return true
	}
	switch req.Event {
	case "ElicitationResult":
		return true
	case "PostToolUse", "postToolUse":
		return strings.EqualFold(strings.TrimSpace(req.ToolName), "request_user_input")
	default:
		return false
	}
}
