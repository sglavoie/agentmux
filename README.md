# Agent Mux

Kitty-based session manager for agent harnesses such as **Claude Code** and
**GitHub Copilot CLI**. It launches projects as Kitty tabs, keeps per-profile
isolated homes (`CLAUDE_CONFIG_DIR`, `COPILOT_HOME`), and updates tab titles
with hook-driven status emojis through a small local daemon.

The binary is **`amx`**.

## What it does

- manages multiple configured projects from one Bubble Tea dashboard,
- supports multiple harness profiles side by side,
- gives each project a default profile, but lets you override it for one launch,
- syncs hook commands into each profile so tab icons track agent state,
- keeps the current Kitty/daemon/tab-color workflow intact.

## Prerequisites

- Go 1.22+
- Kitty with remote control enabled
- [`just`](https://github.com/casey/just) for the provided build/install tasks
- [`alerter`](https://github.com/vjeantet/alerter) on macOS if you want native
  notifications

Your `kitty.conf` must include:

```conf
allow_remote_control yes
listen_on unix:/tmp/kitty-{kitty_pid}
```

## Install

```bash
git clone <repo-url> ~/path/to/agent-mux
cd ~/path/to/agent-mux
just install
```

This builds `amx`, copies it to `~/.local/bin/amx`, symlinks
`agent-mux.conf` into `~/.config/kitty/`, and seeds
`~/.config/agent-mux/config.toml` with a starter config if it does not already
exist.

Then add this to `kitty.conf`:

```conf
include agent-mux.conf
```

Reload Kitty. The bundled config keeps the dashboard hotkey and tab-navigation
bindings.

## Config reference

If you installed manually or want to recreate the starter config:

```bash
amx config init
```

This writes `~/.config/agent-mux/config.toml`. `just install` already seeds the
same file on first install.

The current model is:

- `profiles`: named harness profiles with a harness, home directory, color, and optional default args
- `projects`: launchable project templates with a default profile

Example:

```toml
[defaults]
sort = "alpha"
args = []

[defaults.notifications]
permission = true
stop = true

[[profiles]]
name = "claude-work"
harness = "claude"
home_dir = "~/.config/claude-work"
color = "blue"
args = ["--dangerously-skip-permissions"]

[[profiles]]
name = "copilot-work"
harness = "copilot"
home_dir = "~/.copilot-work"
color = "green"
args = ["--model", "gpt-5"]

[[projects]]
title = "api"
directory = "~/projects/api"
profile = "claude-work"

[[projects]]
title = "docs"
directory = "~/projects/docs"
profile = "copilot-work"
groups = ["work", "docs"]
```

Legacy `accounts`, `sessions`, `config_dir`, `account`, and `claude_flags`
still load for migration, but new configs should use `profiles`, `projects`,
`home_dir`, `profile`, and `args`.

Args resolve with this precedence: project `args` override profile `args`,
which override shared `defaults.args`. Legacy `claude_flags` still work as the
fallback alias for project/default args.
Use `defaults.args` only for harness-neutral flags; put Claude/Copilot-specific
flags on the relevant profile. Config changes are loaded when the dashboard
starts, so restart the dashboard after editing `config.toml`.

Validate with:

```bash
amx config validate
```

## Harness profiles

### Claude Code

Claude profiles launch `claude` and export `CLAUDE_CONFIG_DIR=<home_dir>`.

### GitHub Copilot CLI

Copilot profiles launch `copilot` and export `COPILOT_HOME=<home_dir>`.

Authenticate each Copilot profile inside its own home:

```bash
COPILOT_HOME=~/.copilot-work copilot login
```

That keeps auth, settings, hooks, permissions, and session history isolated per
profile.

## Usage

### Dashboard

```bash
amx
```

This opens the dashboard, or focuses the existing dashboard tab if one is
already running.

Key flows:

- `l`: open the launcher
- `c`: restart the selected running session under another profile
- `o`: open the selected project's directory in a plain shell tab
- `p`: toggle the preview pane
- `?`: show the full keymap

### Launcher

The launcher reads configured `projects`, shows their default profile/harness,
and lets you multi-select launches.

- type to filter live,
- `space` toggles selection,
- `enter` launches the selected projects,
- `C` chooses a **one-time profile override** for the highlighted project,
- `o` opens the highlighted project's directory instead of launching it.

Overrides only affect the current launch. They do not rewrite `config.toml`.

### Filter DSL

The list and launcher share the same filter DSL:

| Token | Meaning |
| --- | --- |
| `word` | case-insensitive substring against title, directory, or profile |
| `@profile` | exact profile match |
| `!state` | state match: `active`, `idle`, `bolt`, `key`, `stop`, `error` |
| `\@foo`, `\!x` | literal leading `@` or `!` |

## Hooks

Sync hooks for every configured profile:

```bash
amx config hooks-sync
amx config hooks-status
```

Hook installation is harness-aware:

- **Claude**: merges Agent Mux hook commands into that profile's
  `settings.json`
- **Copilot**: writes `$COPILOT_HOME/hooks/agent-mux.json`

The hook command is `amx hook-indicator <event>`.

Current Copilot events mapped by Agent Mux include:

- `sessionStart`
- `sessionEnd`
- `userPromptSubmitted`
- `preToolUse`
- `postToolUse`
- `postToolUseFailure`
- `permissionRequest`
- `agentStop`
- `subagentStart`
- `subagentStop`
- `errorOccurred`
- `preCompact`
- `notification`

Claude hook support remains in place alongside Copilot.

## Daemon

The daemon owns tab-state transitions and notifications:

```bash
amx daemon start
amx daemon restart
amx daemon status
amx daemon stop
```

It listens on:

```text
~/.local/share/agent-mux/daemon.sock
```

## Tab icons

The tab title is prefixed with an icon based on the latest hook event:

| Icon | Meaning |
| --- | --- |
| ⚡ | active |
| 🤖 | subagent running |
| 🌀 | compaction running |
| 🛑 | stopped |
| 🔑 | permission needed |
| ❗ | failure |

## Config commands

| Command | Description |
| --- | --- |
| `amx config init` | create the example config |
| `amx config edit` | open the config in `$EDITOR` |
| `amx config show` | print the parsed config |
| `amx config path` | print the config path |
| `amx config validate` | validate the config |
| `amx config hooks-sync` | sync hooks into all configured profiles |
| `amx config hooks-status` | show hook sync status per profile |

## Build and test

```bash
just build
just test
```

## Troubleshooting

- **No projects appear**: run `amx config validate` and confirm the config is
  at `~/.config/agent-mux/config.toml`.
- **Hooks are missing**: run `amx config hooks-status`, then
  `amx config hooks-sync`.
- **Copilot profile is unauthenticated**: run `copilot login` with the matching
  `COPILOT_HOME`.
- **Kitty control fails**: confirm `allow_remote_control yes` and the `listen_on`
  socket setting are present, then restart Kitty.
