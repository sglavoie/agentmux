package daemon

import (
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
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
	// no-op (e.g. F8 on an icon-free ownerless tab) never creates durable source
	// state.
	muted bool
	// lookupFailed is true when the Kitty snapshot could not be read at all
	// (ListWindows returned an error). It distinguishes a genuinely ownerless
	// tab from an inability to see the tab, where user_clear must fall back to
	// the source pane rather than vanish.
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

	if eventOrAction == agentSessionLoopMailboxQuestion {
		h.handleASLSupervisorMailboxQuestion(payload, ctx.req, ctx.windows, time.Now())
		return Response{OK: true}
	}

	if eventOrAction == agentSessionLoopMailboxReply ||
		eventOrAction == agentSessionLoopMailboxResolution {
		h.handleASLSupervisorMailboxClosure(eventOrAction, payload, time.Now())
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
		// Prompt and focus are too weak for a non-interactive run: a background
		// `claude -p` in the plain split must not drive the owner's icons.
		if !ctx.req.PipeMode &&
			(hookevent.IsPromptEvent(eventOrAction) || (ctx.win != nil && ctx.win.IsActive)) {
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
		if ctx.req.PipeMode {
			h.suppressUnmanagedPipeSource(ctx, eventOrAction, action)
		} else {
			h.suppressOwnerMismatch(ctx, eventOrAction, action)
		}
		return Response{OK: true}
	}

	// A non-interactive run in an unmanaged pane of an ownerless tab has no
	// session to report on, and creating source state here strands an icon on a
	// plain terminal. ASL lifecycle events keep their loop marker, and a pane
	// running an exact active loop keeps delivering that run's harness hooks.
	if ctx.req.PipeMode && ctx.win != nil &&
		ctx.win.UserVars.EffectiveProfile() == "" && ctx.win.UserVars.EffectiveHarness() == "" &&
		!isAgentSessionLoopEvent(eventOrAction) &&
		!h.hasExactActiveLoopCandidate(ctx.req.WindowID) {
		normalized := normalizedFromHookRequest(eventOrAction, ctx.req, ctx.req.WindowID)
		ctx.req.normalized = &normalized
		h.suppressUnmanagedPipeSource(ctx, eventOrAction, action)
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
	// is still in hand. A held stop keeps the stamp and delivers it at commit
	// time.
	normalized := normalizedFromHookRequest(eventOrAction, ctx.req, ctx.req.WindowID)
	stateReq.normalized = &normalized
	stateReq.routeDecision = routeDecisionSource
	stateReq.normalizationOutcome = normalizationOutcomeFor(normalized)
	chooseRoute(ctx.req.routeEvidence, routeDecisionSource, "source", ctx.win)
	stateReq.routeEvidence = cloneRoutingEvidence(ctx.req.routeEvidence)
	deferred := h.holdStopForConfirmation(stateReq, payload)
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
//   - On a tab with no managed owner, a recognized state icon can still come
//     from an ownerless subprocess such as `claude -p`. The request targets the
//     source pane when that icon is visible or the pane has a recorded state;
//     an icon-free ownerless tab remains a no-op.
//   - When the Kitty snapshot could not be read at all, title ownership is
//     unknown; rather than silently dropping the clear, the wipe falls back to
//     the source pane that fired F8.
func (h *Handler) handleUserClearHookEvent(ctx hookEventContext, eventOrAction, action string, payload hookevent.HookPayload) Response {
	targetID := 0
	profile := ""
	harnessName := ""
	forceVisibleClear := false
	switch {
	case ctx.owner != nil:
		targetID = ctx.owner.ID
		profile = ctx.owner.UserVars.EffectiveProfile()
		harnessName = ctx.owner.UserVars.EffectiveHarness()
	case ctx.tab != nil && ctx.win != nil && h.ownerlessSourceHasClearableState(ctx.req.WindowID, ctx.req.currentIconState):
		// Ownerless harness subprocesses still use the source window's reducer
		// and render state icons through renderTitleOwnerLocked. Clear that same
		// source instead of requiring a managed cc_account/cc_harness owner.
		targetID = ctx.req.WindowID
		profile = ctx.req.Account
		harnessName = ctx.req.targetHarness
		// A visible icon may outlive in-memory state (for example across a
		// degraded restart). In that case user_clear creates an already-empty
		// state, which is not a state-to-state visual change, so explicitly
		// enqueue it below to converge the live title without waiting for the
		// maintenance interval.
		forceVisibleClear = ctx.req.currentIconState != ""
		slog.Debug("user_clear wiping ownerless source pane",
			"window_id", ctx.req.WindowID,
			"event", eventOrAction,
		)
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
		// No managed owner, recognized live state icon, or recorded source state:
		// keep F8 a pure no-op that creates no durable renderer state.
		slog.Debug("user_clear ignored: tab has no clearable state icon",
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
	case !ctx.lookupFailed:
		stateReq.routeDecision = routeDecisionSource
		chooseRoute(ctx.req.routeEvidence, routeDecisionSource, "user_clear_source", ctx.win)
	case ctx.lookupFailed:
		stateReq.routeDecision = routeDecisionSourceFallback
		chooseRoute(ctx.req.routeEvidence, routeDecisionSourceFallback, "lookup_fallback", ctx.win)
	}
	stateReq.normalizationOutcome = normalizationOutcomeFor(normalized)
	stateReq.routeEvidence = cloneRoutingEvidence(ctx.req.routeEvidence)
	h.handleStateUpdate(stateReq)
	if forceVisibleClear {
		// user_clear above made the new/empty source state durable. Queue that
		// exact generation so an externally stranded icon disappears promptly.
		h.stateMu.Lock()
		if st, ok := h.windowStateLocked(targetID); ok && st.Dirty {
			h.enqueueRenderLocked(targetID, st)
		}
		h.stateMu.Unlock()
	}
	return Response{OK: true}
}

// ownerlessSourceHasClearableState reports whether F8 has anything to dismiss
// on a tab without a managed title owner. The live parsed icon covers stranded
// titles; the desired state covers a recorded icon that has not rendered yet.
func (h *Handler) ownerlessSourceHasClearableState(windowID int, liveIcon string) bool {
	if liveIcon != "" {
		return true
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.windowStateLocked(windowID)
	return ok && desiredIconForState(st) != ""
}
