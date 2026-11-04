package daemon

import (
	"sync"
)

// This file models the daemon's explicit startup readiness. Runtime sessions
// reconcile and restore before the public socket binds, so the first client
// request can read restored state.

// readinessState records whether the daemon reached a fully ready state and, if
// not, the reason it started degraded. It is safe for concurrent use.
type readinessState struct {
	mu       sync.Mutex
	ready    bool
	degraded string
}

// markReady records that the bounded initial snapshot+restore completed. It
// clears any prior degraded reason.
func (r *readinessState) markReady() {
	r.mu.Lock()
	r.ready = true
	r.degraded = ""
	r.mu.Unlock()
}

// markDegraded records that the daemon started (or is running) degraded, with a
// human-readable reason. It leaves ready false.
func (r *readinessState) markDegraded(reason string) {
	r.mu.Lock()
	r.ready = false
	r.degraded = reason
	r.mu.Unlock()
}

// snapshot returns the current readiness and degraded reason.
func (r *readinessState) snapshot() (ready bool, degraded string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready, r.degraded
}

// MarkReady records that the daemon completed startup restore. Exported for
// daemon.Run.
func (h *Handler) MarkReady() {
	h.readiness.markReady()
	h.companionDirty()
}

// MarkDegraded records that the daemon started degraded with the given reason.
// Exported for daemon.Run.
func (h *Handler) MarkDegraded(reason string) {
	h.readiness.markDegraded(reason)
	h.companionDirty()
}

// Readiness reports whether the daemon is fully ready and, when not, why it is
// degraded.
func (h *Handler) Readiness() (ready bool, degraded string) { return h.readiness.snapshot() }
