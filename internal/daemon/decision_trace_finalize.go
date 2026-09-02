package daemon

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/harness"
)

func (h *Handler) effectiveStateForTrace(windowID int) protocol.EffectiveStateEvidence {
	if h == nil || windowID <= 0 {
		return protocol.EffectiveStateEvidence{}
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if st, ok := h.windowStateLocked(windowID); ok {
		return effectiveStateEvidence(st.Effective)
	}
	return protocol.EffectiveStateEvidence{}
}

func eventByReferenceLocked(st *state.WindowState, ref *protocol.SessionEventReference) *protocol.SessionEvent {
	if st == nil || ref == nil || ref.DaemonSeq == 0 {
		return nil
	}
	for i := len(st.EventLog) - 1; i >= 0; i-- {
		if st.EventLog[i].DaemonSeq == ref.DaemonSeq {
			return &st.EventLog[i]
		}
	}
	return nil
}

func ensureDirectRouteEvidence(req protocol.Request) *protocol.RoutingEvidence {
	if req.RouteEvidence != nil {
		evidence := cloneRoutingEvidence(req.RouteEvidence)
		if evidence.TargetWindowID == 0 && evidence.ChosenRoute != routeDecisionRejected {
			evidence.TargetWindowID = req.WindowID
		}
		if evidence.EffectiveHarness == "" {
			evidence.EffectiveHarness = harness.NormalizeKnown(req.TargetHarness)
			if evidence.EffectiveHarness == "" {
				evidence.EffectiveHarness = harness.NormalizeKnown(req.Harness)
			}
		}
		if evidence.EffectiveProfile == "" {
			evidence.EffectiveProfile = req.Account
		}
		return evidence
	}
	evidence := newRoutingEvidence(req.WindowID)
	evidence.TargetWindowID = req.WindowID
	evidence.EffectiveHarness = harness.NormalizeKnown(req.TargetHarness)
	if evidence.EffectiveHarness == "" {
		evidence.EffectiveHarness = harness.NormalizeKnown(req.Harness)
	}
	evidence.EffectiveProfile = req.Account
	evidence.ChosenRoute = routeDecisionSource
	evidence.SelectionReason = "direct"
	return evidence
}

func (h *Handler) finalizeBypassedStateUpdate(req protocol.Request, outcome applyOutcome, before protocol.EffectiveStateEvidence) {
	if req.WindowID <= 0 {
		return
	}
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(req.WindowID)
	event := eventByReferenceLocked(st, req.EventRef)
	if event == nil {
		fallback := sessionEventFromRequest(req, outcome.reduceAction, protocol.IconStatePreserve, desiredIconForState(st))
		fallback.Suppressed = true
		fallback.SuppressionReason = outcome.skipReason
		appended := h.appendEventLocked(req.WindowID, fallback)
		event = eventByReferenceLocked(st, req.EventRef)
		if event == nil {
			event = &appended
		}
	}
	after := effectiveStateEvidence(st.Effective)
	event.TraceID = req.TraceID
	event.RequestID = req.RequestID
	event.SourceEventID = firstNonEmpty(req.SourceEventID, event.SourceEventID)
	event.Attempt = req.Attempt
	event.ReducerDecision = protocol.DecisionBypassed
	if outcome.skipReason == "" {
		outcome.skipReason = protocol.DecisionReasonReducerBypassed
	}
	event.DecisionReason = outcome.skipReason
	event.ReducerReason = outcome.skipReason
	event.ReducerInputAction = outcome.reduceAction
	event.EffectiveBefore = &before
	event.EffectiveAfter = &after
	event.RouteEvidence = ensureDirectRouteEvidence(req)
	event.TargetWindowID = req.WindowID
	if event.IdentityConfidence == "" && req.Normalized != nil {
		event.IdentityConfidence = identityConfidenceFor(*req.Normalized)
	}
	completed := cloneSessionEvent(*event)
	h.stateMu.Unlock()
	h.recordDecisionEvent(completed)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func suppressionReasonForReducer(trace state.ReducerTrace, outcome applyOutcome) string {
	switch trace.Decision {
	case protocol.DecisionDuplicate:
		return protocol.DecisionReasonDuplicateSequence
	case protocol.DecisionReordered:
		return protocol.DecisionReasonReorderedSequence
	case protocol.DecisionQuarantined:
		reason := stableReducerReason(trace)
		if strings.HasPrefix(reason, protocol.DecisionReasonInvalidPayload) {
			return protocol.DecisionReasonInvalidPayload
		}
		return reason
	case protocol.DecisionSuppressed:
		stable := stableReducerReason(trace)
		switch stable {
		case protocol.DecisionReasonTerminalTurnSuppression, protocol.DecisionReasonTerminalGraceSuppression:
			return protocol.DecisionReasonActiveAfterTerminal
		default:
			return stable
		}
	default:
		if outcome.guardBlocked {
			return protocol.DecisionReasonGuardBlocked
		}
		return ""
	}
}

// finalizeReducerDecisionLocked enriches the exact event row appended by the
// legacy compatibility layer. When that layer appended nothing (for example a
// guard refusal), it creates the one authoritative row here. Caller holds
// stateMu.
func (h *Handler) finalizeReducerDecisionLocked(
	req protocol.Request,
	n protocol.NormalizedEvent,
	trace state.ReducerTrace,
	before, after state.EffectiveState,
	identityConfidence string,
	usedFallbackIdentity bool,
	outcome applyOutcome,
) protocol.SessionEvent {
	st := h.ensureWindowStateLocked(req.WindowID)
	event := eventByReferenceLocked(st, req.EventRef)
	if event == nil {
		fallback := sessionEventFromRequest(req, n.Action, protocol.IconStatePreserve, desiredIconForState(st))
		appended := h.appendEventLocked(req.WindowID, fallback)
		event = eventByReferenceLocked(st, req.EventRef)
		if event == nil {
			event = &appended
		}
	}

	reason := suppressionReasonForReducer(trace, outcome)
	wasSuppressed := event.Suppressed
	if reason != "" {
		event.Suppressed = true
		event.SuppressionReason = reason
	}
	if event.Suppressed && !wasSuppressed && event.SuppressionReason != "" {
		meta := h.windowMetadata[req.WindowID]
		if meta.Stats.SuppressionReasons == nil {
			meta.Stats.SuppressionReasons = make(map[string]int)
		}
		meta.Stats.SuppressionReasons[event.SuppressionReason]++
		h.windowMetadata[req.WindowID] = meta
	}

	event.TraceID = req.TraceID
	event.RequestID = req.RequestID
	event.SourceEventID = firstNonEmpty(req.SourceEventID, n.SourceEventID, event.SourceEventID)
	event.Attempt = req.Attempt
	event.ParseOutcome = firstNonEmpty(req.ParseOutcome, event.ParseOutcome, protocol.ParseOutcomeSynthetic)
	switch {
	case usedFallbackIdentity:
		event.NormalizationOutcome = protocol.NormalizationOutcomeUntrustedFallback
	case req.Normalized == nil:
		event.NormalizationOutcome = protocol.NormalizationOutcomeLegacySynthesized
	case outcome.reduceAction != "" && req.Normalized.Action != "" && outcome.reduceAction != req.Normalized.Action:
		event.NormalizationOutcome = protocol.NormalizationOutcomeReducerRewrite
	default:
		event.NormalizationOutcome = firstNonEmpty(req.NormalizationOutcome, normalizationOutcomeFor(n))
	}
	event.IdentityConfidence = identityConfidence
	event.UsedFallbackIdentity = usedFallbackIdentity
	event.ProducerHarness = req.Harness
	event.ProducerConfigDir = req.ProducerConfigDir
	event.TargetHarness = req.TargetHarness
	if !req.TargetHarnessSet {
		event.TargetHarness = req.Harness
	}
	event.Profile = req.Account
	event.SessionID = n.SessionID
	event.RunID = n.RunID
	event.TargetWindowID = req.WindowID
	event.SourceKind = string(n.Source.Kind)
	event.SourceKey = n.Source.Key
	event.SourceSeq = n.Seq
	if n.OriginWindowID > 0 {
		event.OriginWindowID = n.OriginWindowID
	}
	event.ReducerInputAction = n.Action
	event.ReducerInput = &protocol.ReducerInputEvidence{
		EmittedAt: n.At, OriginWindowID: n.OriginWindowID, Prompt: n.Prompt,
		PipeMode: n.PipeMode, ASLPhase: n.Phase, EndsSession: n.EndsSession,
		SubagentDelta: n.SubagentDelta, GuardDuration: h.reducerCfg.GuardDuration,
		StaleActive: h.reducerCfg.StaleActive, StaleSubagent: h.reducerCfg.StaleSubagent,
		StaleCompact: h.reducerCfg.StaleCompact,
	}
	event.ReducerDecision = trace.Decision
	event.ReducerReason = stableReducerReason(trace)
	event.DecisionReason = event.ReducerReason
	if usedFallbackIdentity {
		event.DecisionReason = protocol.DecisionReasonUntrustedIdentityFallback
	}
	event.Quarantined = trace.Decision == protocol.DecisionQuarantined || usedFallbackIdentity ||
		(req.Normalized != nil && req.Normalized.Invalid)
	beforeEvidence := effectiveStateEvidence(before)
	afterEvidence := effectiveStateEvidence(after)
	event.EffectiveBefore = &beforeEvidence
	event.EffectiveAfter = &afterEvidence
	event.RouteEvidence = ensureDirectRouteEvidence(req)
	if event.RouteDecision == "" {
		event.RouteDecision = event.RouteEvidence.ChosenRoute
	}
	// Some reducer-only mutations do not pass through the legacy dirty-marking
	// path. Link those applied/acknowledged rows to the generation the reducer
	// just dirtied so their asynchronous render remains followable.
	if event.RenderGeneration == 0 && st.Dirty &&
		(trace.Decision == protocol.DecisionApplied || trace.Decision == protocol.DecisionAcknowledged) {
		h.linkRequestRenderGenerationLocked(req, st.Seq)
	}
	return cloneSessionEvent(*event)
}

// recordDuplicateDelivery appends a non-mutating causal row for an IPC retry
// that the dedupe cache replayed before reducer admission. It clones the
// original sanitized decision evidence when available, then stamps the current
// trace/attempt identity and an unchanged before/after state.
func (h *Handler) recordDuplicateDelivery(req protocol.Request, ack *protocol.DeliveryAcknowledgement) {
	if h == nil || ack == nil || ack.Status != protocol.DeliveryAcknowledgementDuplicate {
		return
	}
	windowID := ack.TargetWindowID
	if windowID <= 0 {
		windowID = req.WindowID
	}
	if windowID <= 0 {
		return
	}

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(windowID)
	var event protocol.SessionEvent
	for i := len(st.EventLog) - 1; i >= 0; i-- {
		if st.EventLog[i].RequestID == req.RequestID && req.RequestID != "" {
			event = cloneSessionEvent(st.EventLog[i])
			break
		}
	}
	if event.Event == "" {
		event = protocol.SessionEvent{
			Event: req.Event, Action: req.Action, ResultIconState: protocol.IconStatePreserve,
			ParseOutcome:         protocol.ParseOutcomeDecoded,
			NormalizationOutcome: protocol.NormalizationOutcomeNotApplicable,
			OriginWindowID:       req.WindowID,
		}
	}
	now := time.Now()
	event.At = now.Unix()
	event.ReceivedAtNs = now.UnixNano()
	event.DaemonSeq = 0
	event.TraceID = req.TraceID
	event.RequestID = req.RequestID
	event.SourceEventID = firstNonEmpty(req.SourceEventID, event.SourceEventID)
	event.Attempt = req.Attempt
	event.Suppressed = true
	event.SuppressionReason = protocol.DecisionReasonRequestReplayed
	event.ResultIconState = protocol.IconStatePreserve
	event.ReducerDecision = protocol.DecisionDuplicate
	event.ReducerReason = protocol.DecisionReasonRequestReplayed
	event.DecisionReason = protocol.DecisionReasonRequestReplayed
	event.RenderGeneration = 0
	event.RenderOutcome = ""
	event.RenderReason = ""
	event.NotificationTrace = ""
	event.NotificationOutcome = ""
	event.NotificationReason = ""
	event.ProbeTraceIDs = nil
	event.TargetWindowID = windowID
	event.RouteDecision = ack.RouteDecision
	effective := effectiveStateEvidence(st.Effective)
	event.EffectiveBefore = &effective
	effectiveAfter := effective
	event.EffectiveAfter = &effectiveAfter
	if event.RouteEvidence == nil {
		event.RouteEvidence = newRoutingEvidence(req.WindowID)
	}
	event.RouteEvidence.TargetWindowID = windowID
	event.RouteEvidence.ChosenRoute = ack.RouteDecision
	event.RouteEvidence.SelectionReason = protocol.DecisionReasonRequestReplayed
	event.RouteEvidence.EffectiveHarness = ack.TargetHarness
	event.RouteEvidence.EffectiveProfile = ack.Profile
	event = h.appendEventLocked(windowID, event)
	h.stateMu.Unlock()
	h.recordDecisionEvent(event)
}
