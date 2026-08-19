package daemon

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
)

func (h *Handler) effectiveStateForTrace(windowID int) EffectiveStateEvidence {
	if h == nil || windowID <= 0 {
		return EffectiveStateEvidence{}
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if st, ok := h.windowStateLocked(windowID); ok {
		return effectiveStateEvidence(st.Effective)
	}
	return EffectiveStateEvidence{}
}

func eventByReferenceLocked(st *WindowState, ref *sessionEventReference) *SessionEvent {
	if st == nil || ref == nil || ref.daemonSeq == 0 {
		return nil
	}
	for i := len(st.EventLog) - 1; i >= 0; i-- {
		if st.EventLog[i].DaemonSeq == ref.daemonSeq {
			return &st.EventLog[i]
		}
	}
	return nil
}

func ensureDirectRouteEvidence(req Request) *RoutingEvidence {
	if req.routeEvidence != nil {
		evidence := cloneRoutingEvidence(req.routeEvidence)
		if evidence.TargetWindowID == 0 && evidence.ChosenRoute != routeDecisionRejected {
			evidence.TargetWindowID = req.WindowID
		}
		if evidence.EffectiveHarness == "" {
			evidence.EffectiveHarness = harness.NormalizeKnown(req.targetHarness)
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
	evidence.EffectiveHarness = harness.NormalizeKnown(req.targetHarness)
	if evidence.EffectiveHarness == "" {
		evidence.EffectiveHarness = harness.NormalizeKnown(req.Harness)
	}
	evidence.EffectiveProfile = req.Account
	evidence.ChosenRoute = routeDecisionSource
	evidence.SelectionReason = "direct"
	return evidence
}

func (h *Handler) finalizeBypassedStateUpdate(req Request, outcome applyOutcome, before EffectiveStateEvidence) {
	if req.WindowID <= 0 {
		return
	}
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(req.WindowID)
	event := eventByReferenceLocked(st, req.eventRef)
	if event == nil {
		fallback := sessionEventFromRequest(req, outcome.reduceAction, IconStatePreserve, desiredIconForState(st))
		fallback.Suppressed = true
		fallback.SuppressionReason = outcome.skipReason
		appended := h.appendEventLocked(req.WindowID, fallback)
		event = eventByReferenceLocked(st, req.eventRef)
		if event == nil {
			event = &appended
		}
	}
	after := effectiveStateEvidence(st.Effective)
	event.TraceID = req.TraceID
	event.RequestID = req.RequestID
	event.SourceEventID = firstNonEmpty(req.SourceEventID, event.SourceEventID)
	event.Attempt = req.Attempt
	event.ReducerDecision = DecisionBypassed
	if outcome.skipReason == "" {
		outcome.skipReason = DecisionReasonReducerBypassed
	}
	event.DecisionReason = outcome.skipReason
	event.ReducerReason = outcome.skipReason
	event.ReducerInputAction = outcome.reduceAction
	event.EffectiveBefore = &before
	event.EffectiveAfter = &after
	event.RouteEvidence = ensureDirectRouteEvidence(req)
	event.TargetWindowID = req.WindowID
	if event.IdentityConfidence == "" && req.normalized != nil {
		event.IdentityConfidence = identityConfidenceFor(*req.normalized)
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

func suppressionReasonForReducer(trace ReducerTrace, outcome applyOutcome) string {
	switch trace.Decision {
	case DecisionDuplicate:
		return DecisionReasonDuplicateSequence
	case DecisionReordered:
		return DecisionReasonReorderedSequence
	case DecisionQuarantined:
		reason := stableReducerReason(trace)
		if strings.HasPrefix(reason, DecisionReasonInvalidPayload) {
			return DecisionReasonInvalidPayload
		}
		return reason
	case DecisionSuppressed:
		stable := stableReducerReason(trace)
		switch stable {
		case DecisionReasonTerminalTurnSuppression, DecisionReasonTerminalGraceSuppression:
			return DecisionReasonActiveAfterTerminal
		default:
			return stable
		}
	default:
		if outcome.guardBlocked {
			return DecisionReasonGuardBlocked
		}
		return ""
	}
}

// finalizeReducerDecisionLocked enriches the exact event row appended by the
// legacy compatibility layer. When that layer appended nothing (for example a
// guard refusal), it creates the one authoritative row here. Caller holds
// stateMu.
func (h *Handler) finalizeReducerDecisionLocked(
	req Request,
	n NormalizedEvent,
	trace ReducerTrace,
	before, after EffectiveState,
	identityConfidence string,
	usedFallbackIdentity bool,
	outcome applyOutcome,
) SessionEvent {
	st := h.ensureWindowStateLocked(req.WindowID)
	event := eventByReferenceLocked(st, req.eventRef)
	if event == nil {
		fallback := sessionEventFromRequest(req, n.Action, IconStatePreserve, desiredIconForState(st))
		appended := h.appendEventLocked(req.WindowID, fallback)
		event = eventByReferenceLocked(st, req.eventRef)
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
	event.ParseOutcome = firstNonEmpty(req.parseOutcome, event.ParseOutcome, ParseOutcomeSynthetic)
	switch {
	case usedFallbackIdentity:
		event.NormalizationOutcome = NormalizationOutcomeUntrustedFallback
	case req.normalized == nil:
		event.NormalizationOutcome = NormalizationOutcomeLegacySynthesized
	case outcome.reduceAction != "" && req.normalized.Action != "" && outcome.reduceAction != req.normalized.Action:
		event.NormalizationOutcome = NormalizationOutcomeReducerRewrite
	default:
		event.NormalizationOutcome = firstNonEmpty(req.normalizationOutcome, normalizationOutcomeFor(n))
	}
	event.IdentityConfidence = identityConfidence
	event.UsedFallbackIdentity = usedFallbackIdentity
	event.ProducerHarness = req.Harness
	event.ProducerConfigDir = req.ProducerConfigDir
	event.TargetHarness = req.targetHarness
	if !req.targetHarnessSet {
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
	event.ReducerInput = &ReducerInputEvidence{
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
		event.DecisionReason = DecisionReasonUntrustedIdentityFallback
	}
	event.Quarantined = trace.Decision == DecisionQuarantined || usedFallbackIdentity ||
		(req.normalized != nil && req.normalized.Invalid)
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
		(trace.Decision == DecisionApplied || trace.Decision == DecisionAcknowledged) {
		h.linkRequestRenderGenerationLocked(req, st.Seq)
	}
	return cloneSessionEvent(*event)
}

// recordDuplicateDelivery appends a non-mutating causal row for an IPC retry
// that the dedupe cache replayed before reducer admission. It clones the
// original sanitized decision evidence when available, then stamps the current
// trace/attempt identity and an unchanged before/after state.
func (h *Handler) recordDuplicateDelivery(req Request, ack *DeliveryAcknowledgement) {
	if h == nil || ack == nil || ack.Status != DeliveryAcknowledgementDuplicate {
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
	var event SessionEvent
	for i := len(st.EventLog) - 1; i >= 0; i-- {
		if st.EventLog[i].RequestID == req.RequestID && req.RequestID != "" {
			event = cloneSessionEvent(st.EventLog[i])
			break
		}
	}
	if event.Event == "" {
		event = SessionEvent{
			Event: req.Event, Action: req.Action, ResultIconState: IconStatePreserve,
			ParseOutcome:         ParseOutcomeDecoded,
			NormalizationOutcome: NormalizationOutcomeNotApplicable,
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
	event.SuppressionReason = DecisionReasonRequestReplayed
	event.ResultIconState = IconStatePreserve
	event.ReducerDecision = DecisionDuplicate
	event.ReducerReason = DecisionReasonRequestReplayed
	event.DecisionReason = DecisionReasonRequestReplayed
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
	event.RouteEvidence.SelectionReason = DecisionReasonRequestReplayed
	event.RouteEvidence.EffectiveHarness = ack.TargetHarness
	event.RouteEvidence.EffectiveProfile = ack.Profile
	event = h.appendEventLocked(windowID, event)
	h.stateMu.Unlock()
	h.recordDecisionEvent(event)
}
