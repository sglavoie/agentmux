# Agent Mux coding instructions

Agent Mux is a Go CLI and Bubble Tea dashboard for Claude Code, Copilot, and
Codex sessions on a private tmux server. The macOS app embeds Ghostty for its
terminal views. New agent launches use tmux only.

## Checks

- `just build`: build `amx`.
- `just test`: Go tests, including the TUI scenario build.
- `just acceptance`: format, tests, race checks, vet, and scenarios.
- `just check`: acceptance and lint.
- `just mac-test`: hosted Swift tests, outside the Go gate.

Use `go test ./path/to/package -run '^TestName$'` for focused work. Integration
tests must use a temporary HOME and private tmux socket.

## Code map

- `cmd/`: Cobra command surfaces and error envelopes.
- `internal/config`: config loading, validation, profiles, and projects.
- `internal/harness`: agent CLI invocation and hook contracts.
- `internal/tmux`: private server, session discovery, capture, attach, and input.
- `internal/runtimesession`: durable runtime identity and ownership.
- `internal/session`, `internal/actions`: lifecycle and domain actions.
- `internal/daemon`: hook reduction, runtime reconciliation, attention, and IPC.
- `tui/`: dashboard, launcher, history, and terminal views.
- `macos/`: native workspace with embedded Ghostty terminals.

Keep the CLI, daemon, TUI, and app contracts aligned. `README.md`,
`docs/usage.md`, and `docs/operations.md` describe operator behavior.
