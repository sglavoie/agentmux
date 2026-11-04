package daemon

import (
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// daemonLoopDiscoverPlans is the tri-state discovery seam reconciliation uses:
// it needs to tell "gone" from "could not tell". Tests may inject state
// without a filesystem.
var daemonLoopDiscoverPlans = agentloop.DiscoverPlans

type loopLifecycleTarget struct {
	windowID int
	// runtime names the runtime session whose managed hook selected this target.
	runtime string
}

func (h *Handler) handleLoopLifecycleHookEvent(
	eventOrAction string,
	action string,
	payload hookevent.HookPayload,
	ctx hookEventContext,
) bool {
	baseReq := ctx.req
	origin, managed := managedHookOrigin(baseReq)
	if !managed {
		return false
	}
	h.handleLoopLifecycleTarget(eventOrAction, action, payload, baseReq, runtimeLoopTarget(origin))
	// A finish that carries no run identity cannot name which per-run lease it
	// releases. Instead of clearing every run, reconcile the window's leases
	// against on-disk loop state so only genuinely finished runs lose 🔁.
	if eventOrAction == events.AgentSessionLoopFinished && payload.EffectiveRunID() == "" {
		now := time.Now()
		h.reconcileASLLeasesRuntime(now)
	}
	return true
}

func (h *Handler) handleLoopLifecycleTarget(
	eventOrAction string,
	action string,
	payload hookevent.HookPayload,
	baseReq protocol.Request,
	target loopLifecycleTarget,
) {
	targetCtx := h.loopTargetContextFor(target)

	req := events.BuildHookStateUpdateRequest(
		target.windowID,
		action,
		eventOrAction,
		payload,
		targetCtx.sessionTitle,
		targetCtx.profile,
		baseReq.Harness,
		targetCtx.harness,
		targetCtx.currentIcon,
		baseReq.PipeMode,
		false,
		false,
		false,
	)
	events.PropagateIngressTrace(&req, baseReq)
	if req.RouteEvidence == nil {
		req.RouteEvidence = events.NewRoutingEvidence(baseReq.WindowID)
	}
	req.RouteEvidence.ConsideredWindows = nil
	req.RouteEvidence.RejectedAlternatives = nil
	req.RouteEvidence.ASLMatches = 1
	// The session's own slot is both source and target.
	events.ChooseRoute(req.RouteEvidence, events.RouteDecisionSource, "asl_runtime_session", nil)
	req.RouteDecision = events.RouteDecisionSource
	// The fact lands on the managed target but retains the pane identified by
	// the request/payload as its origin for route-aware probes and diagnostics.
	originWindowID := h.aslHookOriginWindow(baseReq, payload)
	normalized := events.NormalizedFromHookRequest(eventOrAction, baseReq, originWindowID)
	req.Normalized = &normalized
	req.NormalizationOutcome = events.NormalizationOutcomeFor(normalized)
	resp := h.handleStateUpdate(req)
	if !resp.OK {
		slog.Debug("loop lifecycle target state update failed",
			"window_id", target.windowID,
			"event", eventOrAction,
			"action", action,
			"error", resp.Error,
		)
	}
	runID := payload.EffectiveRunID()
	h.stateMu.Lock()
	if h.runtimeSlotClosedLocked(target.windowID) {
		// The session ended after its hook was translated. Its slot is
		// closed: no claim, supervision, or loop marker may reopen it.
		h.stateMu.Unlock()
		return
	}
	if eventOrAction == events.AgentSessionLoopFinished {
		h.releaseASLRunLocked(runID, target.windowID)
	} else {
		h.claimASLRunLocked(runID, target.windowID)
	}
	h.stateMu.Unlock()
	if eventOrAction == events.AgentSessionLoopFinished {
		h.finishASLSupervisorQuestion(target.windowID, runID, time.Now())
	} else {
		h.registerASLSupervisorLifecycleTarget(
			target.windowID,
			originWindowID,
			runID,
			payload.StatePath,
		)
	}

	if marker := loopMarkerForLifecycleTarget(eventOrAction); marker != nil {
		h.stateMu.Lock()
		if h.runtimeSlotClosedLocked(target.windowID) {
			h.stateMu.Unlock()
			return
		}
		st := h.ensureWindowStateLocked(target.windowID)
		// The reducer's per-run leases are authoritative once any ASL fact has
		// reached it (reduceStateUpdate already published the reduced verdict
		// above). The discovery-derived marker only stands in for windows whose
		// reducer holds no ASL knowledge — a quarantined stamp, or pre-cutover
		// state.
		loopingBefore, loopingSetBefore := st.Looping, st.LoopingSet
		if st.Reducer == nil || !st.Reducer.LoopStateKnown() {
			st.Looping = *marker
			st.LoopingSet = true
		}
		if st.Looping != loopingBefore || st.LoopingSet != loopingSetBefore {
			h.markWindowDirtyLocked(target.windowID)
		} else {
			h.markWindowActivityDirtyLocked(target.windowID)
		}
		h.stateMu.Unlock()
	}
}

// loopMarkerForLifecycleTarget uses the managed session's own lifecycle event.
// The reducer's per-run lease remains the authority when it has seen the run.
func loopMarkerForLifecycleTarget(eventOrAction string) *bool {
	switch eventOrAction {
	case events.AgentSessionLoopStarted, events.AgentSessionLoopTurnStarted,
		events.AgentSessionLoopTurnHeartbeat, events.AgentSessionLoopTurnHangSuspected,
		events.AgentSessionLoopTurnHangCleared, events.AgentSessionLoopTurnFinished,
		events.AgentSessionLoopVerificationStarted, events.AgentSessionLoopVerificationHeartbeat,
		events.AgentSessionLoopVerificationFinished, events.AgentSessionLoopWaitStarted,
		events.AgentSessionLoopWaitEnded:
		return boolPtr(true)
	case events.AgentSessionLoopPaused, events.AgentSessionLoopFinished:
		return boolPtr(false)
	default:
		return nil
	}
}

func boolPtr(value bool) *bool {
	return &value
}
