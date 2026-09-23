// Package hookevent implements hook payload parsing and event-to-action
// resolution shared by hook clients and the daemon.
package hookevent

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

// eventToAction is a shared map covering Claude (PascalCase) and Copilot
// (camelCase) hook event names that directly map to a visible tab-state change.
// Notification hooks are resolved separately because their payload subtype is
// needed to decide whether they mean "permission", "stop", or "ignore".
// Claude's explicit Elicitation hook still maps directly via eventToAction.
var eventToAction = map[string]string{
	// Idle: session started, waiting for user input.
	"sessionStart": "idle",
	"SessionStart": "idle",
	// Internal Agent Mux signals from agent_session_loop. Lifecycle events are
	// marker-only except for the final run finish: the daemon derives the per-run
	// phase (turn, verification, between, waiting, paused, finished) from their
	// names and owns the authoritative icon + loop lease. Mailbox events share
	// the recognized marker action for delivery compatibility but are intercepted
	// by the supervisor-probe handler before ordinary lifecycle normalization.
	"AgentSessionLoopStarted":               "loop_marker",
	"AgentSessionLoopTurnStarted":           "loop_marker",
	"AgentSessionLoopTurnHeartbeat":         "loop_marker",
	"AgentSessionLoopTurnHangSuspected":     "loop_marker",
	"AgentSessionLoopTurnHangCleared":       "loop_marker",
	"AgentSessionLoopTurnFinished":          "loop_marker",
	"AgentSessionLoopVerificationStarted":   "loop_marker",
	"AgentSessionLoopVerificationHeartbeat": "loop_marker",
	"AgentSessionLoopVerificationFinished":  "loop_marker",
	"AgentSessionLoopWaitStarted":           "loop_marker",
	"AgentSessionLoopWaitEnded":             "loop_marker",
	"AgentSessionLoopMailboxQuestion":       "loop_marker",
	"AgentSessionLoopMailboxReply":          "loop_marker",
	"AgentSessionLoopMailboxResolution":     "loop_marker",
	"AgentSessionLoopPaused":                "loop_marker",
	"AgentSessionLoopFinished":              "idle",

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
	"ElicitationResult":   "active",

	// PostCompact fires for both manual /compact and automatic mid-turn
	// compaction. The Claude hook config splits them via matcher groups that
	// dispatch these pseudo-events because Claude does not include the trigger
	// in its payload. Codex does include payload.trigger, so ResolveAction
	// classifies its bare PostCompact dynamically. The static fallback remains
	// "stop" for legacy or malformed payloads without a recognized trigger.
	"PostCompact":       "stop",
	"PostCompactManual": "stop",
	"PostCompactAuto":   "active",

	// Subagent: subagent is running.
	"subagentStart": "subagent",
	"SubagentStart": "subagent",

	// Compact: context compaction in progress.
	"preCompact": "compact",
	"PreCompact": "compact",

	// Stop: session or turn completed. The agent stopped working; user sees 🛑.
	"agentStop":  "stop",
	"sessionEnd": "stop",
	"Stop":       "stop",
	"SessionEnd": "stop",

	// Stop-failure: terminal failure (API error, context-limit stop).
	"StopFailure": "stop_failure",

	// Permission / waiting-on-you states. Copilot's permissionRequest is only a
	// pre-permission lifecycle signal; the daemon decides later whether it should
	// stay non-blocking or surface the key icon once an explicit prompt arrives.
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
	"ConfigChange": "clear",
}

// ignoredEvents are known hook events that Agent Mux currently does not map to
// tab-state changes. They are intentionally ignored rather than treated as
// unknown so new Claude releases do not produce misleading debug logs when
// these hooks are installed for other purposes.
var ignoredEvents = map[string]bool{
	"InstructionsLoaded": true,
	"MessageDisplay":     true,
	"CwdChanged":         true,
	"WorktreeCreate":     true,
	"WorktreeRemove":     true,
	// FileChanged requires a per-hook matcher field; amx does not configure any
	// matchers so Claude Code will never fire it, but recognize it defensively.
	"FileChanged": true,
}

// validActions is the set of canonical action names accepted from CLI callers.
// user_clear is included so the TUI can request a true icon wipe (event-driven
// idle/clear now preserve the current icon). user_active is its counterpart:
// the operator marks a tab busy (⚡) until the next real hook/ASL event. loop_marker is included so direct
// `amx hook-indicator loop_marker` calls (and tests that exercise the action
// surface) resolve to the daemon-only marker action.
var validActions = map[string]bool{
	"active": true, "stop": true, "permission": true,
	"failure": true, "stop_failure": true,
	"subagent": true, "compact": true, "elicitation": true,
	"idle": true, "clear": true, "user_clear": true, "user_active": true,
	"loop_marker": true,
}

// ResolveAction maps an event name or action name to a canonical action.
// If input is already a valid action name, it is returned as-is.
// If input is a known event name, it is mapped to an action, using hook payload
// when needed (e.g. typed Notification hooks).
// Unknown inputs return ("", false).
//
// ResolveAction is the harness-agnostic compatibility wrapper retained for
// callers that have no harness context. It delegates to ResolveActionForHarness
// with an empty harness, so harness-specific notification semantics (Claude's
// idle_prompt authoritative stop) are not applied.
func ResolveAction(input string, payload HookPayload) (action string, ok bool) {
	return ResolveActionForHarness(input, payload, "")
}

// ResolveActionForHarness is the harness-aware resolver. It behaves exactly like
// ResolveAction except that notification resolution consults harnessName so a
// harness's own terminal notification is honored: Claude documents idle_prompt
// as "Claude is done", which is an authoritative stop even though native Stop
// does not always fire (notably after a user interruption). A later prompt
// supersedes the stop through normal reducer recency, so no special-casing is
// needed beyond resolving the notification to stop.
func ResolveActionForHarness(input string, payload HookPayload, harnessName string) (action string, ok bool) {
	if IsIgnoredEventForHarness(input, payload, harnessName) {
		return "", false
	}
	if action, ok := resolveFailureAction(input, payload); ok {
		return action, true
	}
	if validActions[input] {
		return input, true
	}
	if action, ok := resolveNotificationAction(input, payload, harnessName); ok {
		return action, true
	}
	if action, ok := resolveToolAction(input, payload, harnessName); ok {
		return action, true
	}
	if action, ok := resolveCompactAction(input, payload); ok {
		return action, true
	}
	if a, found := eventToAction[input]; found {
		return a, true
	}
	return "", false
}

// resolveCompactAction distinguishes Codex's manual and automatic compaction
// completions. Automatic compaction resumes the same active turn, while a
// manual /compact finishes at the prompt. Unknown or absent trigger values
// deliberately fall through to eventToAction's conservative stop fallback.
func resolveCompactAction(input string, payload HookPayload) (string, bool) {
	if input != "PostCompact" {
		return "", false
	}
	switch normalizeLifecycleValue(payload.Trigger) {
	case "auto":
		return "active", true
	case "manual":
		return "stop", true
	default:
		return "", false
	}
}

// IsIgnoredEvent reports whether an event is recognized but intentionally has no
// tab-icon effect. It is the harness-agnostic wrapper; callers with a harness in
// hand should use IsIgnoredEventForHarness so harness-specific ignore rules
// (Copilot's background agent_idle notification) apply.
func IsIgnoredEvent(input string, payload HookPayload) bool {
	return IsIgnoredEventForHarness(input, payload, "")
}

// IsIgnoredEventForHarness is the harness-aware ignore check. Beyond the
// harness-agnostic ignore set, Copilot's agent_idle notification is a
// background-agent idle signal, not a main-session stop: Copilot documents
// agentStop as the main-turn terminal event, so the interactive session's stop
// comes from native agentStop/sessionEnd or the daemon's transcript stop probe,
// and subagent lifecycle facts remain responsible for 🤖. Ignoring it for
// Copilot prevents a background agent going idle from falsely stopping the
// interactive session. Other harnesses (and the agnostic wrapper) keep the
// agent_idle → stop mapping, so a harness that documents agent_idle as a real
// stop is unaffected.
func IsIgnoredEventForHarness(input string, payload HookPayload, harnessName string) bool {
	for _, contract := range harness.HookEventMatrix(harnessName) {
		if contract.CanonicalName == input && contract.Behavior == harness.HookObserverOnly {
			return true
		}
	}
	if ignoredEvents[input] {
		return true
	}
	if !isNotificationEvent(input) {
		return false
	}
	switch normalizedNotificationType(payload) {
	case "shell_completed", "shell_detached_completed", "agent_completed":
		return true
	case "agent_idle":
		return isCopilotHarness(harnessName)
	default:
		return false
	}
}

func resolveFailureAction(input string, payload HookPayload) (string, bool) {
	if payloadHasContextLimit(payload) && isContextLimitCarrier(input) {
		return "stop_failure", true
	}

	switch input {
	case "SessionEnd", "sessionEnd":
		reason := normalizeLifecycleValue(payload.Reason)
		if reason == "error" || reason == "timeout" {
			return "stop_failure", true
		}
	case "Stop", "agentStop", "SubagentStop", "subagentStop", "stop":
		// Normalize the harness-reported stop reason: trailing whitespace
		// or unusual casing on a benign "end_turn" must not be promoted to
		// stop_failure (which would surface the error icon on every clean
		// turn end).
		stopReason := normalizeLifecycleValue(payload.EffectiveStopReason())
		if stopReason != "" && stopReason != "end_turn" {
			return "stop_failure", true
		}
	}

	return "", false
}

// isContextLimitCarrier reports whether input names an event that can legitimately
// carry a "context limit reached" payload. Unknown events and lifecycle hooks
// that never describe a stop condition (Setup, PreToolUse, ConfigChange, etc.)
// should not be silently promoted to stop_failure just because their title or
// message happened to contain the recovery hint string.
func isContextLimitCarrier(input string) bool {
	switch input {
	case "Notification", "notification",
		"Stop", "agentStop",
		"SubagentStop", "subagentStop",
		"SessionEnd", "sessionEnd",
		"stop",
		"StopFailure",
		"PostToolUseFailure", "postToolUseFailure",
		"ErrorOccurred", "errorOccurred":
		return true
	default:
		return false
	}
}

func resolveNotificationAction(input string, payload HookPayload, harnessName string) (string, bool) {
	if !isNotificationEvent(input) {
		return "", false
	}
	switch normalizedNotificationType(payload) {
	case "permission_prompt":
		return "permission", true
	case "elicitation_dialog":
		// Copilot uses elicitation_dialog for plan approvals and other
		// user-input-needed prompts. Surface the question icon so the tab
		// distinguishes open user questions from permission gates.
		return "elicitation", true
	case "agent_idle":
		return "stop", true
	case "idle_prompt":
		// Claude's idle_prompt notification is its documented "Claude is done"
		// signal — an authoritative stop. Native Stop does not always fire
		// (notably after a user interruption), so this notification is Claude's
		// own terminal evidence. Gate it on the Claude harness: the type is
		// Claude-specific, and the harness-agnostic wrapper deliberately leaves
		// it as the generic idle fallback so a future harness reusing the name
		// is not silently promoted to stop.
		if isClaudeHarness(harnessName) {
			return "stop", true
		}
		return "", false
	default:
		return "", false
	}
}

// isClaudeHarness reports whether name identifies the Claude harness. Unlike
// harness.Normalize it does not default an empty name to Claude: the
// harness-agnostic ResolveAction wrapper must not apply Claude-specific
// notification semantics.
func isClaudeHarness(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), harness.Claude)
}

// isCopilotHarness reports whether name identifies the GitHub Copilot harness.
// Like isClaudeHarness it does not default an empty name to any harness: the
// agnostic wrapper must not apply Copilot-specific notification semantics.
func isCopilotHarness(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), harness.Copilot)
}

func isCodexHarness(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), harness.Codex)
}

func resolveToolAction(input string, payload HookPayload, harnessName string) (string, bool) {
	if normalizeLifecycleValue(payload.EffectiveToolName()) != "request_user_input" {
		return "", false
	}
	switch input {
	case "PreToolUse", "preToolUse":
		// Codex emits the tool-start hook before its interactive question is
		// rendered. Keep the native source active until the daemon's Codex
		// screen probe confirms that the prompt is actually answerable.
		if isCodexHarness(harnessName) {
			return "active", true
		}
		return "elicitation", true
	case "PostToolUse", "postToolUse":
		return "active", true
	default:
		return "", false
	}
}

func isNotificationEvent(input string) bool {
	return input == "notification" || input == "Notification"
}

func normalizedNotificationType(payload HookPayload) string {
	return normalizeLifecycleValue(payload.EffectiveNotificationType())
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
