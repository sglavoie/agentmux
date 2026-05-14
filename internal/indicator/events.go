// Package indicator implements the hook-indicator logic: event parsing,
// state resolution, title updates, and daemon communication.
package indicator

// eventToAction maps all 17 Claude Code hook event names to the 6 ccm actions.
// Unknown events are silently ignored (return "").
var eventToAction = map[string]string{
	// Idle: session started, waiting for user input.
	"SessionStart": "idle",

	// Active: Claude is processing.
	"UserPromptSubmit": "active",
	"PreToolUse":       "active",
	"SubagentStart":    "active",
	"PreCompact":       "active",
	"PostToolUse":      "active",
	"SubagentStop":     "active",
	"TaskCompleted":    "active",

	// Stop: session/subagent completed.
	"Stop":       "stop",
	"SessionEnd": "stop",

	// Permission: user approval required.
	"PermissionRequest": "permission",

	// Failure: tool execution failed.
	"PostToolUseFailure": "failure",

	// Idle: Claude wants to notify (task done, waiting for user).
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
	"failure": true, "idle": true, "clear": true, "user_clear": true,
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
	return event == "SubagentStop" || event == "TaskCompleted"
}

// IsPromptEvent reports whether event is a direct user prompt submission,
// which should break through stop-guards regardless of the guard window.
func IsPromptEvent(event string) bool {
	return event == "UserPromptSubmit"
}
