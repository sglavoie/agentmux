// Package cli is the shared bootstrap every amx command family runs before it
// does its own job.
//
// It owns these things and deliberately nothing else:
//
//   - how a command failure becomes a process exit status — the ExitError
//     quiet-exit contract and the nonzero status vocabulary (exit.go);
//   - how a command answers a machine caller through the common schema-v1
//     envelope, the structured error payload, and its candidate and parameter
//     help shapes (json.go) and the text form of a typed action failure
//     (action_error.go), and a person through the display-width aligned
//     table and the terminal check that keeps plain output tab-separated for
//     pipes (table.go), with the shared wording of counts and byte sizes
//     (plural.go, bytes.go);
//   - how a command assembles the runtime it acts on — the validated
//     configuration (config.go), the Kitty client and the window the CLI was
//     invoked from (kitty.go), the private tmux backend and the --backend
//     flag a launch entrypoint takes (backend.go), the daemon export-client
//     seam (daemon.go), the harness transcript readers it discovers sources
//     with (transcripts.go), and the context its work runs under (context.go);
//   - the backend report `amx backend status` prints and doctor, debug
//     status, and the desktop app read, which the CLI assembles itself so it
//     is there with the daemon down (backend_status.go);
//   - shell completion of the values more than one family takes: profiles,
//     harnesses, and running runtime sessions, read from configuration and
//     the registry file alone (completion.go, completion_runtime.go);
//   - how the process coordinates with itself — the --debug/AMX_DEBUG
//     verbosity policy and the logger it installs (logging.go), and locating
//     the running amx binary that hook commands write and daemon start/restart
//     re-execs (binary.go).
//
// Command-specific flag parsing, command construction, and command-specific
// output stay in the command packages. This package is not a helpers bag: an
// identifier belongs here only when more than one command family needs it, or
// when it is the vocabulary that makes such a contract nameable.
//
// It must never import a cmd package, and it reaches the daemon only through
// internal/daemon/ipc, naming the request and response shapes from
// internal/daemon/protocol: the transport, auto-start, and detached-spawn
// policies themselves stay in internal/daemon/ipc and internal/daemon/process.
package cli
