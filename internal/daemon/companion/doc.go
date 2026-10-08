// Package companion decides what a companion — the native menu bar app, or
// any other long-lived subscriber — is shown. Project is one pure function
// from values the caller already copied (the per-window state the state query
// publishes, the per-window Kitty sockets, the cached window tree and the
// socket it was read through, the notifier's posture, and the ASL supervisor
// targets and pending questions) to the versioned protocol.CompanionSnapshot,
// and Digest is that snapshot's content identity.
//
// It reads no clock of its own, performs no Kitty, IPC, filesystem, or journal
// I/O, and must never import the daemon root, so the hub can build a snapshot
// outside stateMu from a copy taken under it; the package's own tests assert
// its import list.
//
// Three rules are the package rather than details of it. A session is read
// from the Kitty window tree first and from daemon state second, and a daemon
// state is joined to a tree window only when both are provably about the same
// Kitty instance — window IDs mean something only inside the instance that
// issued them. A state recorded against another instance is published as its
// own tree-less session rather than decorating a window that merely shares its
// number; when the daemon also read that other instance's tree (the
// closed-window GC lists each instance a stored window belongs to), the
// session takes its tab and title from it. The attention bit is
// protocol.AttentionStates over the daemon's icon (falling back to the
// title's) or Kitty's own flag, the same set `amx focus-next` cycles on, so
// the menu bar and the CLI cannot disagree about which session is waiting.
// And the snapshot carries timestamps, never ages, so projecting an unchanged
// world twice yields byte-identical JSON and Digest can tell the hub there is
// nothing to publish.
//
// Truncation at protocol.CompanionSessionCap, and a measured byte budget
// under the 1 MiB frame cap, drop idle and working rows before any row a human
// is waited on. ASL waiting entries name a run and its latest question and
// never carry a question body or an action token.
package companion
