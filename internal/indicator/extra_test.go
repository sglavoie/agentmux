package indicator

import "testing"

// TestResolveAction_CLAUDEMdEventCoverage pins that every hook event listed in
// CLAUDE.md (the canonical event set Agent Mux installs via hooks-sync)
// resolves to a non-empty action through ResolveAction. Any event that lands
// in the installed hooks file but isn't mapped here would silently no-op on
// fire, masking icon transitions the user expects.
//
// The list is intentionally a literal copy of the CLAUDE.md list, not derived
// from eventToAction — so a future change that drops an event from the table
// without removing it from CLAUDE.md surfaces as a test failure.
func TestResolveAction_CLAUDEMdEventCoverage(t *testing.T) {
	events := []string{
		"SessionStart",
		"Setup",
		"UserPromptSubmit",
		"UserPromptExpansion",
		"PreToolUse",
		"SubagentStart",
		"SubagentStop",
		"PostToolUse",
		"PostToolUseFailure",
		"PostToolBatch",
		"PermissionRequest",
		"PermissionDenied",
		"Stop",
		"StopFailure",
		"TaskCreated",
		"TaskCompleted",
		"TeammateIdle",
		"SessionEnd",
		"Notification",
		"ConfigChange",
		"PreCompact",
		"PostCompact",
		"Elicitation",
		"ElicitationResult",
	}
	for _, event := range events {
		action, ok := ResolveAction(event, HookPayload{})
		if !ok || action == "" {
			t.Errorf("CLAUDE.md hook event %q does not resolve to an action (got %q, ok=%v)", event, action, ok)
		}
	}
}

// TestResolveAction_CopilotCamelCaseParity covers Copilot's camelCase event
// names. Each one must resolve to an action so a Copilot harness session
// updates the tab icon consistently with a Claude session.
func TestResolveAction_CopilotCamelCaseParity(t *testing.T) {
	cases := []struct {
		event  string
		action string
	}{
		{"sessionStart", "idle"},
		{"userPromptSubmitted", "active"},
		{"preToolUse", "active"},
		{"postToolUse", "active"},
		{"subagentStart", "subagent"},
		{"subagentStop", "active"},
		{"preCompact", "compact"},
		{"agentStop", "stop"},
		{"sessionEnd", "stop"},
		{"permissionRequest", "permission"},
		{"postToolUseFailure", "failure"},
		{"errorOccurred", "failure"},
		{"notification", "idle"},
	}
	for _, tc := range cases {
		got, ok := ResolveAction(tc.event, HookPayload{})
		if !ok || got != tc.action {
			t.Errorf("ResolveAction(%q) = (%q, %v), want (%q, true)", tc.event, got, ok, tc.action)
		}
	}
}

func TestResolveAction_CodexPascalCaseParity(t *testing.T) {
	cases := []struct {
		event  string
		action string
	}{
		{"SessionStart", "idle"},
		{"UserPromptSubmit", "active"},
		{"PreToolUse", "active"},
		{"PermissionRequest", "permission"},
		{"PostToolUse", "active"},
		{"PreCompact", "compact"},
		{"PostCompact", "stop"},
		{"SubagentStart", "subagent"},
		{"SubagentStop", "active"},
		{"Stop", "stop"},
	}
	for _, tc := range cases {
		got, ok := ResolveAction(tc.event, HookPayload{})
		if !ok || got != tc.action {
			t.Errorf("ResolveAction(%q) = (%q, %v), want (%q, true)", tc.event, got, ok, tc.action)
		}
	}
}

// TestEventToAction_AllValuesAreValidActions enforces that every action listed
// in eventToAction is itself an entry in validActions. A drift between the
// two — e.g. renaming "stop_failure" without updating both — would let
// ResolveAction return an action the daemon then rejects as "unknown action".
func TestEventToAction_AllValuesAreValidActions(t *testing.T) {
	for event, action := range eventToAction {
		if !validActions[action] {
			t.Errorf("event %q maps to action %q which is not in validActions", event, action)
		}
	}
}
