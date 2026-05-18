package indicator

import "strings"

func payloadHasContextLimit(payload HookPayload) bool {
	return hasContextLimitMarker(payload.Title + "\n" + payload.Message)
}

func hasContextLimitMarker(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if normalized == "" {
		return false
	}

	hasLimitMessage := strings.Contains(normalized, "context limit reached") ||
		strings.Contains(normalized, "context window is full") ||
		strings.Contains(normalized, "context window full")
	if !hasLimitMessage {
		return false
	}

	return strings.Contains(normalized, "/compact or /clear") ||
		(strings.Contains(normalized, "/compact") &&
			strings.Contains(normalized, "/clear") &&
			strings.Contains(normalized, "continue"))
}

func screenShowsContextLimit(text string) bool {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	tail := make([]string, 0, 3)
	for i := len(lines) - 1; i >= 0 && len(tail) < 3; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		tail = append([]string{line}, tail...)
	}
	return hasContextLimitMarker(strings.Join(tail, "\n"))
}

func shouldProbeWindowForContextLimit(eventOrAction, action string) bool {
	if action == "failure" || action == "stop_failure" || validActions[eventOrAction] {
		return false
	}

	switch eventOrAction {
	case "TaskCompleted",
		"Stop", "agentStop",
		"SessionEnd", "sessionEnd",
		"Notification", "notification":
		return true
	default:
		return false
	}
}
