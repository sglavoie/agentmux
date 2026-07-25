package hookevent

import (
	"encoding/json"
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

func TestDecodeHookPayload_ParsesLoopWorkspaceFields(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{
		"hook_event_name": "AgentSessionLoopTurnStarted",
		"source": "agent_session_loop",
		"cwd": "/work/trad-x",
		"harness": "codex",
		"kitty_window_id": 51,
		"state_path": "/tmp/asl/state.json"
	}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(loop fields) error: %v", err)
	}
	if payload.CWD != "/work/trad-x" {
		t.Fatalf("CWD = %q, want /work/trad-x", payload.CWD)
	}
	if payload.Harness != "codex" {
		t.Fatalf("Harness = %q, want codex", payload.Harness)
	}
	if payload.EffectiveKittyWindowID() != 51 {
		t.Fatalf("EffectiveKittyWindowID = %d, want 51", payload.EffectiveKittyWindowID())
	}
	if payload.StatePath != "/tmp/asl/state.json" {
		t.Fatalf("StatePath = %q, want /tmp/asl/state.json", payload.StatePath)
	}
}

func TestDecodeHookPayload_ParsesLoopWaitFields(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{
		"hook_event_name": "AgentSessionLoopWaitStarted",
		"source": "agent_session_loop",
		"question_id": "q-7f3a",
		"question_preview": "Should  the\n shim keep\tthe narrow branch?",
		"wait_reason": "reply"
	}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(wait fields) error: %v", err)
	}
	if payload.QuestionID != "q-7f3a" {
		t.Fatalf("QuestionID = %q, want q-7f3a", payload.QuestionID)
	}
	if payload.WaitReason != "reply" {
		t.Fatalf("WaitReason = %q, want reply", payload.WaitReason)
	}
	if want := "Should the shim keep the narrow branch?"; payload.QuestionPreview != want {
		t.Fatalf("QuestionPreview = %q, want collapsed %q", payload.QuestionPreview, want)
	}
}

// An omitted preview stays empty rather than becoming a placeholder: the
// producer drops the key entirely when the question carries no text.
func TestDecodeHookPayload_OmittedQuestionPreviewIsEmpty(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{"question_id": "q-1"}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(no preview) error: %v", err)
	}
	if payload.QuestionPreview != "" {
		t.Fatalf("QuestionPreview = %q, want empty", payload.QuestionPreview)
	}
}

// The producer already bounds the preview; decode re-bounds it so a hand-written
// or replayed payload cannot widen the only free-text field the dashboard shows.
func TestDecodeHookPayload_BoundsOversizedQuestionPreview(t *testing.T) {
	oversized := strings.Repeat("é", 500)
	payload, err := DecodeHookPayload(strings.NewReader(`{"question_preview": "` + oversized + `"}`))
	if err != nil {
		t.Fatalf("DecodeHookPayload(oversized preview) error: %v", err)
	}
	if got := len([]rune(payload.QuestionPreview)); got != maxQuestionPreviewRunes {
		t.Fatalf("QuestionPreview runes = %d, want %d", got, maxQuestionPreviewRunes)
	}
	if payload.QuestionPreview != strings.Repeat("é", maxQuestionPreviewRunes) {
		t.Fatalf("QuestionPreview = %q, want a rune-safe truncation", payload.QuestionPreview)
	}
}

// The question body is model-controlled text that the dashboard renders into a
// terminal, so decode drops whole escape sequences and stray controls instead of
// leaving parameter residue behind. Ordinary Unicode is untouched.
func TestDecodeHookPayload_StripsTerminalControlsFromQuestionPreview(t *testing.T) {
	esc, bel := string(rune(0x1b)), string(rune(0x07))
	c1CSI, c1OSC, st := string(rune(0x9b)), string(rune(0x9d)), string(rune(0x9c))
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"csi", esc + "[2JShip" + esc + "[31m the shim?", "Ship the shim?"},
		{"osc terminated by bel", "Ship " + esc + "]0;pwned" + bel + "the" + bel + " shim?", "Ship the shim?"},
		{"c1 introducers", "Ship " + c1CSI + "31mthe " + c1OSC + "0;pwned" + st + " shim?", "Ship the shim?"},
		{"unterminated osc swallows the rest", "Ship " + esc + "]0;pwned the shim?", "Ship"},
		{"printable unicode", "Caf\u00e9 \U0001f1eb\U0001f1f7 \u2014 ship the shim?", "Caf\u00e9 \U0001f1eb\U0001f1f7 \u2014 ship the shim?"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(map[string]string{"question_preview": tc.raw})
			if err != nil {
				t.Fatalf("marshal %s payload: %v", tc.name, err)
			}
			payload, err := DecodeHookPayload(strings.NewReader(string(encoded)))
			if err != nil {
				t.Fatalf("DecodeHookPayload(%s) error: %v", tc.name, err)
			}
			if payload.QuestionPreview != tc.want {
				t.Fatalf("QuestionPreview = %q, want %q", payload.QuestionPreview, tc.want)
			}
		})
	}
}

func TestHookPayload_EffectiveKittyWindowID(t *testing.T) {
	cases := []struct {
		name    string
		payload HookPayload
		want    int
	}{
		{name: "kitty window id wins", payload: HookPayload{KittyWindowID: 51, WindowID: 52}, want: 51},
		{name: "window id fallback", payload: HookPayload{WindowID: 52}, want: 52},
		{name: "non-positive ignored", payload: HookPayload{KittyWindowID: -1, WindowID: 0}, want: 0},
	}
	for _, tc := range cases {
		if got := tc.payload.EffectiveKittyWindowID(); got != tc.want {
			t.Errorf("%s: EffectiveKittyWindowID() = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestDecodeHookPayload_PreservesCodexFields(t *testing.T) {
	payload, err := DecodeHookPayload(strings.NewReader(`{
		"hook_event_name": "Stop",
		"tool_name": "Bash",
		"source": "startup",
		"trigger": "auto",
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
	if payload.Trigger != "auto" {
		t.Fatalf("Trigger = %q, want auto", payload.Trigger)
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
		"sourceEventId":  {payload.EffectiveSourceEventID(), "evt-c"},
		"emittedTime":    {payload.EffectiveEmittedTime(), "1700000123"},
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
