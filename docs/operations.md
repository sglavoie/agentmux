# Configuration, testing, and troubleshooting

This is the operator index. Each area below has its own guide.

| Guide | Covers |
| --- | --- |
| [Build and test](testing.md) | `just` gates, CI lanes, and the TUI layout regression harness |
| [agent-session-loop rollout](asl-integration.md) | install order, `[defaults.asl]` modes, `amx config asl-sync`, the six doctor pairing dimensions, run-presence cutover, deferred gaps |
| [Paired releases](paired-releases.md) | `just paired-release-gate`, phase order, the signed evidence record, verification, tags and their rollback, maintenance installation, harness compatibility policy |
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

## Upgrading the pair

Agent Mux and `agent-session-loop` are released as a pair, and the install order
is part of the contract: **ASL first, then Agent Mux**. The Agent Mux install
restarts the daemon, and a daemon restarted against a stale ASL says nothing
about whether the two halves agree.

```bash
just verify-asl-install        # in the ASL checkout, after installing it
just install-restart-smoke     # in this checkout
just cockpit-smoke             # from any terminal; needs tmux
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
it runs from any terminal and uses the private tmux server for sessions.

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
it, and they reach the agent in tmux. `amx config validate` rejects
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
