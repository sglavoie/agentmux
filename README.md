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
bindings, including `F5` for the next attention tab, `F8` to clear the current
tab's markers and state icons, and `F7`/`F9` for previous or next managed tabs.

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
- `d`: show the per-session hook event log (scrollable detail pane)
- `?`: show the full keymap

The dashboard keeps the profile badge and harness metadata separate from the
title column, so visible titles are just the project name.

The detail pane's event history persists through terminal hooks (`Stop`,
`SessionEnd`, etc.) and through the watchdog's stale sweep, so you can still
review what a session did after its icon clears. History is held in memory per
Kitty window and capped at 5 000 events (oldest dropped); it is released only
when the window closes or the daemon restarts.

### Launcher

The launcher reads configured `projects`, shows their default profile/harness,
and lets you multi-select launches.

Launcher rows likewise show the project title only; profile information stays in
its own badge/metadata columns.

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

Copilot hook files are user-level for a given `COPILOT_HOME`, so any Copilot CLI
surface that reuses that home — including VS Code or another non-Kitty session
— will run the same hook commands. Agent Mux treats those non-Kitty invocations
as silent no-ops; only Kitty-backed sessions update tab state.

Copilot hook entries use the documented `{"bash": ..., "powershell": ...}`
schema. If you installed hooks with an older Agent Mux build that wrote a
`command` field instead, re-run `amx config hooks-sync` once; the drift
detector backs up the old file as `agent-mux.json.bak`.

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

`agentStop` is the terminal Copilot turn-end hook — the direct analogue of
Claude's `Stop` event — and resolves to the stop action (⏹ icon).
`notification` payloads with `notification_type = agent_idle` also resolve to
the stop action. `notification_type = permission_prompt` and
`notification_type = elicitation_dialog` both resolve to the permission-needed
key icon (🔑), because both mean Copilot is waiting on user input.
`sessionEnd` remains the session-level terminal hook. Copilot still does not
document a `postCompact` hook; if a compaction run ends with only an
idle/cleanup follow-up after `preCompact`, Agent Mux promotes the compact state
to ⏹ instead of silently clearing the tab icon.

Claude hook support remains in place alongside Copilot; `Stop` and
`SessionEnd` are terminal. `TeammateIdle` is the lone non-terminal
child-complete signal that preserves the parent session's current icon.

## Daemon

The daemon owns tab-state transitions and notifications:

```bash
amx daemon start
amx daemon restart
amx daemon status
amx daemon stop
amx daemon export-events
```

It listens on:

```text
~/.local/share/agent-mux/daemon.sock
```

### Investigating hook event flow

`amx daemon export-events` writes a schema-v2 JSON snapshot for investigation.
Top-level fields describe the export (`schema_version`, `exported_at`,
`daemon_pid`, `daemon_uptime_seconds`); `windows` keeps the legacy
`window_id -> []SessionEvent` map, and `window_meta` adds per-window `stats`,
durable session identity, and best-effort live Kitty/session metadata:

```json
{
  "schema_version": 2,
  "windows": {"42": [ ... ]},
  "window_meta": {
    "42": {
      "stats": { ... },
      "durable": { ... },
      "live": { "found": true, ... }
    }
  }
}
```

Durable metadata can outlive the legacy `windows` buffer after a tab closes;
`live.found` flips to `false` when Kitty/session lookups are no longer
available. Event rows keep the raw `event`/`action` fields plus normalized
debug fields such as `tool_name`, `result_icon_state`, `current_icon_state`,
`notification_type`, `stop_reason`, `reason`, and
`suppressed`/`suppression_reason` when present.

Combine with `jq` to filter for the events relevant to a bug report:

```bash
amx daemon export-events | jq '.windows[] | .[] | select(.event == "agentStop")'
```

Inspect the richer metadata for one window:

```bash
amx daemon export-events | jq '.window_meta["42"]'
```

Pass `--window ID` to restrict the dump to one Kitty window, or `--output
PATH` to write to a file instead of stdout. The export is read-only and does
not auto-start the daemon, so the buffer of a still-running investigation
session is never replaced.

## Watchdog

The daemon includes a watchdog that turns three independent signals into a
"can I trust my icons?" guarantee:

1. **Per-state stale thresholds.** Each active-class icon (⚡ active, 🤖
   subagent, 🌀 compact) has its own grace period. When a session displays
   one of those icons but the daemon stops receiving fresh hook events for
   longer than the threshold, the daemon clears the icon and (optionally)
   fires a one-shot notification.
2. **Glanceable age column in the dashboard.** The list shows how long ago
   the most recent hook event was for each active-class row. As the age
   approaches the configured threshold the cell shifts into a warning
   style, so a stuck session is visible before it becomes stale.
3. **Account-level hook silence.** A separate sweep watches whether each
   profile is producing _any_ hook events at all. If an entire profile
   goes silent for longer than the hook-silence threshold (default 4
   hours), the watchdog fires a single notification — useful for catching
   a misconfigured `settings.json` or a daemon-down window where every
   session simply looks idle.

Configure the four thresholds under `[defaults.watchdog]`:

```toml
[defaults.watchdog]
active = 600           # bolt icon; bursty tool loops settle within ~10 min
subagent = 1800        # robot icon; agentic workflows can legitimately last ~30 min
compact = 180          # compaction icon; should always finish within minutes
hook_silence = 14400   # account-level silence; default 4 hours
```

Each field independently falls back to the value above when set to `0`.
The legacy `stale_active_threshold = N` key still loads and is mirrored
into `[defaults.watchdog].active` for backward compatibility.

Per-profile overrides live on the profile itself:

```toml
[[profiles]]
name = "claude-work"
harness = "claude"
home_dir = "~/.config/claude-work"
color = "blue"
watchdog = { active = 300, subagent = 2400, hook_silence = 0 }
```

`hook_silence = 0` on a per-profile override disables the silence sweep
for that profile only.

The notifications themselves are gated by `[defaults.notifications]`:

```toml
[defaults.notifications]
stale_active = true
hook_silence = true
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
- **The bolt icon stays after the agent is done**: run `amx config hooks-status`
  and `amx config hooks-sync` to restore terminal hooks like
  `sessionEnd`/`Stop`/`agentStop` and the child-complete `TeammateIdle` hook.
  Current releases also recover across daemon restarts on the next cleanup
  event. For Copilot, also upgrade to version `1.0.45` or newer, because
  older releases had `agentStop` hook issues.
- **Copilot profile is unauthenticated**: run `copilot login` with the matching
  `COPILOT_HOME`.
- **Kitty control fails**: confirm `allow_remote_control yes` and the `listen_on`
  socket setting are present, then restart Kitty.
