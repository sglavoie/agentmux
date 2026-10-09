// Package probes is the daemon's external-evidence layer: how it looks at the
// world outside its own hook stream, and how it decides that a look is still
// valid. Two kinds of evidence live here. The OS process table answers "is the
// exact harness instance still alive?" through the ps PID/lstart
// harness-liveness inspector, its bounded batches, and its cross-platform
// absent-process fallback. The Kitty window tree answers "which panes exist,
// which are managed, and has anything probe-relevant changed since capture?"
// through the immutable snapshot cache that keeps ListWindows off the hook
// path, the derived per-tab and per-pane freshness epochs, and the
// producer/source/target probe binding with its receipt and epoch validation.
//
// A third kind is backend-neutral and applies to Kitty and tmux alike. A
// backend.Screen observation is checked by CheckScreen before any classifier
// reads it, so a capture that failed, is stale, describes another pane or
// server generation, or was left behind by a dead program is withheld instead
// of read (screen.go). Panes a backend reports through backend.Discoverer get
// their own freshness epochs in PaneStore, and PaneHarnessProcess combines a
// pane's discovery with the process table's answer (pane.go).
//
// It must never import the daemon root, and it performs no Kitty, IPC, or
// journal I/O of its own — it interprets a captured tree rather than fetching
// one. It never decides what an observation MEANS for a window: that stays in
// the root, which owns the reducer, the renderer, the notifier, and every
// harness probe adapter. Process launch and control belong to
// internal/daemon/process, not here.
package probes
