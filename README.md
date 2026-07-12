# Agent Mux

Kitty-first project workbench for **Claude Code**, **GitHub Copilot CLI**, and
**OpenAI Codex**. It groups projects, worktrees, conversations, plans,
handoffs, search, and activity; launches work as Kitty tabs; keeps per-profile
isolated homes (`CLAUDE_CONFIG_DIR`, `COPILOT_HOME`, `CODEX_HOME`); and uses an
always-on local daemon to own durable tab-state rendering. Harness hooks are
thin, fire-and-forget event clients.

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

## Usage

### Project workbench and continuity

The default TUI opens on a project-centric workspace home. It groups configured
launch variants, linked Git worktrees, live Kitty tabs, indexed conversations,
explicitly linked plans, and durable meaningful activity. `:` opens the global
context-aware command palette, `/` opens unified full-text search, and `v`
switches to the legacy live-session power view.

The same workflows are scriptable. Machine-readable commands use one
`{"schema_version":1,"data":...}` success envelope; failures use a structured
`error` envelope and ambiguous selectors return candidates instead of guessing.

```bash
amx workspace list --json
amx workspace discover
amx workspace adopt <candidate> --profile work
amx workspace adopt <candidate> --profile work --yes

amx session continue codex <session-id>
amx session fork claude <session-id>
amx session fork-worktree codex <session-id> --yes
amx session handoff claude:<session-id> --profile codex-work

amx plan link <plan-path-or-unique-name> <workspace>
amx plan unlink <plan-path-or-unique-name> <workspace>
amx search "migration fallback" --type conversation --type plan
```

`session continue` focuses the exact live Kitty owner when one exists and
otherwise resumes the closed conversation. Native forks are used for Claude
and Codex. Copilot forks use a portable handoff because Copilot does not expose
native fork semantics. Worktree forks create a tracked isolated Git worktree
and never copy uncommitted changes unless `--allow-dirty` is explicitly
confirmed.

### Dashboard

```bash
amx
```

This opens the dashboard, or focuses the existing dashboard tab if one is
already running. When opened with the bundled `Ctrl+Shift+D` binding, the
dashboard selects the running session from the Kitty tab that triggered it;
an unmanaged source tab leaves the current/default dashboard selection alone.

Key flows:

- `l`: open the launcher
- `r`: restart the selected running session, after confirmation
- `c`: restart the selected running session under another profile
- `C`: launch a new session from the selected row, with profile selection first
- `s`: search past session transcripts for the selected project
- `x`: clear the selected session's status icon
- `X`: close the selected running session
- `o`: open the selected project's directory in a plain shell tab
- `p`: toggle the preview pane
- `d`: show the per-session hook event log (scrollable detail pane)
- `S`: search past session transcripts across every configured profile
- `P`: search saved agent plans; search and viewer screens show the selected
  plan directory path, `Y` copies the full path, and the plan viewer wraps long
  lines, supports less-style `Ctrl+d/u/f/b` paging, and `y` copies the selected
  raw line
- `?`: show the full keymap

Rows marked with `🔁` have at least one active `agent-session-loop` run whose
workspace matches the project row. Loop matching canonicalizes the workspace
path, prefers the same harness when a matching managed Kitty tab or row is open,
and falls back to legacy CWD-only matching for older loop state or unknown
harnesses. Agent Mux also mirrors `🔁` into the Kitty project tab that owns the
loop while it is active, preserving existing state, star, and mute icons.
Finished runs stop counting as active when loop discovery sees
`run_finished_at` in `state.json`, and the `AgentSessionLoopFinished` hook
clears active-class icons while reconciling the tab-title `🔁` marker.
Run and tail loops outside the dashboard with `agent-session-loop` directly.

The dashboard keeps the profile badge and harness metadata separate from the
title column, so visible titles are just the project name.

A sticky status row occupies the final terminal line for health, active mode
summaries, and transient action messages; keybinding hints stay immediately
above it.

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
- `s` searches past session transcripts for the highlighted project,
- `C` chooses a **one-time profile override** for the highlighted project,
- `o` opens the highlighted project's directory instead of launching it.

Overrides are useful for cross-harness launches. For example, a project such as
`sis` can keep `profile = "vta"` as its Claude default while `C` launches that
same project under a Codex profile such as `vta-codex`.

Overrides only affect launcher behavior. They do not rewrite `config.toml`.
Dashboard `C` uses the same profile picker, but launches from the highlighted
running row instead of from the launcher list. Pressing `enter` in the launcher
is an explicit request for new tab(s): if the selected project/profile is
already running, Agent Mux opens another instance instead of skipping or
focusing the existing tab.

### Past sessions

Press `S` from the dashboard to open the past-session search across every
configured profile and project. Press `s` from the dashboard or launcher to
open the same search filtered to the highlighted project. During dashboard
startup, amx loads the SQLite conversation index in the background and then
starts a slower scan of each profile's transcript directory (Claude
`~/.claude/projects`, Copilot `~/.copilot/session-state`, Codex
`~/.codex/sessions`). Opening past-session search usually shows the warmed
cache immediately; while a scan is still running, existing rows stay visible
and the header shows the scan status. Active metadata and search documents live
in `~/.local/share/agent-mux/agent-mux.db`. The former `transcripts.json` and
`transcripts.search/` files remain untouched rollback inputs for this
compatibility release; the daemon never loads them on its request path.

When multiple configured profiles point at the same harness transcript home,
amx scans that home once and stores one metadata row per physical transcript.
Those rows keep a stable display profile but are searchable under every
candidate profile because older transcript files do not reliably encode the
exact launch profile.

- `/` enters a free-text query; results stream the matching JSONL on demand
  and show the per-session match count.
- `h`, `p`, `r` open single-value chip pickers for harness, profile, and
  project; press `/` inside a chip picker to filter its options.
- `enter` opens the transcript viewer (role-coded lines, visible transcript
  path, `n`/`N` jumps between matches, `j`/`k`, `g`/`G`, and `Ctrl+d/u/f/b`
  for scrolling, `y` copies the selected message text, `Y` copies the full
  transcript path). Search results also show the selected transcript path and
  support `Y` path copy.
- `d` opens a three-way delete prompt: `i` hides the row from the index only
  (file kept on disk; rescans do not resurrect it), `f` also removes the
  underlying JSONL file (irreversible).

Maintenance commands:

```bash
amx index status
amx index status --json
amx index rebuild
amx transcripts status
amx transcripts compact
amx transcripts rebuild --all
```

`index status` reports database integrity, schema/migration and compatibility
import state, FTS freshness, secure filesystem modes, entity counts, and the
running daemon's activity-writer health. Once the schema is current, ordinary
`index status`, `transcripts status`, and `doctor` inspection is read-only: it
does not synchronize compatibility files, repair search documents, or run the
FTS5 integrity-check. Its FTS freshness result is a read-only comparison of
search documents with FTS docsize rows; `fts_integrity_verified: false` means
that count-consistent FTS corruption can still be present. Run `amx doctor
--strict` for the authoritative FTS5 integrity-check, or run `amx index
rebuild` when an explicit derived-index repair is also appropriate; both
commands verify the external-content FTS index.

Missing search documents are split into two categories. A **repairable** row
still has a surviving transcript, plan, handoff artifact, or durable activity
record from which search content can be reconstructed. An **unavailable
placeholder** has lost its source artifact; Agent Mux retains its durable
metadata, reports a warning, and does not make an otherwise consistent store
unhealthy. `index rebuild` is the explicit repair surface: it refreshes derived
conversation, plan, project-search, handoff-search, activity-search, and
discovery projections, verifies the external-content FTS index, and reports
anything that remained unavailable. It fingerprints explicit plan links,
handoff metadata, activity/read history, workspace conversation membership,
and managed-worktree relationships before and after the rebuild and fails if
protected state changes. The `transcripts` commands remain compatibility tools
for the older transcript-only projection.

Every long-running process owns one datastore pool. CLI commands open one pool
at command entry, the daemon activity worker owns one for its lifetime, and the
TUI injects one process-scoped pool into transcript, plan, workspace, activity,
and unified-search services. Routine dashboard refresh projects from an
immutable synchronized workspace catalog and performs only reads; configuration
or Git discovery writes occur only at the explicit workspace synchronization
boundary. See [`docs/datastore_ownership.md`](docs/datastore_ownership.md).

Legacy JSON indexes and search sidecars are retained for one compatibility
release. They can be removed later only after every supported prior version can
upgrade directly, field experience shows SQLite fallback is no longer needed,
and a documented database backup/export path exists. `amx index rebuild` never
deletes them. Normal startup compares normalized metadata plus sidecar
size/mtime and reads only changed sidecar bodies; an unchanged warm startup
does not begin a compatibility write transaction. Removing a rollback entry
does not delete the durable SQLite row. A binary also refuses a future schema
version instead of attempting a downgrade.

Before an operator-data repair, install the intended binary, capture status,
and make an online SQLite backup. Review the reported repairable and unavailable
counts before running the rebuild:

```bash
amx_bin="$HOME/.local/bin/amx"
db="$HOME/.local/share/agent-mux/agent-mux.db"
backup="${db}.$(date +%Y%m%d-%H%M%S).bak"

"$amx_bin" index status --json | tee /tmp/amx-index-before.json
sqlite3 "$db" ".backup '$backup'"
chmod 600 "$backup"

# Explicit operator-confirmed repair; do not automate this step.
"$amx_bin" index rebuild
"$amx_bin" index status --json | tee /tmp/amx-index-after.json
```

Keep the backup until the before/after entity counts, import records, protected
state hash, and FTS health are satisfactory. Unavailable-placeholder warnings
may remain after a successful rebuild when their source artifacts no longer
exist; restore the source or keep the placeholder. To reverse the database
change, stop Agent Mux processes, preserve the repaired database for diagnosis,
and restore the backup as `agent-mux.db` with mode `0600` before restarting.

If the initial status command itself reports `database disk image is
malformed`, do not proceed directly to rebuild and do not rely on `.backup`
being able to traverse the damaged B-tree. Stop the daemon first and preserve
the database plus any WAL/SHM companions byte-for-byte for recovery analysis:

```bash
"$amx_bin" daemon stop
recovery="$HOME/.local/share/agent-mux-recovery-$(date +%Y%m%d-%H%M%S)"
mkdir -m 700 "$recovery"
cp -p "$db" "$recovery/"
for companion in "$db-wal" "$db-shm"; do
  if [ -e "$companion" ]; then cp -p "$companion" "$recovery/"; fi
done
```

Only after that preservation step should an operator choose between an
explicit `"$amx_bin" index rebuild` attempt (appropriate when diagnostics
isolate corruption to derived FTS state) and lower-level SQLite recovery.

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

`agent-session-loop` / `asl` uses that same hook endpoint when `amx` is on
`PATH`. It emits `AgentSessionLoopStarted`, `AgentSessionLoopTurnStarted`,
`AgentSessionLoopTurnHeartbeat`, `AgentSessionLoopTurnFinished`,
`AgentSessionLoopVerificationStarted`, `AgentSessionLoopVerificationHeartbeat`,
`AgentSessionLoopVerificationFinished`, `AgentSessionLoopPaused`, and
`AgentSessionLoopFinished` with JSON that includes `source`, `reason`, `run_id`,
`plan_dir`, `cwd`, `state_path`, and `harness`. Every event carries the run's
`run_id` and a persisted monotonic source sequence, so the daemon keys one
lease per run: overlapping runs in one workspace hold independent facts, a
run's turn/verification phases render ⚡🔁, between-turns and paused states
keep 🔁, and one run finishing never changes another run's state (verified
live with two overlapping runs in a single window). Turn and verification
heartbeats re-assert the lease every 30 seconds. `ASL_AGENT_MUX_NOTIFY=off`
disables the emission entirely; the ASL test suite forces it off so nested
test runs never leak events into a live daemon (also verified live). Start payloads use the run's
top-level harness; turn-start payloads use `active_turn.harness`; finish
payloads use the last completed turn harness when present. A mid-run harness
switch moves and clears the loop marker on the matching project tab. Agent Mux
also reads `state_path` when a lifecycle payload needs to recover `cwd` or
harness, using `active_turn.harness` before top-level `harness`.

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

Codex's `PostCompact` hook is classified from its `trigger` payload. Automatic
mid-turn compaction restores the active bolt (⚡) because Codex continues the
same turn; manual `/compact` lands on the stop icon (⏹). Missing or unknown
trigger values keep the conservative terminal behavior for compatibility.

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
as reset cleanup too. The daemon defers reset-sensitive terminal stops so late
reset markers can win, then rewrites the
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
amx doctor
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

The daemon is the only writer for state icons and the `🔁` loop marker. Its
state lives in each Kitty window's `amx_state` user var, so a restart restores
the intended state and the renderer converges the live title on its next pass.
The base title, `★`, and `🔇` remain owned by their existing writers. Hooks only
forward raw events, window/socket context, and pipe-mode detection; they
auto-start the daemon when needed, log a dropped delivery, and exit successfully.

Because F8 and agent-session-loop hooks can fire from any pane in a split tab,
the daemon retargets them to the tab's managed title owner rather than writing
titles from the indicator:

- **F8 (`user_clear`)** clears only the owner's state icon. It is retargeted to
  the managed owner from whichever pane pressed the binding — owner, managed
  non-owner, or unmanaged sibling — so the wipe always lands on the daemon-owned
  title and is never dropped as an owner mismatch. `🔁`, the base title, harness
  emoji, `★`, and `🔇` are all preserved. A tab with no managed owner is a
  successful no-op that creates no durable state.
- **Active-loop sibling routing** lets an `agent-session-loop` running in an
  *unmanaged* split pane drive its managed owner's state machine. The daemon
  routes the sibling's ordinary hooks (bolt, stop, permission, failure, compact,
  question, subagent) to the owner only when loop discovery returns a live,
  unfinished candidate whose window ID exactly matches the source pane. CWD or
  harness similarity alone never authorizes routing, so ordinary sibling agents
  and inactive managed panes keep their `owner_mismatch` suppression.

For the acceptance record, required live-client transitions, and dogfood
checklist, see [Daemon State Acceptance Evidence](docs/daemon_state_acceptance.md).

### Investigating hook event flow

`amx daemon export-events` writes a schema-v6 JSON snapshot for investigation.
Version 6 retains every v5 field and adds the daemon's global receipt sequence
and nanosecond receipt time per event, normalized source identity
(`source_kind`/`source_key`/`source_seq`), the route decision (`source`,
`owner`, or `source_fallback`), the reducer's effective state and live source
facts per window (`shadow`), watchdog reasons, render generations, IPC queue
latency, and the build identity of both the exporting CLI and the daemon.
Top-level fields describe the export timestamp and daemon health; `windows` is
an array of per-window entries, each carrying its own `stats`, `durable`,
`renderer`, `shadow`, `watchdog`, `live`, and `events` fields so a single
window can be inspected without paired map lookups:

```json
{
  "schema_version": 6,
  "daemon": { "pid": 1234, "uptime_seconds": 97, "ready": true, "build": { ... }, "metrics": { ... } },
  "cli_build": { ... },
  "windows": [
    {
      "window_id": 42,
      "stats": { ... },
      "durable": { ... },
      "renderer": { "desired_icon": "bolt", "state_set": true, "render_failures": 0, "queued_gen": 7, "rendered_gen": 7 },
      "shadow": { "effective_icon": "bolt", "effective_looping": true, "facts": [ ... ] },
      "watchdog": { "last_event": "...", "last_reason": "..." },
      "live": { "found": true, ... },
      "events": [ ... ]
    }
  ]
}
```

The full export can exceed 1 MiB on a busy daemon; the IPC client accepts
responses up to 64 MiB, and `--window`/`--current-window` bound the payload to
one window when investigating a single tab.

Durable metadata can outlive the live Kitty join after a tab closes;
`live.found` flips to `false` when Kitty/session lookups are no longer
available. `stats.suppression_reasons` aggregates suppressed transitions by
reason, while `renderer` reports desired icon/loop state (`looping_set` shows
when the daemon has explicitly taken ownership), the known Kitty
socket, and any consecutive render failure with its last error. Event rows keep
the raw `event`/`action` fields plus lifecycle
context such as `tool_name`, `source`, `result_icon_state`,
`current_icon_state`, `notification_type`, `stop_reason`, `reason`,
`prompt_kind`, `reset_flow`, and `suppressed`/`suppression_reason` when
present. Codex screen prompt detection appears as synthetic
`event: "ScreenPromptProbe"` rows with `source: "codex_screen"` and
`prompt_kind` values such as `edit_approval`, `request_user_input`,
`command_approval`, and `permission_grant`. Codex idle `/clear` recovery can
appear as `event: "ScreenClearProbe"` with `action: "user_clear"`,
`source: "codex_screen"`, and `reset_flow: true`. A stuck active Codex tab
whose turn finished without a `Stop` hook appears as
`event: "ScreenStopProbe"` with `action: "stop"`, `source: "codex_screen"`,
`result_icon_state: "stop"`, and `current_icon_state: "bolt"`.

Codex 0.133.0 and 0.134.0 expose edit approvals and `request_user_input` waits
through TUI/server-request flows rather than normal lifecycle hooks. Agent Mux
keeps the Codex hook installation to supported command hooks and uses a
best-effort Kitty screen fallback to classify visible Codex prompts, including
the 0.134.0 bottom-pane question UI, while preserving terminal, warning, and
error states. A separate conservative stop-only fallback recognizes Codex's
cleared idle UI after `/clear` when full Kitty scrollback no longer contains a
transcript. A third conservative fallback repairs a stuck bolt on a completed
turn: when an active Codex tab still shows the bolt icon but its latest visible
status is `Worked for …` with an empty composer and no visible prompt, the
daemon drives the existing `stop` transition (`event: "ScreenStopProbe"`) so the
icon settles on `⏹` instead of staying `⚡`. It only fires on bolt tabs and never
downgrades protected attention states (`key`, `question`, `error`, `warning`,
`subagent`, `compact`). The daemon runs these probes every 1.5s.
In split Kitty tabs, screen probes read only the tab-title owner: the active
managed pane, or the first managed pane if none is active. Sibling panes cannot
overwrite the shared title with Claude context-limit detection or Codex
`ScreenPromptProbe`, `ScreenClearProbe`, or `ScreenStopProbe` events.

The primary live-debug workflow is the current tab:

```bash
amx daemon export-events --current-window
amx daemon export-events --current-window --last 25
amx doctor
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
inactive Kitty tab-bar accent on every session launched under that profile.
Active tab colors come from `kitty.conf` so the focused tab remains prominent.
Harness identity uses `[harness_emojis]` for the tab-title prefix and
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
just acceptance
just check
```

`just acceptance` runs the full Go suite, race-sensitive datastore, workspace,
daemon, and TUI datastore-lifecycle tests, `go vet`, and every committed TUI
scenario. `just check` adds the repository linter and is the final local/CI
gate. The end-to-end coverage map is documented in
[`docs/acceptance.md`](docs/acceptance.md).

### TUI layout regression harness

A hidden developer command, `amx tui-test`, drives the dashboard model
in-process against built-in fixtures to catch overlay/modal sizing regressions
(the actions overlay and profile picker width bugs, for example). It never
launches Kitty and never touches your real config or data. See
[`docs/tui-testing/README.md`](docs/tui-testing/README.md) for the command
surface, scenario schema, and committed example scenarios, or run the examples
directly:

```bash
amx tui-test run docs/tui-testing/scenarios/*.json
```

## Troubleshooting

- **Icons, hooks, or indexed data look wrong**: run `amx doctor` first. It checks daemon
  reachability and binary freshness, `kitten @ ls`, hooks for every configured
  profile, renderer drift, known Kitty sockets, recent suppression reasons,
  SQLite integrity/migrations/imports/FTS freshness, private file modes, and
  activity-writer health. Use `amx index status` for the index-only view and
  `amx index rebuild` to refresh derived data without deleting rollback files
  or durable links/history.
  `amx doctor --json` emits a structured report; `--strict` also evaluates the
  correctness invariants (reducer/renderer agreement, ASL visibility, subagent
  visibility, render-generation drift, queue saturation, build identity) and
  exits 2 on any failure.
- **Suspected intermittent state bugs**: run
  `amx daemon audit --duration 10m --json`. It samples the invariants every
  two seconds for the duration, deduplicates findings, and exits 2 if any
  failure-severity violation was observed in any sample. The daemon must stay
  up for the whole window — a restart mid-audit records `daemon_unreachable`.
- **Deploying a new build**: use `just install` (stages the binary beside the
  destination and renames it into place, so the replacement is atomic) or
  `just install-restart-smoke` (install, restart the daemon, wait for
  `ready: true`, then gate on `amx doctor --strict`, which verifies the CLI
  and daemon are the same artifact by build hash). Durable per-window state is
  restored from `amx_state` Kitty user vars across the restart; restore
  republishes each window's reduced state, so a terminal icon whose
  unacknowledged fact aged past the one-hour tombstone retention while the
  daemon was down clears on restart instead of pinning stale state.
- **No projects appear**: run `amx config validate` and confirm the config is
  at `~/.config/agent-mux/config.toml`.
- **Hooks are missing**: run `amx config hooks-status`, then
  `amx config hooks-sync`.
- **The bolt icon stays after the agent is done**: run `amx config hooks-status`
  and `amx config hooks-sync` to restore terminal hooks like
  `sessionEnd`/`Stop`/`agentStop` and the child-complete `TeammateIdle` hook.
  Current releases also recover across daemon restarts on the next cleanup
  event. For Codex, a completed-turn screen fallback also settles a stuck bolt
  on `⏹` when the tab shows `Worked for …` with an empty composer; look for
  `event: "ScreenStopProbe"` in `amx daemon export-events --window <id>` to
  confirm it fired. In split panes, run the export against the managed
  tab-title owner because sibling panes do not drive screen-probe title changes.
  For Copilot, also upgrade to version `1.0.45` or newer, because older
  releases had `agentStop` hook issues.
- **`/clear` still shows a stop icon or stop notification**: inspect
  `amx daemon export-events --window <id>`. Claude/Copilot should show the
  documented hook reset payloads; Codex may show `ScreenClearProbe` when idle
  `/clear` happens without an immediate hook. If neither appears, confirm the
  tab is managed by Agent Mux and Kitty `get-text` remote control works.
- **The loop marker stays after a run is finished**: inspect
  `amx daemon export-events --current-window --last 50` for
  `AgentSessionLoopFinished`, then inspect the loop `state.json` for
  `run_finished_at`. If the marker remains, confirm no other active loop state
  still matches the same workspace and harness. Legacy loop state without a
  recognized harness still matches by CWD, and if no same-harness managed Kitty
  tab is open, Agent Mux falls back to the same workspace rather than updating an
  unrelated caller tab.
- **F8 does nothing from a split pane**: F8 clears the tab's managed state icon
  regardless of which pane is focused. Inspect
  `amx daemon export-events --window <owner-id>` (the managed tab-title owner,
  not the pane you pressed F8 in) and confirm a non-suppressed `user_clear`
  event on the owner. If the owner shows `owner_mismatch` for `user_clear`, the
  tab has no managed owner or the daemon predates the routing fix — run
  `amx daemon restart` after `just install`.
- **A loop running in a split pane doesn't drive the tab**: an
  `agent-session-loop` started in an *unmanaged* sibling pane only routes to the
  managed owner when loop discovery finds a live, unfinished candidate whose
  window ID matches that pane exactly. Inspect
  `amx daemon export-events --window <owner-id>` and confirm the loop's states
  land on the owner (not `owner_mismatch` on the source pane). If the marker or
  states stay put, confirm the loop's `state.json` has no `run_finished_at`, the
  PID is still live, and the source pane's window ID matches the discovered
  candidate.
- **Daemon can't reach Kitty after an F8-triggered auto-start**: check that the
  window's renderer metadata records a `unix:`/`tcp:` socket, never `fd:`.
  `amx doctor` reports known Kitty sockets and renderer drift; a `fd:` value or
  render failures indicate a stale binary — `just install` then
  `amx daemon restart`.
- **Copilot profile is unauthenticated**: run `copilot login` with the matching
  `COPILOT_HOME`.
- **Kitty control fails**: confirm `allow_remote_control yes` and the `listen_on`
  socket setting are present, then restart Kitty.
