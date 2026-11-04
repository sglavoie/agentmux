// Package companion projects daemon state into snapshots for long-lived
// subscribers such as the native menu bar app. Project reads a caller-owned
// copy of reducer state, tmux runtime bindings, notification posture, and ASL
// supervisor questions. Digest identifies the resulting content.
//
// The projection performs no I/O and reads no clock of its own. The daemon
// copies its inputs under stateMu, then builds the snapshot outside the lock.
// Each session is addressed by runtime session ID; state is joined to a slot
// only when that slot has a tmux binding. Attention follows
// protocol.AttentionStates, and timestamps rather than ages keep an unchanged
// world's digest stable across repeated builds.
//
// The session count and encoded-byte budgets retain attention rows before
// working or idle rows. ASL waiting entries name a run and its latest question
// without carrying the question body or an action token.
package companion
