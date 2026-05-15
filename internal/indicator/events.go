// Package indicator implements the hook-indicator logic: event parsing,
// state resolution, title updates, and daemon communication.
package indicator

// eventToAction maps supported harness hook event names to amx actions.
// Unknown events are silently ignored (return "").
var eventToAction = map[string]string{
	// Idle: session started, waiting for user input.
	"sessionStart": "idle",
	"SessionStart": "idle",

	// Active: the harness is processing.
	"userPromptSubmitted": "active",
	"UserPromptSubmit":   "active",
	"preToolUse":         "active",
	"PreToolUse":         "active",
	"postToolUse":        "active",
	"PostToolUse":        "active",
	"subagentStop":       "active",
	"SubagentStop":       "active",
	"TaskCompleted":      "active",
	"PostCompact":        "active",
	"ElicitationResult":  "active",

	// Subagent: subagent is running.
	"subagentStart": "subagent",
	"SubagentStart": "subagent",

	// Compact: context compaction in progress.
	"preCompact": "compact",
	"PreCompact": "compact",

	// Stop: session/subagent completed.
	"agentStop":   "stop",
	"sessionEnd":  "stop",
	"Stop":       "stop",
	"SessionEnd": "stop",

	// Stop-failure: terminal failure (API error, context-limit stop).
	"StopFailure": "stop_failure",

	// Permission: user approval required.
	"permissionRequest": "permission",
	"PermissionRequest": "permission",
	"PermissionDenied":  "permission",
	"Elicitation":       "permission",

	// Failure: tool execution failed.
	"postToolUseFailure": "failure",
	"PostToolUseFailure": "failure",
	"errorOccurred":      "failure",
	"ErrorOccurred":      "failure",

	// Idle: the harness wants to notify (task done, waiting for user).
	"notification": "idle",
	"Notification": "idle",
	"TeammateIdle": "idle",

	// Clear: state reset.
	"ConfigChange":   "clear",
	"WorktreeCreate": "clear",
	"WorktreeRemove": "clear",
}

// validActions is the set of canonical action names accepted from CLI callers.
// user_clear is included so the TUI can request a true icon wipe (event-driven
// idle/clear now preserve the current icon).
var validActions = map[string]bool{
	"active": true, "stop": true, "permission": true,
	"failure": true, "stop_failure": true,
	"subagent": true, "compact": true,
	"idle": true, "clear": true, "user_clear": true,
}

// ResolveAction maps an event name or action name to a canonical action.
// If input is already a valid action name, it is returned as-is.
// If input is a known event name, it is mapped to an action.
// Unknown inputs return ("", false).
func ResolveAction(input string) (action string, ok bool) {
	if validActions[input] {
		return input, true
	}
	if a, found := eventToAction[input]; found {
		return a, true
	}
	return "", false
}

// PreservesCurrent reports whether event represents a sub-unit completing
// while the parent session remains active. The daemon should keep whatever
// icon is currently showing rather than forcing "active".
func PreservesCurrent(event string) bool {
	return event == "TaskCompleted"
}

// IsPromptEvent reports whether event is a direct user prompt submission,
// which should break through stop-guards regardless of the guard window.
func IsPromptEvent(event string) bool {
	return event == "UserPromptSubmit" || event == "userPromptSubmitted"
}
