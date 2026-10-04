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
- shows every live session at once as a grid of live pane previews (`F2`, or
  `amx overview`),
- supports multiple harness profiles side by side,
- gives each project a default profile, but lets you override it for one launch,
- searches filesystem-backed Past Sessions and Plans without a database,
- creates standalone handoff files and manages Git worktrees through safe reopen,
  cleanup, branch-retention, and metadata-recovery workflows,
- syncs hook commands into each profile so tab icons track agent state,
- answers, resumes, stops, and aborts in-flight `agent-session-loop` runs from
  the `A` cockpit view,
- keeps the current Kitty/daemon/tab-color workflow intact.

The everyday commands beyond the dashboard (`amx`):

| Command | What it does |
| --- | --- |
| `amx overview` | grid of every live session (`F2`) |
| `amx open-dashboard` | focus the dashboard, opening it in a new tab if absent |
| `amx project list` / `launch` | list configured projects; launch one in a new tab |
| `amx session list` / `continue` / `fork` / `handoff` | conversation continuity |
| `amx worktree list` / `reopen` / `remove` / `prune` | managed Git worktrees |
| `amx mute-toggle` | mute or unmute a tab's notifications |
| `amx notifications snooze` / `test` | snooze or test notifications |
| `amx daemon status` / `restart` | the always-on daemon |
| `amx doctor --strict` | installation and daemon health |
| `amx config init` / `edit` / `validate` | configuration |

Most take `--json` and answer with the schema-v1 envelope.

## Prerequisites

- Go 1.22+
- Kitty with remote control enabled
- [`just`](https://github.com/casey/just) for the provided build/install tasks
- [`alerter`](https://github.com/vjeantet/alerter) 26.x or newer on macOS when
  any native notification is enabled (`brew install vjeantet/tap/alerter`)
- `agent-session-loop` (optional) for the `A` cockpit view. Without it, Agent
  Mux behaves exactly as it did before the integration existed. Install or
  upgrade it *before* upgrading Agent Mux; a bridge that is missing or speaks
  an older protocol is reported as unavailable, never driven. Run
  `amx config asl-sync` once after installing both halves: the daemon is spawned
  detached and does not inherit your shell's `PATH`, so pairing resolves the
  executable and persists an absolute path.

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
bindings, including `F2` for the live session overview,
`F4` for the next attention tab, `F8` to clear the current tab's markers and
state icons, `F11` to mark the current tab active (`⚡` until its next real
hook event, the end of a turn observed after the mark, `F8`, or the usual
ten-minute staleness), and `F7`/`F9` for
previous or next managed tabs. On macOS, `F11` reaches Kitty only with
"Use F1, F2, etc. keys as standard function keys" enabled or as `fn`+`F11`;
otherwise the system takes it for Show Desktop.
It also disables Kitty tab bell markers and audible bells for any Kitty
instance that includes the snippet, so Agent Mux's tab icons remain the
attention signal. `F1` opens the same binding reference from inside Kitty, and
it is generated from the catalog the snippet itself is generated from, so the
two cannot disagree about what a key does.

Reloading matters for bindings and nothing else. `just install` replaces the
`amx` binary and restarts the daemon, so a new *behavior* is live immediately;
a new or changed **keybinding** only reaches a running Kitty once that instance
reloads its configuration (`Ctrl+Shift+F5`, or restart Kitty). Upgrading from a
release without `F2` is exactly that case: `amx overview` works from any shell
straight away, but the key does nothing until Kitty has re-read the snippet.

### Menu bar companion (optional, macOS)

`Agent Mux.app` puts the live sessions and their attention counts in the menu
bar, focuses a session from a row or a global shortcut, shows the daemon's
notifications as native banners, and answers ASL supervisor questions. It
needs macOS 14+ and Xcode, and `amx` installed first:

```bash
just install
just mac-install    # Release build into /Applications, then relaunch
```

Kitty needs `allow_remote_control yes` and `listen_on unix:/tmp/kitty` for the
app to focus sessions. See [Menu bar companion](docs/companion-app.md) for
permissions, shortcuts, and troubleshooting, and
[`macos/README.md`](macos/README.md) for building and signing.

### Upgrading the pair

Agent Mux and `agent-session-loop` are released as a pair, and the install order
is part of the contract: **ASL first, then Agent Mux**. The Agent Mux install
restarts the daemon, and a daemon restarted against a stale ASL says nothing
about whether the two halves agree.

```bash
just verify-asl-install        # in the ASL checkout, after installing it
just install-restart-smoke     # in this checkout
just cockpit-smoke             # from a Kitty window
amx config asl-sync --check
```

`install-restart-smoke` stages the binary beside its destination and renames it
into place, so there is no window where `amx` is absent or half-copied, and a
running daemon keeps its already-open executable. It then restarts the daemon,
waits for `ready: true`, and gates on `amx doctor --strict`, which verifies that
the CLI and the daemon are the same artifact by hash and revision.

A pairing preflight refuses whole-root if a live run still carries state the new
build cannot pair with, naming each run. Let those runs finish rather than
migrating around them.

Cutting an actual release — as opposed to reinstalling one — runs
`just paired-release-gate asl_root=… signing_key=…`, which performs all of the
above plus both full suites and produces a signed evidence record and annotated
tags in both repositories. See
[Paired releases](docs/paired-releases.md) for the procedure, the
evidence schema, how to verify a record's signature, and how to roll back.

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
- [Menu bar companion (macOS)](docs/companion-app.md)
- [Configuration commands and the operator index](docs/operations.md)
- [Build and test](docs/testing.md)
- [agent-session-loop rollout](docs/asl-integration.md)
- [Paired releases](docs/paired-releases.md)
- [Screen classifier health](docs/screen-classifier-health.md)
- [Troubleshooting](docs/troubleshooting.md)
