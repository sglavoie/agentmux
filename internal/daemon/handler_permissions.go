package daemon

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
)

func isCopilotRequest(req protocol.Request) bool {
	if harness.NormalizeKnown(req.Harness) == harness.Copilot {
		return true
	}
	if req.TargetHarnessSet {
		// Hook-origin requests explicitly separate producer and target roles. An
		// empty producer there is unknown and must stay harness-agnostic.
		return false
	}
	if strings.TrimSpace(req.Harness) != "" {
		// An explicit unsupported producer is unknown, not legacy omission.
		return false
	}
	// Backward compatibility for legacy direct state_update clients that did
	// not serialize Harness and relied on the event dialect instead.
	switch req.Event {
	case "sessionStart", "sessionEnd", "userPromptSubmitted", "preToolUse",
		"postToolUse", "postToolUseFailure", "permissionRequest", "agentStop",
		"subagentStart", "subagentStop", "errorOccurred", "preCompact",
		"notification":
		return true
	default:
		return false
	}
}

func isCopilotPermissionResolutionEvent(req protocol.Request) bool {
	if req.IsPrompt {
		return true
	}
	switch req.Event {
	case "preToolUse", "PreToolUse":
		return true
	default:
		return false
	}
}

func isCopilotPermissionRequest(req protocol.Request) bool {
	return isCopilotRequest(req) &&
		req.Action == "permission" &&
		req.Event == "permissionRequest"
}

func isCopilotPermissionPromptNotification(req protocol.Request) bool {
	return isCopilotRequest(req) &&
		req.Action == "permission" &&
		(req.Event == "notification" || req.Event == "Notification") &&
		req.NotificationType == "permission_prompt"
}

func isCopilotElicitationPrompt(req protocol.Request) bool {
	return isCopilotRequest(req) &&
		req.Action == "elicitation" &&
		(req.Event == "notification" || req.Event == "Notification") &&
		req.NotificationType == "elicitation_dialog"
}

func isCopilotAutoApprovedToolPermission(req protocol.Request) bool {
	return isCopilotPermissionPromptNotification(req)
}

func isCopilotTrailingAutoApprovedToolPermission(req protocol.Request, priorAutoApproved bool) bool {
	return priorAutoApproved && isCopilotAutoApprovedToolPermission(req)
}

func shouldResetCopilotToolPermissionState(req protocol.Request) bool {
	if !isCopilotRequest(req) {
		return false
	}
	if req.IsPrompt {
		return true
	}
	switch req.Event {
	case "sessionStart", "SessionStart":
		return true
	}
	switch req.Action {
	case "clear", "user_clear", "user_active":
		return true
	default:
		return false
	}
}

func shouldConfirmCopilotAutoApprovedToolPermission(req protocol.Request) bool {
	return isCopilotRequest(req) && req.Action != "permission"
}

func shouldSuppressCopilotPendingPrompt(req protocol.Request, prevAction string) bool {
	if !isCopilotRequest(req) || (prevAction != "permission" && prevAction != "elicitation") {
		return false
	}
	switch {
	case req.Action == "stop" && req.Event == "agentStop":
		return true
	case req.Action == "active" && !isCopilotPermissionResolutionEvent(req):
		return true
	default:
		return false
	}
}

// Copilot exposes preCompact but not a documented postCompact hook. If the
// first follow-up is only an idle/cleanup event, preserve the compaction
// context long enough to surface a stop icon instead of silently clearing it.
func shouldSynthesizeCopilotCompactStop(req protocol.Request, prevAction string) bool {
	return isCopilotRequest(req) &&
		prevAction == "compact" &&
		(req.Action == "idle" || req.Action == "clear")
}
