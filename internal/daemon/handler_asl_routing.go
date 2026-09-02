package daemon

import (
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// tryRouteActiveLoopSiblingHook is the ASL sibling-pane exception to generic
// owner-mismatch suppression. A pane running an active agent-session-loop is
// usually an unmanaged split inside a managed tab, so its ordinary harness
// hooks (bolt, stop, permission, failure, compact, question, subagent) would be
// dropped as owner_mismatch even though they represent the tab's real work.
// When an exact active loop candidate identifies the source pane, this reroutes
// the hook to the managed title owner(s) of the same workspace so the managed
// state machine advances exactly as an in-pane loop would.
//
// It returns true only when routing actually happened. Discovery failure, a
// missing candidate, a candidate whose WindowID does not exactly match the
// source pane, or the absence of any managed workspace target all return false
// so the caller retains the existing owner_mismatch suppression and ordinary
// sibling agents stay isolated. A candidate can only be present when its PID is
// live and its run is unfinished — agentloop.Discover rejects dead or finished
// runs before they ever reach this path — so CWD/harness similarity alone never
// authorizes a sibling hook.
func (h *Handler) tryRouteActiveLoopSiblingHook(ctx hookEventContext, eventOrAction, action string, payload hookevent.HookPayload) bool {
	if ctx.win == nil {
		return false
	}
	// The exception exists only for unmanaged split panes borrowing a managed
	// state machine. A managed non-owner carries and updates its own reducer via
	// the ordinary source path, even when an exact loop candidate identifies it.
	if ctx.win.UserVars.EffectiveProfile() != "" || ctx.win.UserVars.EffectiveHarness() != "" {
		return false
	}

	candidates, err := h.activeLoopCandidates()
	if err != nil {
		slog.Debug("asl sibling routing skipped: loop discovery failed",
			"window_id", ctx.req.WindowID,
			"event", eventOrAction,
			"err", err,
		)
		return false
	}

	candidate, ok := exactActiveLoopCandidate(candidates, ctx.req.WindowID)
	if !ok {
		return false
	}
	ctx.req.RouteEvidence.ASLMatches = 1
	ctx.req.RouteEvidence.SelectionReason = "asl"
	// Exact ASL evidence may supply the producer for legacy unstamped hooks. It
	// never comes from the managed target, so cross-harness routing retains the
	// loop candidate's event dialect and source identity.
	if ctx.req.Harness == "" {
		ctx.req.Harness = harness.NormalizeKnown(candidate.Harness)
		if hookevent.IsIgnoredEventForHarness(eventOrAction, payload, ctx.req.Harness) {
			ctx.req.Event = eventOrAction
			h.appendSuppressedHookEvent(ctx.req, "", suppressionReasonIgnoredEvent)
			return true
		}
		if resolved, resolvedOK := hookevent.ResolveActionForHarness(eventOrAction, payload, ctx.req.Harness); resolvedOK {
			action = resolved
		}
	}

	targets, _ := matchManagedLoopTargets(ctx.windows, []agentloop.Candidate{candidate})
	switch len(targets) {
	case 0:
		slog.Debug("asl sibling routing skipped: no managed target for active loop",
			"window_id", ctx.req.WindowID,
			"event", eventOrAction,
			"candidate_cwd", candidate.CWD,
			"candidate_harness", candidate.Harness,
		)
		return false
	case 1:
		h.routeUnmanagedHook(ctx, targets, eventOrAction, action, payload)
		return true
	default:
		// More than one managed owner matches this unmanaged pane's workspace, so
		// which one should borrow its state machine is ambiguous. Borrowing state
		// for a single pane requires a unique target; diagnose and leave state
		// unchanged rather than fanning one pane's hook onto several owners.
		slog.Debug("asl sibling routing skipped: ambiguous workspace match; leaving state unchanged",
			"window_id", ctx.req.WindowID,
			"event", eventOrAction,
			"match_count", len(targets),
			"candidate_cwd", candidate.CWD,
			"candidate_harness", candidate.Harness,
		)
		return false
	}
}

// routeUnmanagedHook resolves source-dependent parts once against the real
// source pane, then drives the selected managed target through the normal
// state-update path. It is shared by focus/prompt routes and exact ASL routes.
func (h *Handler) routeUnmanagedHook(
	ctx hookEventContext,
	targets []loopLifecycleTarget,
	eventOrAction string,
	action string,
	payload hookevent.HookPayload,
) {
	contextLimitDetected := hookevent.PayloadHasContextLimit(payload) ||
		(ctx.req.PayloadEncoding == "json_compact" && ctx.req.ContextLimit)
	// As on the in-pane hook path, the source-pane screen read is a targeted
	// asynchronous probe rather than an inline GetText: the base transitions
	// land on the managed owners immediately, and the probe upgrades them to
	// error if the banner is showing on the unmanaged source pane.
	probeContextLimit := !contextLimitDetected && hookevent.ShouldProbeWindowForContextLimit(eventOrAction, action) && h.kittyClient != nil

	var probeTargets []contextLimitProbeTarget
	for _, target := range targets {
		probeTargets = append(probeTargets, h.routeUnmanagedHookTarget(ctx, target, eventOrAction, action, payload, contextLimitDetected))
	}
	if probeContextLimit {
		h.enqueueContextLimitProbe(contextLimitProbe{
			sourceWindowID: ctx.req.WindowID,
			req:            ctx.req,
			event:          eventOrAction,
			payload:        payload,
			targets:        probeTargets,
		})
	}
}

// routeUnmanagedHookTarget builds and dispatches one managed target's state
// update. Every state-dependent field — session title, current icon, profile,
// mute, and (through handleStateUpdate) the guard, notification target, event
// log, and renderer state — is read from the managed target, never from the
// unmanaged source pane. The loop marker is untouched here, so a routed
// transition preserves an in-flight 🔁.
func (h *Handler) routeUnmanagedHookTarget(
	ctx hookEventContext,
	target loopLifecycleTarget,
	eventOrAction string,
	action string,
	payload hookevent.HookPayload,
	contextLimitDetected bool,
) contextLimitProbeTarget {
	lateCopilotStart := h.shouldPreserveLateCopilotSessionStart(
		target.windowID, ctx.req.Harness, eventOrAction, payload,
	)
	preservesIcon := (hookevent.PreservesCurrent(eventOrAction) && action == "active") || lateCopilotStart
	resetFlowDetected := (hookevent.PayloadHasResetFlow(ctx.req.Harness, eventOrAction, payload) ||
		(ctx.req.PayloadEncoding == "json_compact" && ctx.req.ResetFlow)) && !lateCopilotStart
	parts := palette.ParseTitleParts(target.tab.Title)
	currentIcon := parts.State
	if owner, ok := kitty.FindTabTitleOwner(target.tab); ok && owner.ID != target.windowID {
		// A sticky route may target an inactive managed pane after focus moved.
		// Its own reducer state, not the newly active pane's shared title, is the
		// correct legacy/current-icon context for the terminal transition.
		_, currentIcon = h.lastKnownState(target.windowID)
	}
	profile := ""
	harnessName := ""
	muted := parts.Muted
	if target.win != nil {
		profile = target.win.UserVars.EffectiveProfile()
		harnessName = target.win.UserVars.EffectiveHarness()
		muted = muted || target.win.UserVars.Mute != ""
	}
	h.setMutedFromSnapshot(target.windowID, muted)

	// Captured before dispatch so an asynchronous context-limit probe can later
	// promote this managed owner to error with the same render context, even if
	// the base transition below is deferred by the reset-stop debounce.
	probeTarget := contextLimitProbeTarget{
		windowID:      target.windowID,
		sessionTitle:  parts.Base,
		profile:       profile,
		targetHarness: harnessName,
		currentIcon:   currentIcon,
	}

	stateReq := events.BuildHookStateUpdateRequest(
		target.windowID,
		action,
		eventOrAction,
		payload,
		parts.Base,
		profile,
		ctx.req.Harness,
		harnessName,
		currentIcon,
		ctx.req.PipeMode,
		preservesIcon,
		contextLimitDetected,
		resetFlowDetected,
		ctx.req.KittyListenOn,
	)
	events.PropagateIngressTrace(&stateReq, ctx.req)
	// Stamp the source-scoped view for the shadow reducer against the managed
	// target: a routed sibling hook advances the owner's fact map, exactly as
	// its legacy transition advances the owner's state machine.
	normalized := events.NormalizedFromHookRequest(eventOrAction, ctx.req, ctx.req.WindowID)
	stateReq.Normalized = &normalized
	stateReq.RouteDecision = events.RouteDecisionOwner
	stateReq.NormalizationOutcome = events.NormalizationOutcomeFor(normalized)
	selectionReason := ctx.req.RouteEvidence.SelectionReason
	if selectionReason == "" {
		selectionReason = "owner"
	}
	events.ChooseRoute(stateReq.RouteEvidence, events.RouteDecisionOwner, selectionReason, target.win)
	if h.holdStopForConfirmation(stateReq, payload) {
		return probeTarget
	}
	h.handleStateUpdate(stateReq)
	return probeTarget
}

// activeLoopCandidates returns the current active agent-session-loop candidates,
// memoized for at most one renderer interval. Discovery scans on-disk loop
// state, so a hook burst from the same pane reuses the cached slice instead of
// rescanning. The cache is invalidated whenever a loop lifecycle event changes
// the active set (invalidateActiveLoopCache) and rebuilds lazily, including
// after a daemon restart when the cache starts empty. Discovery runs outside
// stateMu so a slow filesystem scan cannot block state-update handlers.
func (h *Handler) activeLoopCandidates() ([]agentloop.Candidate, error) {
	now := time.Now()

	h.stateMu.Lock()
	if h.activeLoopCacheValid && !h.activeLoopCacheAt.IsZero() && now.Sub(h.activeLoopCacheAt) <= rendering.Interval {
		cached := make([]agentloop.Candidate, len(h.activeLoopCache))
		copy(cached, h.activeLoopCache)
		h.stateMu.Unlock()
		return cached, nil
	}
	// Capture the arrival generation so a lifecycle invalidation that races this
	// scan can be detected before the result is published.
	gen := h.activeLoopCacheGen
	h.stateMu.Unlock()

	candidates, err := daemonLoopDiscover(agentloop.DiscoverOptions{})
	if err != nil {
		return nil, err
	}

	h.stateMu.Lock()
	// Publish only when no invalidation happened while the scan was in flight. A
	// generation bump means the active set changed mid-scan, so this slice is
	// already stale; caching it would mask the newer state until the next
	// rendering.Interval. The caller still gets the freshly scanned candidates.
	if h.activeLoopCacheGen == gen {
		h.activeLoopCache = candidates
		h.activeLoopCacheAt = now
		h.activeLoopCacheValid = true
	}
	h.stateMu.Unlock()
	return candidates, nil
}

// invalidateActiveLoopCache drops any memoized discovery so the next lookup
// rescans. Called whenever a loop lifecycle event may have changed the active
// set.
func (h *Handler) invalidateActiveLoopCache() {
	h.stateMu.Lock()
	h.activeLoopCache = nil
	h.activeLoopCacheAt = time.Time{}
	h.activeLoopCacheValid = false
	h.activeLoopCacheGen++
	h.stateMu.Unlock()
}

// hasExactActiveLoopCandidate reports whether an active agent-session-loop run
// exactly identifies windowID. Discovery failure fails closed, matching
// tryRouteActiveLoopSiblingHook.
func (h *Handler) hasExactActiveLoopCandidate(windowID int) bool {
	candidates, err := h.activeLoopCandidates()
	if err != nil {
		return false
	}
	_, ok := exactActiveLoopCandidate(candidates, windowID)
	return ok
}

// exactActiveLoopCandidate returns the unique discovered candidate whose
// WindowID identifies windowID. Every candidate is already live and unfinished
// (agentloop.Discover guarantees it), but duplicate exact matches are still
// ambiguous and fail closed; zero or mismatched IDs never authorize routing.
func exactActiveLoopCandidate(candidates []agentloop.Candidate, windowID int) (agentloop.Candidate, bool) {
	if windowID <= 0 {
		return agentloop.Candidate{}, false
	}
	var match agentloop.Candidate
	matches := 0
	for _, candidate := range candidates {
		if candidate.WindowID == windowID {
			match = candidate
			matches++
		}
	}
	return match, matches == 1
}
