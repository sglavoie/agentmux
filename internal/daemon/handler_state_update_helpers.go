package daemon

import (
	"time"
)

const (
	suppressionReasonGuardBlocked         = "guard_blocked"
	suppressionReasonSubagentStillRunning = "subagent_still_running"
	suppressionReasonCopilotMidSubagent   = "copilot_mid_subagent"
	suppressionReasonCopilotPendingPrompt = "copilot_pending_prompt"
	suppressionReasonActiveAfterTerminal  = "active_after_terminal"
)

func actionFromIconState(iconState string) string {
	switch iconState {
	case "bolt":
		return "active"
	case "compact":
		return "compact"
	case "subagent":
		return "subagent"
	case "stop":
		return "stop"
	case "key":
		return "permission"
	case "warning":
		return "failure"
	case "error":
		return "stop_failure"
	case "question":
		return "elicitation"
	default:
		return ""
	}
}

func (h *Handler) lastKnownAction(windowID int, currentIconState string) string {
	h.stateMu.Lock()
	last := h.lastAction[windowID]
	h.stateMu.Unlock()
	if last != "" {
		return last
	}
	return actionFromIconState(currentIconState)
}

func sessionEventFromRequest(req Request, action, resultIconState string) SessionEvent {
	return SessionEvent{
		At:               time.Now().Unix(),
		Event:            req.Event,
		Action:           action,
		ToolName:         req.ToolName,
		Source:           req.Source,
		PreservesIcon:    req.PreservesIcon,
		ResultIconState:  resultIconState,
		CurrentIconState: req.CurrentIconState,
		NotificationType: req.NotificationType,
		StopReason:       req.StopReason,
		Reason:           req.Reason,
		ContextLimit:     req.ContextLimit,
		ResetFlow:        req.ResetFlow,
	}
}

func suppressedSessionEvent(req Request, action, suppressionReason string) SessionEvent {
	ev := sessionEventFromRequest(req, action, IconStatePreserve)
	ev.Suppressed = true
	ev.SuppressionReason = suppressionReason
	return ev
}
