package daemon

import (
	"log/slog"
	"time"
)

func (h *Handler) lastKnownAction(windowID int, currentIconState string) string {
	h.stateMu.Lock()
	last := h.lastAction[windowID]
	h.stateMu.Unlock()
	if last != "" {
		return last
	}
	return actionFromIconState(currentIconState)
}

func actionFromIconState(iconState string) string {
	switch iconState {
	case "bolt":
		return "active"
	case "compact":
		return "compact"
	case "subagent":
		return "subagent"
	case "stop":
		return "stop"
	case "key":
		return "permission"
	case "error":
		// The "error" glyph is shared by failure and stop_failure. Without a
		// daemon-side lastAction record (e.g. fresh daemon restart), we cannot
		// tell them apart from the title alone, so prefer the terminal
		// stop_failure: clearing a real stop_failure on a benign follow-up is
		// worse than over-preserving a transient failure.
		return "stop_failure"
	case "question":
		return "elicitation"
	default:
		return ""
	}
}

// appendEventLocked records an event into the per-window ring buffer. The
// caller must hold stateMu. Older entries are dropped from the front once the
// slice would exceed maxEventsPerWindow.
func (h *Handler) appendEventLocked(req Request, action string, suppressed bool) {
	buf := h.eventLog[req.WindowID]
	buf = append(buf, SessionEvent{
		At:            time.Now().Unix(),
		Event:         req.Event,
		Action:        action,
		ToolName:      req.ToolName,
		Suppressed:    suppressed,
		PreservesIcon: req.PreservesIcon,
	})
	if len(buf) > maxEventsPerWindow {
		buf = buf[len(buf)-maxEventsPerWindow:]
	}
	h.eventLog[req.WindowID] = buf
}

func (h *Handler) handleStateUpdate(req Request) Response {
	if req.Action == "" {
		return Response{OK: false, Error: "action is required for state_update"}
	}
	if _, ok := actionPriority[req.Action]; !ok {
		return Response{OK: false, Error: "unknown action: " + req.Action}
	}

	slog.Debug("hook event received",
		"window_id", req.WindowID,
		"account", req.Account,
		"event", req.Event,
		"action", req.Action,
		"preserves_icon", req.PreservesIcon,
	)

	// Only terminal/idle transitions invalidate a pending permission notification.
	// PostToolUse/active/subagent events are normal companions to a real
	// permission_prompt and must not cancel the notification.
	// elicitation and failure are intentionally omitted: they are mid-session
	// states, not terminal, so they don't *invalidate* a permission here. When
	// either of them does fire a notification of its own, the notify layer's
	// cancelPendingLocked supersedes the pending entry (see notify.go MaybeNotify).
	switch req.Action {
	case "idle", "clear", "user_clear", "stop", "stop_failure":
		h.notifier.CancelPending(req.WindowID)
	}

	// SubagentStart: increment reference count so the robot icon persists
	// even when interleaved parent "active" events arrive.
	if req.Action == "subagent" {
		h.stateMu.Lock()
		h.subagentCount[req.WindowID]++
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
		h.stateMu.Unlock()

		if wasTracked && remaining > 0 && req.Action == "active" {
			// More agents still running — refresh the subagent guard (reset expiry)
			// and suppress this "active" transition. Only "active" is suppressed:
			// a SubagentStop carrying a real failure (action == "stop_failure",
			// e.g. stopReason = "error" / "max_tokens") must surface so the user
			// sees the failure even when sibling agents are still running.
			h.guard.TryUpdate(req.WindowID, "subagent", false)
			h.stateMu.Lock()
			h.appendEventLocked(req, req.Action, true)
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
				h.notifier.CancelPending(req.WindowID)
			}
			slog.Debug("copilot mid-subagent suppression",
				"window_id", req.WindowID,
				"event", req.Event,
				"action", req.Action,
				"subagent_count", count,
			)
			h.stateMu.Lock()
			h.appendEventLocked(req, req.Action, true)
			h.stateMu.Unlock()
			return Response{OK: true, Allowed: false}
		}
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
		delete(h.subagentCount, req.WindowID)
		delete(h.notifiedStale, req.WindowID)
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
		prev := h.lastKnownAction(req.WindowID, req.CurrentIconState)
		if prev == "stop" || prev == "stop_failure" {
			h.notifier.MaybeNotify(req.WindowID, req.SessionTitle, req.Action, req.PipeMode)
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
			preservedAction = h.lastKnownAction(req.WindowID, req.CurrentIconState)
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
		h.notifier.MaybeNotify(req.WindowID, req.SessionTitle, req.Action, req.PipeMode)
		h.stateMu.Lock()
		h.appendEventLocked(req, req.Action, true)
		h.stateMu.Unlock()
		return Response{OK: true, Allowed: false}
	}

	// Auto-approved permission: preToolUse fired before permissionRequest, so the
	// tool is already running. The key icon should not override the active bolt —
	// there is nothing for the user to approve.
	// Only treat it as auto-approved when the active guard was specifically set by
	// a PreToolUse hook event (not by PostToolUse of a prior tool).
	h.stateMu.Lock()
	prevEvent := h.lastActiveEvent[req.WindowID]
	h.stateMu.Unlock()
	isAutoApprovedPerm := effective == "permission" &&
		prevGuard == "active" &&
		(prevEvent == "PreToolUse" || prevEvent == "preToolUse")

	// Copilot's preToolUse → permissionRequest sequence is speculative: it
	// fires even for tools that auto-approve, producing a spurious "Permission
	// requested" notification when no user input is required. Claude's
	// PreToolUse → permission sequence is a *real* user-facing prompt and its
	// notification must still fire (TestHandler_PermissionSurvivesActiveFollowUp).
	// Event-name casing is the harness signal here: Claude declares PascalCase
	// hooks, Copilot declares camelCase hooks, and req.Event preserves the
	// original casing.
	isCopilotAutoApprovedPerm := isAutoApprovedPerm && prevEvent == "preToolUse"

	h.stateMu.Lock()
	now := time.Now()
	h.activity[req.WindowID] = now
	h.lastSessionTitle[req.WindowID] = req.SessionTitle
	h.lastPipeMode[req.WindowID] = req.PipeMode
	if req.Account != "" {
		h.lastAccount[req.WindowID] = req.Account
		// Per-account event tracking for the hook-silence watchdog. Re-arms
		// the one-shot latch so a future silence stretch can notify again.
		h.lastSeenEventAt[req.Account] = now
		delete(h.notifiedHookSilence, req.Account)
	}
	if effective == "active" {
		h.lastActiveEvent[req.WindowID] = req.Event
	}
	// Record the last substantive action so idle/clear can detect a stale bolt
	// even after the state guard has expired. Preserve-current events keep the
	// prior state semantics instead of rewriting them to a synthesized "active".
	recordedAction := effective
	if req.PreservesIcon {
		recordedAction = preservedAction
	}
	if recordedAction != "" && recordedAction != "idle" && recordedAction != "clear" && recordedAction != "user_clear" && !isAutoApprovedPerm {
		h.lastAction[req.WindowID] = recordedAction
		delete(h.notifiedStale, req.WindowID)
	}
	// Append this transition into the per-window ring buffer. Terminal actions
	// are recorded normally so the terminal hook itself is the last visible row
	// in the detail pane.
	h.appendEventLocked(req, effective, false)
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
		// Prefer prevGuard (still within the guard window); fall back to
		// lastAction, then the currently displayed title icon, for the common case
		// where the guard already expired or the daemon restarted while the bolt
		// icon is still showing in the title.
		prev := prevGuard
		if prev == "" {
			prev = h.lastKnownAction(req.WindowID, req.CurrentIconState)
		}
		switch prev {
		case "active", "compact", "subagent":
			// Agent finished — clear the active indicator and reset tracking.
			iconState = ""
			h.stateMu.Lock()
			delete(h.lastAction, req.WindowID)
			delete(h.lastActiveEvent, req.WindowID)
			h.stateMu.Unlock()
		default:
			// stop/permission/failure icons persist until the user notices them.
			iconState = IconStatePreserve
		}
	} else if effective == "user_clear" {
		// TUI-initiated wipe — reset lastAction so a future idle doesn't try
		// to clear an icon that no longer exists.
		h.stateMu.Lock()
		delete(h.lastAction, req.WindowID)
		delete(h.lastActiveEvent, req.WindowID)
		h.stateMu.Unlock()
	}
	if isCopilotAutoApprovedPerm {
		h.notifier.CancelPending(req.WindowID)
	} else {
		h.notifier.MaybeNotify(req.WindowID, req.SessionTitle, effective, req.PipeMode)
	}

	return Response{OK: true, Allowed: true, IconState: iconState}
}
