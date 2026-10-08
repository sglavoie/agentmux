// Package asl is what the daemon knows about an agent-session-loop run, and
// how it reads it.
//
// Two directions of evidence meet here. Outbound, the optional bridge
// subprocess answers snapshot, question, action, and workbench requests, and
// this package owns the seam, its capability block, the four request handlers,
// the refusals that carry them, and the client-side decoding of their
// replies. Inbound, ASL publishes durable per-run state that the daemon reads:
// the projection phase a discovered run is in, the supervisor mailbox markers
// that say a human is being waited on, and the closure markers that say they
// are not. This package owns that reading — the parsing, the per-run monotonic
// generation/sequence position that decides whether a read is stale, the
// per-run supervision bookkeeping, the lease-evidence rule that keeps one
// unreadable plan from retiring a healthy run's lease, and the reconciliation
// and hook-delivery counters that make a silent pairing legible.
//
// It does not own what an ASL observation DOES to a window. Seeding a lease,
// retiring one, raising the question icon, and notifying about it are reducer
// mutations under the daemon's stateMu, and they stay in the daemon root with
// the reducer, the renderer, and the notifier. This package must never import
// the daemon root, and it performs no Kitty, IPC, or journal I/O.
//
// Generic hook decoding stays in internal/daemon/events: an ASL lifecycle
// event is normalized there alongside every other hook, and transport dispatch
// stays with the daemon's IPC surface. This package supplies the ASL-specific
// vocabulary and decisions that both of those reach for.
package asl
