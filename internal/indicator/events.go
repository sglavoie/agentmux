// Package indicator implements the hook-indicator logic: event parsing,
// state resolution, title updates, and daemon communication.
package indicator

// eventToAction is a shared map covering Claude (PascalCase) and Copilot
// (camelCase) hook event names that directly map to a visible tab-state change.
// Notification hooks are resolved separately because their payload subtype is
// needed to decide whether they mean "permission", "stop", or "ignore".
// Claude's explicit Elicitation hook still maps directly via eventToAction.
var eventToAction = map[string]string{
	// Idle: session started, waiting for user input.
	"sessionStart": "idle",
	"SessionStart": "idle",

	// Active: the harness is processing.
	"Setup":               "active",
	"userPromptSubmitted": "active",
	"UserPromptSubmit":    "active",
	"UserPromptExpansion": "active",
	"preToolUse":          "active",
	"PreToolUse":          "active",
	"postToolUse":         "active",
	"PostToolUse":         "active",
	"PostToolBatch":       "active",
	"subagentStop":        "active",
	"SubagentStop":        "active",
	"TaskCreated":         "active",
	"TaskCompleted":       "active",
	"TeammateIdle":        "active",
	"PostCompact":         "stop",
	"ElicitationResult":   "active",

	// Subagent: subagent is running.
	"subagentStart": "subagent",
	"SubagentStart": "subagent",

	// Compact: context compaction in progress.
	"preCompact": "compact",
	"PreCompact": "compact",

	// Stop: session or turn completed. The agent stopped working; user sees ⏹.
	"agentStop":  "stop",
	"sessionEnd": "stop",
	"Stop":       "stop",
	"SessionEnd": "stop",

	// Stop-failure: terminal failure (API error, context-limit stop).
	"StopFailure": "stop_failure",

	// Permission: user approval required.
	"permissionRequest": "permission",
	"PermissionRequest": "permission",
	"PermissionDenied":  "permission",
	"Elicitation":       "elicitation",

	// Failure: tool execution failed.
	"postToolUseFailure": "failure",
	"PostToolUseFailure": "failure",
	"errorOccurred":      "failure",
	"ErrorOccurred":      "failure",

	// Idle fallback: generic notifications without a typed payload still clear
	// the bolt. Typed notifications are resolved in resolveNotificationAction.
	"notification": "idle",
	"Notification": "idle",

	// Clear: state reset.
	"ConfigChange":   "clear",
	"WorktreeCreate": "clear",
	"WorktreeRemove": "clear",
}

// ignoredEvents are known hook events that Agent Mux currently does not map to
// tab-state changes. They are intentionally ignored rather than treated as
// unknown so new Claude releases do not produce misleading debug logs when
// these hooks are installed for other purposes.
var ignoredEvents = map[string]bool{
	"InstructionsLoaded": true,
	"CwdChanged":         true,
	// FileChanged requires a per-hook matcher field; amx does not configure any
	// matchers so Claude Code will never fire it, but recognize it defensively.
	"FileChanged": true,
}

// validActions is the set of canonical action names accepted from CLI callers.
// user_clear is included so the TUI can request a true icon wipe (event-driven
// idle/clear now preserve the current icon).
var validActions = map[string]bool{
	"active": true, "stop": true, "permission": true,
	"failure": true, "stop_failure": true,
	"subagent": true, "compact": true, "elicitation": true,
	"idle": true, "clear": true, "user_clear": true,
}

// ResolveAction maps an event name or action name to a canonical action.
// If input is already a valid action name, it is returned as-is.
// If input is a known event name, it is mapped to an action, using hook payload
// when needed (e.g. typed Notification hooks).
// Unknown inputs return ("", false).
func ResolveAction(input string, payload HookPayload) (action string, ok bool) {
	if validActions[input] {
		return input, true
	}
	if IsIgnoredEvent(input, payload) {
		return "", false
	}
	if action, ok := resolveFailureAction(input, payload); ok {
		return action, true
	}
	if action, ok := resolveNotificationAction(input, payload); ok {
		return action, true
	}
	if a, found := eventToAction[input]; found {
		return a, true
	}
	return "", false
}

// IsIgnoredEvent reports whether an event is recognized but intentionally has no
// tab-icon effect.
func IsIgnoredEvent(input string, payload HookPayload) bool {
	if ignoredEvents[input] {
		return true
	}
	if !isNotificationEvent(input) {
		return false
	}
	switch payload.EffectiveNotificationType() {
	case "shell_completed", "shell_detached_completed", "agent_completed":
		return true
	default:
		return false
	}
}

func resolveFailureAction(input string, payload HookPayload) (string, bool) {
	if payloadHasContextLimit(payload) {
		return "stop_failure", true
	}

	switch input {
	case "SessionEnd", "sessionEnd":
		if payload.Reason == "error" || payload.Reason == "timeout" {
			return "stop_failure", true
		}
	case "Stop", "agentStop", "SubagentStop", "subagentStop":
		stopReason := payload.EffectiveStopReason()
		if stopReason != "" && stopReason != "end_turn" {
			return "stop_failure", true
		}
	}

	return "", false
}

func resolveNotificationAction(input string, payload HookPayload) (string, bool) {
	if !isNotificationEvent(input) {
		return "", false
	}
	switch payload.EffectiveNotificationType() {
	case "permission_prompt":
		return "permission", true
	case "elicitation_dialog":
		// Copilot uses elicitation_dialog for plan approvals and other
		// user-input-needed prompts. Surface the same key icon as a permission
		// prompt so the tab consistently signals "waiting on you".
		return "permission", true
	case "agent_idle":
		return "stop", true
	default:
		return "", false
	}
}

func isNotificationEvent(input string) bool {
	return input == "notification" || input == "Notification"
}

// PreservesCurrent reports whether event represents a child completing while
// the parent session remains alive. The daemon should preserve the currently
// visible icon instead of drawing a new one for the synthesized non-terminal
// "active" action.
func PreservesCurrent(event string) bool {
	switch event {
	case "TaskCompleted", "TeammateIdle":
		return true
	default:
		return false
	}
}

// IsPromptEvent reports whether event is a direct user prompt submission,
// which should break through stop-guards regardless of the guard window.
func IsPromptEvent(event string) bool {
	return event == "UserPromptSubmit" ||
		event == "userPromptSubmitted" ||
		event == "UserPromptExpansion"
}
