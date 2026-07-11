package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
)

// These tests pin the normalized event schema: every harness payload variant
// normalizes to a deterministic source key + action, and malformed or unknown
// payloads are quarantined/inert so they can never fold into a terminal fact.
// They exercise normalize.go against the reducer contract only; production
// rendering is untouched.

// TestNormalizeHarnessSourceKeys covers the source-key assignment rules for
// harness, subagent, and ASL events, including their documented fallbacks.
func TestNormalizeHarnessSourceKeys(t *testing.T) {
	cases := []struct {
		name       string
		event      string
		harness    string
		payload    string
		windowID   int
		wantSource SourceID
		wantAction string
		wantPhase  string
	}{
		{
			name:       "harness keyed by harness plus session",
			event:      "UserPromptSubmit",
			harness:    "claude",
			payload:    `{"session_id":"sess-1"}`,
			windowID:   51,
			wantSource: SourceID{Kind: SourceHarness, Key: "claude:sess-1"},
			wantAction: "active",
		},
		{
			name:       "harness falls back to origin window without session",
			event:      "UserPromptSubmit",
			harness:    "codex",
			payload:    `{}`,
			windowID:   77,
			wantSource: SourceID{Kind: SourceHarness, Key: "win:77"},
			wantAction: "active",
		},
		{
			name:       "subagent keyed by agent id",
			event:      "SubagentStart",
			harness:    "claude",
			payload:    `{"agent_id":"agent-7","session_id":"sess-1"}`,
			windowID:   51,
			wantSource: SourceID{Kind: SourceSubagent, Key: "agent-7"},
			wantAction: "subagent",
		},
		{
			name:       "subagent without agent id falls back to legacy per-session bucket",
			event:      "SubagentStart",
			harness:    "claude",
			payload:    `{"session_id":"sess-1"}`,
			windowID:   51,
			wantSource: SourceID{Kind: SourceLegacy, Key: "session:sess-1"},
			wantAction: "subagent",
		},
		{
			name:       "subagent legacy bucket falls back to window without session",
			event:      "SubagentStart",
			harness:    "claude",
			payload:    `{}`,
			windowID:   88,
			wantSource: SourceID{Kind: SourceLegacy, Key: "session:win:88"},
			wantAction: "subagent",
		},
		{
			name:       "asl keyed by run id with turn phase",
			event:      "AgentSessionLoopTurnStarted",
			harness:    "codex",
			payload:    `{"run_id":"run-42"}`,
			windowID:   51,
			wantSource: SourceID{Kind: SourceASL, Key: "run-42"},
			wantPhase:  ASLPhaseTurn,
		},
		{
			name:       "asl without run id falls back to window bucket",
			event:      "AgentSessionLoopStarted",
			harness:    "codex",
			payload:    `{}`,
			windowID:   51,
			wantSource: SourceID{Kind: SourceASL, Key: "win:51"},
			wantPhase:  ASLPhaseBetween,
		},
		{
			name:       "asl finished maps to finished phase",
			event:      "AgentSessionLoopFinished",
			harness:    "codex",
			payload:    `{"run_id":"run-42"}`,
			windowID:   51,
			wantSource: SourceID{Kind: SourceASL, Key: "run-42"},
			wantPhase:  ASLPhaseFinished,
		},
		{
			name:       "user_clear carries synthetic source",
			event:      "user_clear",
			harness:    "claude",
			payload:    `{}`,
			windowID:   51,
			wantSource: SourceID{Kind: SourceProbe, Key: "user"},
			wantAction: "user_clear",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := NormalizedFromHook(tc.event, tc.harness, tc.payload, tc.windowID)
			if n.Invalid {
				t.Fatalf("unexpected quarantine: %s", n.Reason)
			}
			if n.Source != tc.wantSource {
				t.Errorf("source = %+v, want %+v", n.Source, tc.wantSource)
			}
			if n.Action != tc.wantAction {
				t.Errorf("action = %q, want %q", n.Action, tc.wantAction)
			}
			if n.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", n.Phase, tc.wantPhase)
			}
		})
	}
}

// TestNormalizeCamelAndSnakeAgree proves both harness dialects normalize to the
// same source key and identity, so the same logical event never splits into two
// facts because of spelling.
func TestNormalizeCamelAndSnakeAgree(t *testing.T) {
	snake := NormalizedFromHook("SubagentStart", "claude",
		`{"agent_id":"agent-7","turn_id":"turn-9","source_sequence":"5","emitted_time":"1700000000"}`, 51)
	camel := NormalizedFromHook("SubagentStart", "claude",
		`{"agentId":"agent-7","turnId":"turn-9","sourceSequence":"5","emittedTime":"1700000000"}`, 51)
	if snake.Source != camel.Source {
		t.Fatalf("source mismatch: snake %+v vs camel %+v", snake.Source, camel.Source)
	}
	if snake != camel {
		t.Fatalf("normalized events differ:\n snake=%+v\n camel=%+v", snake, camel)
	}
	if snake.Seq != 5 || snake.At != 1700000000 || snake.TurnID != "turn-9" {
		t.Fatalf("identity not parsed: %+v", snake)
	}
}

// TestNormalizeDeterministic verifies normalization is a pure function: the same
// payload normalizes to the identical event every time.
func TestNormalizeDeterministic(t *testing.T) {
	payload := `{"session_id":"sess-1","turn_id":"turn-9","source_sequence":"3","emitted_time":"1700000000"}`
	a := NormalizedFromHook("PreToolUse", "claude", payload, 51)
	b := NormalizedFromHook("PreToolUse", "claude", payload, 51)
	if a != b {
		t.Fatalf("normalization not deterministic:\n a=%+v\n b=%+v", a, b)
	}
}

// TestNormalizeQuarantinesMalformed proves malformed payloads and untrustworthy
// numeric identity fields are quarantined as invalid_payload and can never
// mutate visible state when handed to the reducer.
func TestNormalizeQuarantinesMalformed(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		payload string
	}{
		{name: "unparseable json", event: "Stop", payload: `{`},
		{name: "non-numeric source_sequence", event: "Stop", payload: `{"source_sequence":"abc"}`},
		{name: "non-numeric emitted_time", event: "Stop", payload: `{"emitted_time":"soon"}`},
		{name: "asl bad sequence", event: "AgentSessionLoopFinished", payload: `{"run_id":"r","source_sequence":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := NormalizedFromHook(tc.event, "claude", tc.payload, 51)
			if !n.Invalid {
				t.Fatalf("event = %+v, want quarantined", n)
			}
			if n.Source.Kind != SourceInvalid {
				t.Errorf("source kind = %q, want %q", n.Source.Kind, SourceInvalid)
			}
			if n.Deliverable() {
				t.Fatal("quarantined event must not be deliverable")
			}

			// A quarantined terminal-looking event must not paint any icon.
			r := NewReducer(zeroCfg)
			r.ApplyNormalized(n)
			if got := r.Reduce(1000); got.Icon != "" {
				t.Fatalf("quarantined event produced icon %q; want empty", got.Icon)
			}
			traces := r.Traces()
			if len(traces) != 1 || traces[0].Decision != DecisionQuarantined {
				t.Fatalf("traces = %+v; want one quarantined entry", traces)
			}
		})
	}
}

// TestNormalizeUnknownAndIgnoredAreInert confirms unknown and intentionally
// ignored events resolve to no action, are not deliverable, and never mutate a
// fact — but are distinct from malformed quarantine (their payload was fine).
func TestNormalizeUnknownAndIgnoredAreInert(t *testing.T) {
	unknown := NormalizedFromHook("TotallyMadeUpEvent", "claude", `{}`, 51)
	if unknown.Invalid {
		t.Fatalf("unknown event should be inert, not quarantined: %+v", unknown)
	}
	if unknown.Deliverable() || unknown.Action != "" {
		t.Fatalf("unknown event should be inert: %+v", unknown)
	}
	if unknown.Reason != reasonUnknownEvent {
		t.Errorf("unknown reason = %q, want %q", unknown.Reason, reasonUnknownEvent)
	}

	ignored := NormalizedFromHook("InstructionsLoaded", "claude", `{}`, 51)
	if ignored.Deliverable() || ignored.Reason != reasonIgnoredEvent {
		t.Fatalf("ignored event should be inert with ignored reason: %+v", ignored)
	}

	// Neither can dislodge a live fact.
	r := NewReducer(zeroCfg)
	r.Apply(ReducerEvent{Source: harnessSource(), Action: "permission", Seq: 1, At: 10})
	r.ApplyNormalized(unknown)
	r.ApplyNormalized(ignored)
	if got := r.Reduce(100); got.Icon != "key" {
		t.Fatalf("inert events changed icon to %q; want key preserved", got.Icon)
	}
}

// TestNormalizeCannotProduceTerminalState is the core reliability property: no
// malformed or unknown payload, however it is shaped, can drive the reducer to a
// terminal (stop/error) icon.
func TestNormalizeCannotProduceTerminalState(t *testing.T) {
	hostile := []string{
		`{`,                               // malformed json
		`{"source_sequence":"NaN"}`,       // bad sequence
		`{"stop_reason":"context_limit"}`, // stop-ish reason on an unknown event
	}
	events := []string{"NopeEvent", "AlsoUnknown", "Stop???"}
	for _, ev := range events {
		for _, p := range hostile {
			r := NewReducer(zeroCfg)
			r.ApplyNormalized(NormalizedFromHook(ev, "claude", p, 51))
			got := r.Reduce(1000)
			if got.Icon == "stop" || got.Icon == "error" {
				t.Fatalf("event=%q payload=%q produced terminal icon %q", ev, p, got.Icon)
			}
		}
	}
}

// TestNormalizedApplyFeedsReducer proves a well-formed harness event flows
// through ApplyNormalized into the same fact an equivalent ReducerEvent would.
func TestNormalizedApplyFeedsReducer(t *testing.T) {
	n := NormalizedFromHook("PermissionRequest", "claude",
		`{"session_id":"sess-1","source_sequence":"4","emitted_time":"1700000000"}`, 51)
	if !n.Deliverable() {
		t.Fatalf("expected deliverable event, got %+v", n)
	}
	r := NewReducer(zeroCfg)
	r.ApplyNormalized(n)
	got := r.Reduce(1700000001)
	if got.Icon != "key" {
		t.Fatalf("icon = %q, want key", got.Icon)
	}
	if got.Source != (SourceID{Kind: SourceHarness, Key: "claude:sess-1"}) {
		t.Fatalf("source = %+v, want harness claude:sess-1", got.Source)
	}
}

// TestProbeSourceIsSynthetic pins the explicit synthetic probe source key used
// by the daemon-side context-limit probe path.
func TestProbeSourceIsSynthetic(t *testing.T) {
	if got := ProbeSource(); got != (SourceID{Kind: SourceProbe, Key: "context_limit"}) {
		t.Fatalf("ProbeSource() = %+v", got)
	}
}

// TestNormalizeUsesHarnessNormalization confirms the harness name is normalized
// into the source key so "Claude" and "claude" collapse onto one source.
func TestNormalizeUsesHarnessNormalization(t *testing.T) {
	mixed := NormalizedFromHook("UserPromptSubmit", "Claude", `{"session_id":"s"}`, 1)
	want := SourceID{Kind: SourceHarness, Key: harness.Normalize("Claude") + ":s"}
	if mixed.Source != want {
		t.Fatalf("source = %+v, want %+v", mixed.Source, want)
	}
}
