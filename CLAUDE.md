# Agent Mux coding guide

Agent Mux manages Claude Code, Copilot, and Codex sessions on a private tmux
server. The `amx` CLI and Bubble Tea dashboard work in any terminal. The macOS
app embeds Ghostty to show the same tmux sessions.

## Build and test

- `just build` builds `amx`.
- `just test` runs the Go suite, including the `tuitest` build.
- `just acceptance` runs format checks, tests, race checks, vet, and TUI scenarios.
- `just check` adds lint.
- `just mac-test` runs the hosted Swift tests; it is outside the Go gate.
- `just cockpit-smoke` checks an installed CLI, daemon, and private tmux server.

Use a temporary HOME and private tmux socket in tests. Never connect tests to
the operator's running server or configuration. Run focused tests while editing,
then the applicable gates before shipping.

## Architecture

- `cmd/` assembles Cobra commands and reports CLI errors.
- `internal/config` loads and validates `config.toml`. New launches use tmux.
- `internal/tmux` owns the private server, session topology, attach, capture,
  and terminal switching. It does not read the operator's `~/.tmux.conf`.
- `internal/runtimesession` stores durable launch and ownership records.
- `internal/session` and `internal/actions` implement lifecycle and domain
  actions through the terminal backend seam.
- `internal/daemon` reduces hook events, reconciles runtime sessions, tracks
  attention, and publishes companion state over a local Unix socket.
- `tui/` is the dashboard and terminal views. `macos/` is the native workspace
  and embedded Ghostty terminal.

For current operator behavior, use `README.md`, `docs/usage.md`, and
`docs/operations.md`. Keep help text, tests, and both terminal surfaces aligned
when changing a command or event contract.
