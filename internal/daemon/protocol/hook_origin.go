package protocol

// HookOrigin is the daemon-validated identity of the runtime that produced one
// hook delivery. The daemon resolves its runtime session through the registry,
// so the reducer slot the event lands in is a daemon decision.
//
// Slot is the daemon allocated reducer key for the registered target.
type HookOrigin struct {
	// RuntimeSessionID names the AgentMux runtime session that owns the slot.
	// Isolated reducer tests may leave it empty.
	RuntimeSessionID string `json:"runtime_session_id,omitempty"`
	// Backend, Instance, and Generation identify the backend server; Pane is
	// the backend's own pane reference inside it.
	Backend    string `json:"backend"`
	Instance   string `json:"instance,omitempty"`
	Generation string `json:"generation,omitempty"`
	Pane       string `json:"pane"`
	Slot       int    `json:"slot"`
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
	// translated into a valid pane reference.
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
