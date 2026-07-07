package daemon

import (
	"log/slog"
	"strings"
	"time"
)

func (h *Handler) handleStateUpdate(req Request) Response {
	if req.WindowID <= 0 {
		return Response{OK: false, Error: "window_id is required for state_update"}
	}
	if req.Action == "" {
		return Response{OK: false, Error: "action is required for state_update"}
	}
	if _, ok := actionPriority[req.Action]; !ok {
		return Response{OK: false, Error: "unknown action: " + req.Action}
	}

	if req.Action == "loop_marker" {
		return h.handleLoopMarkerStateUpdate(req)
	}

	receivedAt := time.Now()
	h.stateMu.Lock()
	req = h.applyResetFlowLocked(req)
	h.touchWindowMetadataLocked(req)
	if req.Muted {
		h.mutedWindows[req.WindowID] = true
	} else {
		delete(h.mutedWindows, req.WindowID)
	}
	if req.Account != "" {
		accountKey := strings.ToLower(req.Account)
		h.lastSeenEventAt[accountKey] = receivedAt
		delete(h.notifiedHookSilence, accountKey)
	}
	h.stateMu.Unlock()

	slog.Debug("hook event received",
		"window_id", req.WindowID,
		"account", req.Account,
		"event", req.Event,
		"action", req.Action,
		"source", req.Source,
		"preserves_icon", req.PreservesIcon,
		"reset_flow", req.ResetFlow,
	)

	prevAction := h.lastKnownAction(req.WindowID, req.CurrentIconState)
	h.stateMu.Lock()
	prevActiveEvent := h.lastActiveEvent[req.WindowID]
	copilotPermissionState := h.copilotToolPermissionState[req.WindowID]
	if shouldResetCopilotToolPermissionState(req) {
		delete(h.copilotToolPermissionState, req.WindowID)
		copilotPermissionState = copilotToolPermissionNone
	}
	if isCopilotElicitationPrompt(req) ||
		(copilotPermissionState == copilotToolPermissionPending && isCopilotPermissionPromptNotification(req)) {
		delete(h.copilotToolPermissionState, req.WindowID)
		copilotPermissionState = copilotToolPermissionNone
	} else if copilotPermissionState == copilotToolPermissionPending &&
		shouldConfirmCopilotAutoApprovedToolPermission(req) {
		h.copilotToolPermissionState[req.WindowID] = copilotToolPermissionAutoApproved
		copilotPermissionState = copilotToolPermissionAutoApproved
	}
	priorAutoApprovedToolPrompt := copilotPermissionState == copilotToolPermissionAutoApproved
	h.stateMu.Unlock()
	syntheticStop := shouldSynthesizeCopilotCompactStop(req, prevAction)
	if shouldSuppressCopilotPendingPrompt(req, prevAction) {
		h.guard.TryUpdate(req.WindowID, "permission", false)
		slog.Debug("copilot pending prompt suppression",
			"window_id", req.WindowID,
			"event", req.Event,
			"action", req.Action,
		)
		h.stateMu.Lock()
		h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, req.Action, suppressionReasonCopilotPendingPrompt))
		h.stateMu.Unlock()
		return Response{OK: true, Allowed: false}
	}
	if shouldSuppressPendingElicitationActive(req, prevAction) {
		slog.Debug("pending elicitation active suppression",
			"window_id", req.WindowID,
			"event", req.Event,
			"tool_name", req.ToolName,
		)
		h.stateMu.Lock()
		h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, req.Action, suppressionReasonElicitationPending))
		h.stateMu.Unlock()
		return Response{OK: true, Allowed: false}
	}

	// Only terminal/idle transitions invalidate a pending permission notification.
	// PostToolUse/active/subagent events are normal companions to a real
	// permission_prompt and must not cancel the notification.
	// elicitation and failure are intentionally omitted: they are mid-session
	// states, not terminal, so they don't *invalidate* a permission here. When
	// either of them does fire a notification of its own, the notify layer's
	// cancelPendingLocked supersedes the pending entry (see notify.go MaybeNotify).
	switch req.Action {
	case "idle", "clear", "user_clear", "stop", "stop_failure":
		h.cancelPendingNotification(req.WindowID)
	}

	// SubagentStart: increment reference count so the robot icon persists
	// even when interleaved parent "active" events arrive.
	if req.Action == "subagent" {
		h.stateMu.Lock()
		h.subagentCount[req.WindowID]++
		h.updateDurableSubagentCountLocked(req.WindowID)
		h.stateMu.Unlock()
	}

	// SubagentStop: decrement counter; suppress "active" while other agents run.
	if req.Event == "SubagentStop" || req.Event == "subagentStop" {
		h.stateMu.Lock()
		wasTracked := h.subagentCount[req.WindowID] > 0
		if wasTracked {
			h.subagentCount[req.WindowID]--
		}
		remaining := h.subagentCount[req.WindowID]
		if wasTracked && remaining == 0 {
			// Drop the zero-valued entry so per-window state cannot accumulate
			// indefinitely across many sessions that never hit a terminal event.
			delete(h.subagentCount, req.WindowID)
		}
		h.updateDurableSubagentCountLocked(req.WindowID)
		h.stateMu.Unlock()

		if wasTracked && remaining > 0 && req.Action == "active" {
			// More agents still running — refresh the subagent guard (reset expiry)
			// and suppress this "active" transition. Only "active" is suppressed:
			// a SubagentStop carrying a real failure (action == "stop_failure",
			// e.g. stopReason = "error" / "max_tokens") must surface so the user
			// sees the failure even when sibling agents are still running.
			h.guard.TryUpdate(req.WindowID, "subagent", false)
			h.stateMu.Lock()
			h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, req.Action, suppressionReasonSubagentStillRunning))
			h.stateMu.Unlock()
			return Response{OK: true, Allowed: false}
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
	if (req.Event == "agentStop" && req.Action != "stop_failure") || req.Event == "permissionRequest" {
		h.stateMu.Lock()
		running := h.subagentCount[req.WindowID] > 0
		count := h.subagentCount[req.WindowID]
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
			h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, req.Action, suppressionReasonCopilotMidSubagent))
			h.stateMu.Unlock()
			return Response{OK: true, Allowed: false}
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
		h.copilotToolPermissionState[req.WindowID] = copilotToolPermissionPending
		h.activity[req.WindowID] = now
		h.lastSessionTitle[req.WindowID] = req.SessionTitle
		h.lastPipeMode[req.WindowID] = req.PipeMode
		delete(h.notifiedStale, req.WindowID)
		if req.Account != "" {
			h.lastAccount[req.WindowID] = req.Account
		}
		meta := h.windowMetadata[req.WindowID]
		meta.Durable.ActivityAt = now.Unix()
		h.windowMetadata[req.WindowID] = meta
		h.appendEventLocked(req.WindowID, sessionEventFromRequest(req, req.Action, IconStatePreserve))
		h.stateMu.Unlock()
		return Response{OK: true, Allowed: true, IconState: IconStatePreserve}
	}

	// Terminal / cleanup states reset the subagent counter.
	// This list intentionally includes "failure" even though the cancel-switch
	// above does not: counter cleanup is safe (losing the count just clears the
	// 🤖 over-suppression), but cancelling a pending permission is unsafe (the
	// user may never see the prompt). A tool failure is a strong signal that any
	// in-flight subagent bookkeeping is stale, hence reset; but it doesn't make
	// a pending permission stale, hence no cancel.
	//
	// eventLog is intentionally preserved across terminal actions so the detail
	// pane can still surface the session's history after the session ends.
	// Window-existence GC in SweepStale drops the entry once Kitty closes the
	// window.
	if req.Action == "stop" || req.Action == "stop_failure" || req.Action == "failure" ||
		req.Action == "idle" || req.Action == "clear" || req.Action == "user_clear" {
		h.stateMu.Lock()
		if req.Action == "clear" || req.Action == "user_clear" {
			delete(h.copilotToolPermissionState, req.WindowID)
		}
		delete(h.subagentCount, req.WindowID)
		if req.Action != "user_clear" {
			delete(h.pendingReset, req.WindowID)
		}
		delete(h.notifiedStale, req.WindowID)
		delete(h.contextLimitNotified, req.WindowID)
		delete(h.lastSessionTitle, req.WindowID)
		delete(h.lastPipeMode, req.WindowID)
		delete(h.lastAccount, req.WindowID)
		h.stateMu.Unlock()
	}

	// Block non-prompt active events from overwriting a terminal stop state.
	// PostToolBatch can race with Stop in async execution; once the stop guard
	// expires, lastAction is the main barrier, with the current title icon as a
	// restart-safe fallback after daemon auto-starts or restarts. This check
	// runs BEFORE TryUpdate so a suppressed "active" cannot leave the guard
	// armed at "active" — otherwise a subsequent idle would see prevGuard
	// "active" and ask the indicator to clear the persisting stop icon.
	if req.Action == "active" && !req.IsPrompt {
		if prevAction == "stop" || prevAction == "stop_failure" {
			h.maybeNotify(req.WindowID, req.SessionTitle, req.Account, req.Action, req.PipeMode)
			h.stateMu.Lock()
			h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, req.Action, suppressionReasonActiveAfterTerminal))
			h.stateMu.Unlock()
			return Response{OK: true, Allowed: false}
		}
	}

	prevGuard := h.guard.Current(req.WindowID)
	preservedAction := ""
	if req.PreservesIcon {
		// Child-complete events reuse the non-terminal "active" action to refresh
		// activity bookkeeping without surfacing a new icon. Preserve whichever
		// state the user was already seeing so a follow-up idle/clear keeps the
		// same terminal-vs-active semantics instead of acting like a fresh bolt.
		preservedAction = prevGuard
		if preservedAction == "" {
			preservedAction = prevAction
		}
	}
	allowed, effective := h.guard.TryUpdate(req.WindowID, req.Action, req.IsPrompt)
	slog.Debug("state transition",
		"window_id", req.WindowID,
		"profile", req.Account,
		"action", req.Action,
		"allowed", allowed,
	)
	if !allowed {
		// The guard suppressed the icon transition, but the user's intent may
		// still warrant a notification (permission/stop/failure/elicitation).
		// Pass req.Action — the notifier filters by its own per-action config and
		// cooldown, so this cannot double-fire with the success path below.
		h.maybeNotify(req.WindowID, req.SessionTitle, req.Account, req.Action, req.PipeMode)
		h.stateMu.Lock()
		h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, req.Action, suppressionReasonGuardBlocked))
		h.stateMu.Unlock()
		return Response{OK: true, Allowed: false}
	}

	// Auto-approved permission: the Claude harness can emit PermissionRequest
	// after PreToolUse for a tool that is already running, and Copilot can later
	// emit a trailing notification.permission_prompt even after the turn already
	// continued. In both cases there is nothing left for the user to approve, so
	// preserve the current icon instead of surfacing the key. Other harnesses
	// (e.g. Codex) use PreToolUse → PermissionRequest as the normal real
	// permission flow and must not be caught by this heuristic.
	isAutoApprovedPerm := effective == "permission" &&
		((isClaudeRequest(req) && prevGuard == "active" &&
			(prevActiveEvent == "PreToolUse" || prevActiveEvent == "preToolUse")) ||
			isCopilotTrailingAutoApprovedToolPermission(req, priorAutoApprovedToolPrompt))

	// Restrict the Copilot special-case to trailing notification.permission_prompt
	// noise. Explicit user-blocking prompts like elicitation_dialog must still
	// show the question icon.
	isCopilotAutoApprovedPerm := isAutoApprovedPerm &&
		isCopilotAutoApprovedToolPermission(req)

	h.stateMu.Lock()
	now := time.Now()
	h.activity[req.WindowID] = now
	h.lastSessionTitle[req.WindowID] = req.SessionTitle
	h.lastPipeMode[req.WindowID] = req.PipeMode
	if req.Account != "" {
		h.lastAccount[req.WindowID] = req.Account
	}
	if effective == "active" {
		h.lastActiveEvent[req.WindowID] = req.Event
	}
	if isCopilotAutoApprovedPerm {
		h.copilotToolPermissionState[req.WindowID] = copilotToolPermissionAutoApproved
	} else if effective == "permission" && isCopilotRequest(req) &&
		isCopilotPermissionPromptNotification(req) {
		delete(h.copilotToolPermissionState, req.WindowID)
	}
	// Record the last substantive action so idle/clear can detect a stale bolt
	// even after the state guard has expired. Preserve-current events keep the
	// prior state semantics instead of rewriting them to a synthesized "active".
	recordedAction := effective
	if req.PreservesIcon {
		recordedAction = preservedAction
	} else if syntheticStop {
		recordedAction = "stop"
	}
	if recordedAction != "" && recordedAction != "idle" && recordedAction != "clear" && recordedAction != "user_clear" && !isAutoApprovedPerm {
		h.lastAction[req.WindowID] = recordedAction
		delete(h.notifiedStale, req.WindowID)
		delete(h.contextLimitNotified, req.WindowID)
	}
	meta := h.windowMetadata[req.WindowID]
	meta.Durable.ActivityAt = now.Unix()
	if effective == "active" {
		meta.Durable.LastActiveEvent = req.Event
	}
	if recordedAction != "" && recordedAction != "idle" && recordedAction != "clear" && recordedAction != "user_clear" && !isAutoApprovedPerm {
		meta.Durable.LastAction = recordedAction
	}
	h.windowMetadata[req.WindowID] = meta
	h.stateMu.Unlock()

	if req.PreservesIcon {
		if preservedAction == "" {
			h.guard.Clear(req.WindowID)
		} else {
			h.guard.TryUpdate(req.WindowID, preservedAction, false)
		}
	}

	iconState := IconState(effective)
	if req.PreservesIcon {
		iconState = IconStatePreserve
	} else if isAutoApprovedPerm {
		// Clear the permission guard immediately — it was auto-resolved, so a
		// subsequent idle can fall back to lastAction ("active") and clear the bolt.
		h.guard.Clear(req.WindowID)
		iconState = IconStatePreserve
	} else if effective == "idle" || effective == "clear" {
		// Prefer prevGuard (still within the guard window); fall back to the
		// pre-update lastAction/current title icon snapshot captured in prevAction.
		// This keeps cleanup decisions tied to the state the user was actually
		// seeing before the current hook arrived.
		prev := prevGuard
		if prev == "" {
			prev = prevAction
		}
		switch prev {
		case "active", "subagent":
			// Agent finished — clear the active indicator and reset tracking.
			iconState = ""
			h.stateMu.Lock()
			delete(h.lastAction, req.WindowID)
			delete(h.lastActiveEvent, req.WindowID)
			h.stateMu.Unlock()
		case "compact":
			if syntheticStop {
				iconState = IconState("stop")
			} else {
				iconState = ""
				h.stateMu.Lock()
				delete(h.lastAction, req.WindowID)
				delete(h.lastActiveEvent, req.WindowID)
				h.stateMu.Unlock()
			}
		default:
			// stop/permission/failure icons persist until the user notices them.
			iconState = IconStatePreserve
		}
	} else if effective == "user_clear" {
		// Explicit wipe (from the TUI or a reset flow) — reset lastAction so a
		// future idle doesn't try to clear an icon that no longer exists.
		h.stateMu.Lock()
		delete(h.lastAction, req.WindowID)
		delete(h.lastActiveEvent, req.WindowID)
		h.stateMu.Unlock()
	}
	if isCopilotAutoApprovedPerm {
		h.cancelPendingNotification(req.WindowID)
	} else {
		h.maybeNotify(req.WindowID, req.SessionTitle, req.Account, effective, req.PipeMode)
	}

	h.stateMu.Lock()
	loggedAction := effective
	if syntheticStop {
		loggedAction = "stop"
	}
	h.appendEventLocked(req.WindowID, sessionEventFromRequest(req, loggedAction, iconState))
	h.stateMu.Unlock()

	return Response{OK: true, Allowed: true, IconState: iconState}
}

// handleLoopMarkerStateUpdate records an ASL lifecycle signal without
// disturbing the visible state machine. The marker is event/activity metadata
// only — it appends to the per-window event log, refreshes the durable export
// snapshot, and keeps the hook-silence latch armed so a profile that is only
// emitting loop markers still counts as alive. It deliberately skips the guard,
// lastAction, notifications, stale tracking, and icon resolution paths so a
// running ASL loop cannot introduce or disturb a bolt icon.
func (h *Handler) handleLoopMarkerStateUpdate(req Request) Response {
	receivedAt := time.Now()
	h.stateMu.Lock()
	h.touchWindowMetadataLocked(req)
	if req.Muted {
		h.mutedWindows[req.WindowID] = true
	} else {
		delete(h.mutedWindows, req.WindowID)
	}
	if req.Account != "" {
		accountKey := strings.ToLower(req.Account)
		h.lastSeenEventAt[accountKey] = receivedAt
		delete(h.notifiedHookSilence, accountKey)
	}
	h.appendEventLocked(req.WindowID, sessionEventFromRequest(req, req.Action, IconStatePreserve))
	h.stateMu.Unlock()

	slog.Debug("loop marker received",
		"window_id", req.WindowID,
		"account", req.Account,
		"event", req.Event,
		"action", req.Action,
		"source", req.Source,
	)

	return Response{OK: true, Allowed: true, IconState: IconStatePreserve}
}
