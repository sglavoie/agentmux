package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestCompactHarnessSourcesRemainDistinctAndStopOwnSession(t *testing.T) {
	claudeSource := SourceID{Kind: SourceHarness, Key: "claude:claude-session"}
	codexSource := SourceID{Kind: SourceHarness, Key: "codex:codex-session"}

	claudeActive := normalizedCompactPayload(t, "PreToolUse", "claude", hookPayloadFixture{
		SessionID: "claude-session", TurnID: "claude-turn", ToolUseID: "claude-tool", SourceSequence: "1", EmittedTime: "100",
	})
	codexActive := normalizedCompactPayload(t, "PreToolUse", "codex", hookPayloadFixture{
		SessionID: "codex-session", TurnID: "codex-turn", ToolUseID: "codex-tool", SourceSequence: "1", EmittedTime: "200",
	})
	claudeStop := normalizedCompactPayload(t, "SessionEnd", "claude", hookPayloadFixture{
		SessionID: "claude-session", TurnID: "claude-turn", SourceSequence: "2", EmittedTime: "150", Reason: "user_exit",
	})

	if claudeActive.Source != claudeSource || codexActive.Source != codexSource || claudeActive.Source == codexActive.Source {
		t.Fatalf("compact sources collapsed: claude=%+v codex=%+v", claudeActive.Source, codexActive.Source)
	}
	r := NewReducer(ReducerConfig{})
	r.ApplyNormalized(claudeActive)
	r.ApplyNormalized(codexActive)
	r.ApplyNormalized(claudeStop)

	claudeFact := r.facts[claudeSource]
	codexFact := r.facts[codexSource]
	if claudeFact == nil || claudeFact.action != "stop" || !claudeFact.terminated {
		t.Fatalf("Claude stop did not terminate its compact source: %+v", claudeFact)
	}
	if codexFact == nil || codexFact.action != "active" || codexFact.acked || codexFact.terminated {
		t.Fatalf("Claude stop mutated the distinct Codex source: %+v", codexFact)
	}
}

func TestMalformedCompactRequestCannotMutateReducerState(t *testing.T) {
	h := newTestHandler()
	valid := compactHookPayloadJSON(t, hookevent.HookPayload{
		SessionID: "session-1", TurnID: "turn-1", ToolUseID: "tool-1", SourceSequence: "1", EmittedTime: "100",
	})
	if resp := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 42, Event: "PreToolUse", Harness: "claude",
		PayloadJSON: valid, PayloadEncoding: "json_compact",
	}); !resp.OK {
		t.Fatalf("valid compact request failed: %+v", resp)
	}

	h.stateMu.Lock()
	before := *h.store[42].Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:session-1"}]
	beforeCount := len(h.store[42].Reducer.facts)
	h.stateMu.Unlock()

	resp := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 42, Event: "Stop", Harness: "claude",
		PayloadJSON: `{`, PayloadEncoding: "json_compact", ContextLimit: true, ResetFlow: true,
	})
	if resp.Delivery == nil || resp.Delivery.Status != DeliveryAcknowledgementRejected || resp.Delivery.Reason != InvalidPayloadReason {
		t.Fatalf("malformed compact acknowledgement = %+v", resp)
	}

	h.stateMu.Lock()
	after := *h.store[42].Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:session-1"}]
	afterCount := len(h.store[42].Reducer.facts)
	h.stateMu.Unlock()
	if afterCount != beforeCount || after != before {
		t.Fatalf("malformed compact request mutated reducer facts: before=%+v after=%+v counts=%d/%d", before, after, beforeCount, afterCount)
	}
}

func TestRawHookDeliveryDoesNotInheritIndicatorResetClassification(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 0
	t.Cleanup(func() { resetFlowStopDelay = oldDelay })

	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{
		windows: hookEventWindows("api", []kitty.Window{{
			ID:       42,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Codex},
		}}),
	})

	resp := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 42, Event: "SessionEnd",
		PayloadJSON:     hookPayloadJSON(t, hookevent.HookPayload{Reason: "clear"}),
		PayloadEncoding: "json", ResetFlow: true,
	})
	if !resp.OK {
		t.Fatalf("raw hook response = %+v", resp)
	}

	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 {
		t.Fatalf("events = %+v, want one raw SessionEnd event", detail.Events)
	}
	event := detail.Events[0]
	if event.Action != "stop" || event.ResetFlow {
		t.Fatalf("raw SessionEnd action/reset_flow = (%q, %t), want (stop, false)", event.Action, event.ResetFlow)
	}
}

// hookPayloadFixture keeps the source-isolation test readable while still
// exercising the production HookPayload -> compact JSON -> normalized event
// boundary used by large deliveries.
type hookPayloadFixture struct {
	SessionID      string
	TurnID         string
	ToolUseID      string
	SourceSequence string
	EmittedTime    string
	Reason         string
}

func normalizedCompactPayload(t *testing.T, event, harnessName string, fixture hookPayloadFixture) NormalizedEvent {
	t.Helper()
	payloadJSON := compactHookPayloadJSON(t, hookevent.HookPayload{
		SessionID: fixture.SessionID, TurnID: fixture.TurnID, ToolUseID: fixture.ToolUseID,
		SourceSequence: fixture.SourceSequence, EmittedTime: fixture.EmittedTime, Reason: fixture.Reason,
	})
	normalized := NormalizedFromHook(event, harnessName, payloadJSON, 42, false)
	if normalized.Invalid || !normalized.Deliverable() {
		t.Fatalf("normalized compact payload = %+v", normalized)
	}
	return normalized
}

func compactHookPayloadJSON(t *testing.T, payload hookevent.HookPayload) string {
	t.Helper()
	encoded, err := hookevent.CompactHookPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
