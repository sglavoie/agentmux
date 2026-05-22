# Agent Mux Copilot Instructions

This repository is a Go CLI/TUI application. The main binary is `amx`, which
manages Claude Code and GitHub Copilot CLI sessions as Kitty tabs.

## Build, test, and lint commands

- Prefer the `Justfile` targets for normal local checks:
  - `just build` — build `./amx`
  - `just test` — run `go test ./...`
  - `just vet` — run `go vet ./...`
  - `just lint` — run `golangci-lint run`
  - `just check` — run test + vet + lint
- Run a single test with Go directly, for example:
  - `go test ./internal/session -run '^TestLaunch_DedupSkipsExisting$'`
  - Replace the package path and test name as needed.

## High-level architecture

- `main.go` and `cmd/` are the Cobra entrypoints. Running `amx` loads config,
  focuses an existing dashboard tab if one is already running, or launches the
  Bubble Tea dashboard otherwise.
- `internal/config` owns `~/.config/agent-mux/config.toml`: loading, defaulting,
  validation, and normalization of legacy field names into the current
  profile/project model.
- `internal/harness` is the abstraction layer for supported harnesses. Each
  harness defines its executable, home-dir env var, default home dir, pipe-mode
  flags, and hook-file contract. Claude and Copilot are both wired through this
  interface.
- `internal/session` is the session lifecycle layer. It launches Kitty tabs,
  computes effective args/env, applies tab colors, deduplicates sessions, and
  reconstructs running sessions from Kitty metadata for query/restart flows.
- `internal/kitty` is the only package that should speak `kitten @`. Match
  expression helpers and remote-control subprocess handling live there.
- `internal/hooks`, `cmd/hook_indicator.go`, and `internal/indicator` implement
  the hook pipeline: `amx config hooks-sync` installs per-harness hooks, those
  hooks call `amx hook-indicator <event>`, and indicator code maps raw hook
  events to canonical tab-state actions.
- `internal/daemon` is a local Unix-socket service that guards noisy icon
  transitions, tracks session activity, and owns macOS notifications. Indicator
  code asks the daemon whether a state change should be applied before updating
  a tab title.
- `internal/palette` owns embedded tab colors and state icons. Session state is
  rendered into the Kitty tab title prefix rather than tracked in a separate UI
  model.
- `tui/` is the Bubble Tea dashboard: list mode, launcher, filter DSL, preview
  pane, actions overlay, and profile picker. `internal/overrides` persists
  picker profile overrides between launches.

## Key conventions

- Prefer the current user-facing config names: `profiles`, `projects`, `args`,
  and `home_dir`. The code still intentionally supports legacy aliases:
  `accounts`, `sessions`, `claude_flags`, and `config_dir`.
- Managed sessions are identified by Kitty user vars, not by process inspection.
  Launch code writes both current `amx_*` vars and legacy `cc_*` vars, and
  query/restart logic rebuilds session state from those vars later.
- Base tab titles use the project title only. Profile identity stays in Kitty
  user vars and the dashboard badge. Star and state icons are prepended by
  `internal/palette`. Use `session.FormatTitle`, `palette.BuildTitle`, and
  `palette.ParseTitle` instead of manual title string handling.
- Args precedence is `project args -> profile args -> defaults.args`. Home-dir
  precedence is `profile.home_dir -> config_dir -> harness default`. Restart
  behavior depends on the user-var snapshot preserving those effective values.
- Hook changes are cross-cutting. If an event name or action changes, update the
  harness hook contract, `internal/indicator/events.go`, daemon icon/priority
  behavior, and the related tests together.
- Hook storage differs by harness: Claude merges hooks into
  `<home_dir>/settings.json`; Copilot writes `<home_dir>/hooks/agent-mux.json`.
- The daemon intentionally preserves attention states. `TaskCompleted` keeps the
  current icon, `idle`/`clear` only clear active-like icons, and stop,
  permission, and failure states persist until the user clears them.
- Kitty remote control depends on `KITTY_LISTEN_ON` and a Kitty config with
  remote control enabled. Use the local `docs/kitty_*.md` files when changing
  Kitty-specific behavior or match expressions.
- Picker profile overrides are persisted in
  `~/.local/share/agent-mux/launch_overrides.json`, keyed by project title.
  Plain shell tabs opened with `session.OpenDirectory` intentionally omit the
  managed-session user vars so they do not appear in the dashboard.
- The TUI filter DSL is shared between the dashboard list and the launcher:
  bare words are substrings, `@profile` matches a profile, `%harness` matches a
  harness, and `!state` matches active/idle/icon states.
