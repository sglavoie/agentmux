# Agent Mux

Kitty-based session manager for agent harnesses such as **Claude Code**,
**GitHub Copilot CLI**, and **OpenAI Codex**. It launches projects as Kitty
tabs, keeps per-profile isolated homes (`CLAUDE_CONFIG_DIR`, `COPILOT_HOME`,
`CODEX_HOME`), and updates tab titles
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
- `S`: search past session transcripts across every configured profile
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

- press `/` to filter live,
- `space` toggles selection,
- `enter` launches the selected projects,
- `C` chooses a **one-time profile override** for the highlighted project,
- `o` opens the highlighted project's directory instead of launching it.

Overrides are useful for cross-harness launches. For example, a project such as
`sis` can keep `profile = "vta"` as its Claude default while `C` launches that
same project under a Codex profile such as `vta-codex`.

Overrides only affect launcher behavior. They do not rewrite `config.toml`, and
pressing `enter` in the launcher is an explicit request for new tab(s): if the
selected project/profile is already running, Agent Mux opens another instance
instead of skipping or focusing the existing tab.

### Past sessions

Press `S` from the dashboard to open the past-session search. amx walks each
profile's transcript directory (Claude `~/.claude/projects`, Copilot
`~/.copilot/session-state`, Codex `~/.codex/sessions`) and indexes every
JSONL conversation it finds. Session metadata is stored at
`~/.local/share/agent-mux/transcripts.json`; searchable transcript text is
stored in per-session sidecars under `~/.local/share/agent-mux/transcripts.search/`.
The daemon never loads the transcript index or search sidecars.

- `/` enters a free-text query; results stream the matching JSONL on demand
  and show the per-session match count.
- `h`, `p`, `r` cycle single-value chip filters for harness, profile, and
  project.
- `enter` opens the transcript viewer (role-coded lines, `n`/`N` jumps
  between matches, `j`/`k` and `g`/`G` for scrolling).
- `d` opens a three-way delete prompt: `i` hides the row from the index only
  (file kept on disk; rescans do not resurrect it), `f` also removes the
  underlying JSONL file (irreversible).

Maintenance commands:

```bash
amx transcripts status
amx transcripts compact
amx transcripts rebuild --all
```

`status` reports metadata and sidecar sizes, visible/hidden entry counts, and
missing sidecars. `compact` migrates legacy inline `search_text` fields into
sidecars and rewrites `transcripts.json` as metadata-only JSON. `rebuild --all`
deletes and rebuilds the sidecar store from configured transcript directories,
temporarily disabling the scan-age cutoff for that pass.

Retention is controlled by `[defaults.session_archive] keep_days` (default
30). Refresh and prune operations hide index entries older than `keep_days`;
the on-disk transcript files are left intact. Set `keep_days = 0` to use the
default; negative values are rejected at validation time.

Each refresh of the past-session list only walks files modified within
`scan_max_age_days` days (default: inherits `keep_days`). Set to `0` to
inherit `keep_days`, or to `-1` for an unbounded walk; existing index
entries remain searchable past the cutoff until `keep_days` retires them.

### Filter DSL

The list and launcher share the same filter DSL:

| Token | Meaning |
| --- | --- |
| `word` | case-insensitive substring against title, directory, or profile |
| `@profile` | exact profile match |
| `!state` | state match: `active`, `idle`, `bolt`, `key`, `stop`, `warning`, `error`, `subagent`, `compact`, `question` |
| `\@foo`, `\!x` | literal leading `@` or `!` |

## Hooks

Sync hooks for every configured profile:

```bash
amx config hooks-sync
amx config hooks-status
```

Managed hook files embed the absolute `amx` path returned by the binary that ran
`hooks-sync`. Re-run `amx config hooks-sync` when that path changes (for example,
when switching from a repo-local binary to `~/.local/bin/amx`). If you rebuilt
or reinstalled the same binary path and only changed Go logic, hook sync is not
needed.

Hook installation is harness-aware:

- **Claude**: merges Agent Mux hook commands into that profile's
  `settings.json`
- **Copilot**: writes `$COPILOT_HOME/hooks/agent-mux.json`
- **Codex**: writes `$CODEX_HOME/hooks.json`

The hook command is `amx hook-indicator <event>`.

Copilot and Codex hook files are user-level for a given harness home, so any CLI
surface that reuses that home — including VS Code or another non-Kitty session
— will run the same hook commands. Agent Mux treats those non-Kitty invocations
as silent no-ops; only Kitty-backed sessions update tab state.

Copilot hook entries use the documented `{"bash": ..., "powershell": ...}`
schema. If you installed hooks with an older Agent Mux build that wrote a
`command` field instead, re-run `amx config hooks-sync` once; the drift
detector backs up the old file as `agent-mux.json.bak`.

Codex hook entries use Codex's grouped `{"hooks": { ... }}` JSON shape. Agent
Mux preserves non-Agent-Mux hooks already present in `hooks.json`, replaces only
its prior hook entries, writes synchronous command hooks (no `async` field), and
sets `timeout: 5` so `hook-indicator` does not inherit Codex's long default
hook timeout.

Current Codex events mapped by Agent Mux include:

- `SessionStart`
- `UserPromptSubmit`
- `PreToolUse`
- `PermissionRequest`
- `PostToolUse`
- `PreCompact`
- `PostCompact`
- `SubagentStart`
- `SubagentStop`
- `Stop`

Codex's `PostCompact` hook is treated as terminal, so a completed compaction
run lands on the same stop icon (⏹) path as `Stop`.

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
the stop action. Copilot `permissionRequest` fires earlier, before the
permission service decides whether a tool call is auto-allowed, denied by rules,
or escalated to a real user prompt, so Agent Mux treats that hook as
pre-permission activity and preserves the current icon until the outcome is
clearer. `notification_type = permission_prompt` and
`notification_type = elicitation_dialog` are the explicit user-blocking signals
that resolve to different sticky icons: `permission_prompt` uses the
permission-needed key icon (🔑), while `elicitation_dialog` uses the question
icon (❓) for plan approvals and similar user-input prompts. Because the managed
Kitty snippet disables tab bell markers and audible bells globally, these icons
become the visible attention signal instead of Kitty's bell state. If Copilot
has already continued the turn after a speculative `permissionRequest`, a later
trailing `notification.permission_prompt` is treated as stale noise and does
not replace the current icon.
`sessionEnd` remains the session-level terminal hook. Copilot still does not
document a `postCompact` hook; if a compaction run ends with only an
idle/cleanup follow-up after `preCompact`, Agent Mux promotes the compact state
to ⏹ instead of silently clearing the tab icon.

Claude hook support remains in place alongside Copilot; `Stop` and
`SessionEnd` are terminal. `TeammateIdle` is the lone non-terminal
child-complete signal that preserves the parent session's current icon.

Agent Mux classifies reset flows from the harness lifecycle it actually sees at
runtime instead of from prompt-only heuristics. Claude `/clear` is detected from
`SessionEnd.reason = clear` and the follow-up `SessionStart.source = clear`.
Copilot reset commands are still detected from `userPromptSubmitted.prompt`
when the first token is `/clear`, `/reset`, or `/new`, plus the documented
`sessionStart.source = new` lifecycle hint, but interactive Copilot sessions can
also emit `/clear` first as `sessionEnd.reason = user_exit` and only start the
next session on the next prompt. Agent Mux treats that user-driven session end
as reset cleanup too. Terminal stop hooks pause briefly before reaching the
daemon so late reset markers can win, and the daemon then rewrites the
follow-up stop/idle cleanup path to `user_clear` so reset flows clear the tab
without surfacing a stop icon or stop notification. If runtime behavior still
diverges, inspect `amx daemon export-events --current-window --last 50` from
the live tab before changing the delay heuristic.

If the managed hook binary is newer than the running daemon, the next hook event
from that binary automatically restarts the daemon so new indicator/daemon logic
takes effect without a manual restart. Use `amx daemon restart` when you want to
force that refresh immediately, or when debugging a hook path mismatch.

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

Startup is guarded by a single-instance lock next to the socket. Concurrent
hook bursts and duplicate `amx daemon start` calls reuse the serving daemon
instead of creating process storms. `amx daemon restart` asks the current
daemon to shut down, waits briefly for socket removal, then starts one detached
replacement. Daemon startup intentionally avoids transcript maintenance; use
the `amx transcripts ...` commands or the dashboard search path for transcript
storage work.

### Investigating hook event flow

`amx daemon export-events` writes a schema-v4 JSON snapshot for investigation.
Top-level fields describe the export timestamp and daemon health; `windows` is
an array of per-window entries, each carrying its own `stats`, `durable`,
`live`, and `events` fields so a single window can be inspected without paired
map lookups:

```json
{
  "schema_version": 4,
  "daemon": { "pid": 1234, "uptime_seconds": 97 },
  "windows": [
    {
      "window_id": 42,
      "stats": { ... },
      "durable": { ... },
      "live": { "found": true, ... },
      "events": [ ... ]
    }
  ]
}
```

Durable metadata can outlive the live Kitty join after a tab closes;
`live.found` flips to `false` when Kitty/session lookups are no longer
available. Event rows keep the raw `event`/`action` fields plus lifecycle
context such as `tool_name`, `source`, `result_icon_state`,
`current_icon_state`, `notification_type`, `stop_reason`, `reason`,
`prompt_kind`, `reset_flow`, and `suppressed`/`suppression_reason` when
present. Codex screen prompt detection appears as synthetic
`event: "ScreenPromptProbe"` rows with `source: "codex_screen"` and
`prompt_kind` values such as `edit_approval`, `request_user_input`,
`command_approval`, and `permission_grant`.

Codex 0.133.0 and 0.134.0 expose edit approvals and `request_user_input` waits
through TUI/server-request flows rather than normal lifecycle hooks. Agent Mux
keeps the Codex hook installation to supported command hooks and uses a
best-effort Kitty screen fallback to classify visible Codex prompts, including
the 0.134.0 bottom-pane question UI, while preserving terminal, warning, and
error states; the daemon runs that probe every 1.5s.

The primary live-debug workflow is the current tab:

```bash
amx daemon export-events --current-window
amx daemon export-events --current-window --last 25
```

Trim every exported window to its trailing events without changing cumulative
`stats`:

```bash
amx daemon export-events --last 50
```

Inspect reset/session provenance directly from the exported events:

```bash
amx daemon export-events --current-window --last 20 \
  | jq '.windows[0].events[] | {at, event, action, source, reason, stop_reason, notification_type, reset_flow}'
```

`--current-window` resolves the target from `KITTY_WINDOW_ID` and errors when
run outside Kitty (or when combined with `--window`). `--window ID` still works
for explicit targeting, and `--output PATH` writes the JSON to a file instead of
stdout. The export is read-only and does not auto-start the daemon, so the
buffer of a still-running investigation session is never replaced.

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

`color` is required on every profile and sets the project-title color in
the dashboard, picker, detail header, and account chips, as well as the
Kitty tab-bar accent on every session launched under that profile. Harness
identity uses `[harness_emojis]` for the tab-title prefix and
`[harness_colors]` for harness-name cells in the dashboard and launch picker
(see the starter `config.toml`).

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
| ⚠️ | tool failed (recoverable) |
| ❗ | stopped with failure |

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
- **`/clear` still shows a stop icon or stop notification**: inspect
  `amx daemon export-events --window <id>` and confirm the reset sequence is
  arriving with the documented hook payloads for your harness before tuning
  reset-flow timing.
- **Copilot profile is unauthenticated**: run `copilot login` with the matching
  `COPILOT_HOME`.
- **Kitty control fails**: confirm `allow_remote_control yes` and the `listen_on`
  socket setting are present, then restart Kitty.
