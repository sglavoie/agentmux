# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

cc-manager-kitty is a Claude Code session manager for the Kitty terminal. It manages multiple Claude Code sessions as Kitty tabs, with support for multiple accounts (each with its own `CLAUDE_CONFIG_DIR`), color-coded tab bars, and an interactive Bubble Tea TUI dashboard. The tool is a single Go binary (`amx`) that uses Kitty's remote control protocol (`kitten @`) for all terminal interactions.

## Build and install

Requires Go 1.22+ and Kitty. Build targets are defined in the `Justfile`:

- `just build` — compile `./amx` binary
- `just install` — build and copy binary to `~/.local/bin/amx`, symlink `cc-manager.conf` to `~/.config/kitty/`
- `just uninstall` — remove binary and config symlink
- `just test` — run `go test ./...`
- `just check` — full suite: test + vet + lint (requires `golangci-lint`)
- `just bump VERSION` — update version constant in `cmd/root.go`

To verify a change manually, run `amx --version` or `amx --help` after `just install`.

## Architecture

The codebase is a standard Go module. All functionality lives in the `amx` binary built from `main.go`.

### Package structure

**`cmd/`** — Cobra CLI commands

- `root.go` — `amx` default command: loads config, checks for existing dashboard, launches TUI. Version constant is here.
- `config.go` — `amx config` subcommands: `init`, `edit`, `show`, `path`, `validate`, `hooks-sync`, `hooks-status`
- `daemon.go` — `amx daemon` subcommands: `start`, `stop`, `status`
- `hook_indicator.go` — `amx hook-indicator <event>`: internal command invoked by Claude Code hooks

**`internal/config/`** — Configuration loading and validation

- `types.go` — TOML struct definitions: `Config`, `Defaults`, `Account`, `Session`, `HooksConfig`, `AccountHookOverride`
- `config.go` — `Load()` reads and defaults-fills `config.toml`
- `validate.go` — cross-section validation (accounts exist, colors valid, sort values valid)
- `paths.go` — `DefaultConfigPath()` → `~/.config/cc-manager/config.toml`

**`internal/kitty/`** — Kitty remote control

- `client.go` — `KittyClient` interface + `SubprocessClient` implementation using `kitten @` subprocess calls
- `exec.go` — low-level command execution
- `types.go` — `OSWindow`, `Tab`, `Window` structs (from `kitten @ ls` JSON)
- `match.go` — match expression builders (`MatchTabByID`, `MatchWindowByID`, etc.)

**`internal/palette/`** — Color and icon management

- `palette.go` — `Lookup(name)`, `Names()` — color lookups from embedded JSON
- `icons.go` — icon definitions and state mapping
- `embed.go` — `//go:embed` for palette and icon JSON data

**`internal/session/`** — Session query and lifecycle

- `query.go` — `List()`, `Find()` — enumerate running sessions from Kitty
- `launch.go` — `Launch()` — create tab with dedup, color application, sorted insertion
- `close.go` — `Close()` — close a running session
- `restart.go` — `Restart()` — close and relaunch
- `title.go` — `FormatTitle()`, `ParseTitle()` — tab title manipulation
- `sort.go` — session sort by account or alphabetically
- `env.go` — build environment variables for launch

**`internal/daemon/`** — IPC and state guarding

- `daemon.go` — main daemon loop (Unix socket, 30-min idle timeout)
- `server.go` / `client.go` — socket server and client (auto-start capable)
- `handler.go` — request processing and notification dispatch
- `guard.go` — `StateGuard` prevents rapid icon state transitions
- `notify.go` — macOS notification dispatch via `alerter` when installed, falling back to `osascript`
- `protocol.go` — message types: `TypeStateUpdate`, `TypeStateQuery`, `TypeShutdown`

Socket path: `~/.local/share/cc-manager/daemon.sock`

**`internal/indicator/`** — Hook integration

- `indicator.go` — entry point: resolves event → action, sends to daemon, updates tab title
- `events.go` — `ResolveAction()` maps Claude Code event names to actions (`active`, `stop`, `permission`, `failure`, `idle`, `clear`)
- `pipe.go` — pipe mode detection (suppresses macOS notifications for `claude -p`)

**`internal/hooks/`** — Claude settings integration

- `merge.go` — merge `amx hook-indicator <event>` hooks into each account's `settings.json`
- `status.go` — report per-account hook sync status (`in_sync`, `drifted`, `missing`)

**`tui/`** — Bubble Tea v2 TUI dashboard

- `app.go` — `Run()` entry point
- `model.go` — root state machine; modes: `ModeList`, `ModeSearch`, `ModeAccountFilter`, `ModeActions`, `ModeConfirm`, `ModePicker`, `ModeConfigPicker`
- `list.go`, `search.go`, `picker.go`, `actions.go`, `confirm.go`, `config_picker.go` — per-mode logic
- `keys.go` — keyboard bindings
- `style.go` — Lipgloss color scheme
- `footer.go`, `tick.go`, `msg.go` — help bar, timer, message types

**`embed/`** — Embedded files

- `embed.go` — `//go:embed` directives
- `example_config.toml` — template written by `amx config init`

### Adding a new harness

1. Create `internal/harness/<name>.go` implementing `harness.Impl` and `harness.HookContract`.
2. Register the implementation in `internal/harness/registry.go` (`registry[name] = ...`).
3. Add event name mappings (if any) to `internal/indicator/events.go`.
4. Add a test fixture in `testdata/` with the new harness and extend `config_test.go`.
5. Run `just check` — the compiler will flag any `Impl` methods you missed.

## CLI commands

| Command | Description |
|---------|-------------|
| `amx` | Launch TUI dashboard (focuses existing dashboard if already open) |
| `amx config init` | Write example `config.toml` to `~/.config/cc-manager/` |
| `amx config edit` | Open `config.toml` in `$EDITOR` |
| `amx config show` | Print current config as TOML |
| `amx config path` | Print config file path |
| `amx config validate` | Validate config and report errors |
| `amx config hooks-sync [ACCOUNT]` | Merge hooks into account `settings.json` files |
| `amx config hooks-status` | Show per-account hook sync status |
| `amx daemon start` | Start daemon in foreground (blocks; use `--debug` for live logs) |
| `amx daemon restart` | Stop existing daemon and relaunch detached in background |
| `amx daemon stop` | Stop running daemon |
| `amx daemon status` | Check daemon status |
| `amx daemon export-events` | Dump per-window hook event history as JSON (for investigation) |
| `amx hook-indicator <event>` | Handle a Claude Code hook event (called by hooks) |
| `amx resort` | Reorder Kitty tabs to match the configured sort |

## Config format

Single file: `~/.config/cc-manager/config.toml`

```toml
[defaults]
sort = "alpha"              # "alpha" or "account"
refresh_interval = 2        # TUI refresh in seconds
guard_duration = 1          # state-guard window in seconds
claude_flags = []           # global flags applied to all sessions

[defaults.notifications]
error = false
permission = true
stop = true
stale_active = true         # per-state stale sweep one-shot notification
hook_silence = true         # account-level "no hook event in N hours" notification

[defaults.watchdog]          # per-state stale + account-level silence
active = 600                 # seconds before a bolt-icon session is flagged stale
subagent = 1800              # seconds before a robot-icon (subagent) is flagged
compact = 180                # seconds before a compaction icon is flagged
hook_silence = 14400         # seconds without any hook event for a profile

[[accounts]]
name = "work"
config_dir = "~/.config/claude-work"
color = "blue"              # one of 10 named colors

[[sessions]]
title = "api"               # project title; profile shows separately in the dashboard
directory = "~/projects/api"
account = "work"
claude_flags = []           # per-session override; omit to inherit defaults
env = {}                    # environment variables injected at launch
groups = []                 # organizational groups

[hooks.overrides.work]
disabled_events = ["Stop"]  # suppress hook events for this account
```

**Colors**: green, blue, red, orange, purple, teal, pink, yellow, gray, cyan

**Session `claude_flags`**: omit to inherit `defaults.claude_flags`; set to `[]` to run with no flags.

**Deprecation**: `stale_active_threshold` is replaced by `[defaults.watchdog].active`. The legacy key still loads and is mirrored into `Watchdog.Active` when the new field is unset; both surface in `amx config show` via `Deprecations`.

The config path defaults to `~/.config/cc-manager/config.toml` and can be overridden with `amx_CONFIG` environment variable.

## Key conventions

Account detection reads the `cc_account` Kitty user var set at launch time (`--var cc_account=NAME`). This is the only method — no fallback to process cmdlines in the Go implementation.

Tab titles use the project title only. The dashboard shows the profile
separately via its badge (`[W]`, `[P]`, etc.), while state icons (bolt `⚡`,
stop `⏹`, key `🔑`, error `✗`) and the star marker (`★`) are prepended to the
base title by `palette.BuildTitle()`.

The palette and icon set are embedded at compile time in `internal/palette/` via `//go:embed`. To change a color or icon, edit the embedded JSON and rebuild.

Socket discovery uses `KITTY_LISTEN_ON` environment variable only. Kitty must be configured with `allow_remote_control yes` and `listen_on unix:/tmp/kitty-{kitty_pid}`.

Debug logging uses `slog` and is enabled with `--debug` flag or `amx_DEBUG=1`.

Hook events installed by `amx config hooks-sync`: `SessionStart`, `Setup`, `UserPromptSubmit`, `UserPromptExpansion`, `PreToolUse`, `SubagentStart`, `SubagentStop`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `PermissionRequest`, `PermissionDenied`, `Stop`, `StopFailure`, `TaskCreated`, `TaskCompleted`, `TeammateIdle`, `SessionEnd`, `Notification`, `ConfigChange`, `PreCompact`, `PostCompact`, `Elicitation`, `ElicitationResult`. Each hook entry calls `amx hook-indicator <EVENT>`. `WorktreeCreate` and `WorktreeRemove` are intentionally excluded: they are Claude Code control hooks that must print the worktree path on stdout, which `amx hook-indicator` does not do — installing them breaks subagent spawning with "WorktreeCreate hook failed: no successful output".

Version is tracked as a constant in `cmd/root.go`; update with `just bump VERSION`.

## Kitty documentation reference

The `docs/` directory contains concise reference documents extracted from the official Kitty documentation. Consult these when working on Kitty-specific functionality:

- `docs/kitty_remote_control.md` — `kitten @` protocol, socket discovery, command summaries (`ls`, `launch`, `set-tab-color`, `close-tab`, `set-tab-title`, `focus-tab`)
- `docs/kitty_launch.md` — launch options: tabs, env vars, user variables (`--var`), working directories, focus, process lifecycle
- `docs/kitty_kittens.md` — custom Kitty kittens in Python (reference only; the Go TUI is not a kitten)
- `docs/kitty_tab_bar.md` — tab bar configuration and per-tab color overrides via `kitten @ set-tab-color`
- `docs/kitty_match_syntax.md` — `--match` expression syntax, including `var:cc_account` matching used by this project
