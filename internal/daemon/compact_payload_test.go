package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestCompactHarnessSourcesRemainDistinctAndStopOwnSession(t *testing.T) {
	claudeSource := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:claude-session"}
	codexSource := protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:codex-session"}

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
	r := state.NewReducer(state.ReducerConfig{})
	r.ApplyNormalized(claudeActive)
	r.ApplyNormalized(codexActive)
	r.ApplyNormalized(claudeStop)

	claudeFact := factOf(r, claudeSource)
	codexFact := factOf(r, codexSource)
	if claudeFact == nil || claudeFact.Action != "stop" || !claudeFact.Terminated {
		t.Fatalf("Claude stop did not terminate its compact source: %+v", claudeFact)
	}
	if codexFact == nil || codexFact.Action != "active" || codexFact.Acked || codexFact.Terminated {
		t.Fatalf("Claude stop mutated the distinct Codex source: %+v", codexFact)
	}
}

func TestMalformedCompactRequestCannotMutateReducerState(t *testing.T) {
	h := newTestHandler()
	valid := compactHookPayloadJSON(t, hookevent.HookPayload{
		SessionID: "session-1", TurnID: "turn-1", ToolUseID: "tool-1", SourceSequence: "1", EmittedTime: "100",
	})
	if resp := h.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 42, Event: "PreToolUse", Harness: "claude",
		PayloadJSON: valid, PayloadEncoding: "json_compact",
	}); !resp.OK {
		t.Fatalf("valid compact request failed: %+v", resp)
	}

	h.stateMu.Lock()
	before := *factOf(h.store[42].Reducer, protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:session-1"})
	beforeCount := len(h.store[42].Reducer.Facts())
	h.stateMu.Unlock()

	resp := h.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 42, Event: "Stop", Harness: "claude",
		PayloadJSON: `{`, PayloadEncoding: "json_compact", ContextLimit: true, ResetFlow: true,
	})
	if resp.Delivery == nil || resp.Delivery.Status != protocol.DeliveryAcknowledgementRejected || resp.Delivery.Reason != events.InvalidPayloadReason {
		t.Fatalf("malformed compact acknowledgement = %+v", resp)
	}

	h.stateMu.Lock()
	after := *factOf(h.store[42].Reducer, protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:session-1"})
	afterCount := len(h.store[42].Reducer.Facts())
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
	h.SetStopConfirm(0, 0, false) // pin the immediate-stop path, not the confirmed-idle hold
	h.SetKittyClient(&fakeKittyClient{
		windows: hookEventWindows("api", []kitty.Window{{
			ID:       42,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Codex},
		}}),
	})

	resp := h.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 42, Event: "SessionEnd",
		PayloadJSON:     hookPayloadJSON(t, hookevent.HookPayload{Reason: "clear"}),
		PayloadEncoding: "json", ResetFlow: true,
	})
	if !resp.OK {
		t.Fatalf("raw hook response = %+v", resp)
	}

	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
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

func normalizedCompactPayload(t *testing.T, event, harnessName string, fixture hookPayloadFixture) protocol.NormalizedEvent {
	t.Helper()
	payloadJSON := compactHookPayloadJSON(t, hookevent.HookPayload{
		SessionID: fixture.SessionID, TurnID: fixture.TurnID, ToolUseID: fixture.ToolUseID,
		SourceSequence: fixture.SourceSequence, EmittedTime: fixture.EmittedTime, Reason: fixture.Reason,
	})
	normalized := events.NormalizedFromHook(event, harnessName, payloadJSON, 42, false)
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
