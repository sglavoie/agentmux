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

	// activity tracks the unix-time of the last successful state update per
	// Kitty window. The dashboard reads this via TypeStateQuery to decide
	// whether a session is "Active" (recent) or "Idle" (over threshold).
	activityMu sync.Mutex
	activity   map[int]time.Time

	// onActivity is called on every successfully handled request.
	// The daemon uses this to reset the idle timer.
	onActivity func()

	// onShutdown is called when a TypeShutdown request is received.
	onShutdown func()
}

// NewHandler creates a Handler with the given guard and notifier.
func NewHandler(guard *StateGuard, notifier *Notifier) *Handler {
	return &Handler{
		guard:    guard,
		notifier: notifier,
		activity: make(map[int]time.Time),
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

	h.activityMu.Lock()
	h.activity[req.WindowID] = time.Now()
	h.activityMu.Unlock()

	iconState := IconState(effective)
	// Event-driven idle/clear must NOT wipe the title — a fresh permission/stop
	// icon should persist until the next real action (active/stop/permission/
	// failure) overwrites it. user_clear (TUI-initiated) is the only path that
	// actually empties the icon.
	if req.PreservesIcon || effective == "idle" || effective == "clear" {
		iconState = IconStatePreserve
	}
	h.notifier.MaybeNotify(req.WindowID, req.SessionTitle, effective, req.PipeMode)

	return Response{OK: true, Allowed: true, IconState: iconState}
}

func (h *Handler) handleStateQuery() Response {
	h.activityMu.Lock()
	snapshot := make(map[int]int64, len(h.activity))
	for id, t := range h.activity {
		snapshot[id] = t.Unix()
	}
	h.activityMu.Unlock()
	return Response{OK: true, Activity: snapshot}
}

func (h *Handler) handleShutdown() Response {
	if h.onShutdown != nil {
		go h.onShutdown()
	}
	return Response{OK: true}
}
