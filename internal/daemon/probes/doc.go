// Package probes validates external evidence before the daemon applies it to
// reducer state.
//
// The process inspector checks the exact harness PID and start token. Screen
// observations identify a tmux pane and server generation, capture time, size,
// and exit state; CheckScreen withholds failed, stale, mismatched, or exited
// observations before a classifier reads their text. PaneStore advances a
// freshness epoch when probe-relevant discovery facts change.
//
// This package interprets observations supplied by the daemon. It does no
// backend, IPC, or journal I/O and does not decide which reducer fact to set.
package probes
