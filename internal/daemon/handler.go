package daemon

import (
	"log/slog"
	"os"
	"sync"
	"time"
)

// Handler processes daemon requests.
type Handler struct {
	guard    *StateGuard
	notifier *Notifier

	// startedAt records the daemon start time so health queries can report
	// uptime. Zero value means uptime should be reported as 0.
	startedAt time.Time

	// stateMu protects activity and lastAction.
	stateMu  sync.Mutex
	activity map[int]time.Time

	// lastAction records the most recent non-idle/clear action per window.
	// It outlives the state guard so that an idle event arriving after the
	// guard expires can still detect a stale bolt and clear it.
	lastAction map[int]string

	// lastActiveEvent records the hook event name that last set the "active"
	// state, so isAutoApprovedPerm can distinguish PreToolUse (same-tool
	// auto-approval) from PostToolUse (prior tool completed).
	lastActiveEvent map[int]string

	// subagentCount tracks the number of active sub-agents per window.
	// It is incremented on SubagentStart and decremented on SubagentStop.
	// While the count is > 0, SubagentStop events are suppressed so the
	// robot icon persists until all concurrent agents finish.
	subagentCount map[int]int

	// onActivity is called on every successfully handled request.
	// The daemon uses this to reset the idle timer.
	onActivity func()

	// onShutdown is called when a TypeShutdown request is received.
	onShutdown func()
}

// NewHandler creates a Handler with the given guard and notifier.
func NewHandler(guard *StateGuard, notifier *Notifier) *Handler {
	return &Handler{
		guard:           guard,
		notifier:        notifier,
		startedAt:       time.Now(),
		activity:        make(map[int]time.Time),
		lastAction:      make(map[int]string),
		lastActiveEvent: make(map[int]string),
		subagentCount:   make(map[int]int),
	}
}

// Handle processes a Request and returns a Response.
func (h *Handler) Handle(req Request) Response {
	if h.onActivity != nil {
		h.onActivity()
	}

	switch req.Type {
	case TypeStateUpdate:
		return h.handleStateUpdate(req)
	case TypeStateQuery:
		return h.handleStateQuery()
	case TypeHealth:
		return h.handleHealth()
	case TypeShutdown:
		return h.handleShutdown()
	default:
		return Response{OK: false, Error: "unknown request type: " + string(req.Type)}
	}
}

func (h *Handler) handleHealth() Response {
	var uptime int64
	if !h.startedAt.IsZero() {
		uptime = int64(time.Since(h.startedAt).Seconds())
	}
	return Response{OK: true, PID: os.Getpid(), UptimeSeconds: uptime}
}

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
		return "failure"
	case "question":
		return "elicitation"
	default:
		return ""
	}
}

func (h *Handler) handleStateUpdate(req Request) Response {
	if req.Action == "" {
		return Response{OK: false, Error: "action is required for state_update"}
	}
	if _, ok := actionPriority[req.Action]; !ok {
		return Response{OK: false, Error: "unknown action: " + req.Action}
	}

	// Only terminal/idle transitions invalidate a pending permission notification.
	// PostToolUse/active/subagent events are normal companions to a real
	// permission_prompt and must not cancel the notification.
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
		h.stateMu.Unlock()

		if wasTracked && remaining > 0 {
			// More agents still running — refresh the subagent guard (reset expiry)
			// and suppress this "active" transition.
			h.guard.TryUpdate(req.WindowID, "subagent", false)
			return Response{OK: true, Allowed: false}
		}
		if wasTracked && remaining == 0 {
			// Last agent finished — clear guard so the "active" below is allowed
			// immediately rather than waiting for the guard TTL to expire.
			h.guard.Clear(req.WindowID)
		}
	}

	// Terminal / cleanup states reset the subagent counter.
	if req.Action == "stop" || req.Action == "stop_failure" || req.Action == "failure" ||
		req.Action == "idle" || req.Action == "clear" || req.Action == "user_clear" {
		h.stateMu.Lock()
		delete(h.subagentCount, req.WindowID)
		h.stateMu.Unlock()
	}

	prevGuard := h.guard.Current(req.WindowID)
	allowed, effective := h.guard.TryUpdate(req.WindowID, req.Action, req.IsPrompt)
	slog.Debug("state transition",
		"window_id", req.WindowID,
		"profile", req.Account,
		"action", req.Action,
		"allowed", allowed,
	)
	if !allowed {
		return Response{OK: true, Allowed: false}
	}

	// Block non-prompt active events from overwriting a terminal stop state.
	// PostToolBatch can race with Stop in async execution; once the stop guard
	// expires, lastAction is the main barrier, with the current title icon as a
	// restart-safe fallback after daemon auto-starts or restarts.
	if effective == "active" && !req.IsPrompt {
		prev := h.lastKnownAction(req.WindowID, req.CurrentIconState)
		if prev == "stop" || prev == "stop_failure" {
			return Response{OK: true, Allowed: false}
		}
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

	h.stateMu.Lock()
	h.activity[req.WindowID] = time.Now()
	if effective == "active" {
		h.lastActiveEvent[req.WindowID] = req.Event
	}
	// Record the last substantive action so idle/clear can detect a stale bolt
	// even after the state guard has expired.
	if effective != "idle" && effective != "clear" && effective != "user_clear" && !isAutoApprovedPerm {
		h.lastAction[req.WindowID] = effective
	}
	h.stateMu.Unlock()

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
	h.notifier.MaybeNotify(req.WindowID, req.SessionTitle, effective, req.PipeMode)

	return Response{OK: true, Allowed: true, IconState: iconState}
}

func (h *Handler) handleStateQuery() Response {
	h.stateMu.Lock()
	snapshot := make(map[int]int64, len(h.activity))
	for id, t := range h.activity {
		snapshot[id] = t.Unix()
	}
	h.stateMu.Unlock()
	return Response{OK: true, Activity: snapshot}
}

func (h *Handler) handleShutdown() Response {
	if h.onShutdown != nil {
		go h.onShutdown()
	}
	return Response{OK: true}
}
