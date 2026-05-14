# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

cc-manager-kitty is a Claude Code session manager for the Kitty terminal. It manages multiple Claude Code sessions as Kitty tabs, with support for multiple accounts (each with its own `CLAUDE_CONFIG_DIR`), color-coded tab bars, and an interactive Bubble Tea TUI dashboard. The tool is a single Go binary (`ccm`) that uses Kitty's remote control protocol (`kitten @`) for all terminal interactions.

## Build and install

Requires Go 1.22+ and Kitty. Build targets are defined in the `Justfile`:

- `just build` — compile `./ccm` binary
- `just install` — build and copy binary to `~/.local/bin/ccm`, symlink `cc-manager.conf` to `~/.config/kitty/`
- `just uninstall` — remove binary and config symlink
- `just test` — run `go test ./...`
- `just check` — full suite: test + vet + lint (requires `golangci-lint`)
- `just bump VERSION` — update version constant in `cmd/root.go`

To verify a change manually, run `ccm --version` or `ccm --help` after `just install`.

## Architecture

The codebase is a standard Go module. All functionality lives in the `ccm` binary built from `main.go`.

### Package structure

**`cmd/`** — Cobra CLI commands

- `root.go` — `ccm` default command: loads config, checks for existing dashboard, launches TUI. Version constant is here.
- `config.go` — `ccm config` subcommands: `init`, `edit`, `show`, `path`, `validate`, `hooks-sync`, `hooks-status`
- `daemon.go` — `ccm daemon` subcommands: `start`, `stop`, `status`
- `hook_indicator.go` — `ccm hook-indicator <event>`: internal command invoked by Claude Code hooks

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

- `merge.go` — merge `ccm hook-indicator <event>` hooks into each account's `settings.json`
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
- `example_config.toml` — template written by `ccm config init`

## CLI commands

| Command | Description |
|---------|-------------|
| `ccm` | Launch TUI dashboard (focuses existing dashboard if already open) |
| `ccm config init` | Write example `config.toml` to `~/.config/cc-manager/` |
| `ccm config edit` | Open `config.toml` in `$EDITOR` |
| `ccm config show` | Print current config as TOML |
| `ccm config path` | Print config file path |
| `ccm config validate` | Validate config and report errors |
| `ccm config hooks-sync [ACCOUNT]` | Merge hooks into account `settings.json` files |
| `ccm config hooks-status` | Show per-account hook sync status |
| `ccm daemon start` | Start daemon in foreground (blocks; use `--debug` for live logs) |
| `ccm daemon restart` | Stop existing daemon and relaunch detached in background |
| `ccm daemon stop` | Stop running daemon |
| `ccm daemon status` | Check daemon status |
| `ccm hook-indicator <event>` | Handle a Claude Code hook event (called by hooks) |

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

[[accounts]]
name = "work"
config_dir = "~/.config/claude-work"
color = "blue"              # one of 10 named colors

[[sessions]]
title = "W:api"             # convention: account-initial prefix
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

The config path defaults to `~/.config/cc-manager/config.toml` and can be overridden with `CCM_CONFIG` environment variable.

## Key conventions

Account detection reads the `cc_account` Kitty user var set at launch time (`--var cc_account=NAME`). This is the only method — no fallback to process cmdlines in the Go implementation.

Tab title convention: uppercase account initial followed by colon (e.g., `W:project` for work, `P:dotfiles` for personal). State icons (bolt `⚡`, stop `⏹`, key `🔑`, error `✗`) and the star marker (`★`) are prepended to the base title by `palette.BuildTitle()`.

The palette and icon set are embedded at compile time in `internal/palette/` via `//go:embed`. To change a color or icon, edit the embedded JSON and rebuild.

Socket discovery uses `KITTY_LISTEN_ON` environment variable only. Kitty must be configured with `allow_remote_control yes` and `listen_on unix:/tmp/kitty-{kitty_pid}`.

Debug logging uses `slog` and is enabled with `--debug` flag or `CCM_DEBUG=1`.

Hook events installed by `ccm config hooks-sync`: `SessionStart`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `Stop`, `TaskCompleted`, `SessionEnd`, `Notification`. Each hook entry calls `ccm hook-indicator <EVENT>`.

Version is tracked as a constant in `cmd/root.go`; update with `just bump VERSION`.

## Kitty documentation reference

The `docs/` directory contains concise reference documents extracted from the official Kitty documentation. Consult these when working on Kitty-specific functionality:

- `docs/kitty_remote_control.md` — `kitten @` protocol, socket discovery, command summaries (`ls`, `launch`, `set-tab-color`, `close-tab`, `set-tab-title`, `focus-tab`)
- `docs/kitty_launch.md` — launch options: tabs, env vars, user variables (`--var`), working directories, focus, process lifecycle
- `docs/kitty_kittens.md` — custom Kitty kittens in Python (reference only; the Go TUI is not a kitten)
- `docs/kitty_tab_bar.md` — tab bar configuration and per-tab color overrides via `kitten @ set-tab-color`
- `docs/kitty_match_syntax.md` — `--match` expression syntax, including `var:cc_account` matching used by this project
