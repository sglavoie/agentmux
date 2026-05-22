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
		{"agentStop", "stop"},
		{"StopFailure", "stop_failure"},
		{"PermissionRequest", "permission"},
		{"PermissionDenied", "permission"},
		{"Elicitation", "elicitation"},
		{"PostToolUseFailure", "failure"},
		{"Notification", "idle"},
		{"TeammateIdle", "active"},
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
			name:    "agent idle notification shows stop icon",
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

func TestResolveAction_ContextLimitNotificationShowsStopFailure(t *testing.T) {
	payload := HookPayload{
		Title:   "Context limit reached",
		Message: "/compact or /clear to continue",
	}
	got, ok := ResolveAction("notification", payload)
	if !ok || got != "stop_failure" {
		t.Fatalf("ResolveAction(notification, %+v) = (%q, %v), want (stop_failure, true)", payload, got, ok)
	}
}

func TestResolveAction_SessionEndErrorShowsStopFailure(t *testing.T) {
	got, ok := ResolveAction("sessionEnd", HookPayload{Reason: "error"})
	if !ok || got != "stop_failure" {
		t.Fatalf("ResolveAction(sessionEnd, error) = (%q, %v), want (stop_failure, true)", got, ok)
	}
}

func TestResolveAction_AgentStopNonEndTurnShowsStopFailure(t *testing.T) {
	got, ok := ResolveAction("agentStop", HookPayload{StopReasonCamel: "context_limit_reached"})
	if !ok || got != "stop_failure" {
		t.Fatalf("ResolveAction(agentStop, non-end_turn) = (%q, %v), want (stop_failure, true)", got, ok)
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
		{"FileChanged", HookPayload{}, true},
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
		{"agentStop", false},
		{"TeammateIdle", true},
		{"Notification", false},
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

func TestHasContextLimitMarker(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "exact message",
			text: "Context limit reached · /compact or /clear to continue",
			want: true,
		},
		{
			name: "window full variant",
			text: "Context window is full\n/compact or /clear to continue",
			want: true,
		},
		{
			name: "missing recovery hint",
			text: "Context limit reached",
			want: false,
		},
		{
			name: "unrelated text",
			text: "All good here",
			want: false,
		},
	}
	for _, tc := range cases {
		if got := hasContextLimitMarker(tc.text); got != tc.want {
			t.Errorf("%s: hasContextLimitMarker(%q) = %v, want %v", tc.name, tc.text, got, tc.want)
		}
	}
}

func TestScreenShowsContextLimit(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "warning at screen tail",
			text: "earlier line\n\nContext limit reached · /compact or /clear to continue\n\n✻ Cooked for 17m 6s\n",
			want: true,
		},
		{
			name: "quoted text earlier in scrollback does not trip fallback",
			text: "Context limit reached · /compact or /clear to continue\nuser was discussing the warning\n\nnormal latest output\nstill running fine\n",
			want: false,
		},
	}
	for _, tc := range cases {
		if got := screenShowsContextLimit(tc.text); got != tc.want {
			t.Errorf("%s: screenShowsContextLimit(%q) = %v, want %v", tc.name, tc.text, got, tc.want)
		}
	}
}

func TestResolveAction_CopilotTurnEndIsStop(t *testing.T) {
	// Regression: commit 5858318 had reclassified agentStop as a non-terminal
	// active+preserve-current event. That broke the icon transition because
	// agentStop is in fact Copilot's "main agent finishes a turn" — the
	// direct analogue of Claude's Stop. Pin the correct mapping.
	got, ok := ResolveAction("agentStop", HookPayload{StopReasonCamel: "end_turn"})
	if !ok || got != "stop" {
		t.Fatalf("ResolveAction(agentStop, end_turn) = (%q, %v), want (stop, true)", got, ok)
	}
	if PreservesCurrent("agentStop") {
		t.Error("PreservesCurrent(agentStop) = true, want false")
	}
}

func TestShouldProbeWindowForContextLimit(t *testing.T) {
	cases := []struct {
		event  string
		action string
		want   bool
	}{
		{"TaskCompleted", "active", true},
		{"agentStop", "active", true},
		{"notification", "idle", true},
		{"UserPromptSubmit", "active", false},
		{"StopFailure", "stop_failure", false},
		{"stop_failure", "stop_failure", false},
	}
	for _, tc := range cases {
		if got := shouldProbeWindowForContextLimit(tc.event, tc.action); got != tc.want {
			t.Errorf("shouldProbeWindowForContextLimit(%q, %q) = %v, want %v", tc.event, tc.action, got, tc.want)
		}
	}
}
