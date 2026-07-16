package daemon

import (
	"strings"
	"time"
)

const (
	suppressionReasonGuardBlocked         = DecisionReasonGuardBlocked
	suppressionReasonSubagentStillRunning = DecisionReasonSubagentStillRunning
	suppressionReasonCopilotMidSubagent   = DecisionReasonCopilotMidSubagent
	suppressionReasonCopilotPendingPrompt = DecisionReasonCopilotPendingPrompt
	suppressionReasonActiveAfterTerminal  = DecisionReasonActiveAfterTerminal
	suppressionReasonElicitationPending   = DecisionReasonElicitationPending
	suppressionReasonUnknownEvent         = DecisionReasonUnknownEvent
	suppressionReasonAutoApprovedPerm     = DecisionReasonAutoApprovedPermission
	suppressionReasonOwnerMismatch        = DecisionReasonOwnerMismatch
	suppressionReasonIgnoredEvent         = DecisionReasonIgnoredEvent
	suppressionReasonInvalidPayload       = DecisionReasonInvalidPayload
)

func (h *Handler) lastKnownState(windowID int) (action, iconState string) {
	h.stateMu.Lock()
	if st, ok := h.windowStateLocked(windowID); ok {
		action = st.LastAction
		iconState = desiredIconForState(st)
	}
	h.stateMu.Unlock()
	return action, iconState
}

func sessionEventFromRequest(req Request, action, resultIconState, currentIconState string) SessionEvent {
	now := time.Now()
	ev := SessionEvent{
		At:                   now.Unix(),
		ReceivedAtNs:         now.UnixNano(),
		Event:                req.Event,
		Action:               action,
		ToolName:             req.ToolName,
		Source:               req.Source,
		PreservesIcon:        req.PreservesIcon,
		ResultIconState:      resultIconState,
		CurrentIconState:     currentIconState,
		NotificationType:     req.NotificationType,
		StopReason:           req.StopReason,
		Reason:               req.Reason,
		ContextLimit:         req.ContextLimit,
		ResetFlow:            req.ResetFlow,
		RouteDecision:        req.routeDecision,
		OriginWindowID:       req.WindowID,
		TraceID:              req.TraceID,
		ParentTraceID:        req.parentTraceID,
		RequestID:            req.RequestID,
		SourceEventID:        req.SourceEventID,
		Attempt:              req.Attempt,
		PayloadSize:          req.PayloadSize,
		PayloadHMAC:          req.PayloadHMAC,
		PayloadEncoding:      req.PayloadEncoding,
		ParseOutcome:         req.parseOutcome,
		NormalizationOutcome: req.normalizationOutcome,
		ProducerHarness:      req.Harness,
		TargetHarness:        req.targetHarness,
		Profile:              req.Account,
		SaturationReason:     req.saturationReason,
		TargetWindowID:       req.WindowID,
		RouteEvidence:        cloneRoutingEvidence(req.routeEvidence),
		eventRef:             req.eventRef,
	}
	// The normalized stamp carries the source-scoped identity resolved from the
	// raw payload; legacy clients and synthetic requests have none. A
	// quarantined stamp keeps its identity fields for attribution — the reducer
	// separately refuses to fold it.
	if req.normalized != nil {
		ev.SourceKind = string(req.normalized.Source.Kind)
		ev.SourceKey = req.normalized.Source.Key
		ev.SourceSeq = req.normalized.Seq
		ev.IdentityConfidence = identityConfidenceFor(*req.normalized)
		ev.SessionID = req.normalized.SessionID
		ev.RunID = req.normalized.RunID
		ev.TurnID = req.normalized.TurnID
		ev.ToolUseID = req.normalized.ToolUse
		if ev.SourceEventID == "" {
			ev.SourceEventID = req.normalized.SourceEventID
		}
		if ev.NormalizationOutcome == "" {
			ev.NormalizationOutcome = normalizationOutcomeFor(*req.normalized)
		}
		if req.normalized.OriginWindowID > 0 {
			ev.OriginWindowID = req.normalized.OriginWindowID
		}
	}
	if ev.ParseOutcome == "" {
		ev.ParseOutcome = ParseOutcomeSynthetic
	}
	if ev.NormalizationOutcome == "" {
		ev.NormalizationOutcome = NormalizationOutcomeNotApplicable
	}
	if ev.RouteEvidence != nil && ev.RouteEvidence.TargetWindowID > 0 {
		ev.TargetWindowID = ev.RouteEvidence.TargetWindowID
	}
	return ev
}

func suppressedSessionEvent(req Request, action, suppressionReason, currentIconState string) SessionEvent {
	ev := sessionEventFromRequest(req, action, IconStatePreserve, currentIconState)
	ev.Suppressed = true
	ev.SuppressionReason = suppressionReason
	return ev
}

func shouldSuppressPendingElicitationActive(req Request, prevAction string) bool {
	if req.PreservesIcon || isCopilotRequest(req) || prevAction != "elicitation" || req.Action != "active" {
		return false
	}
	return !isElicitationResolutionEvent(req)
}

func isElicitationResolutionEvent(req Request) bool {
	if req.IsPrompt {
		return true
	}
	switch req.Event {
	case "ElicitationResult":
		return true
	case "PostToolUse", "postToolUse":
		return strings.EqualFold(strings.TrimSpace(req.ToolName), "request_user_input")
	default:
		return false
	}
}
