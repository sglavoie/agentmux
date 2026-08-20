# Agent Mux

Kitty-first session manager for **Claude Code**, **GitHub Copilot CLI**, and
**OpenAI Codex**. It groups live sessions and configured launch targets,
preserves transcript and plan history, supports continuity handoffs, keeps per-profile
isolated homes (`CLAUDE_CONFIG_DIR`, `COPILOT_HOME`, `CODEX_HOME`); and uses an
always-on local daemon to own durable tab-state rendering. Harness hooks are
thin, fire-and-forget event clients.

The binary is **`amx`**.

## What it does

- manages multiple configured projects from one Bubble Tea dashboard,
- supports multiple harness profiles side by side,
- gives each project a default profile, but lets you override it for one launch,
- searches filesystem-backed Past Sessions and Plans without a database,
- creates standalone handoff files and manages Git worktrees through safe reopen,
  cleanup, branch-retention, and metadata-recovery workflows,
- syncs hook commands into each profile so tab icons track agent state,
- answers, resumes, stops, and aborts in-flight `agent-session-loop` runs from
  the `A` cockpit view,
- keeps the current Kitty/daemon/tab-color workflow intact.

## Prerequisites

- Go 1.22+
- Kitty with remote control enabled
- [`just`](https://github.com/casey/just) for the provided build/install tasks
- [`alerter`](https://github.com/vjeantet/alerter) 26.x or newer on macOS when
  any native notification is enabled (`brew install vjeantet/tap/alerter`)
- `agent-session-loop` (optional) for the `A` cockpit view. Without it, Agent
  Mux behaves exactly as it did before the integration existed. Install or
  upgrade it *before* upgrading Agent Mux; a bridge that is missing or speaks
  an older protocol is reported as unavailable, never driven.

Your `kitty.conf` must include:

```conf
allow_remote_control yes
listen_on unix:/tmp/kitty-{kitty_pid}
```

The `listen_on unix:` socket matters for more than the dashboard: it is the only
endpoint the detached daemon can dial. Kitty hands a `launch
--allow-remote-control` child (such as the F8 keybinding) a process-local
`fd:N` descriptor in `KITTY_LISTEN_ON`, which is meaningless to any other
process. Agent Mux normalizes that `fd:N` back to `unix:/tmp/kitty-<kitty_pid>`
before forwarding it, and never persists or dials a raw `fd:` endpoint. If the
`listen_on` line is missing, the daemon cannot reach Kitty from a hook fired by
such a child.

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
bindings, including `F4` for the next attention tab, `F8` to clear the current
tab's markers and state icons, and `F7`/`F9` for previous or next managed tabs.
It also disables Kitty tab bell markers and audible bells for any Kitty
instance that includes the snippet, so Agent Mux's tab icons remain the
attention signal.

## Harness profiles

A profile name is a unique launch identity, not a display label. Each project
stores the profile it should launch by default, and one-time launcher overrides
target another profile by this same `name`. If you use the same account or
project family across harnesses, give each harness variant its own ID, such as
`vta` for Claude and `vta-codex` for Codex.

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

### OpenAI Codex

Codex profiles launch `codex` and export `CODEX_HOME=<home_dir>`.

Agent Mux syncs Codex lifecycle hooks into `$CODEX_HOME/hooks.json`. The sample
Codex profile in `amx config init` includes
`--dangerously-bypass-hook-trust` so Codex will run the managed hook commands;
remove or override it if you prefer a different trust flow.

## Documentation

The detailed guides are organized by operational area:

- [Dashboard, launcher, managed worktrees, past sessions, plans, and filtering](docs/usage.md)
- [Harness hook setup and event contracts](docs/hooks.md)
- [Daemon lifecycle, routing, diagnostics, and privacy](docs/daemon.md)
- [Watchdog, tab icons, and stop confirmation](docs/watchdog.md)
- [Configuration commands, development, testing, and troubleshooting](docs/operations.md)
