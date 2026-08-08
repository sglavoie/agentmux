package hookevent

import "testing"

func TestResolveAction_DirectActions(t *testing.T) {
	for _, action := range []string{"active", "stop", "permission", "failure", "stop_failure", "subagent", "compact", "elicitation", "idle", "clear", "user_clear", "loop_marker"} {
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
		{"AgentSessionLoopStarted", "loop_marker"},
		{"AgentSessionLoopTurnStarted", "loop_marker"},
		{"AgentSessionLoopTurnHangSuspected", "loop_marker"},
		{"AgentSessionLoopTurnHangCleared", "loop_marker"},
		{"AgentSessionLoopWaitStarted", "loop_marker"},
		{"AgentSessionLoopWaitEnded", "loop_marker"},
		{"AgentSessionLoopMailboxQuestion", "loop_marker"},
		{"AgentSessionLoopMailboxReply", "loop_marker"},
		{"AgentSessionLoopMailboxResolution", "loop_marker"},
		{"AgentSessionLoopFinished", "idle"},
		{"Setup", "active"},
		{"UserPromptSubmit", "active"},
		{"UserPromptExpansion", "active"},
		{"PreToolUse", "active"},
		{"SubagentStart", "subagent"},
		{"PreCompact", "compact"},
		{"PostCompact", "stop"},
		{"PostCompactManual", "stop"},
		{"PostCompactAuto", "active"},
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
		{
			name:    "idle_prompt without harness context stays generic idle",
			payload: HookPayload{NotificationType: "idle_prompt"},
			want:    "idle",
			ok:      true,
		},
		{
			name:    "notification type is normalized before resolving",
			payload: HookPayload{NotificationTypeCamel: " Permission_Prompt "},
			want:    "permission",
			ok:      true,
		},
	}
	for _, tc := range cases {
		got, ok := ResolveAction("notification", tc.payload)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: ResolveAction(notification, %+v) = (%q, %v), want (%q, %v)", tc.name, tc.payload, got, ok, tc.want, tc.ok)
		}
	}
}

// TestResolveActionForHarness_ClaudeIdlePrompt locks in the harness-aware
// notification contract: Claude's idle_prompt is an authoritative stop, but
// only when the harness is Claude. Other harnesses (and the agnostic wrapper)
// leave the unrecognized notification type as the generic idle fallback so a
// future harness reusing the name is not silently promoted to stop.
func TestResolveActionForHarness_ClaudeIdlePrompt(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		harness string
		payload HookPayload
		want    string
		ok      bool
	}{
		{
			name:    "claude idle_prompt resolves to stop",
			event:   "Notification",
			harness: "claude",
			payload: HookPayload{NotificationType: "idle_prompt"},
			want:    "stop",
			ok:      true,
		},
		{
			name:    "claude idle_prompt camel + mixed case",
			event:   "notification",
			harness: "Claude",
			payload: HookPayload{NotificationTypeCamel: " Idle_Prompt "},
			want:    "stop",
			ok:      true,
		},
		{
			name:    "codex idle_prompt falls back to generic idle, never stop",
			event:   "Notification",
			harness: "codex",
			payload: HookPayload{NotificationType: "idle_prompt"},
			want:    "idle",
			ok:      true,
		},
		{
			name:    "empty harness idle_prompt falls back to generic idle, never stop",
			event:   "Notification",
			harness: "",
			payload: HookPayload{NotificationType: "idle_prompt"},
			want:    "idle",
			ok:      true,
		},
		{
			name:    "claude permission_prompt is unchanged by harness",
			event:   "Notification",
			harness: "claude",
			payload: HookPayload{NotificationType: "permission_prompt"},
			want:    "permission",
			ok:      true,
		},
		{
			name:    "claude agent_idle stays stop",
			event:   "Notification",
			harness: "claude",
			payload: HookPayload{NotificationType: "agent_idle"},
			want:    "stop",
			ok:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveActionForHarness(tc.event, tc.payload, tc.harness)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("ResolveActionForHarness(%q, %+v, %q) = (%q, %v), want (%q, %v)", tc.event, tc.payload, tc.harness, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestResolveActionForHarness_CopilotAgentIdleIgnored locks in the harness-aware
// notification contract for Copilot: agent_idle is a background-agent idle
// signal, not a main-session stop, so it is ignored for Copilot while remaining
// a stop for Claude and the agnostic wrapper. Native agentStop/sessionEnd stay
// the primary terminal signals for Copilot.
func TestResolveActionForHarness_CopilotAgentIdleIgnored(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		harness string
		payload HookPayload
		want    string
		ok      bool
	}{
		{
			name:    "copilot agent_idle is ignored, never stop",
			event:   "notification",
			harness: "copilot",
			payload: HookPayload{NotificationType: "agent_idle"},
			ok:      false,
		},
		{
			name:    "copilot agent_idle camel + mixed case is ignored",
			event:   "Notification",
			harness: "Copilot",
			payload: HookPayload{NotificationTypeCamel: " Agent_Idle "},
			ok:      false,
		},
		{
			name:    "copilot agentStop remains the primary terminal stop",
			event:   "agentStop",
			harness: "copilot",
			payload: HookPayload{StopReasonCamel: "end_turn"},
			want:    "stop",
			ok:      true,
		},
		{
			name:    "copilot sessionEnd remains a stop",
			event:   "sessionEnd",
			harness: "copilot",
			want:    "stop",
			ok:      true,
		},
		{
			name:    "claude agent_idle still resolves to stop",
			event:   "Notification",
			harness: "claude",
			payload: HookPayload{NotificationType: "agent_idle"},
			want:    "stop",
			ok:      true,
		},
		{
			name:    "agnostic agent_idle still resolves to stop",
			event:   "notification",
			harness: "",
			payload: HookPayload{NotificationType: "agent_idle"},
			want:    "stop",
			ok:      true,
		},
		{
			name:    "copilot permission_prompt is unaffected",
			event:   "notification",
			harness: "copilot",
			payload: HookPayload{NotificationType: "permission_prompt"},
			want:    "permission",
			ok:      true,
		},
		{
			name:    "copilot elicitation_dialog is unaffected",
			event:   "notification",
			harness: "copilot",
			payload: HookPayload{NotificationType: "elicitation_dialog"},
			want:    "elicitation",
			ok:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveActionForHarness(tc.event, tc.payload, tc.harness)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("ResolveActionForHarness(%q, %+v, %q) = (%q, %v), want (%q, %v)", tc.event, tc.payload, tc.harness, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestIsIgnoredEventForHarness pins the harness-aware ignore rule: Copilot
// agent_idle is ignored (a background-agent notification), but the agnostic and
// Claude variants are not, so their agent_idle still resolves to a stop.
func TestIsIgnoredEventForHarness(t *testing.T) {
	cases := []struct {
		event   string
		harness string
		payload HookPayload
		want    bool
	}{
		{"Notification", "copilot", HookPayload{NotificationType: "agent_idle"}, true},
		{"notification", "Copilot", HookPayload{NotificationTypeCamel: " Agent_Idle "}, true},
		{"Notification", "claude", HookPayload{NotificationType: "agent_idle"}, false},
		{"Notification", "", HookPayload{NotificationType: "agent_idle"}, false},
		{"Notification", "copilot", HookPayload{NotificationType: "permission_prompt"}, false},
		{"Notification", "copilot", HookPayload{NotificationType: "shell_completed"}, true},
		{"InstructionsLoaded", "copilot", HookPayload{}, true},
	}
	for _, tc := range cases {
		if got := IsIgnoredEventForHarness(tc.event, tc.payload, tc.harness); got != tc.want {
			t.Errorf("IsIgnoredEventForHarness(%q, %+v, %q) = %v, want %v", tc.event, tc.payload, tc.harness, got, tc.want)
		}
	}
}

func TestResolveAction_PostCompactTrigger(t *testing.T) {
	cases := []struct {
		name    string
		trigger string
		want    string
	}{
		{name: "automatic compaction resumes active turn", trigger: "auto", want: "active"},
		{name: "manual compaction stops at prompt", trigger: "manual", want: "stop"},
		{name: "trigger is normalized", trigger: " Auto \t", want: "active"},
		{name: "missing trigger keeps conservative fallback", want: "stop"},
		{name: "unknown trigger keeps conservative fallback", trigger: "scheduled", want: "stop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveAction("PostCompact", HookPayload{Trigger: tc.trigger})
			if !ok || got != tc.want {
				t.Fatalf("ResolveAction(PostCompact, trigger=%q) = (%q, %v), want (%q, true)", tc.trigger, got, ok, tc.want)
			}
		})
	}
}

func TestResolveAction_RequestUserInputToolPrompt(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		payload HookPayload
		want    string
	}{
		{
			name:    "harness-agnostic request_user_input start shows question icon",
			event:   "PreToolUse",
			payload: HookPayload{ToolName: "request_user_input"},
			want:    "elicitation",
		},
		{
			name:    "camel case tool name is normalized",
			event:   "preToolUse",
			payload: HookPayload{ToolNameCamel: " request_user_input "},
			want:    "elicitation",
		},
		{
			name:    "request_user_input completion resumes active icon",
			event:   "PostToolUse",
			payload: HookPayload{ToolName: "request_user_input"},
			want:    "active",
		},
		{
			name:    "ordinary tools remain active",
			event:   "PreToolUse",
			payload: HookPayload{ToolName: "Bash"},
			want:    "active",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveAction(tc.event, tc.payload)
			if !ok || got != tc.want {
				t.Fatalf("ResolveAction(%q, %+v) = (%q, %v), want (%q, true)", tc.event, tc.payload, got, ok, tc.want)
			}
		})
	}
}

func TestResolveActionForHarness_CodexRequestUserInputWaitsForScreen(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		harness string
		payload HookPayload
		want    string
	}{
		{
			name:    "Codex tool start stays active before prompt renders",
			event:   "PreToolUse",
			harness: "codex",
			payload: HookPayload{ToolName: "request_user_input"},
			want:    "active",
		},
		{
			name:    "Codex camel-case tool start stays active",
			event:   "preToolUse",
			harness: " CODEX ",
			payload: HookPayload{ToolNameCamel: " request_user_input "},
			want:    "active",
		},
		{
			name:    "Codex completion remains active",
			event:   "PostToolUse",
			harness: "codex",
			payload: HookPayload{ToolName: "request_user_input"},
			want:    "active",
		},
		{
			name:    "Claude keeps native question semantics",
			event:   "PreToolUse",
			harness: "claude",
			payload: HookPayload{ToolName: "request_user_input"},
			want:    "elicitation",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveActionForHarness(tc.event, tc.payload, tc.harness)
			if !ok || got != tc.want {
				t.Fatalf(
					"ResolveActionForHarness(%q, %+v, %q) = (%q, %v), want (%q, true)",
					tc.event, tc.payload, tc.harness, got, ok, tc.want,
				)
			}
		})
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

func TestResolveAction_DirectStopContextLimitShowsStopFailure(t *testing.T) {
	cases := []struct {
		name    string
		payload HookPayload
	}{
		{
			name:    "stop reason marker",
			payload: HookPayload{StopReason: " context_limit_reached "},
		},
		{
			name: "payload text marker",
			payload: HookPayload{
				Title:   "Context limit reached",
				Message: "/compact or /clear to continue",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveAction("stop", tc.payload)
			if !ok || got != "stop_failure" {
				t.Fatalf("ResolveAction(stop, %+v) = (%q, %v), want (stop_failure, true)", tc.payload, got, ok)
			}
		})
	}
}

// TestResolveAction_NormalizesStopReasonAndReason locks in the contract that
// the failure-promotion checks tolerate the casing and whitespace harnesses
// occasionally include in JSON lifecycle fields. Without normalization, a
// stop event carrying a trailing-space "end_turn " — which is structurally a
// clean turn end — was being promoted to stop_failure and painting the error
// icon on top of every successful Codex/Claude completion that round-tripped
// through such a payload. The matching SessionEnd path normalizes "Error"
// and " timeout " the same way so the icon transitions stay consistent.
func TestResolveAction_NormalizesStopReasonAndReason(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		payload HookPayload
		want    string
	}{
		{name: "stop with trailing whitespace end_turn stays stop", event: "Stop", payload: HookPayload{StopReason: "end_turn "}, want: "stop"},
		{name: "stop with mixed case end_turn stays stop", event: "Stop", payload: HookPayload{StopReason: "End_Turn"}, want: "stop"},
		{name: "agentStop with whitespace end_turn stays stop", event: "agentStop", payload: HookPayload{StopReasonCamel: " end_turn "}, want: "stop"},
		{name: "stop with non-end_turn whitespace still promotes", event: "Stop", payload: HookPayload{StopReason: " context_limit_reached "}, want: "stop_failure"},
		{name: "session end uppercase Error promotes", event: "SessionEnd", payload: HookPayload{Reason: "Error"}, want: "stop_failure"},
		{name: "session end padded timeout promotes", event: "sessionEnd", payload: HookPayload{Reason: " timeout "}, want: "stop_failure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveAction(tc.event, tc.payload)
			if !ok {
				t.Fatalf("ResolveAction(%q, %+v) = ok=false, want %q", tc.event, tc.payload, tc.want)
			}
			if got != tc.want {
				t.Fatalf("ResolveAction(%q, %+v) = %q, want %q", tc.event, tc.payload, got, tc.want)
			}
		})
	}
}

func TestResolveAction_UnknownEventIgnored(t *testing.T) {
	_, ok := ResolveAction("BogusEvent", HookPayload{})
	if ok {
		t.Error("unknown event should return ok=false")
	}
}

// TestResolveAction_ContextLimitOnlyPromotesForFailureCarriers locks in the
// rule that the "context limit reached" string in a payload only promotes the
// action to stop_failure for events that legitimately describe a stop
// condition. A stray copy of the recovery hint in an unrelated event's title
// or message (Setup, PreToolUse, ConfigChange, or an unknown event) must not
// flip the icon to the sticky error state.
func TestResolveAction_ContextLimitOnlyPromotesForFailureCarriers(t *testing.T) {
	ctxPayload := HookPayload{
		Title:   "Context limit reached",
		Message: "/compact or /clear to continue",
	}
	cases := []struct {
		name      string
		event     string
		wantOK    bool
		wantValue string
	}{
		{name: "notification still promotes", event: "notification", wantOK: true, wantValue: "stop_failure"},
		{name: "Stop still promotes", event: "Stop", wantOK: true, wantValue: "stop_failure"},
		{name: "SessionEnd still promotes", event: "SessionEnd", wantOK: true, wantValue: "stop_failure"},
		{name: "direct stop still promotes", event: "stop", wantOK: true, wantValue: "stop_failure"},
		{name: "Setup keeps active", event: "Setup", wantOK: true, wantValue: "active"},
		{name: "PreToolUse keeps active", event: "PreToolUse", wantOK: true, wantValue: "active"},
		{name: "ConfigChange keeps clear", event: "ConfigChange", wantOK: true, wantValue: "clear"},
		{name: "unknown event stays unknown", event: "BogusEvent", wantOK: false, wantValue: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveAction(tc.event, ctxPayload)
			if ok != tc.wantOK || got != tc.wantValue {
				t.Fatalf("ResolveAction(%q, ctxPayload) = (%q, %v), want (%q, %v)", tc.event, got, ok, tc.wantValue, tc.wantOK)
			}
		})
	}
}

func TestIsContextLimitCarrier(t *testing.T) {
	carriers := []string{
		"Notification", "notification",
		"Stop", "agentStop",
		"SubagentStop", "subagentStop",
		"SessionEnd", "sessionEnd",
		"stop",
		"StopFailure",
		"PostToolUseFailure", "postToolUseFailure",
		"ErrorOccurred", "errorOccurred",
	}
	nonCarriers := []string{
		"Setup", "PreToolUse", "PostToolUse", "UserPromptSubmit",
		"ConfigChange", "PreCompact", "PostCompact",
		"SessionStart", "sessionStart",
		"BogusEvent", "",
	}
	for _, e := range carriers {
		if !isContextLimitCarrier(e) {
			t.Errorf("isContextLimitCarrier(%q) = false, want true", e)
		}
	}
	for _, e := range nonCarriers {
		if isContextLimitCarrier(e) {
			t.Errorf("isContextLimitCarrier(%q) = true, want false", e)
		}
	}
}

func TestIsIgnoredEvent(t *testing.T) {
	cases := []struct {
		event   string
		payload HookPayload
		want    bool
	}{
		{"InstructionsLoaded", HookPayload{}, true},
		{"MessageDisplay", HookPayload{}, true},
		{"CwdChanged", HookPayload{}, true},
		{"FileChanged", HookPayload{}, true},
		{"WorktreeCreate", HookPayload{}, true},
		{"WorktreeRemove", HookPayload{}, true},
		{"Notification", HookPayload{NotificationType: "shell_completed"}, true},
		{"Notification", HookPayload{NotificationTypeCamel: " Shell_Completed "}, true},
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
