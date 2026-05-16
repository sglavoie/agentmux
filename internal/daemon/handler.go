package daemon

import (
	"log/slog"
	"sync"
	"time"
)

// Handler processes daemon requests.
type Handler struct {
	guard    *StateGuard
	notifier *Notifier

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
	case TypeShutdown:
		return h.handleShutdown()
	default:
		return Response{OK: false, Error: "unknown request type: " + string(req.Type)}
	}
}

func (h *Handler) handleStateUpdate(req Request) Response {
	if req.Action == "" {
		return Response{OK: false, Error: "action is required for state_update"}
	}
	if _, ok := actionPriority[req.Action]; !ok {
		return Response{OK: false, Error: "unknown action: " + req.Action}
	}

	// Any non-permission follow-up event makes a pending permission
	// notification stale, even if the state guard later blocks the transition.
	if req.Action != "permission" {
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
	// expires, lastAction is the only remaining barrier.
	if effective == "active" && !req.IsPrompt {
		h.stateMu.Lock()
		prev := h.lastAction[req.WindowID]
		h.stateMu.Unlock()
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
		// lastAction for the common case where the guard already expired but
		// the bolt icon is still showing in the title.
		prev := prevGuard
		if prev == "" {
			h.stateMu.Lock()
			prev = h.lastAction[req.WindowID]
			h.stateMu.Unlock()
		}
		switch IconState(prev) {
		case "bolt", "compact", "subagent":
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
