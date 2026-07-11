package daemon

import (
	"log/slog"
	"strings"
	"time"

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
		slog.Debug("hook event ignored: missing event")
		return Response{OK: true}
	}

	payload, err := hookevent.DecodeHookPayload(strings.NewReader(req.PayloadJSON))
	if err != nil {
		slog.Warn("hook event payload unreadable; continuing with empty payload", "event", eventOrAction, "err", err)
		payload = hookevent.HookPayload{}
	}

	ctx := h.resolveHookEventContext(req)
	if ctx.req.WindowID <= 0 {
		slog.Debug("hook event ignored: missing window ID", "event", eventOrAction)
		return Response{OK: true}
	}

	if hookevent.IsIgnoredEvent(eventOrAction, payload) {
		ctx.req.Event = eventOrAction
		h.appendSuppressedHookEvent(ctx.req, "", suppressionReasonIgnoredEvent)
		slog.Debug("hook event intentionally ignored",
			"window_id", ctx.req.WindowID,
			"event", eventOrAction,
			"notification_type", payload.EffectiveNotificationType(),
		)
		return Response{OK: true}
	}

	action, ok := hookevent.ResolveAction(eventOrAction, payload)
	if !ok {
		ctx.req.Event = eventOrAction
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

	if shouldNoOpForTabTitleOwner(eventOrAction, ctx.tab, ctx.req.WindowID) {
		// A non-owner pane running an active agent-session-loop drives the tab's
		// managed state machine; reroute its hooks to the managed owner before
		// falling back to generic owner-mismatch suppression.
		if h.tryRouteActiveLoopSiblingHook(ctx, eventOrAction, action, payload) {
			return Response{OK: true}
		}
		ctx.req.Event = eventOrAction
		h.appendSuppressedHookEvent(ctx.req, action, suppressionReasonOwnerMismatch)
		if owner, ok := kitty.FindTabTitleOwner(ctx.tab); ok {
			slog.Debug("hook event ignored: window does not own tab title",
				"window_id", ctx.req.WindowID,
				"owner_window_id", owner.ID,
				"event", eventOrAction,
			)
		}
		return Response{OK: true}
	}

	preservesIcon := hookevent.PreservesCurrent(eventOrAction) && action == "active"
	contextLimitDetected := hookevent.PayloadHasContextLimit(payload)
	// The screen read that used to promote this event to stop_failure inline is
	// now a targeted asynchronous probe: dispatch the base event immediately and
	// let the probe upgrade the icon to error if the banner is present, so a slow
	// GetText can never delay the acknowledgement.
	probeContextLimit := !contextLimitDetected && hookevent.ShouldProbeWindowForContextLimit(eventOrAction, action) && h.kittyClient != nil

	// The source pane is the state target on this path, so record its snapshot
	// mute now (notification suppression consults it inside handleStateUpdate).
	h.setMutedFromSnapshot(ctx.req.WindowID, ctx.muted)

	resetFlowDetected := hookevent.PayloadHasResetFlow(ctx.req.Harness, eventOrAction, payload)
	stateReq := buildHookStateUpdateRequest(
		ctx.req.WindowID,
		action,
		eventOrAction,
		payload,
		ctx.req.SessionTitle,
		ctx.req.Account,
		ctx.req.Harness,
		ctx.req.currentIconState,
		ctx.req.PipeMode,
		preservesIcon,
		contextLimitDetected,
		resetFlowDetected,
		ctx.req.KittyListenOn,
	)
	// Stamp the source-scoped view for the shadow reducer while the raw payload
	// is still in hand. A deferred stop keeps the stamp and delivers it at
	// commit time.
	normalized := NormalizedFromHook(eventOrAction, ctx.req.Harness, req.PayloadJSON, ctx.req.WindowID)
	stateReq.normalized = &normalized
	stateReq.routeDecision = routeDecisionSource
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
				windowID:     ctx.req.WindowID,
				sessionTitle: ctx.req.SessionTitle,
				profile:      ctx.req.Account,
				harness:      ctx.req.Harness,
				currentIcon:  ctx.req.currentIconState,
			}},
		})
	}
	return Response{OK: true}
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
		harnessName,
		ctx.req.currentIconState,
		ctx.req.PipeMode,
		false, // preservesIcon: an explicit wipe never preserves the current icon
		false, // contextLimitDetected
		false, // resetFlowDetected: F8 is a manual wipe, not a reset flow
		ctx.req.KittyListenOn,
	)
	// The wipe was retargeted, so normalize against the target window: the
	// shadow reducer must acknowledge the owner's facts, not the source pane's.
	normalized := NormalizedFromHook(eventOrAction, harnessName, ctx.req.PayloadJSON, targetID)
	stateReq.normalized = &normalized
	switch {
	case ctx.owner != nil:
		stateReq.routeDecision = routeDecisionOwner
	case ctx.lookupFailed:
		stateReq.routeDecision = routeDecisionSourceFallback
	}
	h.handleStateUpdate(stateReq)
	return Response{OK: true}
}

func (h *Handler) resolveHookEventContext(req Request) hookEventContext {
	ctx := hookEventContext{req: req}
	if ctx.req.WindowID <= 0 {
		return ctx
	}
	if h.kittyClient == nil {
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
		return ctx
	}
	ctx.windows = windows
	tab, win, found := kitty.FindTabByWindowID(windows, ctx.req.WindowID)
	if !found {
		return ctx
	}
	ctx.tab = tab
	ctx.win = win
	if owner, ok := kitty.FindTabTitleOwner(tab); ok {
		ctx.owner = owner
	}
	parts := palette.ParseTitleParts(tab.Title)
	ctx.req.SessionTitle = parts.Base
	ctx.req.currentIconState = parts.State
	ctx.req.Account = win.UserVars.EffectiveProfile()
	ctx.req.Harness = win.UserVars.EffectiveHarness()
	// Record the mute state on ctx rather than mutating the daemon store here:
	// context resolution must stay side-effect free so a no-op path (an
	// ownerless F8) never creates durable renderer state for the source pane.
	// The path that updates the source window records it via setMutedFromSnapshot.
	ctx.muted = parts.Muted || win.UserVars.Mute != ""
	return ctx
}

func shouldNoOpForTabTitleOwner(eventOrAction string, tab *kitty.Tab, windowID int) bool {
	if isAgentSessionLoopEvent(eventOrAction) {
		return false
	}
	owner, ok := kitty.FindTabTitleOwner(tab)
	return ok && owner.ID != windowID
}

func buildHookStateUpdateRequest(
	windowID int,
	action string,
	eventOrAction string,
	payload hookevent.HookPayload,
	sessionTitle string,
	profile string,
	harnessName string,
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
		Harness:          harnessName,
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
	h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, action, suppressionReason, desiredIconForState(st)))
	h.stateMu.Unlock()
}
