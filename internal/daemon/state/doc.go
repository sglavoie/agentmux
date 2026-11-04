// Package state is the daemon's side-effect-free fact reducer and the durable
// per-window state: the source facts, the effective-state reduction,
// retention, and the checkpoint payload schema with its version migrations.
//
// It must never import the daemon root, and it performs no terminal, IPC, or
// notification I/O of its own.
package state
