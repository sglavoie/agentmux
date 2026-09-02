package events

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// SessionEventFromRequest projects an accepted request into the durable
// session event the window retains. It is the one place that decides which
// request fields survive into history, so a field added to the request but not
// here is deliberately not part of the record.
func SessionEventFromRequest(req protocol.Request, action, resultIconState, currentIconState string) protocol.SessionEvent {
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
		RouteEvidence:        CloneRoutingEvidence(req.RouteEvidence),
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
		ev.IdentityConfidence = IdentityConfidenceFor(*req.Normalized)
		ev.SessionID = req.Normalized.SessionID
		ev.RunID = req.Normalized.RunID
		ev.TurnID = req.Normalized.TurnID
		ev.ToolUseID = req.Normalized.ToolUse
		if ev.SourceEventID == "" {
			ev.SourceEventID = req.Normalized.SourceEventID
		}
		if ev.NormalizationOutcome == "" {
			ev.NormalizationOutcome = NormalizationOutcomeFor(*req.Normalized)
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

// SuppressedSessionEvent is the same record for an event the daemon accepted
// but declined to act on: it preserves the icon and carries why.
func SuppressedSessionEvent(req protocol.Request, action, suppressionReason, currentIconState string) protocol.SessionEvent {
	ev := SessionEventFromRequest(req, action, protocol.IconStatePreserve, currentIconState)
	ev.Suppressed = true
	ev.SuppressionReason = suppressionReason
	return ev
}
