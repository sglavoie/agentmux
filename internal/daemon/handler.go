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

	// onActivity is called on every successfully handled request.
	// The daemon uses this to reset the idle timer.
	onActivity func()

	// onShutdown is called when a TypeShutdown request is received.
	onShutdown func()
}

// NewHandler creates a Handler with the given guard and notifier.
func NewHandler(guard *StateGuard, notifier *Notifier) *Handler {
	return &Handler{
		guard:      guard,
		notifier:   notifier,
		activity:   make(map[int]time.Time),
		lastAction: make(map[int]string),
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

	// Cancel any pending debounced permission notification when the session
	// moves past the permission state (auto-approved or session ended).
	if prevGuard == "permission" && effective != "permission" {
		h.notifier.CancelPending(req.WindowID)
	}

	// Auto-approved permission: preToolUse fired before permissionRequest, so the
	// tool is already running. The key icon should not override the active bolt —
	// there is nothing for the user to approve.
	isAutoApprovedPerm := effective == "permission" && prevGuard == "active"

	h.stateMu.Lock()
	h.activity[req.WindowID] = time.Now()
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
