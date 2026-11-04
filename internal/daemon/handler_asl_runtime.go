package daemon

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// ASL routing by runtime-session identity.
//
// An agent-session-loop run reaches the daemon through the same hook endpoint
// as a harness, so a run inside a managed session delivers managed hooks: each
// names its AgentMux runtime session, and hook translation has already turned
// that into the one reducer slot the session holds. That slot is the run's
// target. No terminal listing is consulted, so an unattached session routes
// the same way as one displayed in a workspace.
//
// managedHookOrigin returns the runtime-session origin of a managed hook.
func managedHookOrigin(req protocol.Request) (protocol.HookOrigin, bool) {
	if req.Origin == nil || req.Origin.RuntimeSessionID == "" || req.Origin.Slot <= 0 {
		return protocol.HookOrigin{}, false
	}
	return *req.Origin, true
}

// runtimeLoopTarget is the lifecycle target of a managed hook: its tmux slot.
func runtimeLoopTarget(origin protocol.HookOrigin) loopLifecycleTarget {
	return loopLifecycleTarget{
		windowID: origin.Slot,
		runtime:  origin.RuntimeSessionID,
	}
}

func (h *Handler) aslHookOriginWindow(req protocol.Request, _ hookevent.HookPayload) int {
	if origin, managed := managedHookOrigin(req); managed {
		return origin.Slot
	}
	return req.WindowID
}

// loopTargetContext is the title-derived context a lifecycle target supplies
// to a state update.
type loopTargetContext struct {
	sessionTitle string
	currentIcon  string
	profile      string
	harness      string
}

func (h *Handler) loopTargetContextFor(target loopLifecycleTarget) loopTargetContext {
	ctx := loopTargetContext{}
	_, ctx.currentIcon = h.lastKnownState(target.windowID)
	h.stateMu.Lock()
	if st, ok := h.windowStateLocked(target.windowID); ok {
		ctx.sessionTitle = st.LastSessionTitle
		ctx.profile = st.LastAccount
		ctx.harness = st.LastHarness
	}
	h.stateMu.Unlock()
	return ctx
}

// claimASLRunLocked records that runID was delivered by a managed hook of the
// runtime session holding slot. Callers hold stateMu.
func (h *Handler) claimASLRunLocked(runID string, slot int) {
	runID = strings.TrimSpace(runID)
	if runID == "" || slot <= 0 {
		return
	}
	if h.aslRuntimeRuns == nil {
		h.aslRuntimeRuns = make(map[string]int)
	}
	h.aslRuntimeRuns[runID] = slot
}

// releaseASLRunLocked forgets runID's claim when slot still holds it. Callers
// hold stateMu.
func (h *Handler) releaseASLRunLocked(runID string, slot int) {
	if h.aslRuntimeRuns[runID] == slot {
		delete(h.aslRuntimeRuns, runID)
	}
}

// aslRuntimeClaimsLocked maps each run a runtime session holds to that
// session's slot. Two sources agree on what "holds" means:
//
//   - a managed lifecycle hook named the run (claimASLRunLocked), for as long
//     as the slot stays bound to a runtime session. A mailbox hook does not
//     claim: it comes from whichever session is supervising, which need not
//     be the one the run lives in;
//   - a live ASL lease on a runtime slot. Only a managed hook can write to
//     such a slot, so the lease is the same evidence, and it is the form that
//     survives a daemon restart through the runtime checkpoint.
//
// A claim is exact identity — the run ID a session's own hook carried — never
// a directory or harness match. Callers hold stateMu.
func (h *Handler) aslRuntimeClaimsLocked() map[string]int {
	claims := make(map[string]int, len(h.aslRuntimeRuns))
	for slot, st := range h.store {
		if !isRuntimeSyntheticSlot(slot) || st == nil || st.Reducer == nil {
			continue
		}
		for _, fact := range st.Reducer.Facts() {
			if fact.Kind != protocol.SourceASL || !fact.LeaseLive || fact.RunID == "" {
				continue
			}
			if held, ok := claims[fact.RunID]; !ok || slot < held {
				claims[fact.RunID] = slot
			}
		}
	}
	for runID, slot := range h.aslRuntimeRuns {
		if _, bound := h.runtimeRoutes.binding(slot); !bound {
			delete(h.aslRuntimeRuns, runID)
			continue
		}
		claims[runID] = slot
	}
	return claims
}

// hasRuntimeASLStateLocked reports whether any runtime slot exists, which is
// what makes an ASL sweep worth running with no terminal snapshot to attribute
// candidates against. Callers hold stateMu.
func (h *Handler) hasRuntimeASLStateLocked() bool {
	for slot := range h.store {
		if isRuntimeSyntheticSlot(slot) {
			return true
		}
	}
	return false
}
