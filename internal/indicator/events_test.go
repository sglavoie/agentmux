package indicator

import "testing"

func TestResolveAction_DirectActions(t *testing.T) {
	for _, action := range []string{"active", "stop", "permission", "failure", "stop_failure", "subagent", "compact", "idle", "clear", "user_clear"} {
		got, ok := ResolveAction(action, HookPayload{})
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
		{"Setup", "active"},
		{"UserPromptSubmit", "active"},
		{"UserPromptExpansion", "active"},
		{"PreToolUse", "active"},
		{"SubagentStart", "subagent"},
		{"PreCompact", "compact"},
		{"PostCompact", "stop"},
		{"PostToolUse", "active"},
		{"PostToolBatch", "active"},
		{"SubagentStop", "active"},
		{"TaskCreated", "active"},
		{"TaskCompleted", "active"},
		{"ElicitationResult", "active"},
		{"Stop", "stop"},
		{"SessionEnd", "stop"},
		{"StopFailure", "stop_failure"},
		{"PermissionRequest", "permission"},
		{"PermissionDenied", "permission"},
		{"Elicitation", "elicitation"},
		{"PostToolUseFailure", "failure"},
		{"Notification", "idle"},
		{"TeammateIdle", "stop"},
		{"ConfigChange", "clear"},
		{"WorktreeCreate", "clear"},
		{"WorktreeRemove", "clear"},
	}
	for _, tc := range cases {
		got, ok := ResolveAction(tc.event, HookPayload{})
		if !ok || got != tc.action {
			t.Errorf("ResolveAction(%q) = (%q, %v), want (%q, true)", tc.event, got, ok, tc.action)
		}
	}
}

func TestResolveAction_NotificationPayloads(t *testing.T) {
	cases := []struct {
		name    string
		payload HookPayload
		want    string
		ok      bool
	}{
		{
			name:    "permission prompt notification shows key icon",
			payload: HookPayload{NotificationType: "permission_prompt"},
			want:    "permission",
			ok:      true,
		},
		{
			name:    "elicitation notification shows question icon",
			payload: HookPayload{NotificationType: "elicitation_dialog"},
			want:    "elicitation",
			ok:      true,
		},
		{
			name:    "agent idle notification maps to stop",
			payload: HookPayload{NotificationType: "agent_idle"},
			want:    "stop",
			ok:      true,
		},
		{
			name:    "background shell completion is ignored",
			payload: HookPayload{NotificationType: "shell_completed"},
			ok:      false,
		},
	}
	for _, tc := range cases {
		got, ok := ResolveAction("notification", tc.payload)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: ResolveAction(notification, %+v) = (%q, %v), want (%q, %v)", tc.name, tc.payload, got, ok, tc.want, tc.ok)
		}
	}
}

func TestResolveAction_UnknownEventIgnored(t *testing.T) {
	_, ok := ResolveAction("BogusEvent", HookPayload{})
	if ok {
		t.Error("unknown event should return ok=false")
	}
}

func TestIsIgnoredEvent(t *testing.T) {
	cases := []struct {
		event   string
		payload HookPayload
		want    bool
	}{
		{"InstructionsLoaded", HookPayload{}, true},
		{"CwdChanged", HookPayload{}, true},
		{"Notification", HookPayload{NotificationType: "shell_completed"}, true},
		{"Notification", HookPayload{NotificationType: "agent_idle"}, false},
		{"Notification", HookPayload{}, false},
	}
	for _, tc := range cases {
		if got := IsIgnoredEvent(tc.event, tc.payload); got != tc.want {
			t.Errorf("IsIgnoredEvent(%q, %+v) = %v, want %v", tc.event, tc.payload, got, tc.want)
		}
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
		{"SubagentStop", false},
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
		{"UserPromptExpansion", true},
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
