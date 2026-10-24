# Configuration, testing, and troubleshooting

This is the operator index. Each area below has its own guide.

| Guide | Covers |
| --- | --- |
| [Build and test](testing.md) | `just` gates, the three non-hermetic gates, CI lanes, the live Kitty worktree checklist, the TUI layout regression harness |
| [agent-session-loop rollout](asl-integration.md) | install order, `[defaults.asl]` modes, `amx config asl-sync`, the six doctor pairing dimensions, run-presence cutover, deferred gaps |
| [Paired releases](paired-releases.md) | `just paired-release-gate`, phase order, the signed evidence record, verification, tags and their rollback, maintenance installation, harness compatibility policy |
| [Daily-driver trial](daily-driver-trial.md) | installing the desktop candidate, switching new launches to it, what a trial covers and how it is logged, rollback, Kitty as the fallback |
| [Screen classifier health](screen-classifier-health.md) | pattern-set sources in `amx doctor`, `screenstate_override_invalid` and `screenstate_persistent_decline`, the decline-signal drill |
| [Troubleshooting](troubleshooting.md) | symptom-first recovery for icons, hooks, routing, the daemon, the ASL cockpit, and debug bundles |

## Which install or update command

- `just install`: put a freshly built `amx` in `~/.local/bin` (and seed a
  starter config); the running daemon is not restarted.
- `just install-restart-smoke`: install, restart the daemon onto it, and gate on
  `amx doctor --strict`. Use it when there is no Xcode, or in the
  [pair upgrade](#upgrading-the-pair) order.
- `just update`: the one to run after pulling or committing here: install, sync
  hooks, `mac-install`, then restart and verify. Needs Xcode.
- `just mac-install`: rebuild and reinstall only Agent Mux.app; it never stops
  the tmux server.

The full table, with the smoke recipes, is in
[Build and test](testing.md#which-install-or-smoke-recipe).

## Config commands

Start with the [minimum profile/project configuration](../README.md#first-session).
The dashboard reloads valid configuration edits automatically, deferring changes
while a form or confirmation is open. Invalid edits retain the last valid settings.
Daemon-owned settings need `amx daemon restart`; `amx doctor` reports when its
configuration is older than the file.

| Command | Description |
| --- | --- |
| `amx config init` | create the example config |
| `amx config edit` | open the config in `$EDITOR` |
| `amx config show` | print the config file exactly as written |
| `amx config profiles --json` | list all configured profile names and harnesses, including profiles with no project |
| `amx config path` | print the config path |
| `amx config validate` | validate the config; warns about unknown and deprecated keys and about project directories that are missing or not directories. `--strict` fails on such a directory instead. `--json` reports the same as data, with a nonzero exit only under `--strict` |
| `amx config hooks-sync` | sync hooks into all configured profiles |
| `amx config hooks-status` | show hook sync status per profile |
| `amx config confirmation` | report whether destructive actions (End Session, restart, and restart under another profile) ask for confirmation. `--policy follow-cli` (the default) reads `[defaults] confirm_destructive` from `config.toml` each time; `always` and `never` answer without reading it. `--json` reports `{required}`. Under **Follow config.toml**, the desktop app runs it just before a destructive action |

## Kitty (optional)

Kitty is a second backend, for sessions that run as Kitty tabs. Nothing above
needs it. To use it:

```bash
just install-kitty    # links agent-mux.conf into ~/.config/kitty/
```

Your `kitty.conf` must include:

```conf
allow_remote_control yes
listen_on unix:/tmp/kitty-{kitty_pid}
include agent-mux.conf
```

and launches go there with `launch_backend = "kitty"` (or unset, for now) or
`--backend kitty`. `just kitty-smoke`, run from a Kitty window, checks the
installed `amx` against that Kitty.

The `listen_on unix:` socket matters for more than the dashboard: it is the only
endpoint the detached daemon can dial. Kitty hands a `launch
--allow-remote-control` child (such as the F8 keybinding) a process-local
`fd:N` descriptor in `KITTY_LISTEN_ON`, which is meaningless to any other
process. Agent Mux normalizes that `fd:N` back to `unix:/tmp/kitty-<kitty_pid>`
before forwarding it, and never persists or dials a raw `fd:` endpoint. If the
`listen_on` line is missing, the daemon cannot reach Kitty from a hook fired by
such a child.

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

Reloading matters for bindings and nothing else: a new or changed
**keybinding** only reaches a running Kitty once that instance reloads its
configuration (`Ctrl+Shift+F5`, or restart Kitty). Upgrading from a
release without `F2` is exactly that case: `amx overview` works from any shell
straight away, but the key does nothing until Kitty has re-read the snippet.


## Upgrading the pair

Agent Mux and `agent-session-loop` are released as a pair, and the install order
is part of the contract: **ASL first, then Agent Mux**. The Agent Mux install
restarts the daemon, and a daemon restarted against a stale ASL says nothing
about whether the two halves agree.

```bash
just verify-asl-install        # in the ASL checkout, after installing it
just install-restart-smoke     # in this checkout
just cockpit-smoke             # from any terminal; needs tmux, not Kitty
amx config asl-sync --check
```

`install-restart-smoke` stages the binary beside its destination and renames it
into place, so there is no window where `amx` is absent or half-copied, and a
running daemon keeps its already-open executable. It then restarts the daemon,
waits for `ready: true` (and, when ASL is required, for the bridge handshake that
follows it), and gates on `amx doctor --strict`, which verifies that
the CLI and the daemon are the same artifact by hash and revision.

`just update` does the same and also syncs the hook files
(`amx config hooks-sync`) and rebuilds and reinstalls the menu bar app (it needs
Xcode). It installs `amx` and the app before restarting the daemon, so
`amx doctor --strict` checks the app's bundled `amx` against the daemon too.
It is the one command to run after pulling or committing in this checkout, and
it runs from any terminal: restarted outside Kitty, the daemon keeps the Kitty
its predecessor was reading, and with no Kitty running it is ready without one.

A pairing preflight refuses whole-root if a live run still carries state the new
build cannot pair with, naming each run. Let those runs finish rather than
migrating around them.

Cutting an actual release — as opposed to reinstalling one — runs
`just paired-release-gate asl_root=… signing_key=…`, which performs all of the
above plus both full suites and produces a signed evidence record and annotated
tags in both repositories. See
[Paired releases](paired-releases.md) for the procedure, the
evidence schema, how to verify a record's signature, and how to roll back.


## Harness profiles

A profile name is a unique launch identity, not a display label. Each project
stores the profile it should launch by default, and one-time launcher overrides
target another profile by this same `name`. If you use the same account or
project family across harnesses, give each harness variant its own ID, such as
`vta` for Claude and `vta-codex` for Codex.

A profile's `home_dir` is exported to the harness as the variable named below.
`config_dir` is the older spelling of the same key and is still read: a
profile with no `home_dir` uses it, and when both are set `home_dir` wins.
With neither, the harness's usual home is used. New configurations should
write `home_dir`.

A project may add environment variables of its own with `env`, a table of
strings:

```toml
[[projects]]
title = "api-staging"
directory = "~/projects/api"
profile = "claude-work"
env = { API_ENV = "staging" }
```

They are applied after the profile's home variable, so a project key wins over
it, and they reach the agent on either backend. `amx config validate` rejects
an empty key or one containing `=`.

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
