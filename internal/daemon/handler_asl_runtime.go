package daemon

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// ASL routing by runtime-session identity.
//
// An agent-session-loop run reaches the daemon through the same hook endpoint
// as a harness, so a run inside a managed session delivers managed hooks: each
// names its AgentMux runtime session, and hook translation has already turned
// that into the one reducer slot the session holds. That slot is the run's
// target. Nothing about the terminal is consulted to find it, so the run is
// placed the same way whether its session lives in Kitty or tmux, and whether
// or not Kitty — or any viewer at all — is running.
//
// Workspace matching against the Kitty snapshot remains the route for legacy
// hooks only. It is a similarity policy (same directory, compatible harness),
// which is why a run that a runtime session has claimed is excluded from it: a
// Kitty tab that merely shares the run's directory must never receive the
// lease, the question icon, or the notification of a run living elsewhere.

// managedHookOrigin returns the runtime-session origin of a managed hook. A
// legacy hook, even one attributed to a runtime session that claimed its
// pane, is not managed: it named a Kitty window, not a session.
func managedHookOrigin(req protocol.Request) (protocol.HookOrigin, bool) {
	if req.Origin == nil || req.Origin.Legacy || req.Origin.RuntimeSessionID == "" || req.Origin.Slot <= 0 {
		return protocol.HookOrigin{}, false
	}
	return *req.Origin, true
}

// runtimeLoopTarget is the lifecycle target of a managed hook: the session's
// own slot. A Kitty session keeps the tab and window the snapshot lists for it
// so its title context is read as before; any other backend, and a Kitty
// session the snapshot does not show, has neither.
func runtimeLoopTarget(origin protocol.HookOrigin, windows []kitty.OSWindow, payload hookevent.HookPayload) loopLifecycleTarget {
	target := loopLifecycleTarget{
		windowID: origin.Slot,
		runtime:  origin.RuntimeSessionID,
		workspace: agentloop.WorkspaceTarget{
			CWD:     strings.TrimSpace(payload.CWD),
			Harness: normalizeKnownLoopHarness(payload.Harness),
		},
	}
	if origin.Backend != string(runtimesession.BackendKitty) {
		return target
	}
	target.workspace.WindowID = origin.Slot
	if tab, win, found := kitty.FindTabByWindowID(windows, origin.Slot); found {
		target.tab = tab
		target.win = win
	}
	return target
}

// aslHookTargets resolves where one ASL hook lands. A managed hook has exactly
// one target, its runtime session's slot; a legacy hook keeps the workspace
// fan-out over managed Kitty title owners.
func aslHookTargets(req protocol.Request, windows []kitty.OSWindow, payload hookevent.HookPayload) ([]loopLifecycleTarget, []agentloop.WorkspaceTarget) {
	if origin, managed := managedHookOrigin(req); managed {
		_, openTargets := loopLifecycleWindowTargets(windows)
		return []loopLifecycleTarget{runtimeLoopTarget(origin, windows, payload)}, openTargets
	}
	return loopLifecycleTargets(windows, payload)
}

// aslHookOriginWindow names the pane a hook's facts originate from. A managed
// hook originates from its own slot: a payload window ID is Kitty's, and for a
// session on another backend it can only be an inherited leftover.
func aslHookOriginWindow(req protocol.Request, payload hookevent.HookPayload) int {
	if origin, managed := managedHookOrigin(req); managed && origin.Backend != string(runtimesession.BackendKitty) {
		return origin.Slot
	}
	if payloadWindowID := payload.EffectiveKittyWindowID(); payloadWindowID > 0 {
		return payloadWindowID
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

// loopTargetContextFor reads a target's context. A target with a Kitty tab
// reads it from the snapshot and refreshes the mute cache from it, exactly as
// before runtime sessions existed. A target without one has no title to parse,
// so its context is what the daemon already holds for the slot, and its mute
// state is left as recorded.
func (h *Handler) loopTargetContextFor(target loopLifecycleTarget) loopTargetContext {
	if target.tab == nil {
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
	parts := palette.ParseTitleParts(target.tab.Title)
	ctx := loopTargetContext{sessionTitle: parts.Base, currentIcon: parts.State}
	muted := parts.Muted
	if target.win != nil {
		ctx.profile = target.win.UserVars.EffectiveProfile()
		ctx.harness = target.win.UserVars.EffectiveHarness()
		muted = muted || target.win.UserVars.Mute != ""
	}
	if target.runtime != "" {
		if owner, ok := kitty.FindTabTitleOwner(target.tab); ok && owner.ID != target.windowID {
			// The shared title shows another pane's state.
			_, ctx.currentIcon = h.lastKnownState(target.windowID)
		}
	}
	h.setMutedFromSnapshot(target.windowID, muted)
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
//   - a live ASL lease on a non-Kitty slot. Only a managed hook can write to
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

// hasRuntimeASLStateLocked reports whether any non-Kitty slot exists, which is
// what makes an ASL sweep worth running with no Kitty snapshot to attribute
// candidates against. Callers hold stateMu.
func (h *Handler) hasRuntimeASLStateLocked() bool {
	for slot := range h.store {
		if isRuntimeSyntheticSlot(slot) {
			return true
		}
	}
	return false
}
