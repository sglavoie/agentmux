package daemon

import (
	"log/slog"
	"strings"
	"time"
)

// applyOutcome carries what the legacy state machine decided about a request
// so the authoritative reducer step can fold the same event it processed.
type applyOutcome struct {
	// skipReduce marks a request the legacy machine consumed as a
	// harness-specific quirk (copilot prompt bookkeeping, subagent reference
	// counting, auto-approved permissions, preserve-current child completions).
	// Those events carry no source-scoped fact: folding them into the reducer
	// would repaint icons the quirk layer exists to hold back.
	skipReduce bool
	// skipReason is the stable taxonomy entry explaining an intentional reducer
	// bypass. It is persisted even when the legacy event row was otherwise
	// marked allowed (for example a preserve-current completion).
	skipReason string
	// reduceAction is the action the legacy machine settled on after
	// reset-flow rewrites and copilot synthetic stops, so the reducer folds
	// what actually happened rather than the pre-rewrite stamp.
	reduceAction string
	// notifyAction is the action notification decisions are made from. It
	// tracks reduceAction except for synthetic copilot stops, which the legacy
	// notifier never surfaced.
	notifyAction string
	// guardBlocked marks a request the legacy StateGuard refused. The event
	// still reaches the reducer, which owns the visible outcome — the reducer
	// step logs the authoritative suppression reason (or falls back to
	// guard_blocked when the reducer did not suppress).
	guardBlocked bool
}

// handleStateUpdate runs one state-changing request through the legacy state
// machine (applyStateUpdate) for its guard bookkeeping, harness quirk
// handling, and event logging — then folds the event into the window's
// source-fact reducer, whose effective state is the authoritative visible
// state (DesiredIcon + Looping). The legacy scalar decision is kept only as
// divergence evidence in the shadow export. Suppressed transitions still
// return OK, so the reducer sees them too and applies its own suppression
// rules.
func (h *Handler) handleStateUpdate(req Request) Response {
	if req.Type == "" {
		req.Type = TypeStateUpdate
	}
	req = ensureRequestTraceID(req)
	if req.eventRef == nil {
		req.eventRef = &sessionEventReference{}
	}
	if req.parseOutcome == "" {
		req.parseOutcome = ParseOutcomeSynthetic
	}
	before := h.effectiveStateForTrace(req.WindowID)
	resp, outcome := h.applyStateUpdate(req)
	if resp.OK && !outcome.skipReduce {
		h.reduceStateUpdate(req, outcome, resp, time.Now())
	} else if resp.OK {
		h.finalizeBypassedStateUpdate(req, outcome, before)
	}
	return resp
}

func (h *Handler) applyStateUpdate(req Request) (Response, applyOutcome) {
	var outcome applyOutcome
	if req.WindowID <= 0 {
		return Response{OK: false, Error: "window_id is required for state_update"}, outcome
	}
	if req.Action == "" {
		return Response{OK: false, Error: "action is required for state_update"}, outcome
	}
	if _, ok := actionPriority[req.Action]; !ok {
		return Response{OK: false, Error: "unknown action: " + req.Action}, outcome
	}

	if req.Action == "loop_marker" {
		return h.handleLoopMarkerStateUpdate(req), outcome
	}

	// Child-complete signals refresh activity bookkeeping while preserving the
	// visible icon; they never become source facts.
	outcome.skipReduce = req.PreservesIcon
	if req.PreservesIcon {
		outcome.skipReason = DecisionReasonPreservesIcon
	}

	// A newer prompt or explicit wipe supersedes a stop still on hold; drop it
	// before this update lands so the held stop cannot fire afterward and
	// repaint a stale icon. This cancellation is also serialized with a timer
	// callback that has fired and claimed its generation, so it either
	// invalidates that claim or waits for the Stop to apply first.
	if supersedesStopHold(req) {
		h.cancelStopHold(req.WindowID)
	}

	receivedAt := time.Now()
	h.stateMu.Lock()
	inputAction := req.Action
	req = h.applyResetFlowLocked(req)
	if req.Action != inputAction {
		req.normalizationOutcome = NormalizationOutcomeReducerRewrite
	}
	// A reset flow may have just rewritten the action (stop → user_clear); the
	// reducer and notifier must see the settled action, not the raw stamp.
	outcome.reduceAction = req.Action
	outcome.notifyAction = req.Action
	h.touchWindowMetadataLocked(req)
	h.rememberKittySocketLocked(req.WindowID, req.KittyListenOn)
	st := h.ensureWindowStateLocked(req.WindowID)
	// Capture the visible state before any mutation so the final dirty mark
	// can distinguish a visual change (immediate render) from activity-only
	// bookkeeping (batched persistence).
	visualIconBefore := desiredIconForState(st)
	visualLoopingBefore := st.Looping
	if req.Account != "" {
		st.LastAccount = req.Account
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

	prevAction, currentIconState := h.lastKnownState(req.WindowID)
	if currentIconState == "" {
		currentIconState = req.currentIconState
	}
	h.stateMu.Lock()
	st = h.ensureWindowStateLocked(req.WindowID)
	copilotPermissionState := st.CopilotToolPermissionState
	if shouldResetCopilotToolPermissionState(req) {
		st.CopilotToolPermissionState = copilotToolPermissionNone
		copilotPermissionState = copilotToolPermissionNone
	}
	if isCopilotElicitationPrompt(req) ||
		(copilotPermissionState == copilotToolPermissionPending && isCopilotPermissionPromptNotification(req)) {
		st.CopilotToolPermissionState = copilotToolPermissionNone
		copilotPermissionState = copilotToolPermissionNone
	} else if copilotPermissionState == copilotToolPermissionPending &&
		shouldConfirmCopilotAutoApprovedToolPermission(req) {
		st.CopilotToolPermissionState = copilotToolPermissionAutoApproved
		copilotPermissionState = copilotToolPermissionAutoApproved
	}
	priorAutoApprovedToolPrompt := copilotPermissionState == copilotToolPermissionAutoApproved
	h.stateMu.Unlock()
	syntheticStop := shouldSynthesizeCopilotCompactStop(req, prevAction)
	if syntheticStop {
		// The legacy machine records this idle as a stop; the reducer must fold
		// the same synthetic stop. The notifier keeps the raw idle (legacy never
		// notified synthetic copilot stops).
		outcome.reduceAction = "stop"
	}
	if shouldSuppressCopilotPendingPrompt(req, prevAction) {
		// Re-arm the actual prior state — prevAction is "permission" or
		// "elicitation" here. Re-arming "permission" unconditionally would
		// upgrade a pending ❓ guard (priority 2) to 🔑 (priority 3), blocking a
		// re-fired elicitation within the guard window.
		h.guard.TryUpdate(req.WindowID, prevAction, false)
		slog.Debug("copilot pending prompt suppression",
			"window_id", req.WindowID,
			"event", req.Event,
			"action", req.Action,
		)
		h.stateMu.Lock()
		h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, req.Action, suppressionReasonCopilotPendingPrompt, currentIconState))
		h.stateMu.Unlock()
		outcome.skipReduce = true
		outcome.skipReason = DecisionReasonCopilotPendingPrompt
		return Response{OK: true, Allowed: false}, outcome
	}
	if shouldSuppressPendingElicitationActive(req, prevAction) {
		slog.Debug("pending elicitation active suppression",
			"window_id", req.WindowID,
			"event", req.Event,
			"tool_name", req.ToolName,
		)
		h.stateMu.Lock()
		h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, req.Action, suppressionReasonElicitationPending, currentIconState))
		h.stateMu.Unlock()
		outcome.skipReduce = true
		outcome.skipReason = DecisionReasonElicitationPending
		return Response{OK: true, Allowed: false}, outcome
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

	if resp, handled := h.handleSubagentStateUpdate(req, currentIconState, &outcome); handled {
		return resp, outcome
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
		st := h.ensureWindowStateLocked(req.WindowID)
		if req.Action == "clear" || req.Action == "user_clear" {
			st.CopilotToolPermissionState = copilotToolPermissionNone
		}
		st.SubagentCount = 0
		if req.Action != "user_clear" {
			st.PendingReset = resetFlowNone
		}
		st.NotifiedStale = false
		st.ContextLimitNotified = false
		st.LastSessionTitle = ""
		st.LastPipeMode = false
		st.LastAccount = ""
		h.stateMu.Unlock()
	}

	// The global active-after-terminal barrier that used to live here is gone:
	// terminal trailing suppression is now the reducer's job, scoped to the
	// same source and turn (with a guard_duration grace for unidentified
	// events) so a genuinely new turn, tool call, or post-grace activity is
	// authoritative recovery instead of being suppressed indefinitely.
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
		// The guard suppressed the legacy icon transition; the reducer still
		// sees the event and makes the authoritative visible-state and
		// notification decisions — including which suppression reason the
		// event log records. Requests the reducer never sees (preserve-icon
		// child completions) log the legacy reason here.
		outcome.guardBlocked = true
		if outcome.skipReduce {
			h.stateMu.Lock()
			h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, req.Action, suppressionReasonGuardBlocked, currentIconState))
			h.stateMu.Unlock()
		}
		return Response{OK: true, Allowed: false}, outcome
	}

	// Auto-approved permission: Copilot can emit a trailing
	// notification.permission_prompt even after the turn already continued. In
	// that case there is nothing left for the user to approve, so preserve the
	// current icon instead of surfacing the key. Claude and Codex both treat
	// PreToolUse → PermissionRequest as a real, user-blocking prompt and must
	// surface the key immediately, so neither is caught by this heuristic.
	isAutoApprovedPerm := effective == "permission" &&
		isCopilotTrailingAutoApprovedToolPermission(req, priorAutoApprovedToolPrompt)

	// Restrict the Copilot special-case to trailing notification.permission_prompt
	// noise. Explicit user-blocking prompts like elicitation_dialog must still
	// show the question icon.
	isCopilotAutoApprovedPerm := isAutoApprovedPerm &&
		isCopilotAutoApprovedToolPermission(req)

	h.stateMu.Lock()
	now := time.Now()
	st = h.ensureWindowStateLocked(req.WindowID)
	st.Activity = now
	st.LastSessionTitle = req.SessionTitle
	st.LastPipeMode = req.PipeMode
	if req.Account != "" {
		st.LastAccount = req.Account
	}
	if effective == "active" {
		st.LastActiveEvent = req.Event
	}
	if isCopilotAutoApprovedPerm {
		st.CopilotToolPermissionState = copilotToolPermissionAutoApproved
	} else if effective == "permission" && isCopilotRequest(req) &&
		isCopilotPermissionPromptNotification(req) {
		st.CopilotToolPermissionState = copilotToolPermissionNone
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
		st.LastAction = recordedAction
		st.NotifiedStale = false
		st.ContextLimitNotified = false
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
			st := h.ensureWindowStateLocked(req.WindowID)
			st.LastAction = ""
			st.LastActiveEvent = ""
			h.stateMu.Unlock()
		case "compact":
			if syntheticStop {
				iconState = IconState("stop")
			} else {
				iconState = ""
				h.stateMu.Lock()
				st := h.ensureWindowStateLocked(req.WindowID)
				st.LastAction = ""
				st.LastActiveEvent = ""
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
		st := h.ensureWindowStateLocked(req.WindowID)
		st.LastAction = ""
		st.LastActiveEvent = ""
		h.stateMu.Unlock()
	}
	if isCopilotAutoApprovedPerm {
		h.cancelPendingNotification(req.WindowID)
	} else if isAutoApprovedPerm {
		// The heuristic can misfire on a real prompt (Claude fires
		// PermissionRequest after PreToolUse for genuinely blocking dialogs
		// too), so the debounced notification still goes out even though the
		// icon stays preserved: a truly auto-approved tool call's follow-up
		// activity arrives within the debounce and the user ignores one toast,
		// while a real prompt the user must see is never silently swallowed.
		h.maybeNotifyTraced(req.WindowID, req.SessionTitle, req.Account, effective, req.PipeMode, notificationTraceFromRequest(req))
	}
	if isAutoApprovedPerm {
		// The permission resolved itself; there is no fact to fold and no key
		// icon to surface.
		outcome.skipReduce = true
		outcome.skipReason = DecisionReasonAutoApprovedPermission
	}

	h.stateMu.Lock()
	loggedAction := effective
	if syntheticStop {
		loggedAction = "stop"
	}
	if iconState != IconStatePreserve {
		st := h.ensureWindowStateLocked(req.WindowID)
		st.DesiredIcon = iconState
		st.StateSet = true
	}
	if isAutoApprovedPerm {
		h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, loggedAction, suppressionReasonAutoApprovedPerm, currentIconState))
	} else {
		h.appendEventLocked(req.WindowID, sessionEventFromRequest(req, loggedAction, iconState, currentIconState))
	}
	st = h.ensureWindowStateLocked(req.WindowID)
	h.linkRequestRenderGenerationLocked(req, st.Seq+1)
	h.syncRendererMetadataLocked(req.WindowID)
	// Visual changes render immediately; everything else is batched
	// persistence flushed by the next maintenance tick.
	if st := h.ensureWindowStateLocked(req.WindowID); desiredIconForState(st) != visualIconBefore || st.Looping != visualLoopingBefore {
		h.markWindowDirtyLocked(req.WindowID)
	} else {
		h.markWindowActivityDirtyLocked(req.WindowID)
	}
	h.stateMu.Unlock()

	return Response{OK: true, Allowed: true, IconState: iconState}, outcome
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
	h.rememberKittySocketLocked(req.WindowID, req.KittyListenOn)
	st := h.ensureWindowStateLocked(req.WindowID)
	if req.Account != "" {
		accountKey := strings.ToLower(req.Account)
		h.lastSeenEventAt[accountKey] = receivedAt
		delete(h.notifiedHookSilence, accountKey)
	}
	loopingBefore, loopingSetBefore := st.Looping, st.LoopingSet
	switch req.Event {
	case agentSessionLoopStarted, agentSessionLoopTurnStarted,
		agentSessionLoopTurnHeartbeat, agentSessionLoopTurnHangSuspected,
		agentSessionLoopTurnHangCleared, agentSessionLoopTurnFinished,
		agentSessionLoopVerificationStarted, agentSessionLoopVerificationHeartbeat,
		agentSessionLoopVerificationFinished, agentSessionLoopPaused:
		st.Looping = true
		st.LoopingSet = true
	case agentSessionLoopFinished:
		st.Looping = false
		st.LoopingSet = true
	}
	h.appendEventLocked(req.WindowID, sessionEventFromRequest(req, req.Action, IconStatePreserve, desiredIconForState(st)))
	h.linkRequestRenderGenerationLocked(req, st.Seq+1)
	h.syncRendererMetadataLocked(req.WindowID)
	// A heartbeat that leaves the marker as-is is activity bookkeeping; only a
	// marker flip needs an immediate repaint.
	if st.Looping != loopingBefore || st.LoopingSet != loopingSetBefore {
		h.markWindowDirtyLocked(req.WindowID)
	} else {
		h.markWindowActivityDirtyLocked(req.WindowID)
	}
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
