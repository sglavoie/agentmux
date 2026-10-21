package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/eventlog"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

func TestHookDecisionTraceCorrelatesRoutingNormalizationAndReducer(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: unmanagedRouteWindows("api", harness.Codex, true)})
	journal, err := diagnostics.NewJournal(diagnostics.PathsForRoot(t.TempDir()), diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h.setDecisionJournal(journal)

	payload := hookevent.HookPayload{
		SessionID: "session-1", TurnID: "turn-1", SourceSequence: "7",
		SourceEventID: "source-7", ToolUseID: "tool-7", Prompt: "TOP SECRET PROMPT",
	}
	resp := h.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 43, Event: "UserPromptSubmit",
		ProducerHarness: harness.Claude, Harness: harness.Claude,
		ProducerConfigDir: "/Users/dev/.claude-personal",
		PayloadJSON:       hookPayloadJSON(t, payload),
		TraceID:           "trace-7", RequestID: "request-7", SourceEventID: "source-7", Attempt: 2,
		PayloadSize: 123, PayloadHMAC: "hmac:local", PayloadEncoding: "json",
		SaturationReason: "dedupe_cache",
	})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}

	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 {
		t.Fatalf("events = %+v; want one", detail.Events)
	}
	event := detail.Events[0]
	if event.TraceID != "trace-7" || event.RequestID != "request-7" ||
		event.SourceEventID != "source-7" || event.Attempt != 2 ||
		event.TurnID != "turn-1" || event.ToolUseID != "tool-7" ||
		event.PayloadSize != 123 || event.PayloadHMAC != "hmac:local" || event.PayloadEncoding != "json" {
		t.Fatalf("correlation = %+v", event)
	}
	if event.ParseOutcome != protocol.ParseOutcomeDecoded ||
		event.NormalizationOutcome != protocol.NormalizationOutcomeNormalized ||
		event.IdentityConfidence != protocol.IdentityConfidenceDeclared ||
		event.ReducerDecision != protocol.DecisionApplied || event.ReducerInputAction != "active" {
		t.Fatalf("decision = %+v", event)
	}
	if event.EffectiveBefore == nil || event.EffectiveAfter == nil ||
		event.EffectiveBefore.Icon != "" || event.EffectiveAfter.Icon != "bolt" {
		t.Fatalf("before/after = %+v -> %+v", event.EffectiveBefore, event.EffectiveAfter)
	}
	if event.ProducerHarness != harness.Claude || event.TargetHarness != harness.Codex ||
		event.Profile != "managed" || event.SessionID != "session-1" ||
		event.SourceKind != string(protocol.SourceHarness) || event.SourceKey != "claude:session-1" || event.SourceSeq != 7 {
		t.Fatalf("source/target evidence = %+v", event)
	}
	if event.ProducerConfigDir != "/Users/dev/.claude-personal" {
		t.Fatalf("producer config dir = %q, want cross-window propagation of the producer home", event.ProducerConfigDir)
	}
	if event.ReducerInput == nil || !event.ReducerInput.Prompt || event.ReducerInput.OriginWindowID != 43 ||
		event.ReducerInput.EmittedAt == 0 || event.ReducerInput.GuardDuration == 0 {
		t.Fatalf("offline reducer input = %+v", event.ReducerInput)
	}
	route := event.RouteEvidence
	if route == nil || route.ChosenRoute != events.RouteDecisionOwner || route.TargetWindowID != 42 ||
		!route.SourceMatched || !route.OwnerMatched || !route.FocusCandidate || !route.PromptCandidate ||
		route.ConsideredWindowCount != 2 || len(route.ConsideredWindows) != 2 {
		t.Fatalf("route evidence = %+v", route)
	}

	records, err := journal.Read(context.Background(), diagnostics.Selector{TraceID: "trace-7"})
	if err != nil {
		t.Fatalf("journal records = %+v, err = %v", records, err)
	}
	var decision diagnostics.TraceEnvelope
	for _, record := range records {
		if record.Stage == "daemon.decision" {
			decision = record
			break
		}
	}
	if decision.Stage == "" {
		t.Fatalf("journal records lack daemon.decision: %+v", records)
	}
	encoded, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != string(protocol.DecisionApplied) ||
		decision.Evidence["daemon_sequence"] == "" || decision.Evidence["render_generation"] == "" ||
		decision.Evidence["turn_id"] != "turn-1" || decision.Evidence["tool_use_id"] != "tool-7" ||
		decision.Evidence["replay.version"] != "1" || decision.Evidence["replay.emitted_at"] == "" ||
		decision.Evidence["replay.is_user_submission"] != "true" ||
		decision.Evidence["saturation"] != "dedupe_cache" ||
		decision.Evidence["producer_config_dir"] != "/Users/dev/.claude-personal" ||
		!strings.Contains(string(encoded), "request-7") || strings.Contains(string(encoded), payload.Prompt) {
		t.Fatalf("journal decision is incomplete or leaked prompt: %s", encoded)
	}
}

func TestHookDecisionTraceDistinguishesQuarantineAndUntrustedFallback(t *testing.T) {
	t.Parallel()

	t.Run("unreadable payload is quarantined without reduction", func(t *testing.T) {
		h := newTestHandler()
		resp := h.Handle(protocol.Request{
			Type: protocol.TypeHookEvent, WindowID: 42, Event: "PreToolUse",
			Harness: harness.Claude, PayloadJSON: `{bad json`,
			TraceID: "trace-invalid", RequestID: "request-invalid",
		})
		if !resp.OK {
			t.Fatalf("response = %+v", resp)
		}
		detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
		if len(detail.Events) != 1 {
			t.Fatalf("events = %+v", detail.Events)
		}
		event := detail.Events[0]
		if !event.Quarantined || event.ReducerDecision != protocol.DecisionQuarantined ||
			event.ParseOutcome != protocol.ParseOutcomeInvalid ||
			event.NormalizationOutcome != protocol.NormalizationOutcomeQuarantined ||
			event.IdentityConfidence != protocol.IdentityConfidenceInvalid {
			t.Fatalf("quarantined event = %+v", event)
		}
		h.stateMu.Lock()
		st := h.store[42]
		h.stateMu.Unlock()
		if st == nil || st.Reducer != nil || st.DesiredIcon != "" {
			t.Fatalf("quarantined payload mutated reducer state: %+v", st)
		}
	})

	t.Run("malformed identity uses explicit legacy fallback", func(t *testing.T) {
		h := newTestHandler()
		payload := hookevent.HookPayload{SessionID: "session-1", SourceSequence: "not-a-number"}
		resp := h.Handle(protocol.Request{
			Type: protocol.TypeHookEvent, WindowID: 42, Event: "PreToolUse",
			Harness: harness.Claude, PayloadJSON: hookPayloadJSON(t, payload),
			TraceID: "trace-fallback", RequestID: "request-fallback",
		})
		if !resp.OK {
			t.Fatalf("response = %+v", resp)
		}
		detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
		if len(detail.Events) != 1 {
			t.Fatalf("events = %+v", detail.Events)
		}
		event := detail.Events[0]
		if !event.Quarantined || !event.UsedFallbackIdentity ||
			event.NormalizationOutcome != protocol.NormalizationOutcomeUntrustedFallback ||
			event.IdentityConfidence != protocol.IdentityConfidenceUntrustedFallback ||
			event.SourceKind != string(protocol.SourceLegacy) || event.ReducerDecision != protocol.DecisionApplied ||
			event.DecisionReason != protocol.DecisionReasonUntrustedIdentityFallback ||
			event.EffectiveAfter == nil || event.EffectiveAfter.Icon != "bolt" {
			t.Fatalf("fallback event = %+v", event)
		}
	})
}

func TestHookDecisionTraceRecordsResetRewrite(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	resp := h.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 42, Event: "SessionEnd", Harness: harness.Claude,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "session-1", Reason: "clear"}),
	})
	if !resp.OK {
		t.Fatalf("response = %+v", resp)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 {
		t.Fatalf("events = %+v", detail.Events)
	}
	event := detail.Events[0]
	if event.TraceID == "" || event.Action != "user_clear" || event.ReducerInputAction != "user_clear" ||
		event.NormalizationOutcome != protocol.NormalizationOutcomeReducerRewrite ||
		event.ReducerDecision != protocol.DecisionAcknowledged {
		t.Fatalf("reset trace = %+v", event)
	}
}

func TestDecisionTraceDistinguishesReducerDuplicateAndReorder(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	source := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:session-1"}
	apply := func(traceID string, seq uint64) {
		normalized := protocol.NormalizedEvent{
			Source: source, Action: "active", Seq: seq, Event: "PreToolUse",
			SessionID: "session-1", OriginWindowID: 42,
		}
		h.handleStateUpdate(protocol.Request{
			Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", Event: "PreToolUse",
			Harness: harness.Claude, TraceID: traceID, Normalized: &normalized,
		})
	}
	apply("trace-new", 2)
	apply("trace-duplicate", 2)
	apply("trace-reordered", 1)

	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 3 {
		t.Fatalf("events = %+v", detail.Events)
	}
	duplicate, reordered := detail.Events[1], detail.Events[2]
	if duplicate.ReducerDecision != protocol.DecisionDuplicate ||
		duplicate.DecisionReason != protocol.DecisionReasonDuplicateSequence ||
		duplicate.SuppressionReason != protocol.DecisionReasonDuplicateSequence {
		t.Fatalf("duplicate = %+v", duplicate)
	}
	if reordered.ReducerDecision != protocol.DecisionReordered ||
		reordered.DecisionReason != protocol.DecisionReasonReorderedSequence ||
		reordered.SuppressionReason != protocol.DecisionReasonReorderedSequence {
		t.Fatalf("reordered = %+v", reordered)
	}
	if duplicate.EffectiveBefore == nil || duplicate.EffectiveAfter == nil ||
		*duplicate.EffectiveBefore != *duplicate.EffectiveAfter ||
		reordered.EffectiveBefore == nil || reordered.EffectiveAfter == nil ||
		*reordered.EffectiveBefore != *reordered.EffectiveAfter {
		t.Fatalf("duplicate/reordered changed effective state: %+v / %+v", duplicate, reordered)
	}
}

func TestDecisionTraceRecordsRejectedOwnerAlternative(t *testing.T) {
	stubDaemonLoopDiscover(t, nil, nil)
	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: unmanagedRouteWindows("api", harness.Codex, false)})
	resp := h.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 43, Event: "PreToolUse", Harness: harness.Claude,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "session-1"}),
	})
	if !resp.OK {
		t.Fatalf("response = %+v", resp)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43})
	if len(detail.Events) != 1 {
		t.Fatalf("events = %+v", detail.Events)
	}
	event := detail.Events[0]
	if event.ReducerDecision != protocol.DecisionSuppressed || event.DecisionReason != protocol.DecisionReasonOwnerMismatch ||
		event.RouteDecision != events.RouteDecisionRejected || event.RouteEvidence == nil ||
		event.RouteEvidence.ChosenRoute != events.RouteDecisionRejected || len(event.RouteEvidence.RejectedAlternatives) == 0 ||
		event.RouteEvidence.RejectedAlternatives[len(event.RouteEvidence.RejectedAlternatives)-1].WindowID != 42 {
		t.Fatalf("owner mismatch evidence = %+v", event)
	}
}

func TestDuplicateReplayClearsDownstreamCausalityWithoutReducerMutation(t *testing.T) {
	t.Parallel()

	sock, _, handler := newTestServerWithRef(t)
	client := ipc.NewClientWithSocket(sock)
	payload := hookPayloadJSON(t, hookevent.HookPayload{SessionID: "session-1"})
	request := protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 42, Event: "PreToolUse", Harness: harness.Claude,
		PayloadJSON: payload, RequestID: "logical-hook-1", TraceID: "trace-original", Attempt: 1,
	}
	first, err := client.Send(request)
	if err != nil || first.Delivery == nil || first.Delivery.Status != protocol.DeliveryAcknowledgementAccepted {
		t.Fatalf("first response = %+v, err = %v", first, err)
	}
	handler.stateMu.Lock()
	poisoned := &handler.ensureWindowStateLocked(42).EventLog[0]
	poisoned.RenderGeneration = 41
	poisoned.RenderOutcome = rendering.StageRendered
	poisoned.RenderReason = "original-render"
	poisoned.NotificationTrace = "original-notification"
	poisoned.NotificationOutcome = "sent"
	poisoned.NotificationReason = "original-notification-reason"
	poisoned.ProbeTraceIDs = []string{"original-probe"}
	handler.stateMu.Unlock()

	request.TraceID = "trace-retry"
	request.Attempt = 2
	retry, err := client.Send(request)
	if err != nil || retry.Delivery == nil || retry.Delivery.Status != protocol.DeliveryAcknowledgementDuplicate {
		t.Fatalf("retry response = %+v, err = %v", retry, err)
	}
	handler.stateMu.Lock()
	st := handler.ensureWindowStateLocked(42)
	eventlog.FinishRender(st, protocol.RenderGenerationTrace{
		Generation: 42,
		Stage:      rendering.StageRendered,
		Reason:     "later-request-render",
		RequestIDs: []string{request.RequestID},
	})
	handler.stateMu.Unlock()

	detail, err := client.Send(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if err != nil || len(detail.Events) != 2 {
		t.Fatalf("detail = %+v, err = %v", detail, err)
	}
	original, duplicate := detail.Events[0], detail.Events[1]
	if original.ReducerDecision != protocol.DecisionApplied || duplicate.ReducerDecision != protocol.DecisionDuplicate ||
		duplicate.TraceID != "trace-retry" || duplicate.Attempt != 2 ||
		duplicate.SuppressionReason != protocol.DecisionReasonRequestReplayed ||
		duplicate.EffectiveBefore == nil || duplicate.EffectiveAfter == nil ||
		*duplicate.EffectiveBefore != *duplicate.EffectiveAfter {
		t.Fatalf("original/duplicate = %+v / %+v", original, duplicate)
	}
	if duplicate.RenderGeneration != 0 || duplicate.RenderOutcome != "" || duplicate.RenderReason != "" ||
		duplicate.NotificationTrace != "" || duplicate.NotificationOutcome != "" || duplicate.NotificationReason != "" ||
		len(duplicate.ProbeTraceIDs) != 0 {
		t.Fatalf("duplicate inherited downstream causality: %+v", duplicate)
	}
}

func TestDuplicateReplayExportOmitsNotificationCausality(t *testing.T) {
	t.Parallel()

	sock, _, handler := newTestServerWithRef(t)
	client := ipc.NewClientWithSocket(sock)
	request := protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 42, Event: "PreToolUse", Harness: harness.Claude,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "session-1"}),
		RequestID:   "logical-hook-1", TraceID: "trace-shared", Attempt: 1,
	}
	first, err := client.Send(request)
	if err != nil || first.Delivery == nil || first.Delivery.Status != protocol.DeliveryAcknowledgementAccepted {
		t.Fatalf("first response = %+v, err = %v", first, err)
	}

	handler.recordNotificationDisposition(notification.NotificationDisposition{
		Trace: notification.NotificationTrace{
			TraceID: request.TraceID, RequestID: request.RequestID,
		},
		WindowID: request.WindowID,
		Action:   "permission",
		Stage:    "dispatch",
		Outcome:  "sent",
		Reason:   "send_success",
	})

	request.Attempt = 2
	retry, err := client.Send(request)
	if err != nil || retry.Delivery == nil || retry.Delivery.Status != protocol.DeliveryAcknowledgementDuplicate {
		t.Fatalf("retry response = %+v, err = %v", retry, err)
	}

	exported, err := client.Send(protocol.Request{Type: protocol.TypeEventsExport, WindowID: 42})
	if err != nil {
		t.Fatalf("events export: %v", err)
	}
	events := exported.EventsByWindow[42]
	if len(events) != 2 {
		t.Fatalf("exported events = %+v; want original and duplicate", events)
	}
	original, duplicate := events[0], events[1]
	if original.NotificationTrace != request.TraceID || original.NotificationOutcome != "sent" ||
		original.NotificationReason != "send_success" {
		t.Fatalf("original notification disposition = %+v", original)
	}
	if duplicate.TraceID != request.TraceID || duplicate.ReducerDecision != protocol.DecisionDuplicate ||
		duplicate.DecisionReason != protocol.DecisionReasonRequestReplayed {
		t.Fatalf("duplicate decision = %+v", duplicate)
	}
	if duplicate.NotificationTrace != "" || duplicate.NotificationOutcome != "" || duplicate.NotificationReason != "" {
		t.Fatalf("duplicate inherited notification causality: %+v", duplicate)
	}
}

func TestProbeLinkSkipsDuplicateReplayRows(t *testing.T) {
	t.Parallel()

	sock, _, handler := newTestServerWithRef(t)
	client := ipc.NewClientWithSocket(sock)
	request := protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 42, Event: "PreToolUse", Harness: harness.Claude,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "session-1"}),
		RequestID:   "logical-hook-1", TraceID: "trace-shared", Attempt: 1,
	}
	first, err := client.Send(request)
	if err != nil || first.Delivery == nil || first.Delivery.Status != protocol.DeliveryAcknowledgementAccepted {
		t.Fatalf("first response = %+v, err = %v", first, err)
	}

	request.Attempt = 2
	retry, err := client.Send(request)
	if err != nil || retry.Delivery == nil || retry.Delivery.Status != protocol.DeliveryAcknowledgementDuplicate {
		t.Fatalf("retry response = %+v, err = %v", retry, err)
	}

	handler.linkProbeChild(request.TraceID, "probe-child", []int{request.WindowID})
	detail, err := client.Send(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: request.WindowID})
	if err != nil || len(detail.Events) != 2 {
		t.Fatalf("detail = %+v, err = %v", detail, err)
	}
	original, duplicate := detail.Events[0], detail.Events[1]
	if original.TraceID != request.TraceID || original.ReducerDecision != protocol.DecisionApplied ||
		len(original.ProbeTraceIDs) != 1 || original.ProbeTraceIDs[0] != "probe-child" {
		t.Fatalf("original probe causality = %+v", original)
	}
	if duplicate.TraceID != request.TraceID || duplicate.ReducerDecision != protocol.DecisionDuplicate ||
		duplicate.DecisionReason != protocol.DecisionReasonRequestReplayed {
		t.Fatalf("duplicate decision = %+v", duplicate)
	}
	if len(duplicate.ProbeTraceIDs) != 0 {
		t.Fatalf("duplicate inherited probe causality: %+v", duplicate)
	}
}
