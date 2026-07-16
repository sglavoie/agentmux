package daemon

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// normalizeProducerFields makes the additive ProducerHarness field
// authoritative when present while keeping the legacy Harness field usable by
// every existing handler path.
func normalizeProducerFields(req Request) Request {
	if producer := strings.TrimSpace(req.ProducerHarness); producer != "" {
		req.Harness = producer
	} else {
		req.ProducerHarness = req.Harness
	}
	return req
}

func baseDeliveryAcknowledgement(req Request, status, reason string) *DeliveryAcknowledgement {
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
	return &DeliveryAcknowledgement{
		SchemaVersion:   DeliveryAcknowledgementSchemaVersion,
		Status:          status,
		Reason:          reason,
		TraceID:         req.TraceID,
		RequestID:       req.RequestID,
		SourceEventID:   req.SourceEventID,
		Attempt:         req.Attempt,
		ProducerHarness: producer,
		SourceWindowID:  req.WindowID,
		TargetWindowID:  req.WindowID,
		RouteDecision:   routeDecisionSource,
	}
}

func cloneDeliveryAcknowledgement(ack *DeliveryAcknowledgement) *DeliveryAcknowledgement {
	if ack == nil {
		return nil
	}
	copy := *ack
	return &copy
}

// attachHookDeliveryAcknowledgement enriches the legacy hook response with a
// stable acknowledgement and side-effect-free routing identity. It runs after
// the handler so accepted legacy behavior is unchanged.
func (h *Handler) attachHookDeliveryAcknowledgement(req Request, resp Response) Response {
	status := DeliveryAcknowledgementAccepted
	reason := "accepted"
	if !resp.OK {
		status = DeliveryAcknowledgementRejected
		reason = strings.TrimSpace(resp.Error)
		if reason == "" {
			reason = "daemon_rejected"
		}
	}

	payload, payloadErr := hookevent.DecodeHookPayload(strings.NewReader(req.PayloadJSON))
	switch {
	case req.WindowID <= 0:
		status, reason = DeliveryAcknowledgementRejected, "invalid_window"
	case strings.TrimSpace(req.Event) == "":
		status, reason = DeliveryAcknowledgementRejected, "missing_event"
	case payloadErr != nil:
		status, reason = DeliveryAcknowledgementRejected, InvalidPayloadReason
	case hookevent.IsIgnoredEventForHarness(req.Event, payload, req.Harness):
		reason = suppressionReasonIgnoredEvent
	case func() bool {
		_, ok := hookevent.ResolveActionForHarness(req.Event, payload, req.Harness)
		return !ok
	}():
		status, reason = DeliveryAcknowledgementRejected, suppressionReasonUnknownEvent
	}

	ack := baseDeliveryAcknowledgement(req, status, reason)
	ctx := h.resolveHookEventContext(req)
	ack.Profile = ctx.req.Account
	if ctx.req.Harness != "" {
		ack.ProducerHarness = ctx.req.Harness
	}
	ack.TargetHarness = ctx.req.targetHarness
	ack.WindowFound = ctx.win != nil
	_, _, ack.SnapshotReady = h.cachedSnapshot()
	if shouldResolveUnmanagedHookRoute(req.Event, ctx) && ctx.owner != nil {
		ack.TargetWindowID = ctx.owner.ID
		ack.TargetHarness = ctx.owner.UserVars.EffectiveHarness()
		ack.Profile = ctx.owner.UserVars.EffectiveProfile()
		ack.RouteDecision = routeDecisionOwner
	} else if ctx.lookupFailed {
		ack.RouteDecision = routeDecisionSourceFallback
	}
	resp.Delivery = ack
	return resp
}

// handleDiagnosticProbe exercises the same cached Kitty routing discovery as
// hook ingress but never enters the reducer queue and never mutates handler
// state. It is the stable IPC primitive used by debugger probes.
func (h *Handler) handleDiagnosticProbe(req Request) Response {
	if req.WindowID <= 0 {
		return Response{
			OK:       false,
			Error:    "diagnostic probe requires a positive window ID",
			Delivery: baseDeliveryAcknowledgement(req, DeliveryAcknowledgementRejected, "invalid_window"),
		}
	}

	ctx := h.resolveHookEventContext(req)
	ack := baseDeliveryAcknowledgement(req, DeliveryAcknowledgementAccepted, "routing_discovered")
	ack.Profile = ctx.req.Account
	if ctx.req.Harness != "" {
		ack.ProducerHarness = ctx.req.Harness
	}
	ack.TargetHarness = ctx.req.targetHarness
	ack.WindowFound = ctx.win != nil
	_, _, ack.SnapshotReady = h.cachedSnapshot()
	if ctx.win != nil && ctx.owner != nil && ctx.owner.ID != ctx.win.ID &&
		ctx.win.UserVars.EffectiveProfile() == "" && ctx.win.UserVars.EffectiveHarness() == "" {
		ack.TargetWindowID = ctx.owner.ID
		ack.TargetHarness = ctx.owner.UserVars.EffectiveHarness()
		ack.Profile = ctx.owner.UserVars.EffectiveProfile()
		ack.RouteDecision = routeDecisionOwner
	} else if ctx.lookupFailed {
		ack.RouteDecision = routeDecisionSourceFallback
	}
	return Response{OK: true, Delivery: ack}
}
