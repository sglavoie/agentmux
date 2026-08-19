package hookevent

import (
	"strings"
	"testing"
)

func TestDecodeHookPayload_ParsesIdentityFields(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{
		"hook_event_name": "SubagentStart",
		"session_id": "sess-1",
		"turn_id": "turn-9",
		"tool_use_id": "tu-3",
		"agent_id": "agent-7",
		"agent_type": "explore",
		"run_id": "run-42",
		"source_sequence": "12",
		"mailbox_projection_sequence": "11",
		"source_event_id": "evt-abc",
		"emitted_time": "1700000000"
	}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(identity) error: %v", err)
	}
	checks := map[string]struct{ got, want string }{
		"session_id":      {payload.EffectiveSessionID(), "sess-1"},
		"turn_id":         {payload.EffectiveTurnID(), "turn-9"},
		"tool_use_id":     {payload.EffectiveToolUseID(), "tu-3"},
		"agent_id":        {payload.EffectiveAgentID(), "agent-7"},
		"agent_type":      {payload.EffectiveAgentType(), "explore"},
		"run_id":          {payload.EffectiveRunID(), "run-42"},
		"source_sequence": {payload.EffectiveSourceSequence(), "12"},
		"mailbox_projection_sequence": {
			payload.EffectiveMailboxProjectionSequence(), "11",
		},
		"source_event_id": {payload.EffectiveSourceEventID(), "evt-abc"},
		"emitted_time":    {payload.EffectiveEmittedTime(), "1700000000"},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", name, c.got, c.want)
		}
	}
}

func TestDecodeHookPayload_ParsesCamelIdentityFields(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{
		"sessionId": "sess-c",
		"turnId": "turn-c",
		"toolUseId": "tu-c",
		"agentId": "agent-c",
		"agentType": "plan",
		"runId": "run-c",
		"sourceSequence": "7",
		"mailboxProjectionSequence": "6",
		"sourceEventId": "evt-c",
		"emittedTime": "1700000123"
	}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(camel identity) error: %v", err)
	}
	checks := map[string]struct{ got, want string }{
		"sessionId":      {payload.EffectiveSessionID(), "sess-c"},
		"turnId":         {payload.EffectiveTurnID(), "turn-c"},
		"toolUseId":      {payload.EffectiveToolUseID(), "tu-c"},
		"agentId":        {payload.EffectiveAgentID(), "agent-c"},
		"agentType":      {payload.EffectiveAgentType(), "plan"},
		"runId":          {payload.EffectiveRunID(), "run-c"},
		"sourceSequence": {payload.EffectiveSourceSequence(), "7"},
		"mailboxProjectionSequence": {
			payload.EffectiveMailboxProjectionSequence(), "6",
		},
		"sourceEventId": {payload.EffectiveSourceEventID(), "evt-c"},
		"emittedTime":   {payload.EffectiveEmittedTime(), "1700000123"},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", name, c.got, c.want)
		}
	}
}

// TestHookPayload_IdentitySnakePreferredOverCamel confirms the snake-case
// spelling wins and a blank snake value falls back to the camelCase alias,
// matching the firstNonBlank convention used by the other effective accessors.
func TestHookPayload_IdentitySnakePreferredOverCamel(t *testing.T) {
	p := HookPayload{
		SessionID: "snake", SessionIDCamel: "camel",
		AgentID: " ", AgentIDCamel: "camel-agent",
		RunID: "\t", RunIDCamel: "camel-run",
	}
	if got := p.EffectiveSessionID(); got != "snake" {
		t.Errorf("EffectiveSessionID() = %q, want snake", got)
	}
	if got := p.EffectiveAgentID(); got != "camel-agent" {
		t.Errorf("EffectiveAgentID() = %q, want camel-agent (blank snake falls back)", got)
	}
	if got := p.EffectiveRunID(); got != "camel-run" {
		t.Errorf("EffectiveRunID() = %q, want camel-run (blank snake falls back)", got)
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

func TestCompactHookPayloadCanonicalizesSemanticAllowlist(t *testing.T) {
	original := HookPayload{
		HookEventName:         "AgentSessionLoopPaused",
		NotificationTypeCamel: "permission_prompt",
		StopReasonCamel:       "end_turn",
		Reason:                "blocked",
		PauseReason:           "question",
		Source:                "agent_session_loop",
		Trigger:               "manual",
		CWD:                   "/work/project",
		Harness:               "codex",
		WindowID:              52,
		PlanDir:               "/plans/run",
		RunIDCamel:            "run-7",
		StatePath:             "/plans/run/state.json",
		RunFinishedAt:         "2026-07-16T22:00:00Z",
		ToolNameCamel:         "Bash",
		SessionIDCamel:        "session-7",
		TurnIDCamel:           "turn-8",
		ToolUseIDCamel:        "tool-9",
		AgentIDCamel:          "agent-10",
		AgentTypeCamel:        "explore",
		SourceSequenceCamel:   "11",
		SourceEventIDCamel:    "event-12",
		EmittedTimeCamel:      "1700000000",
		Prompt:                strings.Repeat("ordinary prompt", 1000),
		Title:                 strings.Repeat("ordinary title", 1000),
		Message:               strings.Repeat("ordinary message", 1000),
	}
	encoded, err := CompactHookPayload(original)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= len(original.Prompt)+len(original.Title)+len(original.Message) ||
		strings.Contains(string(encoded), "ordinary prompt") || strings.Contains(string(encoded), "ordinary title") || strings.Contains(string(encoded), "ordinary message") {
		t.Fatalf("compact payload retained bulk free text: %s", encoded)
	}
	decoded, err := DecodeHookPayload(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.HookEventName != original.HookEventName ||
		decoded.EffectiveNotificationType() != "permission_prompt" || decoded.EffectiveStopReason() != "end_turn" ||
		decoded.Reason != "blocked" || decoded.EffectivePauseReason() != "question" || decoded.Source != original.Source ||
		decoded.Trigger != original.Trigger || decoded.CWD != original.CWD || decoded.Harness != original.Harness ||
		decoded.EffectiveKittyWindowID() != 52 || decoded.PlanDir != original.PlanDir || decoded.EffectiveRunID() != "run-7" ||
		decoded.StatePath != original.StatePath || decoded.RunFinishedAt != original.RunFinishedAt ||
		decoded.EffectiveToolName() != "Bash" || decoded.EffectiveSessionID() != "session-7" ||
		decoded.EffectiveTurnID() != "turn-8" || decoded.EffectiveToolUseID() != "tool-9" ||
		decoded.EffectiveAgentID() != "agent-10" || decoded.EffectiveAgentType() != "explore" ||
		decoded.EffectiveSourceSequence() != "11" || decoded.EffectiveSourceEventID() != "event-12" ||
		decoded.EffectiveEmittedTime() != "1700000000" {
		t.Fatalf("decoded compact payload = %+v", decoded)
	}
	if decoded.EffectivePrompt() != "" || decoded.Title != "" || decoded.Message != "" {
		t.Fatalf("ordinary free text survived compaction: %+v", decoded)
	}
}

func TestCompactHookPayloadPreservesOnlyResetAndContextLimitText(t *testing.T) {
	for _, tc := range []struct {
		name        string
		harness     string
		event       string
		payload     HookPayload
		wantPrompt  string
		wantContext bool
		wantReset   bool
	}{
		{
			name: "reset command only", harness: "copilot", event: "userPromptSubmitted",
			payload:    HookPayload{Prompt: " /CLEAR discard " + strings.Repeat("private transcript ", 1000)},
			wantPrompt: "/clear", wantReset: true,
		},
		{
			name: "context marker only", harness: "claude", event: "Stop",
			payload:     HookPayload{Title: "Context limit reached", Message: "/compact or /clear to continue " + strings.Repeat("private transcript ", 1000)},
			wantContext: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := CompactHookPayload(tc.payload)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "private transcript") {
				t.Fatalf("compact payload retained bulk text: %s", encoded)
			}
			decoded, err := DecodeHookPayload(strings.NewReader(string(encoded)))
			if err != nil {
				t.Fatal(err)
			}
			if decoded.EffectivePrompt() != tc.wantPrompt {
				t.Fatalf("compact prompt = %q, want %q", decoded.EffectivePrompt(), tc.wantPrompt)
			}
			if got := PayloadHasContextLimit(decoded); got != tc.wantContext {
				t.Fatalf("context-limit classification = %t, want %t", got, tc.wantContext)
			}
			if got := PayloadHasResetFlow(tc.harness, tc.event, decoded); got != tc.wantReset {
				t.Fatalf("reset-flow classification = %t, want %t", got, tc.wantReset)
			}
		})
	}
}
