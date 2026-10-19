package daemon

import (
	"github.com/sglavoie/agentmux/internal/runtimesession"
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
	ack.SourceWindowID = h.deliveryKittyWindowID(ack.SourceWindowID)
	ack.TargetWindowID = h.deliveryKittyWindowID(ack.TargetWindowID)
	resp.Delivery = ack
	return resp
}

func (h *Handler) deliveryKittyWindowID(slot int) int {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if key, mapped := h.runtimeRoutes.kittyWindow(slot); mapped {
		return key.windowID
	}
	return slot
}

// handleDiagnosticProbe exercises the same cached Kitty routing discovery as
// hook ingress but never enters the reducer queue and never mutates handler
// state. It is the stable IPC primitive used by debugger probes.
func (h *Handler) handleDiagnosticProbe(req protocol.Request) protocol.Response {
	if strings.TrimSpace(req.RuntimeSessionID) != "" {
		return h.probeRuntimeSession(req)
	}
	if req.WindowID <= 0 {
		return protocol.Response{
			OK:       false,
			Error:    "diagnostic probe requires a positive window ID",
			Delivery: baseDeliveryAcknowledgement(req, protocol.DeliveryAcknowledgementRejected, "invalid_window"),
		}
	}
	if isReusableKittySocket(req.KittyListenOn) {
		h.stateMu.Lock()
		req.WindowID = h.runtimeRoutes.kittySlotOf(req.KittyListenOn, req.WindowID)
		h.stateMu.Unlock()
	}

	ctx := h.resolveHookEventContextReadOnly(req)
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
	ack.SourceWindowID = h.deliveryKittyWindowID(ack.SourceWindowID)
	ack.TargetWindowID = h.deliveryKittyWindowID(ack.TargetWindowID)
	return protocol.Response{OK: true, Delivery: ack}
}

// probeRuntimeSession answers a diagnostic probe that names a runtime session:
// where a managed hook from that session would be routed, or why it would be
// refused. It reads the registry and the routes and changes neither; unlike
// translateHookOrigin it claims no slot and releases none, so probing a
// session that has not reported yet leaves it unreported.
//
// The acknowledgement names the session, not a window: a slot only the daemon
// can name is never returned as a window ID.
func (h *Handler) probeRuntimeSession(req protocol.Request) protocol.Response {
	req.WindowID = 0
	reject := func(reason string) protocol.Response {
		ack := baseDeliveryAcknowledgement(req, protocol.DeliveryAcknowledgementRejected, reason)
		ack.RuntimeSessionID = strings.TrimSpace(req.RuntimeSessionID)
		ack.RouteDecision = events.RouteDecisionRejected
		return protocol.Response{OK: false, Error: reason, Retryable: protocol.RetryableOriginReason(reason), Delivery: ack}
	}
	id, err := runtimesession.ParseID(strings.TrimSpace(req.RuntimeSessionID))
	if err != nil {
		return reject(protocol.OriginReasonInvalidRuntimeSession)
	}
	req.RuntimeSessionID = string(id)
	if h.runtimeDirectory == nil {
		return reject(protocol.OriginReasonRuntimeRegistryUnavailable)
	}
	record, found, err := h.runtimeDirectory.Lookup(id)
	switch {
	case !found && err != nil:
		return reject(protocol.OriginReasonRuntimeRegistryUnavailable)
	case !found:
		return reject(protocol.OriginReasonRuntimeSessionUnknown)
	}
	switch record.State {
	case runtimesession.StateLive:
	case runtimesession.StatePending:
		return reject(protocol.OriginReasonRuntimeSessionPending)
	default:
		return reject(protocol.OriginReasonRuntimeSessionEnded)
	}
	target, ok := runtimeTargetOf(record.Target)
	if !ok {
		return reject(protocol.OriginReasonInvalidRuntimeTarget)
	}

	h.stateMu.Lock()
	slot, bound := h.runtimeRoutes.byRuntime[string(id)]
	binding, _ := h.runtimeRoutes.binding(slot)
	label := h.runtimeLabels[string(id)]
	_, held := h.windowStateLocked(slot)
	h.stateMu.Unlock()
	if bound && binding.target != target {
		// The session is routed at another target generation: its next hook
		// would be refused until the registry and the routes agree.
		return reject(protocol.OriginReasonTargetConflict)
	}

	ack := baseDeliveryAcknowledgement(req, protocol.DeliveryAcknowledgementAccepted, "routing_discovered")
	ack.RuntimeSessionID = string(id)
	ack.Profile = label.profile
	ack.TargetHarness = label.harness
	ack.WindowFound = bound && held
	if target.backend == string(runtimesession.BackendKitty) {
		ack.SourceWindowID, _ = kittyWindowSlot(target.pane)
		ack.TargetWindowID = ack.SourceWindowID
		_, _, ack.SnapshotReady = h.cachedSnapshot()
	}
	return protocol.Response{OK: true, Delivery: ack}
}
