package indicator

import "testing"

func TestResolveAction_DirectActions(t *testing.T) {
	for _, action := range []string{"active", "stop", "permission", "failure", "idle", "clear"} {
		got, ok := ResolveAction(action)
		if !ok || got != action {
			t.Errorf("ResolveAction(%q) = (%q, %v), want (%q, true)", action, got, ok, action)
		}
	}
}

func TestResolveAction_AllEvents(t *testing.T) {
	cases := []struct {
		event  string
		action string
	}{
		{"SessionStart", "idle"},
		{"UserPromptSubmit", "active"},
		{"PreToolUse", "active"},
		{"SubagentStart", "active"},
		{"PreCompact", "active"},
		{"PostToolUse", "active"},
		{"SubagentStop", "active"},
		{"TaskCompleted", "active"},
		{"Stop", "stop"},
		{"SessionEnd", "stop"},
		{"PermissionRequest", "permission"},
		{"PostToolUseFailure", "failure"},
		{"Notification", "idle"},
		{"TeammateIdle", "idle"},
		{"ConfigChange", "clear"},
		{"WorktreeCreate", "clear"},
		{"WorktreeRemove", "clear"},
	}
	for _, tc := range cases {
		got, ok := ResolveAction(tc.event)
		if !ok || got != tc.action {
			t.Errorf("ResolveAction(%q) = (%q, %v), want (%q, true)", tc.event, got, ok, tc.action)
		}
	}
}

func TestResolveAction_UnknownEventIgnored(t *testing.T) {
	_, ok := ResolveAction("BogusEvent")
	if ok {
		t.Error("unknown event should return ok=false")
	}
}

func TestResolveAction_AllEventsHaveValidActions(t *testing.T) {
	for event, action := range eventToAction {
		if !validActions[action] {
			t.Errorf("event %q maps to unknown action %q", event, action)
		}
	}
}

func TestPreservesCurrent(t *testing.T) {
	cases := []struct {
		event string
		want  bool
	}{
		{"SubagentStop", true},
		{"TaskCompleted", true},
		{"UserPromptSubmit", false},
		{"Stop", false},
		{"PostToolUse", false},
	}
	for _, tc := range cases {
		if got := PreservesCurrent(tc.event); got != tc.want {
			t.Errorf("PreservesCurrent(%q) = %v, want %v", tc.event, got, tc.want)
		}
	}
}

func TestIsPromptEvent(t *testing.T) {
	cases := []struct {
		event string
		want  bool
	}{
		{"UserPromptSubmit", true},
		{"Stop", false},
		{"SubagentStop", false},
		{"SessionStart", false},
	}
	for _, tc := range cases {
		if got := IsPromptEvent(tc.event); got != tc.want {
			t.Errorf("IsPromptEvent(%q) = %v, want %v", tc.event, got, tc.want)
		}
	}
}
