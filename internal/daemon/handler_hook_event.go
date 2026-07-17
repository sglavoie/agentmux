package daemon

import (
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// Route decisions recorded onto SessionEvent.RouteDecision: the firing pane
// was the target itself, the event was rerouted to the tab's managed title
// owner, or ownership could not be resolved and the daemon fell back to the
// firing pane.
const (
	routeDecisionSource         = "source"
	routeDecisionOwner          = "owner"
	routeDecisionSourceFallback = "source_fallback"
)

type hookEventContext struct {
	req     Request
	windows []kitty.OSWindow
	tab     *kitty.Tab
	// win is the source pane that fired the hook. It is retained separately
	// from owner so shared-tab operations (F8 user_clear) can route to the
	// managed title owner without losing the origin window.
	win *kitty.Window
	// owner is the managed window responsible for the tab title, or nil when
	// the tab has no managed pane. It is the state/render target.
	owner *kitty.Window
	// muted is the mute state parsed from the live snapshot (title marker or
	// cc_mute user var) for the source pane. It is recorded into the daemon's
	// mute cache only by the path that actually updates the source window, so a
	// no-op (e.g. an ownerless F8) never creates durable source state.
	muted bool
	// lookupFailed is true when the Kitty snapshot could not be read at all
	// (ListWindows returned an error). It distinguishes a genuinely ownerless
	// tab — where an F8 wipe is a safe no-op — from an inability to see the tab,
	// where user_clear must fall back to the source pane rather than vanish.
	lookupFailed bool
}

func (h *Handler) handleHookEvent(req Request) Response {
	eventOrAction := strings.TrimSpace(req.Event)
	if eventOrAction == "" {
		ctx := h.resolveHookEventContext(req)
		ctx.req.parseOutcome = ParseOutcomeMissingEvent
		ctx.req.normalizationOutcome = NormalizationOutcomeNotApplicable
		ctx.req.routeDecision = routeDecisionRejected
		chooseRoute(ctx.req.routeEvidence, routeDecisionRejected, DecisionReasonMissingEvent, nil)
		h.appendSuppressedHookEvent(ctx.req, "", DecisionReasonMissingEvent)
		slog.Debug("hook event ignored: missing event")
		return Response{OK: true}
	}

	payload, err := hookevent.DecodeHookPayload(strings.NewReader(req.PayloadJSON))
	if err != nil {
		ctx := h.resolveHookEventContext(req)
		ctx.req.parseOutcome = ParseOutcomeInvalid
		ctx.req.normalizationOutcome = NormalizationOutcomeQuarantined
		invalid := quarantine(eventOrAction, DecisionReasonInvalidPayload)
		ctx.req.normalized = &invalid
		ctx.req.routeDecision = routeDecisionRejected
		chooseRoute(ctx.req.routeEvidence, routeDecisionRejected, DecisionReasonInvalidPayload, nil)
		if ctx.req.WindowID > 0 {
			ctx.req.Event = eventOrAction
			h.appendSuppressedHookEvent(ctx.req, "", suppressionReasonInvalidPayload)
		}
		slog.Warn("hook event payload unreadable; rejecting delivery", "event", eventOrAction, "err", err)
		return Response{OK: true}
	}

	ctx := h.resolveHookEventContext(req)
	ctx.req.parseOutcome = ParseOutcomeDecoded
	ctx.req.routeEvidence.PromptCandidate = hookevent.IsPromptEvent(eventOrAction)
	ctx.req.routeEvidence.FocusCandidate = ctx.win != nil && ctx.win.IsActive
	if ctx.req.WindowID <= 0 {
		slog.Debug("hook event ignored: missing window ID", "event", eventOrAction)
		return Response{OK: true}
	}

	if hookevent.IsIgnoredEventForHarness(eventOrAction, payload, ctx.req.Harness) {
		ctx.req.Event = eventOrAction
		ctx.req.normalizationOutcome = NormalizationOutcomeIgnored
		normalized := inert(eventOrAction, reasonIgnoredEvent)
		ctx.req.normalized = &normalized
		ctx.req.routeDecision = routeDecisionRejected
		chooseRoute(ctx.req.routeEvidence, routeDecisionRejected, DecisionReasonIgnoredEvent, nil)
		h.appendSuppressedHookEvent(ctx.req, "", suppressionReasonIgnoredEvent)
		slog.Debug("hook event intentionally ignored",
			"window_id", ctx.req.WindowID,
			"event", eventOrAction,
			"notification_type", payload.EffectiveNotificationType(),
		)
		return Response{OK: true}
	}

	action, ok := hookevent.ResolveActionForHarness(eventOrAction, payload, ctx.req.Harness)
	if !ok {
		ctx.req.Event = eventOrAction
		ctx.req.normalizationOutcome = NormalizationOutcomeUnknown
		normalized := inert(eventOrAction, reasonUnknownEvent)
		ctx.req.normalized = &normalized
		ctx.req.routeDecision = routeDecisionRejected
		chooseRoute(ctx.req.routeEvidence, routeDecisionRejected, DecisionReasonUnknownEvent, nil)
		h.appendSuppressedHookEvent(ctx.req, "", suppressionReasonUnknownEvent)
		slog.Debug("hook event unknown, ignoring", "window_id", ctx.req.WindowID, "event", eventOrAction)
		return Response{OK: true}
	}

	if isAgentSessionLoopEvent(eventOrAction) {
		// A loop lifecycle event changes the active-loop set, so the routing
		// cache built from a prior burst is no longer authoritative.
		h.invalidateActiveLoopCache()
		if handled := h.handleLoopLifecycleHookEvent(eventOrAction, action, payload, ctx.req, ctx.windows); handled {
			return Response{OK: true}
		}
	}

	if action == "user_clear" {
		return h.handleUserClearHookEvent(ctx, eventOrAction, action, payload)
	}

	if shouldResolveUnmanagedHookRoute(eventOrAction, ctx) {
		// A previously accepted session stays bound to its original managed
		// target through its terminal event, even if focus has since moved.
		normalized := normalizedFromHookRequest(eventOrAction, ctx.req, ctx.req.WindowID)
		ctx.req.normalized = &normalized
		if target, matches := h.rememberedManagedHookTarget(ctx, normalized); matches == 1 {
			ctx.req.routeEvidence.StickyMatches = matches
			ctx.req.routeEvidence.SelectionReason = "sticky"
			h.routeUnmanagedHook(ctx, []loopLifecycleTarget{target}, eventOrAction, action, payload)
			return Response{OK: true}
		} else if matches > 1 {
			ctx.req.routeEvidence.StickyMatches = matches
			h.suppressOwnerMismatch(ctx, eventOrAction, action)
			return Response{OK: true}
		}

		// A direct prompt is authoritative focus evidence even when the cached
		// Kitty snapshot lags. Other new ordinary sessions must be visibly active.
		if hookevent.IsPromptEvent(eventOrAction) || (ctx.win != nil && ctx.win.IsActive) {
			ctx.req.routeEvidence.PromptCandidate = hookevent.IsPromptEvent(eventOrAction)
			ctx.req.routeEvidence.FocusCandidate = ctx.win != nil && ctx.win.IsActive
			ctx.req.routeEvidence.SelectionReason = "prompt_or_focus"
			target := managedHookTarget(ctx.tab, ctx.owner)
			h.routeUnmanagedHook(ctx, []loopLifecycleTarget{target}, eventOrAction, action, payload)
			return Response{OK: true}
		}

		// An inactive unmanaged pane may still borrow a target when an exact live
		// agent-session-loop candidate provides independent authorization.
		if h.tryRouteActiveLoopSiblingHook(ctx, eventOrAction, action, payload) {
			return Response{OK: true}
		}
		h.suppressOwnerMismatch(ctx, eventOrAction, action)
		return Response{OK: true}
	}

	lateCopilotStart := h.shouldPreserveLateCopilotSessionStart(
		ctx.req.WindowID, ctx.req.Harness, eventOrAction, payload,
	)
	preservesIcon := (hookevent.PreservesCurrent(eventOrAction) && action == "active") || lateCopilotStart
	contextLimitDetected := hookevent.PayloadHasContextLimit(payload) ||
		(ctx.req.PayloadEncoding == "json_compact" && ctx.req.ContextLimit)
	// The screen read that used to promote this event to stop_failure inline is
	// now a targeted asynchronous probe: dispatch the base event immediately and
	// let the probe upgrade the icon to error if the banner is present, so a slow
	// GetText can never delay the acknowledgement.
	probeContextLimit := !contextLimitDetected && hookevent.ShouldProbeWindowForContextLimit(eventOrAction, action) && h.kittyClient != nil

	// The source pane is the state target on this path, so record its snapshot
	// mute now (notification suppression consults it inside handleStateUpdate).
	h.setMutedFromSnapshot(ctx.req.WindowID, ctx.muted)

	resetFlowDetected := (hookevent.PayloadHasResetFlow(ctx.req.Harness, eventOrAction, payload) ||
		(ctx.req.PayloadEncoding == "json_compact" && ctx.req.ResetFlow)) && !lateCopilotStart
	stateReq := buildHookStateUpdateRequest(
		ctx.req.WindowID,
		action,
		eventOrAction,
		payload,
		ctx.req.SessionTitle,
		ctx.req.Account,
		ctx.req.Harness,
		ctx.req.targetHarness,
		ctx.req.currentIconState,
		ctx.req.PipeMode,
		preservesIcon,
		contextLimitDetected,
		resetFlowDetected,
		ctx.req.KittyListenOn,
	)
	propagateIngressTrace(&stateReq, ctx.req)
	// Stamp the source-scoped view for the shadow reducer while the raw payload
	// is still in hand. A deferred stop keeps the stamp and delivers it at
	// commit time.
	normalized := normalizedFromHookRequest(eventOrAction, ctx.req, ctx.req.WindowID)
	stateReq.normalized = &normalized
	stateReq.routeDecision = routeDecisionSource
	stateReq.normalizationOutcome = normalizationOutcomeFor(normalized)
	chooseRoute(ctx.req.routeEvidence, routeDecisionSource, "source", ctx.win)
	stateReq.routeEvidence = cloneRoutingEvidence(ctx.req.routeEvidence)
	deferred := h.deferResetSensitiveStop(stateReq, payload)
	if !deferred {
		h.handleStateUpdate(stateReq)
	}
	if probeContextLimit {
		h.enqueueContextLimitProbe(contextLimitProbe{
			sourceWindowID: ctx.req.WindowID,
			req:            ctx.req,
			event:          eventOrAction,
			payload:        payload,
			targets: []contextLimitProbeTarget{{
				windowID:      ctx.req.WindowID,
				sessionTitle:  ctx.req.SessionTitle,
				profile:       ctx.req.Account,
				targetHarness: ctx.req.targetHarness,
				currentIcon:   ctx.req.currentIconState,
			}},
		})
	}
	return Response{OK: true}
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

// handleUserClearHookEvent routes a keybinding/TUI user_clear (F8) to the tab's
// managed title owner. F8 is bound globally in Kitty, so it can be pressed from
// any pane in a split tab — the managed owner, a managed non-owner, or an
// unmanaged sibling. The wipe must always land on the daemon-owned title, never
// be suppressed as an owner mismatch:
//
//   - From the owner, or any sibling pane, the request is retargeted to the
//     owner and processed through the normal state-update path, so guard
//     clearing, last-action cleanup, notification cancellation, event logging,
//     and durable state updates all run against the owner. The owner's desired
//     state icon is cleared and marked dirty for immediate reconciliation while
//     Looping, base title, harness emoji, star, and mute are preserved.
//   - A tab with no managed owner has no daemon-owned title to wipe, so the
//     request is a successful no-op that never creates durable renderer state.
//   - When the Kitty snapshot could not be read at all, title ownership is
//     unknown; rather than silently dropping the clear, the wipe falls back to
//     the source pane that fired F8.
func (h *Handler) handleUserClearHookEvent(ctx hookEventContext, eventOrAction, action string, payload hookevent.HookPayload) Response {
	targetID := 0
	profile := ""
	harnessName := ""
	switch {
	case ctx.owner != nil:
		targetID = ctx.owner.ID
		profile = ctx.owner.UserVars.EffectiveProfile()
		harnessName = ctx.owner.UserVars.EffectiveHarness()
	case ctx.lookupFailed:
		// The list failed, so we cannot resolve the title owner. Retain a safe
		// fallback: wipe the source pane instead of dropping the clear entirely.
		// Profile/harness/icon fall back to whatever the request already carried.
		targetID = ctx.req.WindowID
		profile = ctx.req.Account
		harnessName = ctx.req.Harness
		slog.Debug("user_clear kitty lookup failed; wiping source pane as fallback",
			"window_id", ctx.req.WindowID,
			"event", eventOrAction,
		)
	default:
		// Genuinely ownerless tab: no daemon-owned title to wipe. A pure no-op
		// that must not create durable renderer state for the source pane.
		slog.Debug("user_clear ignored: tab has no managed title owner",
			"window_id", ctx.req.WindowID,
			"event", eventOrAction,
		)
		return Response{OK: true}
	}

	stateReq := buildHookStateUpdateRequest(
		targetID,
		action,
		eventOrAction,
		payload,
		ctx.req.SessionTitle,
		profile,
		ctx.req.Harness,
		harnessName,
		ctx.req.currentIconState,
		ctx.req.PipeMode,
		false, // preservesIcon: an explicit wipe never preserves the current icon
		false, // contextLimitDetected
		false, // resetFlowDetected: F8 is a manual wipe, not a reset flow
		ctx.req.KittyListenOn,
	)
	propagateIngressTrace(&stateReq, ctx.req)
	// The wipe was retargeted, so normalize against the target window: the
	// shadow reducer must acknowledge the owner's facts, not the source pane's.
	normalized := normalizedFromHookRequest(eventOrAction, ctx.req, targetID)
	stateReq.normalized = &normalized
	switch {
	case ctx.owner != nil:
		stateReq.routeDecision = routeDecisionOwner
		chooseRoute(ctx.req.routeEvidence, routeDecisionOwner, "user_clear_owner", ctx.owner)
	case ctx.lookupFailed:
		stateReq.routeDecision = routeDecisionSourceFallback
		chooseRoute(ctx.req.routeEvidence, routeDecisionSourceFallback, "lookup_fallback", ctx.win)
	}
	stateReq.normalizationOutcome = normalizationOutcomeFor(normalized)
	stateReq.routeEvidence = cloneRoutingEvidence(ctx.req.routeEvidence)
	h.handleStateUpdate(stateReq)
	return Response{OK: true}
}

func (h *Handler) resolveHookEventContext(req Request) hookEventContext {
	producerProvided := strings.TrimSpace(req.Harness) != ""
	req.Harness = harness.NormalizeKnown(req.Harness)
	// With no managed snapshot target, a stamped producer is also the best
	// durable identity for the source pane. A managed source overrides this
	// below, and an unmanaged routed source receives its selected target's
	// metadata when the routed request is built.
	req.targetHarness = req.Harness
	req.targetHarnessSet = true
	req.routeEvidence = newRoutingEvidence(req.WindowID)
	ctx := hookEventContext{req: req}
	if ctx.req.WindowID <= 0 {
		return ctx
	}
	if h.kittyClient == nil {
		ctx.req.routeEvidence.LookupOutcome = routeLookupRequestOnly
		return ctx
	}
	// Route against the last published snapshot instead of a live ListWindows.
	// A slow or wedged Kitty must never delay hook acknowledgement, so the
	// maintenance loop is the only lister. A cold cache (no snapshot published
	// yet — the daemon started degraded and has not reconciled) is treated like
	// an unreadable tab so a user_clear falls back to the source pane rather
	// than silently dropping.
	windows, _, ready := h.cachedSnapshot()
	if !ready {
		slog.Debug("hook event snapshot cache cold; routing from request only", "window_id", ctx.req.WindowID)
		ctx.lookupFailed = true
		ctx.req.routeEvidence.LookupOutcome = routeLookupSnapshotUnavailable
		return ctx
	}
	ctx.req.routeEvidence.SnapshotReady = true
	ctx.windows = windows
	tab, win, found := kitty.FindTabByWindowID(windows, ctx.req.WindowID)
	if !found {
		ctx.req.routeEvidence.LookupOutcome = routeLookupSourceNotFound
		return ctx
	}
	ctx.req.routeEvidence.LookupOutcome = routeLookupSourceFound
	ctx.req.routeEvidence.SourceMatched = true
	ctx.tab = tab
	ctx.win = win
	if owner, ok := kitty.FindTabTitleOwner(tab); ok {
		ctx.owner = owner
	}
	populateRoutingCandidates(ctx.req.routeEvidence, tab, win, ctx.owner)
	parts := palette.ParseTitleParts(tab.Title)
	ctx.req.SessionTitle = parts.Base
	ctx.req.currentIconState = parts.State
	if ctx.owner != nil && ctx.owner.ID != ctx.req.WindowID &&
		(win.UserVars.EffectiveProfile() != "" || win.UserVars.EffectiveHarness() != "") {
		// The shared title reflects the active managed owner, not this managed
		// pane. Seed its legacy compatibility path from its own reduced state so
		// another pane's attention/terminal icon cannot guard-block the update.
		_, ctx.req.currentIconState = h.lastKnownState(ctx.req.WindowID)
	}
	ctx.req.Account = win.UserVars.EffectiveProfile()
	if managedHarness := harness.NormalizeKnown(win.UserVars.EffectiveHarness()); managedHarness != "" {
		ctx.req.targetHarness = managedHarness
	} else if ctx.owner != nil && win.UserVars.EffectiveProfile() == "" && win.UserVars.EffectiveHarness() == "" {
		// This is an unmanaged source inside a managed tab. Do not let a
		// suppressed event stamp producer identity as durable target metadata;
		// an accepted route will supply the selected managed target explicitly.
		ctx.req.targetHarness = ""
	}
	if !producerProvided {
		// Legacy unstamped hooks can infer a producer only from the actual source
		// pane, and only when that pane names a supported managed harness.
		ctx.req.Harness = ctx.req.targetHarness
	}
	// Record the mute state on ctx rather than mutating the daemon store here:
	// context resolution must stay side-effect free so a no-op path (an
	// ownerless F8) never creates durable renderer state for the source pane.
	// The path that updates the source window records it via setMutedFromSnapshot.
	ctx.muted = parts.Muted || win.UserVars.Mute != ""
	return ctx
}

func shouldResolveUnmanagedHookRoute(eventOrAction string, ctx hookEventContext) bool {
	if isAgentSessionLoopEvent(eventOrAction) {
		return false
	}
	if ctx.win == nil || ctx.owner == nil || ctx.owner.ID == ctx.req.WindowID {
		return false
	}
	return ctx.win.UserVars.EffectiveProfile() == "" && ctx.win.UserVars.EffectiveHarness() == ""
}

func buildHookStateUpdateRequest(
	windowID int,
	action string,
	eventOrAction string,
	payload hookevent.HookPayload,
	sessionTitle string,
	profile string,
	producerHarness string,
	targetHarness string,
	currentIconState string,
	pipeMode bool,
	preservesIcon bool,
	contextLimitDetected bool,
	resetFlowDetected bool,
	kittyListenOn string,
) Request {
	return Request{
		Type:             TypeStateUpdate,
		WindowID:         windowID,
		Action:           action,
		Event:            eventOrAction,
		ToolName:         payload.EffectiveToolName(),
		Source:           payload.Source,
		IsPrompt:         hookevent.IsPromptEvent(eventOrAction),
		PreservesIcon:    preservesIcon,
		PipeMode:         pipeMode,
		KittyListenOn:    strings.TrimSpace(kittyListenOn),
		SessionTitle:     sessionTitle,
		Account:          profile,
		Harness:          harness.NormalizeKnown(producerHarness),
		targetHarness:    harness.NormalizeKnown(targetHarness),
		targetHarnessSet: true,
		currentIconState: currentIconState,
		NotificationType: payload.EffectiveNotificationType(),
		StopReason:       payload.EffectiveStopReason(),
		Reason:           payload.Reason,
		ContextLimit:     contextLimitDetected,
		ResetFlow:        resetFlowDetected,
	}
}

func (h *Handler) appendSuppressedHookEvent(req Request, action, suppressionReason string) {
	if req.WindowID <= 0 {
		return
	}
	h.stateMu.Lock()
	h.touchWindowMetadataLocked(req)
	h.rememberKittySocketLocked(req.WindowID, req.KittyListenOn)
	st := h.ensureWindowStateLocked(req.WindowID)
	if req.Account != "" {
		st.LastAccount = req.Account
		accountKey := strings.ToLower(req.Account)
		h.lastSeenEventAt[accountKey] = time.Now()
		delete(h.notifiedHookSilence, accountKey)
	}
	effective := effectiveStateEvidence(st.Effective)
	event := suppressedSessionEvent(req, action, suppressionReason, desiredIconForState(st))
	event.DecisionReason = suppressionReason
	event.ReducerDecision = DecisionSuppressed
	event.ReducerReason = suppressionReason
	event.ReducerInputAction = action
	event.RouteEvidence = ensureDirectRouteEvidence(req)
	event.EffectiveBefore = &effective
	event.EffectiveAfter = &effective
	if suppressionReason == DecisionReasonInvalidPayload {
		event.ReducerDecision = DecisionQuarantined
		event.Quarantined = true
		event.IdentityConfidence = IdentityConfidenceInvalid
	}
	event = h.appendEventLocked(req.WindowID, event)
	h.stateMu.Unlock()
	h.recordDecisionEvent(event)
}
