// Package cli is the shared bootstrap every amx command family runs before it
// does its own job.
//
// It owns four things and deliberately nothing else:
//
//   - how a command failure becomes a process exit status (exit.go);
//   - how a command answers a machine caller through the common schema-v1
//     envelope (json.go), and a person through width-aware tables that keep
//     their tab-separated form off a terminal (table.go);
//   - how a command assembles the runtime it acts on — the validated
//     configuration (config.go), the Kitty client and the window the CLI was
//     invoked from (kitty.go), the daemon connection (daemon.go), the harness
//     transcript readers it discovers sources with (transcripts.go), and the
//     context its work runs under (context.go);
//   - how the process coordinates with itself — the debug verbosity policy and
//     the logger it installs (logging.go), and locating the running amx binary
//     that hook commands write and daemon start/restart re-execs (binary.go).
//
// Command-specific flag parsing, command construction, and command-specific
// output stay in the command packages. This package is not a helpers bag: an
// identifier belongs here only when more than one command family needs it, or
// when it is the vocabulary that makes such a contract nameable.
//
// It must never import a cmd package.
package cli
