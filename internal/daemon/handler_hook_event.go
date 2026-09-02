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
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

type hookEventContext struct {
	req     protocol.Request
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

func (h *Handler) handleHookEvent(req protocol.Request) protocol.Response {
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
	ctx.req.RouteEvidence.FocusCandidate = ctx.win != nil && ctx.win.IsActive
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
		h.handleASLSupervisorMailboxQuestion(payload, ctx.req, ctx.windows, time.Now())
		return protocol.Response{OK: true}
	}

	if eventOrAction == events.AgentSessionLoopMailboxReply ||
		eventOrAction == events.AgentSessionLoopMailboxResolution {
		h.handleASLSupervisorMailboxClosure(eventOrAction, payload, time.Now())
		return protocol.Response{OK: true}
	}

	if events.IsAgentSessionLoopEvent(eventOrAction) {
		// A loop lifecycle event changes the active-loop set, so the routing
		// cache built from a prior burst is no longer authoritative.
		h.invalidateActiveLoopCache()
		if handled := h.handleLoopLifecycleHookEvent(eventOrAction, action, payload, ctx.req, ctx.windows); handled {
			return protocol.Response{OK: true}
		}
	}

	if action == "user_clear" {
		return h.handleUserClearHookEvent(ctx, eventOrAction, action, payload)
	}

	if shouldResolveUnmanagedHookRoute(eventOrAction, ctx) {
		// A previously accepted session stays bound to its original managed
		// target through its terminal event, even if focus has since moved.
		normalized := events.NormalizedFromHookRequest(eventOrAction, ctx.req, ctx.req.WindowID)
		ctx.req.Normalized = &normalized
		if target, matches := h.rememberedManagedHookTarget(ctx, normalized); matches == 1 {
			ctx.req.RouteEvidence.StickyMatches = matches
			ctx.req.RouteEvidence.SelectionReason = "sticky"
			h.routeUnmanagedHook(ctx, []loopLifecycleTarget{target}, eventOrAction, action, payload)
			return protocol.Response{OK: true}
		} else if matches > 1 {
			ctx.req.RouteEvidence.StickyMatches = matches
			h.suppressOwnerMismatch(ctx, eventOrAction, action)
			return protocol.Response{OK: true}
		}

		// A direct prompt is authoritative focus evidence even when the cached
		// Kitty snapshot lags. Other new ordinary sessions must be visibly active.
		// Prompt and focus are too weak for a non-interactive run: a background
		// `claude -p` in the plain split must not drive the owner's icons.
		if !ctx.req.PipeMode &&
			(hookevent.IsPromptEvent(eventOrAction) || (ctx.win != nil && ctx.win.IsActive)) {
			ctx.req.RouteEvidence.PromptCandidate = hookevent.IsPromptEvent(eventOrAction)
			ctx.req.RouteEvidence.FocusCandidate = ctx.win != nil && ctx.win.IsActive
			ctx.req.RouteEvidence.SelectionReason = "prompt_or_focus"
			target := managedHookTarget(ctx.tab, ctx.owner)
			h.routeUnmanagedHook(ctx, []loopLifecycleTarget{target}, eventOrAction, action, payload)
			return protocol.Response{OK: true}
		}

		// An inactive unmanaged pane may still borrow a target when an exact live
		// agent-session-loop candidate provides independent authorization.
		if h.tryRouteActiveLoopSiblingHook(ctx, eventOrAction, action, payload) {
			return protocol.Response{OK: true}
		}
		if ctx.req.PipeMode {
			h.suppressUnmanagedPipeSource(ctx, eventOrAction, action)
		} else {
			h.suppressOwnerMismatch(ctx, eventOrAction, action)
		}
		return protocol.Response{OK: true}
	}

	// A non-interactive run in an unmanaged pane of an ownerless tab has no
	// session to report on, and creating source state here strands an icon on a
	// plain terminal. ASL lifecycle events keep their loop marker, and a pane
	// running an exact active loop keeps delivering that run's harness hooks.
	if ctx.req.PipeMode && ctx.win != nil &&
		ctx.win.UserVars.EffectiveProfile() == "" && ctx.win.UserVars.EffectiveHarness() == "" &&
		!events.IsAgentSessionLoopEvent(eventOrAction) &&
		!h.hasExactActiveLoopCandidate(ctx.req.WindowID) {
		normalized := events.NormalizedFromHookRequest(eventOrAction, ctx.req, ctx.req.WindowID)
		ctx.req.Normalized = &normalized
		h.suppressUnmanagedPipeSource(ctx, eventOrAction, action)
		return protocol.Response{OK: true}
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
		ctx.req.KittyListenOn,
	)
	events.PropagateIngressTrace(&stateReq, ctx.req)
	// Stamp the source-scoped view for the shadow reducer while the raw payload
	// is still in hand. A held stop keeps the stamp and delivers it at commit
	// time.
	normalized := events.NormalizedFromHookRequest(eventOrAction, ctx.req, ctx.req.WindowID)
	stateReq.Normalized = &normalized
	stateReq.RouteDecision = events.RouteDecisionSource
	stateReq.NormalizationOutcome = events.NormalizationOutcomeFor(normalized)
	events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionSource, "source", ctx.win)
	stateReq.RouteEvidence = events.CloneRoutingEvidence(ctx.req.RouteEvidence)
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
				targetHarness: ctx.req.TargetHarness,
				currentIcon:   ctx.req.CurrentIconState,
			}},
		})
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
func (h *Handler) handleUserClearHookEvent(ctx hookEventContext, eventOrAction, action string, payload hookevent.HookPayload) protocol.Response {
	targetID := 0
	profile := ""
	harnessName := ""
	forceVisibleClear := false
	switch {
	case ctx.owner != nil:
		targetID = ctx.owner.ID
		profile = ctx.owner.UserVars.EffectiveProfile()
		harnessName = ctx.owner.UserVars.EffectiveHarness()
	case ctx.tab != nil && ctx.win != nil && h.ownerlessSourceHasClearableState(ctx.req.WindowID, ctx.req.CurrentIconState):
		// Ownerless harness subprocesses still use the source window's reducer
		// and render state icons through renderTitleOwnerLocked. Clear that same
		// source instead of requiring a managed cc_account/cc_harness owner.
		targetID = ctx.req.WindowID
		profile = ctx.req.Account
		harnessName = ctx.req.TargetHarness
		// A visible icon may outlive in-memory state (for example across a
		// degraded restart). In that case user_clear creates an already-empty
		// state, which is not a state-to-state visual change, so explicitly
		// enqueue it below to converge the live title without waiting for the
		// maintenance interval.
		forceVisibleClear = ctx.req.CurrentIconState != ""
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
		return protocol.Response{OK: true}
	}

	stateReq := events.BuildHookStateUpdateRequest(
		targetID,
		action,
		eventOrAction,
		payload,
		ctx.req.SessionTitle,
		profile,
		ctx.req.Harness,
		harnessName,
		ctx.req.CurrentIconState,
		ctx.req.PipeMode,
		false, // preservesIcon: an explicit wipe never preserves the current icon
		false, // contextLimitDetected
		false, // resetFlowDetected: F8 is a manual wipe, not a reset flow
		ctx.req.KittyListenOn,
	)
	events.PropagateIngressTrace(&stateReq, ctx.req)
	// The wipe was retargeted, so normalize against the target window: the
	// shadow reducer must acknowledge the owner's facts, not the source pane's.
	normalized := events.NormalizedFromHookRequest(eventOrAction, ctx.req, targetID)
	stateReq.Normalized = &normalized
	switch {
	case ctx.owner != nil:
		stateReq.RouteDecision = events.RouteDecisionOwner
		events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionOwner, "user_clear_owner", ctx.owner)
	case !ctx.lookupFailed:
		stateReq.RouteDecision = events.RouteDecisionSource
		events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionSource, "user_clear_source", ctx.win)
	case ctx.lookupFailed:
		stateReq.RouteDecision = events.RouteDecisionSourceFallback
		events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionSourceFallback, "lookup_fallback", ctx.win)
	}
	stateReq.NormalizationOutcome = events.NormalizationOutcomeFor(normalized)
	stateReq.RouteEvidence = events.CloneRoutingEvidence(ctx.req.RouteEvidence)
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
	return protocol.Response{OK: true}
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
	return ok && rendering.DesiredIcon(st) != ""
}

func (h *Handler) resolveHookEventContext(req protocol.Request) hookEventContext {
	producerProvided := strings.TrimSpace(req.Harness) != ""
	req.Harness = harness.NormalizeKnown(req.Harness)
	// With no managed snapshot target, a stamped producer is also the best
	// durable identity for the source pane. A managed source overrides this
	// below, and an unmanaged routed source receives its selected target's
	// metadata when the routed request is built.
	req.TargetHarness = req.Harness
	req.TargetHarnessSet = true
	req.RouteEvidence = events.NewRoutingEvidence(req.WindowID)
	ctx := hookEventContext{req: req}
	if ctx.req.WindowID <= 0 {
		return ctx
	}
	if h.kittyClient == nil {
		ctx.req.RouteEvidence.LookupOutcome = events.RouteLookupRequestOnly
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
		ctx.req.RouteEvidence.LookupOutcome = events.RouteLookupSnapshotUnavailable
		return ctx
	}
	ctx.req.RouteEvidence.SnapshotReady = true
	ctx.windows = windows
	tab, win, found := kitty.FindTabByWindowID(windows, ctx.req.WindowID)
	if !found {
		ctx.req.RouteEvidence.LookupOutcome = events.RouteLookupSourceNotFound
		return ctx
	}
	ctx.req.RouteEvidence.LookupOutcome = events.RouteLookupSourceFound
	ctx.req.RouteEvidence.SourceMatched = true
	ctx.tab = tab
	ctx.win = win
	if owner, ok := kitty.FindTabTitleOwner(tab); ok {
		ctx.owner = owner
	}
	events.PopulateRoutingCandidates(ctx.req.RouteEvidence, tab, win, ctx.owner)
	parts := palette.ParseTitleParts(tab.Title)
	ctx.req.SessionTitle = parts.Base
	ctx.req.CurrentIconState = parts.State
	if ctx.owner != nil && ctx.owner.ID != ctx.req.WindowID &&
		(win.UserVars.EffectiveProfile() != "" || win.UserVars.EffectiveHarness() != "") {
		// The shared title reflects the active managed owner, not this managed
		// pane. Seed its legacy compatibility path from its own reduced state so
		// another pane's attention/terminal icon cannot guard-block the update.
		_, ctx.req.CurrentIconState = h.lastKnownState(ctx.req.WindowID)
	}
	ctx.req.Account = win.UserVars.EffectiveProfile()
	if managedHarness := harness.NormalizeKnown(win.UserVars.EffectiveHarness()); managedHarness != "" {
		ctx.req.TargetHarness = managedHarness
	} else if ctx.owner != nil && win.UserVars.EffectiveProfile() == "" && win.UserVars.EffectiveHarness() == "" {
		// This is an unmanaged source inside a managed tab. Do not let a
		// suppressed event stamp producer identity as durable target metadata;
		// an accepted route will supply the selected managed target explicitly.
		ctx.req.TargetHarness = ""
	}
	if !producerProvided {
		// Legacy unstamped hooks can infer a producer only from the actual source
		// pane, and only when that pane names a supported managed harness.
		ctx.req.Harness = ctx.req.TargetHarness
	}
	// Record the mute state on ctx rather than mutating the daemon store here:
	// context resolution must stay side-effect free so a no-op path (F8 on an
	// icon-free ownerless tab) never creates durable renderer state for the
	// source pane.
	// The path that updates the source window records it via setMutedFromSnapshot.
	ctx.muted = parts.Muted || win.UserVars.Mute != ""
	return ctx
}

func shouldResolveUnmanagedHookRoute(eventOrAction string, ctx hookEventContext) bool {
	if events.IsAgentSessionLoopEvent(eventOrAction) {
		return false
	}
	if ctx.win == nil || ctx.owner == nil || ctx.owner.ID == ctx.req.WindowID {
		return false
	}
	return ctx.win.UserVars.EffectiveProfile() == "" && ctx.win.UserVars.EffectiveHarness() == ""
}

func (h *Handler) appendSuppressedHookEvent(req protocol.Request, action, suppressionReason string) {
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

// rememberedManagedHookTarget searches only the source tab's managed reducers
// for an exact live producer/session/origin binding. The returned match count
// makes ambiguity fail closed instead of silently moving one session between
// managed panes.
func (h *Handler) rememberedManagedHookTarget(ctx hookEventContext, normalized protocol.NormalizedEvent) (loopLifecycleTarget, int) {
	producerHarness := harness.NormalizeKnown(ctx.req.Harness)
	if ctx.tab == nil || producerHarness == "" || normalized.SessionID == "" ||
		normalized.OriginWindowID <= 0 {
		return loopLifecycleTarget{}, 0
	}
	// Route identity is the parent producer/session tuple even when the current
	// event is sourced from a subagent fact. The parent harness fact established
	// by the prompt keeps every child event on the same managed reducer.
	parentSource := protocol.SourceID{Kind: protocol.SourceHarness, Key: producerHarness + ":" + normalized.SessionID}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	var target loopLifecycleTarget
	matches := 0
	for i := range ctx.tab.Windows {
		win := &ctx.tab.Windows[i]
		if win.UserVars.EffectiveProfile() == "" && win.UserVars.EffectiveHarness() == "" {
			continue
		}
		st, ok := h.windowStateLocked(win.ID)
		if !ok || st.Reducer == nil || !st.Reducer.HasLiveOriginRoute(
			parentSource, normalized.SessionID, normalized.OriginWindowID,
		) {
			continue
		}
		matches++
		target = managedHookTarget(ctx.tab, win)
	}
	return target, matches
}

func managedHookTarget(tab *kitty.Tab, win *kitty.Window) loopLifecycleTarget {
	if win == nil {
		return loopLifecycleTarget{}
	}
	return loopLifecycleTarget{windowID: win.ID, tab: tab, win: win}
}

func (h *Handler) suppressOwnerMismatch(ctx hookEventContext, eventOrAction, action string) {
	ctx.req.Event = eventOrAction
	ctx.req.RouteDecision = events.RouteDecisionRejected
	events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionRejected, protocol.DecisionReasonOwnerMismatch, nil)
	if ctx.owner != nil {
		ctx.req.RouteEvidence.RejectedAlternatives = append(ctx.req.RouteEvidence.RejectedAlternatives,
			protocol.RejectedRouteEvidence{WindowID: ctx.owner.ID, Reason: protocol.DecisionReasonOwnerMismatch})
	}
	h.appendSuppressedHookEvent(ctx.req, action, suppressionReasonOwnerMismatch)
	if ctx.owner != nil {
		slog.Debug("hook event ignored: unmanaged pane is not authorized to borrow the tab title owner",
			"window_id", ctx.req.WindowID,
			"owner_window_id", ctx.owner.ID,
			"event", eventOrAction,
		)
	}
}

// suppressUnmanagedPipeSource records a non-interactive harness run in an
// unmanaged pane. It is deliberately distinct from owner_mismatch: this is the
// expected, benign outcome of a background subprocess and must not feed the
// owner_mismatch_* doctor findings.
func (h *Handler) suppressUnmanagedPipeSource(ctx hookEventContext, eventOrAction, action string) {
	ctx.req.Event = eventOrAction
	ctx.req.RouteDecision = events.RouteDecisionRejected
	events.ChooseRoute(ctx.req.RouteEvidence, events.RouteDecisionRejected, protocol.DecisionReasonUnmanagedPipeSource, nil)
	if ctx.owner != nil {
		ctx.req.RouteEvidence.RejectedAlternatives = append(ctx.req.RouteEvidence.RejectedAlternatives,
			protocol.RejectedRouteEvidence{WindowID: ctx.owner.ID, Reason: protocol.DecisionReasonUnmanagedPipeSource})
	}
	h.appendSuppressedHookEvent(ctx.req, action, suppressionReasonUnmanagedPipeSource)
	slog.Debug("hook event ignored: non-interactive harness run in an unmanaged pane",
		"window_id", ctx.req.WindowID,
		"event", eventOrAction,
	)
}
