package daemon

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
)

const (
	suppressionReasonGuardBlocked           = protocol.DecisionReasonGuardBlocked
	suppressionReasonSubagentStillRunning   = protocol.DecisionReasonSubagentStillRunning
	suppressionReasonCopilotMidSubagent     = protocol.DecisionReasonCopilotMidSubagent
	suppressionReasonCopilotPendingPrompt   = protocol.DecisionReasonCopilotPendingPrompt
	suppressionReasonActiveAfterTerminal    = protocol.DecisionReasonActiveAfterTerminal
	suppressionReasonElicitationPending     = protocol.DecisionReasonElicitationPending
	suppressionReasonUnknownEvent           = protocol.DecisionReasonUnknownEvent
	suppressionReasonAutoApprovedPerm       = protocol.DecisionReasonAutoApprovedPermission
	suppressionReasonOwnerMismatch          = protocol.DecisionReasonOwnerMismatch
	suppressionReasonUnmanagedPipeSource    = protocol.DecisionReasonUnmanagedPipeSource
	suppressionReasonIgnoredEvent           = protocol.DecisionReasonIgnoredEvent
	suppressionReasonInvalidPayload         = protocol.DecisionReasonInvalidPayload
	suppressionReasonTerminalStopHeld       = protocol.DecisionReasonTerminalStopHeld
	suppressionReasonTerminalStopSuperseded = protocol.DecisionReasonTerminalStopSuperseded
	suppressionReasonTerminalStopBypassed   = protocol.DecisionReasonTerminalStopBypassed
)

func (h *Handler) lastKnownState(windowID int) (action, iconState string) {
	h.stateMu.Lock()
	if st, ok := h.windowStateLocked(windowID); ok {
		action = st.LastAction
		iconState = rendering.DesiredIcon(st)
	}
	h.stateMu.Unlock()
	return action, iconState
}

func shouldSuppressPendingElicitationActive(req protocol.Request, prevAction string) bool {
	if req.PreservesIcon || isCopilotRequest(req) || prevAction != "elicitation" || req.Action != "active" {
		return false
	}
	return !isElicitationResolutionEvent(req)
}

func isElicitationResolutionEvent(req protocol.Request) bool {
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
