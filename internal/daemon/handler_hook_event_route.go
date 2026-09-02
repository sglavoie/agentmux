package daemon

import (
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

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
