package daemon

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
)

const (
	suppressionReasonGuardBlocked           = protocol.DecisionReasonGuardBlocked
	suppressionReasonSubagentStillRunning   = protocol.DecisionReasonSubagentStillRunning
	suppressionReasonCopilotMidSubagent     = protocol.DecisionReasonCopilotMidSubagent
	suppressionReasonCopilotPendingPrompt   = protocol.DecisionReasonCopilotPendingPrompt
	suppressionReasonActiveAfterTerminal    = protocol.DecisionReasonActiveAfterTerminal
	suppressionReasonElicitationPending     = protocol.DecisionReasonElicitationPending
	suppressionReasonUnknownEvent           = protocol.DecisionReasonUnknownEvent
	suppressionReasonAutoApprovedPerm       = protocol.DecisionReasonAutoApprovedPermission
	suppressionReasonOwnerMismatch          = protocol.DecisionReasonOwnerMismatch
	suppressionReasonUnmanagedPipeSource    = protocol.DecisionReasonUnmanagedPipeSource
	suppressionReasonIgnoredEvent           = protocol.DecisionReasonIgnoredEvent
	suppressionReasonInvalidPayload         = protocol.DecisionReasonInvalidPayload
	suppressionReasonTerminalStopHeld       = protocol.DecisionReasonTerminalStopHeld
	suppressionReasonTerminalStopSuperseded = protocol.DecisionReasonTerminalStopSuperseded
	suppressionReasonTerminalStopBypassed   = protocol.DecisionReasonTerminalStopBypassed
)

func (h *Handler) lastKnownState(windowID int) (action, iconState string) {
	h.stateMu.Lock()
	if st, ok := h.windowStateLocked(windowID); ok {
		action = st.LastAction
		iconState = rendering.DesiredIcon(st)
	}
	h.stateMu.Unlock()
	return action, iconState
}

func sessionEventFromRequest(req protocol.Request, action, resultIconState, currentIconState string) protocol.SessionEvent {
	now := time.Now()
	ev := protocol.SessionEvent{
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
		QuestionPreview:      req.QuestionPreview,
		RouteDecision:        req.RouteDecision,
		OriginWindowID:       req.WindowID,
		TraceID:              req.TraceID,
		ParentTraceID:        req.ParentTraceID,
		RequestID:            req.RequestID,
		SourceEventID:        req.SourceEventID,
		Attempt:              req.Attempt,
		PayloadSize:          req.PayloadSize,
		PayloadHMAC:          req.PayloadHMAC,
		PayloadEncoding:      req.PayloadEncoding,
		ParseOutcome:         req.ParseOutcome,
		NormalizationOutcome: req.NormalizationOutcome,
		ProducerHarness:      req.Harness,
		ProducerConfigDir:    req.ProducerConfigDir,
		TargetHarness:        req.TargetHarness,
		Profile:              req.Account,
		SaturationReason:     req.SaturationReason,
		StopHoldOutcome:      req.StopHoldOutcome,
		StopHoldWaitedMs:     req.StopHoldWaitedMs,
		StopConfirmedBy:      req.StopConfirmedBy,
		TargetWindowID:       req.WindowID,
		RouteEvidence:        cloneRoutingEvidence(req.RouteEvidence),
		EventRef:             req.EventRef,
	}
	// The normalized stamp carries the source-scoped identity resolved from the
	// raw payload; legacy clients and synthetic requests have none. A
	// quarantined stamp keeps its identity fields for attribution — the reducer
	// separately refuses to fold it.
	if req.Normalized != nil {
		ev.SourceKind = string(req.Normalized.Source.Kind)
		ev.SourceKey = req.Normalized.Source.Key
		ev.SourceSeq = req.Normalized.Seq
		ev.IdentityConfidence = identityConfidenceFor(*req.Normalized)
		ev.SessionID = req.Normalized.SessionID
		ev.RunID = req.Normalized.RunID
		ev.TurnID = req.Normalized.TurnID
		ev.ToolUseID = req.Normalized.ToolUse
		if ev.SourceEventID == "" {
			ev.SourceEventID = req.Normalized.SourceEventID
		}
		if ev.NormalizationOutcome == "" {
			ev.NormalizationOutcome = normalizationOutcomeFor(*req.Normalized)
		}
		if req.Normalized.OriginWindowID > 0 {
			ev.OriginWindowID = req.Normalized.OriginWindowID
		}
	}
	if ev.ParseOutcome == "" {
		ev.ParseOutcome = protocol.ParseOutcomeSynthetic
	}
	if ev.NormalizationOutcome == "" {
		ev.NormalizationOutcome = protocol.NormalizationOutcomeNotApplicable
	}
	if ev.RouteEvidence != nil && ev.RouteEvidence.TargetWindowID > 0 {
		ev.TargetWindowID = ev.RouteEvidence.TargetWindowID
	}
	return ev
}

func suppressedSessionEvent(req protocol.Request, action, suppressionReason, currentIconState string) protocol.SessionEvent {
	ev := sessionEventFromRequest(req, action, protocol.IconStatePreserve, currentIconState)
	ev.Suppressed = true
	ev.SuppressionReason = suppressionReason
	return ev
}

func shouldSuppressPendingElicitationActive(req protocol.Request, prevAction string) bool {
	if req.PreservesIcon || isCopilotRequest(req) || prevAction != "elicitation" || req.Action != "active" {
		return false
	}
	return !isElicitationResolutionEvent(req)
}

func isElicitationResolutionEvent(req protocol.Request) bool {
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
