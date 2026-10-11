package daemon

import (
	"sync"
)

// This file models the daemon's explicit startup readiness. The daemon takes
// one bounded initial Kitty snapshot and restores durable state BEFORE binding
// the public socket, so the first client request already sees restored state.
// If that bounded snapshot times out (a slow or wedged Kitty), the daemon does
// not block startup: it comes up degraded, serves immediately, and the
// maintenance loop reconciles on its next successful tick. Readiness makes that
// distinction observable instead of hiding it behind a log line.
//
// A daemon with no Kitty socket at all is a third case and is not degraded:
// see MarkReadyWithoutKitty.

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

// MarkReady records that the daemon completed its bounded initial
// snapshot+restore. Exported for daemon.Run.
func (h *Handler) MarkReady() {
	h.readiness.markReady()
	h.companionDirty()
}

// MarkReadyWithoutKitty records that the daemon started with no Kitty to read:
// no socket in its environment, so nothing to restore from and nothing that a
// later tick could find wedged. That is how a daemon restarted from a terminal
// outside Kitty looks, and it is ready for the sessions it does serve. The
// durable restore stays owed to the first Kitty snapshot the maintenance loop
// takes once a hook has brought a socket.
func (h *Handler) MarkReadyWithoutKitty() {
	h.kittyRestorePending.Store(true)
	h.MarkReady()
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
