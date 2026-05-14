package daemon

// Handler processes daemon requests.
type Handler struct {
	guard    *StateGuard
	notifier *Notifier

	// onActivity is called on every successfully handled request.
	// The daemon uses this to reset the idle timer.
	onActivity func()

	// onShutdown is called when a TypeShutdown request is received.
	onShutdown func()
}

// NewHandler creates a Handler with the given guard and notifier.
func NewHandler(guard *StateGuard, notifier *Notifier) *Handler {
	return &Handler{guard: guard, notifier: notifier}
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

	allowed, effective := h.guard.TryUpdate(req.WindowID, req.Action, req.Event)
	if !allowed {
		return Response{OK: true, Allowed: false}
	}

	iconState := IconState(effective)
	if req.Event == "SubagentStop" || req.Event == "TaskCompleted" {
		iconState = IconStatePreserve
	}
	h.notifier.MaybeNotify(req.WindowID, req.Account, effective, req.PipeMode)

	return Response{OK: true, Allowed: true, IconState: iconState}
}

func (h *Handler) handleStateQuery() Response {
	return Response{OK: true}
}

func (h *Handler) handleShutdown() Response {
	if h.onShutdown != nil {
		go h.onShutdown()
	}
	return Response{OK: true}
}
