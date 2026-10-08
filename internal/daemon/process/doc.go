// Package process owns the daemon's own life as an operating-system process:
// how a new daemon is launched (`amx daemon start` in a fresh session with the
// platform detach attributes), how it is cut loose from the terminal that
// launched it (stdio redirected into the private bootstrap log, and the
// AMX_DAEMON_DETACHED / AMX_DAEMON_LAUNCH_ID / AMX_DAEMON_BOOTSTRAP_LOG launch
// handshake the child reads back), how exactly one daemon is allowed to own a
// socket (the flock single-instance lock and the client-side auto-start lock),
// and when a running daemon has been superseded by a newer amx binary.
//
// It is the counterpart of internal/daemon/probes: probes observes processes
// the daemon did not start, and this package starts and owns the one it did.
// Nothing here inspects the process table and nothing here holds an IPC
// transport or speaks the IPC protocol — the retry/deadline path around a
// spawn stays with the client, and reconciling an observed harness exit into
// window state stays in the daemon root with the reducer. It must never
// import the daemon root.
package process
