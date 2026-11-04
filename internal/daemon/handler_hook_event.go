package daemon

import (
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

func (h *Handler) handleHookEvent(req protocol.Request) protocol.Response {
	defer h.persistConversationBindings()
	eventOrAction := strings.TrimSpace(req.Event)
	if eventOrAction == "" {
		ctx := h.resolveHookEventContext(req)
		ctx.req.ParseOutcome = protocol.ParseOutcomeMissingEvent
		ctx.req.NormalizationOutcome = protocol.NormalizationOutcomeNotApplicable
		ctx.req.RouteDecision = events.RouteDecisionRejected
		events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionRejected, protocol.DecisionReasonMissingEvent, nil)
		h.appendSuppressedHookEvent(ctx.req, "", protocol.DecisionReasonMissingEvent)
		slog.Debug("hook event ignored: missing event")
		return protocol.Response{OK: true}
	}

	payload, err := hookevent.DecodeHookPayload(strings.NewReader(req.PayloadJSON))
	if err != nil {
		ctx := h.resolveHookEventContext(req)
		ctx.req.ParseOutcome = protocol.ParseOutcomeInvalid
		ctx.req.NormalizationOutcome = protocol.NormalizationOutcomeQuarantined
		invalid := events.Quarantine(eventOrAction, protocol.DecisionReasonInvalidPayload)
		ctx.req.Normalized = &invalid
		ctx.req.RouteDecision = events.RouteDecisionRejected
		events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionRejected, protocol.DecisionReasonInvalidPayload, nil)
		if ctx.req.WindowID > 0 {
			ctx.req.Event = eventOrAction
			h.appendSuppressedHookEvent(ctx.req, "", suppressionReasonInvalidPayload)
		}
		// Counted only for lifecycle boundaries. An unreadable harness payload
		// is a Claude/Copilot/Codex problem, and folding it into the ASL
		// delivery counters would make the pairing look broken by something it
		// has no part in.
		if events.IsAgentSessionLoopHookEvent(eventOrAction) {
			h.recordASLHookQuarantined(time.Now(), protocol.DecisionReasonInvalidPayload)
		}
		slog.Warn("hook event payload unreadable; rejecting delivery", "event", eventOrAction, "err", err)
		return protocol.Response{OK: true}
	}

	// The compatibility gate runs before anything reads the payload, because a
	// lifecycle event that fails it is not an event this build can place: its
	// phase, run identity, and mailbox generation are the reducer's durable
	// inputs, and half-reading them is worse than dropping the message.
	if refusal := events.ASLHookContractRefusal(eventOrAction, payload); refusal != "" {
		ctx := h.resolveHookEventContext(req)
		ctx.req.Event = eventOrAction
		ctx.req.ParseOutcome = protocol.ParseOutcomeDecoded
		ctx.req.NormalizationOutcome = protocol.NormalizationOutcomeQuarantined
		incompatible := events.Quarantine(eventOrAction, refusal)
		ctx.req.Normalized = &incompatible
		ctx.req.RouteDecision = events.RouteDecisionRejected
		events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionRejected, refusal, nil)
		if ctx.req.WindowID > 0 {
			h.appendSuppressedHookEvent(ctx.req, "", refusal)
		}
		h.recordASLHookQuarantined(time.Now(), refusal)
		// The offered version and identity go to the daemon log only: a hook
		// producer chooses both, and the published reason may not become whatever
		// it wanted an operator to read.
		slog.Warn("agent-session-loop hook is not compatible with this build; quarantining",
			"event", eventOrAction,
			"reason", refusal,
			"offered_schema_version", payload.SchemaVersion,
			"offered_contract_set_id", payload.ContractSetID,
			"expected_schema_version", events.ASLHookSchemaVersion,
			"expected_contract_set_id", aslcontract.ContractSetID,
		)
		return protocol.Response{OK: true}
	}

	ctx := h.resolveHookEventContext(req)
	ctx.req.ParseOutcome = protocol.ParseOutcomeDecoded
	ctx.req.RouteEvidence.PromptCandidate = hookevent.IsPromptEvent(eventOrAction)
	if ctx.req.WindowID <= 0 {
		slog.Debug("hook event ignored: missing window ID", "event", eventOrAction)
		return protocol.Response{OK: true}
	}

	if hookevent.IsIgnoredEventForHarness(eventOrAction, payload, ctx.req.Harness) {
		ctx.req.Event = eventOrAction
		ctx.req.NormalizationOutcome = protocol.NormalizationOutcomeIgnored
		normalized := events.Inert(eventOrAction, events.ReasonIgnoredEvent)
		ctx.req.Normalized = &normalized
		ctx.req.RouteDecision = events.RouteDecisionRejected
		events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionRejected, protocol.DecisionReasonIgnoredEvent, nil)
		h.appendSuppressedHookEvent(ctx.req, "", suppressionReasonIgnoredEvent)
		slog.Debug("hook event intentionally ignored",
			"window_id", ctx.req.WindowID,
			"event", eventOrAction,
			"notification_type", payload.EffectiveNotificationType(),
		)
		return protocol.Response{OK: true}
	}

	action, ok := hookevent.ResolveActionForHarness(eventOrAction, payload, ctx.req.Harness)
	if !ok {
		ctx.req.Event = eventOrAction
		ctx.req.NormalizationOutcome = protocol.NormalizationOutcomeUnknown
		normalized := events.Inert(eventOrAction, events.ReasonUnknownEvent)
		ctx.req.Normalized = &normalized
		ctx.req.RouteDecision = events.RouteDecisionRejected
		events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionRejected, protocol.DecisionReasonUnknownEvent, nil)
		h.appendSuppressedHookEvent(ctx.req, "", suppressionReasonUnknownEvent)
		slog.Debug("hook event unknown, ignoring", "window_id", ctx.req.WindowID, "event", eventOrAction)
		return protocol.Response{OK: true}
	}

	// Everything above this point is a way for a lifecycle payload not to be
	// believed: undecodable, contract-refused, windowless, ignored for the
	// harness, or unresolvable to an action. Past it the daemon has committed to
	// placing the event, so this is where acceptance is counted — one line, for
	// all sixteen boundaries, rather than at each dispatch below where a new
	// branch could silently stop counting.
	if events.IsAgentSessionLoopHookEvent(eventOrAction) {
		h.recordASLHookAccepted(time.Now())
	}

	if eventOrAction == events.AgentSessionLoopMailboxQuestion {
		var targets []loopLifecycleTarget
		if origin, managed := managedHookOrigin(ctx.req); managed {
			targets = []loopLifecycleTarget{runtimeLoopTarget(origin)}
		}
		h.handleASLSupervisorMailboxQuestion(payload, ctx.req, targets, time.Now())
		return protocol.Response{OK: true}
	}

	if eventOrAction == events.AgentSessionLoopMailboxReply ||
		eventOrAction == events.AgentSessionLoopMailboxResolution {
		h.handleASLSupervisorMailboxClosure(eventOrAction, payload, time.Now())
		return protocol.Response{OK: true}
	}

	if events.IsAgentSessionLoopEvent(eventOrAction) {
		if handled := h.handleLoopLifecycleHookEvent(eventOrAction, action, payload, ctx); handled {
			return protocol.Response{OK: true}
		}
	}

	if isOperatorAction(action) {
		return h.handleOperatorHookEvent(ctx, eventOrAction, action, payload)
	}

	lateCopilotStart := h.shouldPreserveLateCopilotSessionStart(
		ctx.req.WindowID, ctx.req.Harness, eventOrAction, payload,
	)
	preservesIcon := (hookevent.PreservesCurrent(eventOrAction) && action == "active") || lateCopilotStart
	contextLimitDetected := hookevent.PayloadHasContextLimit(payload) ||
		(ctx.req.PayloadEncoding == "json_compact" && ctx.req.ContextLimit)
	resetFlowDetected := (hookevent.PayloadHasResetFlow(ctx.req.Harness, eventOrAction, payload) ||
		(ctx.req.PayloadEncoding == "json_compact" && ctx.req.ResetFlow)) && !lateCopilotStart
	stateReq := events.BuildHookStateUpdateRequest(
		ctx.req.WindowID,
		action,
		eventOrAction,
		payload,
		ctx.req.SessionTitle,
		ctx.req.Account,
		ctx.req.Harness,
		ctx.req.TargetHarness,
		ctx.req.CurrentIconState,
		ctx.req.PipeMode,
		preservesIcon,
		contextLimitDetected,
		resetFlowDetected,
	)
	events.PropagateIngressTrace(&stateReq, ctx.req)
	// Stamp the source-scoped view for the shadow reducer while the raw payload
	// is still in hand. A held stop keeps the stamp and delivers it at commit
	// time.
	normalized := events.NormalizedFromHookRequest(eventOrAction, ctx.req, ctx.req.WindowID)
	stateReq.Normalized = &normalized
	stateReq.RouteDecision = events.RouteDecisionSource
	stateReq.NormalizationOutcome = events.NormalizationOutcomeFor(normalized)
	events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionSource, "source", nil)
	stateReq.RouteEvidence = events.CloneRoutingEvidence(ctx.req.RouteEvidence)
	deferred := h.holdStopForConfirmation(stateReq, payload)
	if !deferred {
		h.handleStateUpdate(stateReq)
	}
	return protocol.Response{OK: true}
}

// shouldPreserveLateCopilotSessionStart recognizes Copilot 1.0.70's initial
// hook ordering: the first userPromptSubmitted for a session may reach the
// daemon before sessionStart(source:"new") for that same session. The latter
// is startup bookkeeping, not a /new reset. Treat it as preserve-current only
// when the reducer already has that exact Copilot session as the live owner.
// A genuine /new carries a different session ID and retains reset semantics.
func (h *Handler) shouldPreserveLateCopilotSessionStart(
	windowID int,
	harnessName string,
	event string,
	payload hookevent.HookPayload,
) bool {
	if !strings.EqualFold(strings.TrimSpace(harnessName), "copilot") || event != "sessionStart" ||
		!strings.EqualFold(strings.TrimSpace(payload.Source), "new") {
		return false
	}
	sessionID := strings.TrimSpace(payload.EffectiveSessionID())
	if sessionID == "" {
		return false
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[windowID]
	if st == nil || st.Reducer == nil {
		return false
	}
	current, ok := st.Reducer.CurrentConversation(time.Now().Unix())
	return ok && strings.EqualFold(current.Harness, "copilot") && current.SessionID == sessionID
}

// handleOperatorHookEvent applies a managed session's operator action to its
// registered reducer slot.
func (h *Handler) handleOperatorHookEvent(ctx hookEventContext, eventOrAction, action string, payload hookevent.HookPayload) protocol.Response {
	if _, managed := managedHookOrigin(ctx.req); !managed {
		return protocol.Response{OK: true}
	}
	slot := ctx.req.WindowID
	stateReq := events.BuildHookStateUpdateRequest(
		slot, action, eventOrAction, payload,
		ctx.req.SessionTitle, ctx.req.Account, ctx.req.Harness,
		ctx.req.TargetHarness, ctx.req.CurrentIconState, ctx.req.PipeMode,
		false, false, false,
	)
	events.PropagateIngressTrace(&stateReq, ctx.req)
	normalized := events.NormalizedFromHookRequest(eventOrAction, ctx.req, slot)
	stateReq.Normalized = &normalized
	stateReq.RouteDecision = events.RouteDecisionSource
	events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionSource, action+"_runtime_session", nil)
	stateReq.NormalizationOutcome = events.NormalizationOutcomeFor(normalized)
	stateReq.RouteEvidence = events.CloneRoutingEvidence(ctx.req.RouteEvidence)
	h.handleStateUpdate(stateReq)
	return protocol.Response{OK: true}
}

func (h *Handler) resolveHookEventContext(req protocol.Request) hookEventContext {
	req.Harness = harness.NormalizeKnown(req.Harness)
	req.TargetHarness = req.Harness
	req.TargetHarnessSet = true
	req.RouteEvidence = events.NewRoutingEvidence(req.WindowID)
	ctx := hookEventContext{req: req}
	if ctx.req.WindowID <= 0 {
		return ctx
	}
	ctx.req.RouteEvidence.LookupOutcome = events.RouteLookupRequestOnly
	return ctx
}

type hookEventContext struct {
	req protocol.Request
}

func (h *Handler) appendSuppressedHookEvent(req protocol.Request, action, suppressionReason string) {
	if req.WindowID <= 0 {
		return
	}
	h.stateMu.Lock()
	h.touchWindowMetadataLocked(req)
	st := h.ensureWindowStateLocked(req.WindowID)
	if req.Account != "" {
		st.LastAccount = req.Account
		accountKey := strings.ToLower(req.Account)
		h.lastSeenEventAt[accountKey] = time.Now()
		delete(h.notifiedHookSilence, accountKey)
	}
	effective := events.EffectiveStateEvidence(st.Effective)
	event := events.SuppressedSessionEvent(req, action, suppressionReason, rendering.DesiredIcon(st))
	event.DecisionReason = suppressionReason
	event.ReducerDecision = protocol.DecisionSuppressed
	event.ReducerReason = suppressionReason
	event.ReducerInputAction = action
	event.RouteEvidence = ensureDirectRouteEvidence(req)
	event.EffectiveBefore = &effective
	event.EffectiveAfter = &effective
	if events.IsQuarantineReason(suppressionReason) {
		event.ReducerDecision = protocol.DecisionQuarantined
		event.Quarantined = true
		event.IdentityConfidence = protocol.IdentityConfidenceInvalid
	}
	event = h.appendEventLocked(req.WindowID, event)
	h.stateMu.Unlock()
	h.recordDecisionEvent(event)
}
