package daemon

import (
	"log/slog"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// rememberedManagedHookTarget searches only the source tab's managed reducers
// for an exact live producer/session/origin binding. The returned match count
// makes ambiguity fail closed instead of silently moving one session between
// managed panes.
func (h *Handler) rememberedManagedHookTarget(ctx hookEventContext, normalized NormalizedEvent) (loopLifecycleTarget, int) {
	producerHarness := harness.NormalizeKnown(ctx.req.Harness)
	if ctx.tab == nil || producerHarness == "" || normalized.SessionID == "" ||
		normalized.OriginWindowID <= 0 {
		return loopLifecycleTarget{}, 0
	}
	// Route identity is the parent producer/session tuple even when the current
	// event is sourced from a subagent fact. The parent harness fact established
	// by the prompt keeps every child event on the same managed reducer.
	parentSource := SourceID{Kind: SourceHarness, Key: producerHarness + ":" + normalized.SessionID}

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
	ctx.req.routeDecision = routeDecisionRejected
	chooseRoute(ctx.req.routeEvidence, routeDecisionRejected, DecisionReasonOwnerMismatch, nil)
	if ctx.owner != nil {
		ctx.req.routeEvidence.RejectedAlternatives = append(ctx.req.routeEvidence.RejectedAlternatives,
			RejectedRouteEvidence{WindowID: ctx.owner.ID, Reason: DecisionReasonOwnerMismatch})
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
	ctx.req.routeDecision = routeDecisionRejected
	chooseRoute(ctx.req.routeEvidence, routeDecisionRejected, DecisionReasonUnmanagedPipeSource, nil)
	if ctx.owner != nil {
		ctx.req.routeEvidence.RejectedAlternatives = append(ctx.req.routeEvidence.RejectedAlternatives,
			RejectedRouteEvidence{WindowID: ctx.owner.ID, Reason: DecisionReasonUnmanagedPipeSource})
	}
	h.appendSuppressedHookEvent(ctx.req, action, suppressionReasonUnmanagedPipeSource)
	slog.Debug("hook event ignored: non-interactive harness run in an unmanaged pane",
		"window_id", ctx.req.WindowID,
		"event", eventOrAction,
	)
}
