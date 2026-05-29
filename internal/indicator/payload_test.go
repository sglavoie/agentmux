package indicator

import (
	"strings"
	"testing"
)

func TestDecodeHookPayload_Empty(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(" \n\t "))
	if err != nil {
		t.Fatalf("DecodeHookPayload(empty) error: %v", err)
	}
	if payload != (HookPayload{}) {
		t.Fatalf("DecodeHookPayload(empty) = %+v, want zero payload", payload)
	}
}

func TestDecodeHookPayload_ParsesNotificationFields(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{
		"hook_event_name": "Notification",
		"notification_type": "agent_idle",
		"title": "Claude finished",
		"message": "Waiting for input"
	}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(notification) error: %v", err)
	}
	if payload.HookEventName != "Notification" {
		t.Fatalf("HookEventName = %q, want Notification", payload.HookEventName)
	}
	if payload.EffectiveNotificationType() != "agent_idle" {
		t.Fatalf("EffectiveNotificationType = %q, want agent_idle", payload.EffectiveNotificationType())
	}
	if payload.Title != "Claude finished" || payload.Message != "Waiting for input" {
		t.Fatalf("unexpected decoded notification payload: %+v", payload)
	}
}

func TestDecodeHookPayload_ParsesToolName(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{
		"hook_event_name": "PreToolUse",
		"tool_name": "Bash"
	}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(pretooluse) error: %v", err)
	}
	if payload.HookEventName != "PreToolUse" {
		t.Fatalf("HookEventName = %q, want PreToolUse", payload.HookEventName)
	}
	if payload.ToolName != "Bash" {
		t.Fatalf("ToolName = %q, want Bash", payload.ToolName)
	}
}

func TestDecodeHookPayload_OmittedToolNameIsEmpty(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{"hook_event_name": "SessionStart"}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(session_start) error: %v", err)
	}
	if payload.ToolName != "" {
		t.Fatalf("ToolName = %q, want empty", payload.ToolName)
	}
}

func TestDecodeHookPayload_ParsesReasonFields(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{
		"hook_event_name": "SessionEnd",
		"reason": "error",
		"stop_reason": "context_limit_reached"
	}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(reason fields) error: %v", err)
	}
	if payload.Reason != "error" {
		t.Fatalf("Reason = %q, want error", payload.Reason)
	}
	if payload.EffectiveStopReason() != "context_limit_reached" {
		t.Fatalf("EffectiveStopReason = %q, want context_limit_reached", payload.EffectiveStopReason())
	}
}

func TestDecodeHookPayload_ParsesPromptAndSource(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{
		"hook_event_name": "SessionStart",
		"prompt": "/clear workspace",
		"source": "clear"
	}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(prompt/source) error: %v", err)
	}
	if payload.Prompt != "/clear workspace" {
		t.Fatalf("Prompt = %q, want /clear workspace", payload.Prompt)
	}
	if payload.Source != "clear" {
		t.Fatalf("Source = %q, want clear", payload.Source)
	}
}

func TestDecodeHookPayload_PreservesCodexFields(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{
		"hook_event_name": "Stop",
		"tool_name": "Bash",
		"source": "startup",
		"prompt": "/compact now",
		"stop_reason": "end_turn"
	}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(codex fields) error: %v", err)
	}
	if payload.HookEventName != "Stop" {
		t.Fatalf("HookEventName = %q, want Stop", payload.HookEventName)
	}
	if payload.EffectiveToolName() != "Bash" {
		t.Fatalf("EffectiveToolName = %q, want Bash", payload.EffectiveToolName())
	}
	if payload.Source != "startup" {
		t.Fatalf("Source = %q, want startup", payload.Source)
	}
	if payload.EffectivePrompt() != "/compact now" {
		t.Fatalf("EffectivePrompt = %q, want /compact now", payload.EffectivePrompt())
	}
	if payload.EffectiveStopReason() != "end_turn" {
		t.Fatalf("EffectiveStopReason = %q, want end_turn", payload.EffectiveStopReason())
	}
}

func TestHookPayload_EffectivePrompt(t *testing.T) {
	cases := []struct {
		name    string
		payload HookPayload
		want    string
	}{
		{name: "prompt wins", payload: HookPayload{Prompt: "/clear"}, want: "/clear"},
		{name: "blank prompt falls back", payload: HookPayload{Prompt: " \t", InitialPrompt: "/reset"}, want: "/reset"},
		{name: "snake initial prompt fallback", payload: HookPayload{InitialPrompt: "/reset"}, want: "/reset"},
		{name: "blank snake initial prompt falls back", payload: HookPayload{InitialPrompt: " \n", InitialPromptCamel: "/new"}, want: "/new"},
		{name: "camel initial prompt fallback", payload: HookPayload{InitialPromptCamel: "/new"}, want: "/new"},
		{name: "empty prompt", payload: HookPayload{}, want: ""},
	}
	for _, tc := range cases {
		if got := tc.payload.EffectivePrompt(); got != tc.want {
			t.Errorf("%s: EffectivePrompt() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestHookPayload_EffectiveToolName(t *testing.T) {
	cases := []struct {
		name    string
		payload HookPayload
		want    string
	}{
		{name: "snake case tool name", payload: HookPayload{ToolName: "Bash"}, want: "Bash"},
		{name: "blank snake case tool name falls back", payload: HookPayload{ToolName: " ", ToolNameCamel: "bash"}, want: "bash"},
		{name: "camel case tool name", payload: HookPayload{ToolNameCamel: "bash"}, want: "bash"},
		{name: "empty tool name", payload: HookPayload{}, want: ""},
	}
	for _, tc := range cases {
		if got := tc.payload.EffectiveToolName(); got != tc.want {
			t.Errorf("%s: EffectiveToolName() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestHookPayload_EffectiveLifecycleFieldsSkipBlankPreferredValue(t *testing.T) {
	payload := HookPayload{
		NotificationType:      " ",
		NotificationTypeCamel: "permission_prompt",
		StopReason:            "\t",
		StopReasonCamel:       "context_limit_reached",
	}
	if got := payload.EffectiveNotificationType(); got != "permission_prompt" {
		t.Fatalf("EffectiveNotificationType() = %q, want permission_prompt", got)
	}
	if got := payload.EffectiveStopReason(); got != "context_limit_reached" {
		t.Fatalf("EffectiveStopReason() = %q, want context_limit_reached", got)
	}
}

func TestPayloadHasResetFlow(t *testing.T) {
	cases := []struct {
		name    string
		harness string
		event   string
		payload HookPayload
		want    bool
	}{
		{name: "claude clear session end", harness: "claude", event: "SessionEnd", payload: HookPayload{Reason: "clear"}, want: true},
		{name: "claude clear session start", harness: "claude", event: "SessionStart", payload: HookPayload{Source: "clear"}, want: true},
		{name: "copilot clear command", harness: "copilot", event: "userPromptSubmitted", payload: HookPayload{Prompt: "/clear"}, want: true},
		{name: "copilot reset command with label", harness: "copilot", event: "userPromptSubmitted", payload: HookPayload{Prompt: "/reset worktree cleanup"}, want: true},
		{name: "copilot new command with trailing text", harness: "copilot", event: "userPromptSubmitted", payload: HookPayload{Prompt: " /new scratch session "}, want: true},
		{name: "copilot new session start", harness: "copilot", event: "sessionStart", payload: HookPayload{Source: "new"}, want: true},
		{name: "copilot user exit session end", harness: "copilot", event: "sessionEnd", payload: HookPayload{Reason: "user_exit"}, want: true},
		{name: "claude compact source does not match", harness: "claude", event: "SessionStart", payload: HookPayload{Source: "compact"}, want: false},
		{name: "claude synthetic initial prompt no longer matches", harness: "claude", event: "SessionStart", payload: HookPayload{InitialPromptCamel: "/new scratch"}, want: false},
		{name: "copilot session end reason clear does not match", harness: "copilot", event: "sessionEnd", payload: HookPayload{Reason: "clear"}, want: false},
		{name: "notification source clear does not match", harness: "copilot", event: "notification", payload: HookPayload{Source: "clear"}, want: false},
		{name: "partial command does not match", harness: "copilot", event: "userPromptSubmitted", payload: HookPayload{Prompt: "/clearer"}, want: false},
		{name: "inline mention does not match", harness: "copilot", event: "userPromptSubmitted", payload: HookPayload{Prompt: "please /clear this"}, want: false},
		{name: "normal prompt", harness: "copilot", event: "userPromptSubmitted", payload: HookPayload{Prompt: "summarize this diff"}, want: false},
		{name: "normal end reason", harness: "claude", event: "SessionEnd", payload: HookPayload{Reason: "user_exit"}, want: false},
	}
	for _, tc := range cases {
		if got := payloadHasResetFlow(tc.harness, tc.event, tc.payload); got != tc.want {
			t.Errorf("%s: payloadHasResetFlow(%q, %q, %+v) = %v, want %v", tc.name, tc.harness, tc.event, tc.payload, got, tc.want)
		}
	}
}

func TestDecodeHookPayload_InvalidJSON(t *testing.T) {
	if _, err := DecodeHookPayload(strings.NewReader("{")); err == nil {
		t.Fatal("DecodeHookPayload(invalid JSON) error = nil, want non-nil")
	}
}

// TestDecodeHookPayload_RejectsOversizedInput pins the upper bound on the
// hook payload reader. The daemon already caps requests at 1 MiB and a hook
// script feeding multi-GiB JSON would otherwise force the indicator process
// to buffer the whole stream before that limit could trigger.
func TestDecodeHookPayload_RejectsOversizedInput(t *testing.T) {
	// Craft a payload that is just over the limit by padding the "message"
	// field. Even though the JSON is structurally valid, the byte size
	// alone should trigger rejection before we attempt to unmarshal.
	padding := strings.Repeat("a", maxHookPayloadBytes)
	oversized := `{"message":"` + padding + `"}`
	if len(oversized) <= maxHookPayloadBytes {
		t.Fatalf("test setup: oversized payload (%d bytes) not above limit (%d)", len(oversized), maxHookPayloadBytes)
	}
	if _, err := DecodeHookPayload(strings.NewReader(oversized)); err == nil {
		t.Fatal("DecodeHookPayload(oversized) error = nil, want non-nil")
	}
}

// TestDecodeHookPayload_AcceptsLargeButValidInput is the matching positive
// case: a payload right at the edge of the limit must still parse, so the
// rejection threshold does not creep down accidentally.
func TestDecodeHookPayload_AcceptsLargeButValidInput(t *testing.T) {
	// Pick a message that, together with the surrounding JSON skeleton,
	// totals less than maxHookPayloadBytes by a comfortable margin.
	padding := strings.Repeat("b", maxHookPayloadBytes-64)
	body := `{"message":"` + padding + `"}`
	if len(body) > maxHookPayloadBytes {
		t.Fatalf("test setup: body (%d) exceeds limit (%d)", len(body), maxHookPayloadBytes)
	}
	p, err := DecodeHookPayload(strings.NewReader(body))
	if err != nil {
		t.Fatalf("DecodeHookPayload(near-limit) error: %v", err)
	}
	if len(p.Message) != len(padding) {
		t.Fatalf("Message len = %d, want %d", len(p.Message), len(padding))
	}
}
