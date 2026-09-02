package daemon

import (
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// handleSubagentStateUpdate applies subagent reference counting and Copilot's
// subagent-specific permission behavior. The boolean reports whether the
// request was fully handled and should return before the normal guard path.
func (h *Handler) handleSubagentStateUpdate(req protocol.Request, currentIconState string, outcome *applyOutcome) (protocol.Response, bool) {
	// SubagentStart: increment reference count so the robot icon persists
	// even when interleaved parent "active" events arrive.
	if req.Action == "subagent" {
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(req.WindowID)
		st.SubagentCount++
		h.updateDurableSubagentCountLocked(req.WindowID)
		h.stateMu.Unlock()
	}

	// SubagentStop: decrement counter; suppress "active" while other agents run.
	if req.Event == "SubagentStop" || req.Event == "subagentStop" {
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(req.WindowID)
		wasTracked := st.SubagentCount > 0
		if wasTracked {
			st.SubagentCount--
		}
		remaining := st.SubagentCount
		h.updateDurableSubagentCountLocked(req.WindowID)
		h.stateMu.Unlock()

		if wasTracked && remaining > 0 && req.Action == "active" {
			// More agents still running — refresh the subagent guard (reset expiry)
			// and suppress this "active" transition. Only "active" is suppressed:
			// a SubagentStop carrying a real failure (action == "stop_failure",
			// e.g. stopReason = "error" / "max_tokens") must surface so the user
			// sees the failure even when sibling agents are still running.
			//
			// The reducer still sees this stop (no skipReduce): it owns the
			// per-agent facts and the balanced legacy multiset, and an
			// intermediate stop must end that one agent's contribution — the
			// remaining agents keep 🤖 through source-fact precedence, not
			// through hiding the event.
			h.guard.TryUpdate(req.WindowID, "subagent", false)
			h.stateMu.Lock()
			h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, req.Action, suppressionReasonSubagentStillRunning, currentIconState))
			h.stateMu.Unlock()
			return protocol.Response{OK: true, Allowed: false}, true
		}
		if wasTracked && remaining == 0 {
			// Last agent finished — clear the subagent guard so the "active" below
			// is allowed immediately rather than waiting for the guard TTL to
			// expire. Only clear when the guard is still "subagent": a failure or
			// permission may have legitimately superseded it (e.g. one subagent
			// stop_failure while a sibling was still running), and that terminal
			// state must persist until the user acknowledges it.
			if h.guard.Current(req.WindowID) == "subagent" {
				h.guard.Clear(req.WindowID)
			}
		}
	}

	// Copilot mid-subagent suppression: while background subagents are running,
	// Copilot fires agentStop (a preserve-current child-complete signal) and
	// speculative permissionRequest events that would otherwise overwrite the 🤖
	// guard. Suppress them here so the reference-counted subagent state remains
	// the source of truth.
	//
	// Event-name match (not action) ensures only Copilot's specific events
	// trigger suppression; Claude's Stop / SessionEnd / PermissionRequest fall
	// through to the normal path. sessionEnd (also action "stop") is
	// intentionally NOT in this list so a real session end still clears state.
	//
	// permissionRequest is suppressed via CancelPending rather than MaybeNotify:
	// once subagents are running, the parent prompt has been approved and any
	// permissionRequest is for an auto-executing background tool. The existing
	// debounce cannot cancel itself here — postToolUse (action "active") returns
	// early from MaybeNotify before reaching cancelPendingLocked, and the
	// auto-approved-permission cancel path requires prevGuard == "active", not
	// "subagent". So we drop any pending permission entry outright.
	// stop_failure carries a real Copilot crash and must surface even mid-subagent,
	// mirroring the SubagentStop failure-surfacing rule above. Only the benign
	// per-turn agentStop (resolved as active + preserve-current) and speculative
	// permissionRequest are suppressed by this gate.
	if isCopilotRequest(req) && ((req.Event == "agentStop" && req.Action != "stop_failure") || req.Event == "permissionRequest") {
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(req.WindowID)
		running := st.SubagentCount > 0
		count := st.SubagentCount
		h.stateMu.Unlock()
		if running {
			// Refresh the subagent guard so it does not expire while agents run.
			h.guard.TryUpdate(req.WindowID, "subagent", false)
			if req.Event == "permissionRequest" {
				h.cancelPendingNotification(req.WindowID)
			}
			slog.Debug("copilot mid-subagent suppression",
				"window_id", req.WindowID,
				"event", req.Event,
				"action", req.Action,
				"subagent_count", count,
			)
			h.stateMu.Lock()
			h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, req.Action, suppressionReasonCopilotMidSubagent, currentIconState))
			h.stateMu.Unlock()
			outcome.skipReduce = true
			outcome.skipReason = protocol.DecisionReasonCopilotMidSubagent
			return protocol.Response{OK: true, Allowed: false}, true
		}
	}

	// Copilot permissionRequest fires before the permission service decides
	// whether the tool call is auto-allowed or needs a real user prompt. Keep the
	// current icon on this pre-permission signal and wait for either later tool
	// progress (auto-approved) or a later explicit prompt notification
	// (permission_prompt or elicitation_dialog).
	if isCopilotPermissionRequest(req) {
		h.cancelPendingNotification(req.WindowID)
		h.stateMu.Lock()
		now := time.Now()
		st := h.ensureWindowStateLocked(req.WindowID)
		st.CopilotToolPermissionState = copilotToolPermissionPending
		st.Activity = now
		st.LastSessionTitle = req.SessionTitle
		st.LastPipeMode = req.PipeMode
		st.NotifiedStale = false
		if req.Account != "" {
			st.LastAccount = req.Account
		}
		meta := h.windowMetadata[req.WindowID]
		meta.Durable.ActivityAt = now.Unix()
		h.windowMetadata[req.WindowID] = meta
		h.appendEventLocked(req.WindowID, sessionEventFromRequest(req, req.Action, protocol.IconStatePreserve, currentIconState))
		// The icon is preserved, so this is durable bookkeeping only.
		h.linkRequestRenderGenerationLocked(req, st.Seq+1)
		h.markWindowActivityDirtyLocked(req.WindowID)
		h.stateMu.Unlock()
		outcome.skipReduce = true
		outcome.skipReason = protocol.DecisionReasonReducerBypassed
		return protocol.Response{OK: true, Allowed: true, IconState: protocol.IconStatePreserve}, true
	}

	return protocol.Response{}, false
}
