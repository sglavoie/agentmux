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
- creates standalone handoff files and ordinary Git worktrees for continuity,
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

### Dashboard views and continuity

The default TUI opens on the live-session dashboard and selects the session
from the Kitty tab that opened it when possible. `/` filters the live-session
list; `S` opens Past Sessions and `P` opens Plans.

The same workflows are scriptable. Machine-readable commands use one
`{"schema_version":1,"data":...}` success envelope; failures use a structured
`error` envelope and ambiguous selectors return candidates instead of guessing.

```bash
amx session list --json
amx session continue codex <session-id>
amx session fork claude <session-id>
amx session fork-worktree codex <session-id> --yes
amx session handoff claude:<session-id> --profile codex-work

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

Press `S` from the dashboard to search past sessions across every configured
profile and project. Press `s` from the dashboard or launcher to open the same
view filtered to the highlighted project. Agent Mux warms the filesystem cache
at `~/.local/share/agent-mux/transcripts.json`, then refreshes it from the
configured Claude, Copilot, and Codex transcript directories in the background.
Existing rows remain visible during refreshes.

The JSON metadata file and `transcripts.search/` sidecars are disposable
caches. If either is missing, malformed, or partially unreadable, Agent Mux
quarantines or rebuilds the affected cache from transcript source files. Stable
`.lock` sidecars coordinate writers across processes, and cache replacement
uses atomic renames. Transcript source files remain authoritative and are not
rewritten by refresh or search.

When multiple profiles point at the same harness transcript home, Agent Mux
scans that home once and stores one metadata row per physical transcript. Those
rows keep a stable display profile but remain searchable under every candidate
profile because older transcript files do not reliably encode the exact launch
profile.

Projects configured with the same directory intentionally share that
directory's transcript history. The first configured project remains the
stable title in unfiltered Past Sessions, while selecting another shared
project alias shows that alias in result rows and carries it through viewing,
continuing, forking, handoff, and deletion prompts. The project picker remains
configuration-backed, so every configured alias stays selectable even when it
is not the stored primary title.

- `/` enters a free-text query; results scan matching source transcripts on
  demand and show the per-session match count.
- `h`, `p`, and `r` open single-value chip pickers for harness, profile,
  and project.
- `enter` opens the transcript viewer; `n`/`N` move between matches,
  `j`/`k`, `g`/`G`, and `Ctrl+d/u/f/b` scroll, `y` copies the selected
  message, and `Y` copies the transcript path.
- `c` continues the selected conversation, `f` forks it, `w` creates an
  isolated Git worktree fork, `H` creates a handoff, and `E` creates an
  editable handoff.
- `d` opens a three-way delete prompt: `i` hides the cache row while keeping
  the source file, and `f` also removes the source transcript after explicit
  confirmation.

Maintenance commands:

```bash
amx transcripts status
amx transcripts compact
amx transcripts rebuild --all
```

`status` is read-only and reports metadata, sidecar, missing-sidecar, and
orphan-sidecar usage. `compact` takes the transcript cache lock, migrates any
legacy inline search text, persists metadata, and then removes only regular
hash-named sidecars that are no longer referenced by a cache key. It preserves
referenced sidecars, transcript source files, temporary files, symlinks,
directories, and unrelated artifacts. `rebuild --all` is the complete reset:
it reparses every discoverable source transcript and recreates the entire
disposable sidecar store. Normal refreshes never compact orphan sidecars.

Retention is controlled by `[defaults.session_archive] keep_days` (default
30). Refresh and prune operations hide cache entries older than `keep_days`;
the on-disk transcript files remain intact unless explicitly deleted. Each
refresh only walks files modified within `scan_max_age_days` days (default:
inherits `keep_days`). Set it to `-1` for an unbounded walk.

### Plans and local storage

Press `P` to search saved plan directories. Agent Mux loads warm rows from the
filesystem cache first and refreshes the configured `plans_directory` in the
background. A missing or malformed cache is quarantined and rebuilt from the
plan directories; an unreadable plan never blanks rows that were read
successfully. In Plans, `/` searches, `enter` opens the viewer, `Y` copies the
plan directory, and the viewer supports `j`/`k`, `g`/`G`,
`Ctrl+d/u/f/b`, and `y` to copy the selected raw line.

Agent Mux does not use SQLite. Local files have deliberately narrow ownership:

| Path | Ownership and cleanup |
| --- | --- |
| `~/.local/share/agent-mux/transcripts.json` and `transcripts.search/` | Disposable metadata and search caches rebuilt from harness transcript files. Malformed metadata is renamed with a `.corrupt-<timestamp>` suffix; orphan sidecars are measured by `transcripts status`, pruned explicitly by `transcripts compact`, or fully reset by `transcripts rebuild --all`. |
| `~/.local/share/agent-mux/plans.json` and `plans.search/` | Disposable metadata and search caches rebuilt from directories under `plans_directory`. |
| `~/.local/share/agent-mux/handoffs/` | Private standalone Markdown artifacts. Successful and partially launched handoffs remain until you delete them. |
| `~/.local/share/agent-mux/worktrees/` | Ordinary registered Git worktrees. Successful launches remain until you remove them with Git; a failed launch removes only the worktree created for that attempt. |
| `~/.local/share/agent-mux/launch_overrides.json` | One-time launcher choices; independent of transcript and plan caches. |
| `daemon.sock`, `daemon.lock`, and `daemon.autostart.lock` | Ordinary daemon coordination files, not database state. |

The corresponding `.lock` sidecars are stable advisory locks and may remain
between runs. Do not delete the whole `agent-mux` data directory to repair a
cache: transcript files, plan directories, handoffs, worktrees, configuration,
and launch overrides have separate lifecycles.

### One-off cleanup after upgrading from a SQLite release

Old installations may still contain retired database artifacts. This is a
manual deployment cleanup, not a recurring migration. First build and pass
`just acceptance`, stop the installed daemon, and confirm `amx daemon status`
no longer reports it running. Preserve the caches and assets in the table
above, then remove only these entries from
`~/.local/share/agent-mux/`:

```text
agent-mux.db
agent-mux.db-wal
agent-mux.db-shm
agent-mux.db.recovery.lock
agent-mux.db.rebuild
agent-mux.db.rebuild-startup.lock
recovery/
```

Install atomically with `just install`, restart with `amx daemon start`, and
verify `amx daemon status`, `amx doctor --strict`, `amx session list`, Past
Sessions, and Plans. Recheck the allowlist above afterward; none of the files
or the `recovery/` directory should be recreated. Future runs never delete
these legacy paths automatically.

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

The hook command is
`amx hook-indicator <event> --harness=<claude|codex|copilot>`. Generated hooks
always include `--harness` so the daemon knows which harness produced the
event, even when the event is routed to a tab owned by another harness. The
flag remains optional for legacy or manual invocations; an omitted or unknown
producer stays explicitly unidentified instead of being guessed as Claude.
`amx config hooks-status` detects older unstamped generated hooks; run
`amx config hooks-sync` to resynchronize them.

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
heartbeats re-assert the lease every 30 seconds.
When the final run finishes, that terminal boundary also retires an older
interactive `bolt`/`compact` fact, so a lost Codex/Claude `Stop` or Copilot
`agentStop` cannot leave `⚡` masking the run's `🛑`; newer activity, pipe-mode
children, subagents, and attention/error states remain protected.
`ASL_AGENT_MUX_NOTIFY=off`
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
same turn; manual `/compact` lands on the stop icon (🛑). Missing or unknown
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
Claude's `Stop` event — and resolves to the stop action (🛑 icon).
`notification` payloads with `notification_type = agent_idle` are treated as
**background-agent** notifications for Copilot and are ignored for main-session
state: they no longer resolve to the stop action, so a background agent going
idle can never falsely stop the interactive turn (the tab keeps its current
`⚡`). Subagent lifecycle hooks (`subagentStart`/`subagentStop`) remain
responsible for the 🤖 subagent icon. This is harness-specific: Claude
`agent_idle` still resolves to stop. Copilot `permissionRequest` fires earlier,
before the
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
to 🛑 instead of silently clearing the tab icon.

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

For delivery and state problems, the first-line workflow is offline-first:

```bash
amx debug status
amx debug tail --last 50
amx debug probe
amx debug explain --source-event <source-event-id>
amx debug bundle --mode share -o agent-mux-debug.tar.gz
amx debug replay agent-mux-debug.tar.gz
```

The commands share selectors for harness, profile, Kitty window, native
session, ASL run, event, trace, request, upstream source event, stage, outcome,
and time range. See [Debugging event delivery](docs/debugging.md) for the
failure matrix, privacy model, bundle guidance, and operator smoke procedure.

`status`, `tail`, `explain`, and `bundle` use schema v2. Their diagnostic reads
capture fixed byte prefixes from journal segments and emergency spool files,
then release the shared storage lock before parsing. Readers never promote
spool files or run retention; the daemon owns promotion every 60 seconds and
retention every 15 minutes. Each v2 result includes `journal_snapshot` counters
for files, bytes, records, matches, duplicates, and corrupt lines scanned.

`tail --last N` merges segment and spool evidence, sorts it by event time, and
only then keeps the newest N records, so a delayed spool write cannot displace
newer evidence. This event-time ordering is pinned by acceptance tests. Exact
`explain` queries are bounded by `--graph-depth 2`,
`--max-nodes 250`, and `--max-edges 1000` by default; JSON exposes the applied
limits and truncation counts, while text output warns when candidates were
pruned. `replay` remains schema v1 and accepts both v1 and v2 diagnostic
bundles and causal-record wrappers. Explanations classify failed delivery,
backend timeout, or focus failure as `notification_failure`; intentional
cancellation, dismissal, policy suppression, and no-interaction outcomes such
as `interaction_timeout` remain `notification_suppression`.

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
`amx_state` schema v4 persists each eligible fact's validated harness-process
fingerprint (`ph` harness, `pp` PID, and opaque `pt` start token) alongside the
schema-v3 producer-pane origin (`ow`) and acknowledgement time (`aa`). A compact
payload marker (`pv:1`) records that process tokens were rendered under the
pinned UTC timezone. Readers retain v1-v4 compatibility; v1-v3 facts and early
v4 payloads without that marker restore without lifecycle ownership and continue
using watchdog fallback.
Acknowledged nonterminal replay cutoffs expire after one hour and are capped at
the newest 64; unacknowledged facts and live ASL leases are never removed by
that cap.
The base title, `★`, and `🔇` remain owned by their existing writers. Hooks only
forward raw events, window/socket context, pipe-mode detection, and the safe
process fingerprint; they auto-start the daemon when needed, log a dropped
delivery, and exit successfully.

Producer identity and title ownership are separate. The producer harness and
origin pane determine event semantics and source identity; the active managed
pane supplies the durable profile/harness metadata and is the only pane that
renders the shared title. Every managed pane still persists its own state, so
focusing another managed pane reveals that pane's reducer state without
cross-contamination.

Because hooks can fire from any pane in a split tab, the daemon applies an
active-pane route policy rather than writing titles from the indicator:

- **F8 (`user_clear`)** clears only the owner's state icon. It is retargeted to
  the managed owner from whichever pane pressed the binding — owner, managed
  non-owner, or unmanaged sibling — so the wipe always lands on the daemon-owned
  title and is never dropped as an owner mismatch. `🔁`, the base title, harness
  emoji, `★`, and `🔇` are all preserved. A tab with no managed owner is a
  successful no-op that creates no durable state.
- **Unmanaged producer routing** lets the active unmanaged pane borrow exactly
  one managed target. A direct user prompt is authoritative focus evidence even
  if the cached Kitty snapshot lags; an exact live ASL candidate also authorizes
  its source pane. Once accepted, the producer/session/origin/target route stays
  valid through that session's terminal event after focus moves. A new inactive
  unmanaged session, an ambiguous target, or CWD/harness similarity alone is
  rejected as `owner_mismatch`.

For the acceptance record, required live-client transitions, and dogfood
checklist, see [Daemon State Acceptance Evidence](docs/daemon_state_acceptance.md).
The hermetic process-exit fixture and optional installed drill are documented in
[Process-Lifecycle Acceptance](docs/acceptance-testing.md).

### Investigating hook event flow

`amx daemon export-events` writes a schema v10 JSON snapshot for investigation.
Version 10 adds the safe `ph`/`pp`/`pt` harness-process fingerprint to eligible
source facts, enriched `HarnessProcessExited` events, and process-exit counters
under `watchdog`. The opaque start token detects PID reuse; command lines,
prompts, arguments, and paths are never included. Version 9 adds causal
trace/request/source-event identities, sanitized reducer
inputs and outcomes, routing evidence, request-scoped saturation, bounded
render generations, notification dispositions, and child-probe links. Raw
payloads, prompts, tool arguments, and transcript text are not included.
Pass `--schema 9` for the previous complete causal contract or `--schema 8` for
the pre-causal contract. Both are explicit compatibility projections: schema 9
cannot expose v10 process fields, and schema 8 cannot expose v9 or v10 fields.

Version 8 retains every v7 field and adds producer-pane origins to events
(`events[].origin_window_id`) and source facts (`shadow.facts[].ow`), plus the
acknowledgement timestamp `shadow.facts[].aa` for acknowledged facts. Version 7
retains every v6 field and adds the interactive-ownership `pm` flag to
pipe-mode source facts (`shadow.facts[].pm`) plus the reducer-wide causal order
`o` used to compare equal-timestamp facts across sources (`shadow.facts[].o`).
Each schema-v10 `shadow.facts[]` entry serializes the reducer's
`SourceFactSnapshot` directly, so it uses that type's
compact wire keys — `k` (source kind), `y` (source key), `i` (icon), `o`
(source-independent causal application order), `ow` (producer pane), `aa`
(acknowledgement time), `ph`/`pp`/`pt` (validated process identity), plus `pm`
— rather than descriptive spellings. The flag is presence-encoded:
`pm: true` marks a fact owned by a non-interactive pipe-mode child (a
`claude -p` / `codex exec` / `copilot -p` process), while an **absent** `pm`
field marks the interactive harness owner — the flag is serialized with
`omitempty`, so `false` is never emitted on the wire and the interactive owner
is signaled by the field's absence, not by `pm: false`. Diagnostics use it to
tell an interactive owner apart from a pipe-mode child sharing the tab, which is
why an interactive idle probe can retire a stale interactive bolt without ever
disturbing live pipe-mode work. Version 6 retains every v5 field and adds the
daemon's global receipt sequence and nanosecond receipt time per event,
normalized source identity (`source_kind`/`source_key`/`source_seq`), the route
decision (`source`, `owner`, or `source_fallback`), the reducer's effective
state and live source facts per window (`shadow`), watchdog reasons, render
generations, IPC queue latency, and the build identity of both the exporting CLI
and the daemon.
Top-level fields describe the export timestamp and daemon health; `windows` is
an array of per-window entries, each carrying its own `stats`, `durable`,
`renderer`, `shadow`, `watchdog`, `live`, and `events` fields so a single
window can be inspected without paired map lookups:

```json
{
  "schema_version": 10,
  "daemon": { "pid": 1234, "uptime_seconds": 97, "ready": true, "build": { ... }, "metrics": { ... } },
  "cli_build": { ... },
  "windows": [
    {
      "window_id": 42,
      "stats": { ... },
      "durable": { ... },
      "renderer": { "desired_icon": "bolt", "state_set": true, "render_failures": 0, "queued_gen": 7, "rendered_gen": 7, "generations": [ { "generation": 7, "source_event_ids": ["run-1:2"], "stage": "rendered", "outcome": "success" } ] },
      "shadow": { "effective_icon": "bolt", "effective_looping": true, "facts": [ { "k": "harness", "y": "claude:sess-pipe", "i": "bolt", "o": 10, "ow": 91, "ph": "claude", "pp": 4321, "pt": "Mon Jul 13 10:11:12 2026", "pm": true }, { "k": "harness", "y": "claude:sess-1", "i": "bolt", "o": 11, "ow": 42, "ph": "claude", "pp": 4322, "pt": "Mon Jul 13 10:15:20 2026" }, { "k": "harness", "y": "claude:sess-old", "i": "bolt", "o": 9, "ak": true, "aa": 1700000000, "ow": 91 } ] },
      "watchdog": { "last_event": "HarnessProcessExited", "last_reason": "pipe_process_exited", "process_exits": 1, "last_process_exit_reason": "pipe_process_exited", "last_process_exit_observation": "missing" },
      "live": { "found": true, ... },
      "events": [ { "at": 1700000001, "event": "Stop", "action": "stop", "result_icon_state": "stop", "trace_id": "…", "request_id": "…", "source_event_id": "run-1:2", "parse_outcome": "decoded", "normalization_outcome": "normalized", "source_kind": "harness", "source_key": "claude:sess-1", "route_decision": "owner", "origin_window_id": 91, "reducer_decision": "applied", "render_generation": 7, "render_outcome": "success" } ]
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
`source: "codex_screen"`, and `reset_flow: true`.

In the example, window 91 produced the Claude event while managed window 42
owns the exported reducer and shared tab title. `origin_window_id` and the
producer-derived `source_key` stay with the producer; `route_decision: "owner"`
records the retargeting. Correctly routed cross-harness events are not
`owner_mismatch` suppressions.

Each harness also has a conservative **stop fallback** for a turn that finished
without a native terminal hook. All three route their synthetic stop through the
same race-safe authoritative-idle path, so each emits a watchdog-shaped row
whose `action` is the **actual reduced icon** (not the requested stop — a probe
masked by a live ASL lease, a newer interactive `⚡`, or a higher-precedence
attention/error icon records the icon that actually won), whose `source` is a
distinct `probe:<harness>` key, and whose `reason` reads
`authoritative idle (<harness>) settled visible state "…"→"…"`. These rows no
longer carry the legacy `result_icon_state`/`current_icon_state` fields. The
three are told apart by event name and source key:

- **Codex** — `event: "ScreenStopProbe"`, `source: "probe:codex_screen"`. Fires
  when an active Codex tab still shows `⚡` but its latest visible status is
  `Worked for …` (completion) or the TUI interrupted marker, with an empty
  composer and no visible prompt.
- **Claude** — `event: "ScreenStopProbe"`, `source: "probe:claude_screen"`
  (same event name as Codex, distinct source). Fires on a stuck Claude `⚡`
  whose composer is idle (two consecutive samples) or that shows Claude's
  interruption marker (one sample). Native `Notification/idle_prompt` is the
  primary Claude stop; this screen probe is the interruption/no-hook fallback.
- **Copilot** — `event: "TranscriptStopProbe"`,
  `source: "probe:copilot_transcript"`. Reads no screen: it consults the
  interactive session transcript's latest state-bearing event and settles only
  on a terminal record. Native `agentStop`/`sessionEnd` remain the primary
  Copilot stop.

Codex 0.133.0 and 0.134.0 expose edit approvals and `request_user_input` waits
through TUI/server-request flows rather than normal lifecycle hooks. Agent Mux
keeps the Codex hook installation to supported command hooks and uses a
best-effort Kitty screen fallback to classify visible Codex prompts, including
the 0.134.0 bottom-pane question UI, while preserving terminal, warning, and
error states. A separate conservative stop-only fallback recognizes Codex's
cleared idle UI after `/clear` when full Kitty scrollback no longer contains a
transcript. The per-harness stop fallbacks described above (Codex/Claude
`ScreenStopProbe`, Copilot `TranscriptStopProbe`) repair a turn that finished
without a native terminal hook so the icon settles on `🛑` instead of staying
`⚡`. Each fires only on a stuck `bolt` tab and never downgrades protected
attention states (`key`, `question`, `error`, `warning`, `subagent`, `compact`),
and never retires pipe-mode, subagent, or live ASL facts. The daemon runs these
probes on the same periodic probe loop as the renderer tick (every 2s).
In split Kitty tabs, screen probes read only the tab-title owner: the active
managed pane, or the first managed pane if none is active. Sibling panes cannot
overwrite the shared title with Claude context-limit detection or Codex
`ScreenPromptProbe`, `ScreenClearProbe`, or `ScreenStopProbe` events.

The primary debug workflow starts from persisted evidence and remains useful
when Kitty or the daemon is unavailable:

```bash
amx debug status
amx debug tail --window "$KITTY_WINDOW_ID" --last 25
amx debug explain --window "$KITTY_WINDOW_ID"
amx debug bundle --window "$KITTY_WINDOW_ID" --mode share
amx debug replay agent-mux-debug-*-share.tar.gz
```

Use `amx debug probe --window "$KITTY_WINDOW_ID"` for a live, read-only
transport/routing check. `export-events` remains the compatibility and daemon
snapshot surface:

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

### Diagnostic and native-log privacy

Diagnostic state lives at `$XDG_STATE_HOME/agent-mux`, or
`~/.local/state/agent-mux` when `XDG_STATE_HOME` is unset. Journals contain
bounded causal metadata and local-key payload HMACs. Daemon logs are structured.
Both use private directories (`0700`) and files (`0600`).

AgentMux-managed sessions enable native harness debug logging by default under
`native/<launch-id>/`. These logs are raw local data and may contain prompts,
tool arguments, paths, source text, and provider output. Defaults retain them
for 14 days, cap aggregate native storage at 1024 MiB, and cap one launch at
128 MiB. Set `[defaults.diagnostics] native_logging = false` to opt out.
Explicit harness logging controls take precedence and are diagnosed as
external, degraded, or disabled when Agent Mux cannot enforce their path,
level, permissions, or retention.

Raw hook payload capture is off by default and requires an explicit, temporary
`amx debug capture start --include-hook-payloads` authorization. Raw captures
and raw native logs are never included in bundles. Share bundles use strict
field allowlists, HMAC identifiers, path aliases, and controlled native-log
excerpts. Forensic bundles remain redacted but are marked local-only and must
not be shared. See [Debugging event delivery](docs/debugging.md) for quotas,
capture expiry, bundle modes, and sharing guidance.

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

`just acceptance` runs the full Go suite, race-sensitive transcript, plan,
handoff, worktree, daemon, and TUI tests, `go vet`, and every committed TUI
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

- **Icons, hooks, or cached transcript data look wrong**: run `amx debug status`
  first, then `amx debug explain` with the trace/request/source-event ID from
  `amx debug tail`. `amx doctor` adds live checks for daemon reachability and
  binary freshness, `kitten @ ls`, hooks for every configured
  profile, renderer drift, known Kitty sockets, recent suppression reasons,
  and the reducer/renderer health surface. Use `amx transcripts status` to
  inspect the filesystem transcript cache and `amx transcripts rebuild --all`
  to reconstruct it from source transcripts.
  `amx doctor --json` emits a structured report; `--strict` also evaluates the
  correctness invariants (reducer/renderer agreement, ASL visibility, subagent
  visibility, producer routing, acknowledged-fact pressure, persisted process
  identity, render-generation drift, queue saturation, build identity) and
  exits 2 on any failure. The
  stable routing/retention checks are `owner_mismatch_prompt` (failure for any
  recent prompt mismatch), `owner_mismatch_burst` (warning for at least three
  recent non-prompt mismatches on one window), `unknown_producer_fact`
  (warning), and `acknowledged_fact_pressure` (warning at 49–64 evictable
  acknowledged facts; failure above 64). `malformed_process_identity` fails
  when an exported persisted fact contains a partial, invalid, or
  source-incompatible process fingerprint; it does not perform a live process
  scan. Their event window is 15 minutes.
- **Suspected intermittent state bugs**: preserve a causal snapshot with
  `amx debug bundle --mode share`, validate it with `amx debug replay`, then run
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
- **Doctor reports `owner_mismatch_prompt`, `owner_mismatch_burst`, or
  `unknown_producer_fact`**: run `amx config hooks-status` and resynchronize
  with `amx config hooks-sync` if generated commands lack `--harness`. Then
  inspect both the managed target and producer panes with
  `amx daemon export-events --window <id> --last 50`: a routed row should keep
  the producer's `origin_window_id` and `source_key` while recording
  `route_decision: "owner"`. A new inactive unmanaged session should remain
  suppressed; a direct prompt from the focused pane should not.
- **Doctor reports `acknowledged_fact_pressure`**: inspect
  `shadow.facts[]` in `amx daemon export-events`. Schema-v3 `aa` timestamps mark
  acknowledged replay cutoffs; nonterminal cutoffs are retained for one hour
  and capped at 64. Live ASL leases and unacknowledged facts are intentionally
  exempt. A failure above 64 means the running daemon has not applied current
  compaction logic; restart from the installed current binary.
- **The bolt icon stays after the agent is done**: run `amx config hooks-status`
  and `amx config hooks-sync` to restore terminal hooks like
  `sessionEnd`/`Stop`/`agentStop` and the child-complete `TeammateIdle` hook.
  Current releases also recover across daemon restarts on the next cleanup
  event. For Codex, a completed-turn screen fallback also settles a stuck bolt
  on `🛑` when the tab shows `Worked for …` with an empty composer; look for
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
