package daemon

import (
	"github.com/sglavoie/agentmux/internal/harness"
)

func isCopilotRequest(req Request) bool {
	if harness.Normalize(req.Harness) == "copilot" {
		return true
	}
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

// isClaudeRequest reports whether the request originated from the Claude
// harness. Empty Harness is treated as Claude for backward compatibility with
// hook payloads emitted before per-harness routing existed.
func isClaudeRequest(req Request) bool {
	norm := harness.Normalize(req.Harness)
	return norm == "" || norm == harness.Claude
}

func isCopilotPermissionResolutionEvent(req Request) bool {
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

func isCopilotPermissionRequest(req Request) bool {
	return isCopilotRequest(req) &&
		req.Action == "permission" &&
		req.Event == "permissionRequest"
}

func isCopilotPermissionPromptNotification(req Request) bool {
	return isCopilotRequest(req) &&
		req.Action == "permission" &&
		(req.Event == "notification" || req.Event == "Notification") &&
		req.NotificationType == "permission_prompt"
}

func isCopilotElicitationPrompt(req Request) bool {
	return isCopilotRequest(req) &&
		req.Action == "elicitation" &&
		(req.Event == "notification" || req.Event == "Notification") &&
		req.NotificationType == "elicitation_dialog"
}

func isCopilotAutoApprovedToolPermission(req Request) bool {
	return isCopilotPermissionPromptNotification(req)
}

func isCopilotTrailingAutoApprovedToolPermission(req Request, priorAutoApproved bool) bool {
	return priorAutoApproved && isCopilotAutoApprovedToolPermission(req)
}

type copilotToolPermissionState uint8

const (
	copilotToolPermissionNone copilotToolPermissionState = iota
	copilotToolPermissionPending
	copilotToolPermissionAutoApproved
)

func shouldResetCopilotToolPermissionState(req Request) bool {
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
	case "clear", "user_clear":
		return true
	default:
		return false
	}
}

func shouldConfirmCopilotAutoApprovedToolPermission(req Request) bool {
	return isCopilotRequest(req) && req.Action != "permission"
}

func shouldSuppressCopilotPendingPrompt(req Request, prevAction string) bool {
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
func shouldSynthesizeCopilotCompactStop(req Request, prevAction string) bool {
	return isCopilotRequest(req) &&
		prevAction == "compact" &&
		(req.Action == "idle" || req.Action == "clear")
}
