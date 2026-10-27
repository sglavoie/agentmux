package daemon

import (
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
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
	// guardBlocked marks a request the legacy activity.StateGuard refused. The event
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
func (h *Handler) handleStateUpdate(req protocol.Request) protocol.Response {
	if req.Type == "" {
		req.Type = protocol.TypeStateUpdate
	}
	req = events.EnsureRequestTraceID(req)
	if req.EventRef == nil {
		req.EventRef = &protocol.SessionEventReference{}
	}
	if req.ParseOutcome == "" {
		req.ParseOutcome = protocol.ParseOutcomeSynthetic
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

func (h *Handler) applyStateUpdate(req protocol.Request) (protocol.Response, applyOutcome) {
	var outcome applyOutcome
	if req.WindowID <= 0 {
		return protocol.Response{OK: false, Error: "window_id is required for state_update"}, outcome
	}
	if req.Action == "" {
		return protocol.Response{OK: false, Error: "action is required for state_update"}, outcome
	}
	if _, ok := protocol.ActionPriority(req.Action); !ok {
		return protocol.Response{OK: false, Error: "unknown action: " + req.Action}, outcome
	}

	if req.Action == "loop_marker" {
		return h.handleLoopMarkerStateUpdate(req), outcome
	}

	// Child-complete signals refresh activity bookkeeping while preserving the
	// visible icon; they never become source facts.
	outcome.skipReduce = req.PreservesIcon
	if req.PreservesIcon {
		outcome.skipReason = protocol.DecisionReasonPreservesIcon
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
	// An update that reaches a runtime slot after its session ended — a held
	// stop committing, or an operator action racing the close — must not
	// bring the slot's state back: nothing would ever drop it again.
	if h.runtimeSlotClosedLocked(req.WindowID) {
		h.stateMu.Unlock()
		return protocol.Response{OK: false, Error: "runtime slot is closed"}, outcome
	}
	inputAction := req.Action
	req = h.applyResetFlowLocked(req)
	if req.Action != inputAction {
		req.NormalizationOutcome = protocol.NormalizationOutcomeReducerRewrite
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
	visualIconBefore := rendering.DesiredIcon(st)
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
		currentIconState = req.CurrentIconState
	}
	h.stateMu.Lock()
	st = h.ensureWindowStateLocked(req.WindowID)
	copilotPermissionState := st.CopilotToolPermissionState
	if shouldResetCopilotToolPermissionState(req) {
		st.CopilotToolPermissionState = state.CopilotToolPermissionNone
		copilotPermissionState = state.CopilotToolPermissionNone
	}
	if isCopilotElicitationPrompt(req) ||
		(copilotPermissionState == state.CopilotToolPermissionPending && isCopilotPermissionPromptNotification(req)) {
		st.CopilotToolPermissionState = state.CopilotToolPermissionNone
		copilotPermissionState = state.CopilotToolPermissionNone
	} else if copilotPermissionState == state.CopilotToolPermissionPending &&
		shouldConfirmCopilotAutoApprovedToolPermission(req) {
		st.CopilotToolPermissionState = state.CopilotToolPermissionAutoApproved
		copilotPermissionState = state.CopilotToolPermissionAutoApproved
	}
	priorAutoApprovedToolPrompt := copilotPermissionState == state.CopilotToolPermissionAutoApproved
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
		h.appendEventLocked(req.WindowID, events.SuppressedSessionEvent(req, req.Action, suppressionReasonCopilotPendingPrompt, currentIconState))
		h.stateMu.Unlock()
		outcome.skipReduce = true
		outcome.skipReason = protocol.DecisionReasonCopilotPendingPrompt
		return protocol.Response{OK: true, Allowed: false}, outcome
	}
	if shouldSuppressPendingElicitationActive(req, prevAction) {
		slog.Debug("pending elicitation active suppression",
			"window_id", req.WindowID,
			"event", req.Event,
			"tool_name", req.ToolName,
		)
		h.stateMu.Lock()
		h.appendEventLocked(req.WindowID, events.SuppressedSessionEvent(req, req.Action, suppressionReasonElicitationPending, currentIconState))
		h.stateMu.Unlock()
		outcome.skipReduce = true
		outcome.skipReason = protocol.DecisionReasonElicitationPending
		return protocol.Response{OK: true, Allowed: false}, outcome
	}

	// Only terminal/idle transitions invalidate a pending permission notification.
	// PostToolUse/active/subagent events are normal companions to a real
	// permission_prompt and must not cancel the notification.
	// elicitation and failure are intentionally omitted: they are mid-session
	// states, not terminal, so they don't *invalidate* a permission here. When
	// either of them does fire a notification of its own, the notify layer's
	// cancelPendingLocked supersedes the pending entry (see notify.go MaybeNotify).
	switch req.Action {
	case "idle", "clear", "user_clear", "user_active", "stop", "stop_failure":
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
		req.Action == "idle" || req.Action == "clear" || isOperatorAction(req.Action) {
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(req.WindowID)
		if req.Action == "clear" || isOperatorAction(req.Action) {
			st.CopilotToolPermissionState = state.CopilotToolPermissionNone
		}
		st.SubagentCount = 0
		if !isOperatorAction(req.Action) {
			st.PendingReset = state.ResetFlowNone
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
			h.appendEventLocked(req.WindowID, events.SuppressedSessionEvent(req, req.Action, suppressionReasonGuardBlocked, currentIconState))
			h.stateMu.Unlock()
		}
		return protocol.Response{OK: true, Allowed: false}, outcome
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
		st.CopilotToolPermissionState = state.CopilotToolPermissionAutoApproved
	} else if effective == "permission" && isCopilotRequest(req) &&
		isCopilotPermissionPromptNotification(req) {
		st.CopilotToolPermissionState = state.CopilotToolPermissionNone
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

	iconState := protocol.IconState(effective)
	if req.PreservesIcon {
		iconState = protocol.IconStatePreserve
	} else if isAutoApprovedPerm {
		// Clear the permission guard immediately — it was auto-resolved, so a
		// subsequent idle can fall back to lastAction ("active") and clear the bolt.
		h.guard.Clear(req.WindowID)
		iconState = protocol.IconStatePreserve
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
				iconState = protocol.IconState("stop")
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
			iconState = protocol.IconStatePreserve
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
		outcome.skipReason = protocol.DecisionReasonAutoApprovedPermission
	}

	h.stateMu.Lock()
	loggedAction := effective
	if syntheticStop {
		loggedAction = "stop"
	}
	if iconState != protocol.IconStatePreserve {
		st := h.ensureWindowStateLocked(req.WindowID)
		st.DesiredIcon = iconState
		st.StateSet = true
	}
	if isAutoApprovedPerm {
		h.appendEventLocked(req.WindowID, events.SuppressedSessionEvent(req, loggedAction, suppressionReasonAutoApprovedPerm, currentIconState))
	} else {
		h.appendEventLocked(req.WindowID, events.SessionEventFromRequest(req, loggedAction, iconState, currentIconState))
	}
	st = h.ensureWindowStateLocked(req.WindowID)
	h.linkRequestRenderGenerationLocked(req, st.Seq+1)
	h.syncRendererMetadataLocked(req.WindowID)
	// Visual changes render immediately; everything else is batched
	// persistence flushed by the next maintenance tick.
	if st := h.ensureWindowStateLocked(req.WindowID); rendering.DesiredIcon(st) != visualIconBefore || st.Looping != visualLoopingBefore {
		h.markWindowDirtyLocked(req.WindowID)
	} else {
		h.markWindowActivityDirtyLocked(req.WindowID)
	}
	h.stateMu.Unlock()

	return protocol.Response{OK: true, Allowed: true, IconState: iconState}, outcome
}

// handleLoopMarkerStateUpdate records an ASL lifecycle signal without
// disturbing the visible state machine. The marker is event/activity metadata
// only — it appends to the per-window event log, refreshes the durable export
// snapshot, and keeps the hook-silence latch armed so a profile that is only
// emitting loop markers still counts as alive. It deliberately skips the guard,
// lastAction, notifications, stale tracking, and icon resolution paths so a
// running ASL loop cannot introduce or disturb a bolt icon.
func (h *Handler) handleLoopMarkerStateUpdate(req protocol.Request) protocol.Response {
	receivedAt := time.Now()
	h.stateMu.Lock()
	// A loop marker returns before applyStateUpdate's closed-slot check, so it
	// makes its own: an ASL hook translated before its session ended must not
	// bring the closed slot's state back.
	if h.runtimeSlotClosedLocked(req.WindowID) {
		h.stateMu.Unlock()
		return protocol.Response{OK: false, Error: "runtime slot is closed"}
	}
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
	case events.AgentSessionLoopStarted, events.AgentSessionLoopTurnStarted,
		events.AgentSessionLoopTurnHeartbeat, events.AgentSessionLoopTurnHangSuspected,
		events.AgentSessionLoopTurnHangCleared, events.AgentSessionLoopTurnFinished,
		events.AgentSessionLoopVerificationStarted, events.AgentSessionLoopVerificationHeartbeat,
		events.AgentSessionLoopVerificationFinished:
		st.Looping = true
		st.LoopingSet = true
	case events.AgentSessionLoopFinished, events.AgentSessionLoopPaused:
		st.Looping = false
		st.LoopingSet = true
	}
	h.appendEventLocked(req.WindowID, events.SessionEventFromRequest(req, req.Action, protocol.IconStatePreserve, rendering.DesiredIcon(st)))
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

	return protocol.Response{OK: true, Allowed: true, IconState: protocol.IconStatePreserve}
}

const (
	suppressionReasonGuardBlocked           = protocol.DecisionReasonGuardBlocked
	suppressionReasonSubagentStillRunning   = protocol.DecisionReasonSubagentStillRunning
	suppressionReasonCopilotMidSubagent     = protocol.DecisionReasonCopilotMidSubagent
	suppressionReasonCopilotPendingPrompt   = protocol.DecisionReasonCopilotPendingPrompt
	suppressionReasonActiveAfterTerminal    = protocol.DecisionReasonActiveAfterTerminal
	suppressionReasonElicitationPending     = protocol.DecisionReasonElicitationPending
	suppressionReasonUnknownEvent           = protocol.DecisionReasonUnknownEvent
	suppressionReasonAutoApprovedPerm       = protocol.DecisionReasonAutoApprovedPermission
	suppressionReasonOwnerMismatch          = protocol.DecisionReasonOwnerMismatch
	suppressionReasonUnmanagedPipeSource    = protocol.DecisionReasonUnmanagedPipeSource
	suppressionReasonIgnoredEvent           = protocol.DecisionReasonIgnoredEvent
	suppressionReasonInvalidPayload         = protocol.DecisionReasonInvalidPayload
	suppressionReasonTerminalStopHeld       = protocol.DecisionReasonTerminalStopHeld
	suppressionReasonTerminalStopSuperseded = protocol.DecisionReasonTerminalStopSuperseded
	suppressionReasonTerminalStopBypassed   = protocol.DecisionReasonTerminalStopBypassed
)

func (h *Handler) lastKnownState(windowID int) (action, iconState string) {
	h.stateMu.Lock()
	if st, ok := h.windowStateLocked(windowID); ok {
		action = st.LastAction
		iconState = rendering.DesiredIcon(st)
	}
	h.stateMu.Unlock()
	return action, iconState
}

func shouldSuppressPendingElicitationActive(req protocol.Request, prevAction string) bool {
	if req.PreservesIcon || isCopilotRequest(req) || prevAction != "elicitation" || req.Action != "active" {
		return false
	}
	return !isElicitationResolutionEvent(req)
}

func isElicitationResolutionEvent(req protocol.Request) bool {
	if req.IsPrompt {
		return true
	}
	switch req.Event {
	case "ElicitationResult":
		return true
	case "PostToolUse", "postToolUse":
		return strings.EqualFold(strings.TrimSpace(req.ToolName), "request_user_input")
	default:
		return false
	}
}

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
			h.appendEventLocked(req.WindowID, events.SuppressedSessionEvent(req, req.Action, suppressionReasonSubagentStillRunning, currentIconState))
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
			h.appendEventLocked(req.WindowID, events.SuppressedSessionEvent(req, req.Action, suppressionReasonCopilotMidSubagent, currentIconState))
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
		st.CopilotToolPermissionState = state.CopilotToolPermissionPending
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
		h.appendEventLocked(req.WindowID, events.SessionEventFromRequest(req, req.Action, protocol.IconStatePreserve, currentIconState))
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
