package hookevent

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
)

// TestResolveAction_InstalledClaudeEventCoverage pins that every state-changing
// event the Claude hook contract installs through hooks-sync resolves to a
// non-empty action through ResolveAction. An event that lands in the installed
// hooks file but isn't mapped here would silently no-op on fire, masking icon
// transitions the user expects.
//
// The events are read from the contract rather than copied here, so adding an
// installed event without mapping it fails this test. Observer-only events are
// recorded without changing state and need no action.
func TestResolveAction_InstalledClaudeEventCoverage(t *testing.T) {
	var events []string
	for _, event := range harness.Lookup(harness.Claude).HookContract().EventMatrix() {
		if event.Installation == harness.HookInstalled && event.Behavior == harness.HookStateChanging {
			events = append(events, event.CanonicalName)
		}
	}
	// Guards against a contract change that would leave nothing to check.
	if len(events) < 20 {
		t.Fatalf("Claude contract installs %d state-changing events, want the full set: %v", len(events), events)
	}
	for _, event := range events {
		action, ok := ResolveAction(event, HookPayload{})
		if !ok || action == "" {
			t.Errorf("installed Claude hook event %q does not resolve to an action (got %q, ok=%v)", event, action, ok)
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
