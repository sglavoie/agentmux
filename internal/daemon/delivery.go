package daemon

import (
	"log/slog"
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

func baseDeliveryAcknowledgement(req protocol.Request, status, reason string) *protocol.DeliveryAcknowledgement {
	producer := harness.NormalizeKnown(req.ProducerHarness)
	if producer == "" {
		producer = strings.TrimSpace(req.ProducerHarness)
	}
	if producer == "" {
		producer = harness.NormalizeKnown(req.Harness)
		if producer == "" {
			producer = strings.TrimSpace(req.Harness)
		}
	}
	ack := &protocol.DeliveryAcknowledgement{
		SchemaVersion:   protocol.DeliveryAcknowledgementSchemaVersion,
		Status:          status,
		Reason:          reason,
		TraceID:         req.TraceID,
		RequestID:       req.RequestID,
		SourceEventID:   req.SourceEventID,
		Attempt:         req.Attempt,
		ProducerHarness: producer,
		SourceWindowID:  req.WindowID,
		TargetWindowID:  req.WindowID,
		RouteDecision:   events.RouteDecisionSource,
	}
	ack.RuntimeSessionID = strings.TrimSpace(req.RuntimeSessionID)
	if req.Origin != nil && req.Origin.RuntimeSessionID != "" {
		ack.RuntimeSessionID = req.Origin.RuntimeSessionID
	}
	if isRuntimeSyntheticSlot(req.WindowID) {
		// A non-Kitty slot is a daemon-internal key, not a window anyone can
		// address; the runtime session ID is the routing identity on the wire.
		ack.SourceWindowID, ack.TargetWindowID = 0, 0
	}
	return ack
}

// rejectHookOrigin answers a delivery whose runtime identity could not be
// translated to a slot. Nothing is reduced, logged to a window, or rendered:
// the delivery has no slot it is entitled to touch.
func (h *Handler) rejectHookOrigin(req protocol.Request, rejection *originRejection) protocol.Response {
	slog.Debug("hook delivery refused at origin translation",
		"reason", rejection.reason,
		"runtime_session_id", strings.TrimSpace(req.RuntimeSessionID),
		"window_id", req.WindowID,
		"event", req.Event,
	)
	return protocol.Response{
		OK:        false,
		Error:     rejection.reason,
		Retryable: rejection.retryable,
		Delivery:  baseDeliveryAcknowledgement(req, protocol.DeliveryAcknowledgementRejected, rejection.reason),
	}
}

// attachHookDeliveryAcknowledgement enriches the legacy hook response with a
// stable acknowledgement and side-effect-free routing identity. It runs after
// the handler so accepted legacy behavior is unchanged.
func (h *Handler) attachHookDeliveryAcknowledgement(req protocol.Request, resp protocol.Response) protocol.Response {
	status := protocol.DeliveryAcknowledgementAccepted
	reason := "accepted"
	if !resp.OK {
		status = protocol.DeliveryAcknowledgementRejected
		reason = strings.TrimSpace(resp.Error)
		if reason == "" {
			reason = "daemon_rejected"
		}
	}

	payload, payloadErr := hookevent.DecodeHookPayload(strings.NewReader(req.PayloadJSON))
	switch {
	case req.WindowID <= 0:
		status, reason = protocol.DeliveryAcknowledgementRejected, "invalid_window"
	case strings.TrimSpace(req.Event) == "":
		status, reason = protocol.DeliveryAcknowledgementRejected, "missing_event"
	case payloadErr != nil:
		status, reason = protocol.DeliveryAcknowledgementRejected, events.InvalidPayloadReason
	case hookevent.IsIgnoredEventForHarness(req.Event, payload, req.Harness):
		reason = suppressionReasonIgnoredEvent
	case func() bool {
		_, ok := hookevent.ResolveActionForHarness(req.Event, payload, req.Harness)
		return !ok
	}():
		status, reason = protocol.DeliveryAcknowledgementRejected, suppressionReasonUnknownEvent
	}

	ack := baseDeliveryAcknowledgement(req, status, reason)
	ctx := h.resolveHookEventContext(req)
	ack.Profile = ctx.req.Account
	if ctx.req.Harness != "" {
		ack.ProducerHarness = ctx.req.Harness
	}
	ack.TargetHarness = ctx.req.TargetHarness
	ack.WindowFound = ctx.win != nil
	_, _, ack.SnapshotReady = h.cachedSnapshot()
	if shouldResolveUnmanagedHookRoute(req.Event, ctx) && ctx.owner != nil {
		ack.TargetWindowID = ctx.owner.ID
		ack.TargetHarness = ctx.owner.UserVars.EffectiveHarness()
		ack.Profile = ctx.owner.UserVars.EffectiveProfile()
		ack.RouteDecision = events.RouteDecisionOwner
	} else if ctx.lookupFailed {
		ack.RouteDecision = events.RouteDecisionSourceFallback
	}
	resp.Delivery = ack
	return resp
}

// handleDiagnosticProbe exercises the same cached Kitty routing discovery as
// hook ingress but never enters the reducer queue and never mutates handler
// state. It is the stable IPC primitive used by debugger probes.
func (h *Handler) handleDiagnosticProbe(req protocol.Request) protocol.Response {
	if req.WindowID <= 0 {
		return protocol.Response{
			OK:       false,
			Error:    "diagnostic probe requires a positive window ID",
			Delivery: baseDeliveryAcknowledgement(req, protocol.DeliveryAcknowledgementRejected, "invalid_window"),
		}
	}

	ctx := h.resolveHookEventContext(req)
	ack := baseDeliveryAcknowledgement(req, protocol.DeliveryAcknowledgementAccepted, "routing_discovered")
	ack.Profile = ctx.req.Account
	if ctx.req.Harness != "" {
		ack.ProducerHarness = ctx.req.Harness
	}
	ack.TargetHarness = ctx.req.TargetHarness
	ack.WindowFound = ctx.win != nil
	_, _, ack.SnapshotReady = h.cachedSnapshot()
	if ctx.win != nil && ctx.owner != nil && ctx.owner.ID != ctx.win.ID &&
		ctx.win.UserVars.EffectiveProfile() == "" && ctx.win.UserVars.EffectiveHarness() == "" {
		ack.TargetWindowID = ctx.owner.ID
		ack.TargetHarness = ctx.owner.UserVars.EffectiveHarness()
		ack.Profile = ctx.owner.UserVars.EffectiveProfile()
		ack.RouteDecision = events.RouteDecisionOwner
	} else if ctx.lookupFailed {
		ack.RouteDecision = events.RouteDecisionSourceFallback
	}
	return protocol.Response{OK: true, Delivery: ack}
}
