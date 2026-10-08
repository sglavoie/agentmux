// Package eventlog is the daemon's record of what happened: the bounded
// per-window ring of accepted, suppressed, and daemon-synthesized rows; the
// global monotonic receipt cursor that totally orders them and the
// reference/request lookups that read a row back by it; the durable
// per-window counters and the deep-copied read projections served to session
// detail and event export; the render-completion and probe-trace correlation
// stamped back onto the rows that caused them; and the daemon.decision journal
// record — with its replay.* and route.* evidence — that outlives the
// in-memory ring and that internal/debugger decodes offline.
//
// The boundary is "the row", not "the decision". Deciding what an event does
// to a window stays in the daemon root with the reducer, the renderer, and the
// notifier, and every mutation of theirs under stateMu stays there too. This
// package only records, correlates, projects, and persists rows, so every
// function here is either a pure projection or a caller-locked mutation of a
// window's ring. It must never import the daemon root, and it performs no
// Kitty, IPC, or journal I/O of its own: it builds the record the root
// appends.
//
// It deliberately does not absorb the diagnostic tail, cursor, and replay
// services in internal/debugger: that surface reads immutable diagnostics
// artifacts rather than daemon event rows, it must keep working with no daemon
// running, and the intended import graph gives internal/debugger no eventlog
// edge. What this package owns of "replay" is the producer half — the replay
// evidence stamped onto the durable journal record that internal/debugger
// decodes — so the persisted format and its replay semantics stay pinned on
// the side that writes them.
package eventlog
