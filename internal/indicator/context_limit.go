package indicator

import "github.com/sglavoie/agentmux/internal/contextlimit"

func payloadHasContextLimit(payload HookPayload) bool {
	return contextlimit.HasMarker(payload.Title + "\n" + payload.Message)
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
