package protocol

// HookOrigin is the daemon-validated identity of the runtime that produced one
// hook delivery. The daemon stamps it after translating the request — a
// managed hook resolves its runtime session through the registry, and a legacy
// Kitty hook is translated from its window ID and listen socket — so the
// reducer slot the event lands in is a daemon decision, never client input.
//
// Slot is the per-window reducer key the event was routed to. For a Kitty
// target it is the Kitty window ID, which keeps every existing render and
// persistence path unchanged; any other backend target receives a
// daemon-allocated slot that is never a Kitty identifier and is never sent to
// Kitty.
type HookOrigin struct {
	// RuntimeSessionID names the AgentMux runtime session that owns the slot.
	// It is empty for a legacy hook routed to a slot no runtime session has
	// claimed.
	RuntimeSessionID string `json:"runtime_session_id,omitempty"`
	// Backend, Instance, and Generation identify the backend server; Pane is
	// the backend's own pane reference inside it. Generation is empty for a
	// legacy Kitty hook, which carries none.
	Backend    string `json:"backend"`
	Instance   string `json:"instance,omitempty"`
	Generation string `json:"generation,omitempty"`
	Pane       string `json:"pane"`
	Slot       int    `json:"slot"`
	// Legacy is true when the request carried no runtime-session identity and
	// was translated from Kitty's window environment instead.
	Legacy bool `json:"legacy,omitempty"`
}

// Hook-origin translation reasons. A rejected delivery carries one of them as
// its acknowledgement reason; the retryable ones are conditions a hook client
// may outlive by retrying, the rest are permanent for that delivery.
const (
	// OriginReasonInvalidRuntimeSession: the runtime-session ID is malformed.
	OriginReasonInvalidRuntimeSession = "invalid_runtime_session"
	// OriginReasonRuntimeSessionUnknown: the registry has no such session.
	OriginReasonRuntimeSessionUnknown = "runtime_session_unknown"
	// OriginReasonRuntimeSessionPending: the launch has not committed its
	// target yet. Retryable.
	OriginReasonRuntimeSessionPending = "runtime_session_pending"
	// OriginReasonRuntimeSessionEnded: the session is stale, ended, or
	// abandoned; its late hooks may not affect any live slot.
	OriginReasonRuntimeSessionEnded = "runtime_session_ended"
	// OriginReasonRuntimeRegistryUnavailable: the daemon cannot read the
	// registry right now. Retryable.
	OriginReasonRuntimeRegistryUnavailable = "runtime_registry_unavailable"
	// OriginReasonInvalidRuntimeTarget: the registered target cannot be
	// translated (for example a Kitty target whose reference is not a window).
	OriginReasonInvalidRuntimeTarget = "invalid_runtime_target"
	// OriginReasonTargetConflict: the slot the delivery would route to is held
	// by a different live runtime session or backend instance. Failing closed
	// is what keeps one session's hooks out of another's state.
	OriginReasonTargetConflict = "runtime_target_conflict"
)

// RetryableOriginReason reports whether a translation rejection is transient.
func RetryableOriginReason(reason string) bool {
	switch reason {
	case OriginReasonRuntimeSessionPending, OriginReasonRuntimeRegistryUnavailable:
		return true
	default:
		return false
	}
}
