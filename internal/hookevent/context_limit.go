package hookevent

import "github.com/sglavoie/agentmux/internal/contextlimit"

// PayloadHasContextLimit reports whether the hook payload carries a context
// limit stop/failure marker.
func PayloadHasContextLimit(payload HookPayload) bool {
	return payloadHasContextLimit(payload)
}

func payloadHasContextLimit(payload HookPayload) bool {
	if normalizeLifecycleValue(payload.EffectiveStopReason()) == "context_limit_reached" {
		return true
	}
	return contextlimit.HasMarker(payload.Title + "\n" + payload.Message)
}

// ShouldProbeWindowForContextLimit reports whether a hook event/action pair
// should fall back to screen probing for context-limit text.
func ShouldProbeWindowForContextLimit(eventOrAction, action string) bool {
	return shouldProbeWindowForContextLimit(eventOrAction, action)
}

func shouldProbeWindowForContextLimit(eventOrAction, action string) bool {
	if action == "failure" || action == "stop_failure" || validActions[eventOrAction] {
		return false
	}

	switch eventOrAction {
	case "TaskCompleted",
		"Stop", "agentStop",
		"SubagentStop", "subagentStop",
		"SessionEnd", "sessionEnd",
		"Notification", "notification":
		return true
	default:
		return false
	}
}
