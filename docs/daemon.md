# Daemon

The daemon owns runtime-session state transitions and notifications:

```bash
amx daemon start
amx daemon restart
amx daemon status
amx daemon stop
amx daemon export-events
amx doctor
```

`amx daemon status --json` emits the same readiness, build, notification, and
ASL facts in the schema-v1 envelope, plus `asl_reconcile` when the daemon
reports it. A stopped daemon is still reported in full (`"alive": false` under
`--json`), and the command then exits 1 in both output modes, so a script can
test `amx daemon status` directly instead of matching its first line.

`amx daemon restart` returns once the new daemon answers on its socket and
names its pid (`daemon restarted (pid 4242)`), adding the degraded reason when
it came up not ready. A daemon that has not answered within 10 seconds fails
the restart, so a command run right after it never meets a missing socket.
It also starts a daemon when none is running, which is why every command that
finds no daemon names `amx daemon restart` (`amx daemon start` runs one in the
foreground). `amx daemon stop` with no daemon running fails (`daemon_not_running`) and says
`daemon is not running; nothing to stop`, without the socket error.

For delivery and state problems, the first-line workflow is offline-first:

```bash
amx debug status
amx debug tail --last 50
amx debug probe
amx debug explain --source-event <source-event-id>
amx debug bundle --mode share -o agent-mux-debug.tar.gz
amx debug replay agent-mux-debug.tar.gz
```

The commands share selectors for harness, profile, daemon slot, runtime
session, ASL run, event, trace, request, upstream source event, stage, outcome,
and time range. See [Debugging event delivery](debugging.md) for the
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

Startup also installs the screen classifier's UI-copy patterns, once, on the
startup goroutine before any probe goroutine can read them. Each pattern set
starts from the copy embedded in the binary and is replaced wholesale by
`~/.config/agent-mux/screenstate/<name>.toml` when that file exists and
validates. The load never fails the daemon: an unreadable, undecodable or
invalid override keeps the embedded default and is reported instead, as the
`screenstate_override_invalid` doctor finding. Because the install is one-shot
and there is no file watcher, editing a pattern file takes effect on
`amx daemon restart`. The health response carries the resulting per-set active
source and warnings, which is where `amx doctor` reads them from; a daemon
predating the block reports nothing and `amx doctor` says so rather than
claiming the embedded default. See
[Screen classifier patterns](watchdog.md#screen-classifier-patterns).

The daemon owns reducer state for each registered runtime session. It writes a
`runtime-state.json` checkpoint beside the runtime-session registry and verifies
the session and tmux server generation before restoring a saved entry. The tmux
status writer renders the resulting icon and `🔁` loop marker on the session's
pane. The checkpoint and status update are part of runtime maintenance.

A managed hook names its runtime session. The daemon resolves that identity to
the registered slot, then applies the event to that slot's reducer. Hooks carry
the event, session context, and safe harness-process fingerprint; they do not
choose a pane by title or by a guessed working directory. An ASL hook from a
managed session likewise claims the run for that exact runtime session.

The state-query response includes each slot's icon, loop marker, last action,
last event, and guard action, with optional busy, unseen, reason, branch, and
conversation fields. `presentation_changed_at_ms` is a Unix-millisecond
ordering signal and is omitted when unknown. Clients clear a stale local value
when the field or entire slot is absent.

The dashboard and overview expose **Clear status** and **Mark active** for the
selected session. Clear acknowledges the visible reducer facts; a live ASL
lease keeps its independent `🔁` marker. Mark active records a temporary `⚡`
from `probe:user_active` until a real hook, ASL event, clear, or staleness
supersedes it. Screen probes read the registered tmux pane and can settle a
stuck active icon only from fresh evidence for that pane and server generation.

For the process-exit fixture, see
[Process-Lifecycle Acceptance](process-lifecycle-fixture.md).

## Companion subscription

A long-lived observer — the native menu bar app, or `amx debug subscribe` —
sends one `subscribe` request and then reads server-to-client frames on the same
connection: `snapshot` (the whole companion state, replaced wholesale),
`heartbeat` every 15s, and a final `bye` whose reason is `shutdown` on a daemon
stop or restart and `slow_consumer` when the subscriber fell behind. A stream
that ends without a `bye` ended by accident.

Snapshots are built by the daemon's companion hub. Every publish path that can
change what a companion shows rings a non-blocking signal; the hub waits 100ms
for the burst to settle, copies the state in one short critical section, and
projects outside it, so a subscriber never slows hook acknowledgement. An
unchanged snapshot is not republished, and a 5s safety tick covers facts that
change with the clock alone (such as quiet hours).

```bash
amx debug subscribe --frames 3        # one line per frame
amx debug subscribe --json            # ack, then one schema-v1 record per frame
amx daemon status                     # companion: N subscribers (client version, ...)
```

### Frames and versioning

The subscribe request is `{"type":"subscribe","subscribe":{protocol, client,
client_version, amx_build, features, notification_backend,
notifications_authorized}}`. The identifying fields are reported in health and
doctor and never trusted for a decision. The response line is the ack,
`{protocol, server_protocol_max, subscriber_id, heartbeat_ms, features}`: the
daemon speaks the lower of the subscriber's `protocol` and its own
`CompanionProtocolVersion` (today 1), and `server_protocol_max` tells a client
that is merely older from one that is incompatible. `features` lists
`snapshot`, `heartbeat`, `notifications`, and `runtime_sessions`, plus optional
features the subscriber named in its own `features` and the daemon granted:

- `runtime_sessions` identifies the snapshot shape (version 2): each session
  has a runtime block with its tmux identity. Every subscriber receives it.
- `workspace_views` marks a client that hosts workspace views (the desktop
  workspace). Only such a subscriber is sent `workspace_view` frames, and a
  `workspace_view` request is refused with `workspace_unavailable` while none
  is connected.

Every later line is a frame `{v, seq, type, data}`. `v` is the protocol version,
`seq` rises by one per frame on one stream, and `data` depends on `type`:

| `type` | `data` | When |
| --- | --- | --- |
| `snapshot` | the `CompanionSnapshot` (version 2) | on subscribe, then whenever the projection changes |
| `notification` | `{notification_id, window_id, navigable, title, subtitle, action, run_id, session_id, actions[]}` | to the notification backend only |
| `notification_retract` | `{notification_id, reason}` | a notice still showing is withdrawn |
| `workspace_view` | `{kind, runtime_session_id, await, directory, path, title, raise}`; `kind` is `session`, `dashboard`, `overview`, `shell`, `editor`, or `pager`, and no field is a command line | to `workspace_views` subscribers only, when a `workspace_view` request is accepted or a `focus_session` request finds a workspace (with `raise` set) |
| `heartbeat` | none | every 15s |
| `bye` | `{reason}`: `shutdown` or `slow_consumer` | the last frame of a stream ended on purpose |

Every shape is additive: a reader ignores fields and frame types it does not
know, and the protocol version moves only for a change a reader could not
survive by ignoring it. The snapshot is versioned separately so it can change
shape without renaming the other messages. The stream is server-to-client only;
anything a companion reports back (a disposition, a snooze, a focus) is a
separate request on its own connection.

### Limits

| Limit | Value | On overflow |
| --- | --- | --- |
| Concurrent subscribers | 8 | the ninth subscribe is refused with `subscriber_limit` |
| Queued non-snapshot frames per subscriber | 64 | the subscriber is ended with `bye{slow_consumer}` |
| Snapshot buffering | one slot, latest wins | a newer snapshot replaces an unwritten one |
| One frame write | 2s | the subscriber is ended with `bye{slow_consumer}` |
| One encoded frame | 1 MiB | the frame is dropped and counted |
| Sessions in a snapshot | 512 | idle and working rows are dropped before attention rows |
| ASL waiting entries in a snapshot | 64 | truncated |
| Self-reported client strings | 128 bytes | clamped |

Waiting entries name a run and its latest question and never carry a question
body or an action token. A daemon stop or restart ends every stream with
`bye{shutdown}` before it waits on connections, so a subscriber never holds
the drain open.

### The notification backend role

A subscriber that claims `notification_backend` and also reports
`notifications_authorized` is a backend candidate; a claim without
authorization is not. The newest candidate holds the role, and when it leaves,
the newest remaining one inherits it. A delivery stays bound to the subscriber
it was sent to and ends when that subscriber does. The daemon's
health answer reports `notification_backend_connected`. What the backend is sent,
and how it reports back, is described below.

### Request deadlines

Each connection carries one request and one response. The request line is read
under the 1s connection deadline; once its type is known, a request the daemon
is known to answer slowly gets a longer window, so the server never cuts a
connection the client is still waiting on:

| Request | Server deadline | Why |
| --- | --- | --- |
| everything else | 1s | hook acknowledgement and state queries stay fast |
| `focus_session` | 3s | with no workspace connected, a tmux survey and a client switch |
| `notification_test` | 5s | waits up to 3s for the app to confirm delivery |
| `asl_snapshot`, `asl_question` | 3s | the bridge read timeout (2s) plus 1s |
| `asl_action` | 6s | the bridge action timeout (5s) plus 1s |
| `subscribe` ack (client side) | 2s | after the ack the stream has no overall deadline |

### Focus, snooze, and notification delivery

A companion focuses a session with `focus_session`, naming its runtime session:

```bash
printf '%s\n' '{"type":"focus_session","focus_session":{"runtime_session_id":"rs-0123456789abcdef0123456789abcdef"}}' \
  | nc -U ~/.local/share/agent-mux/daemon.sock
```

The daemon looks the session up in the registry and refuses one it cannot
show: `invalid_session` for no ID, `runtime_session_unknown` for an unregistered
session, and `stale_target` for an agent that stopped. A live one goes to
whoever can show it:

- **A connected workspace.** Every subscriber that named `workspace_views` is
  sent a `workspace_view` frame of kind `session` with `await` and `raise`
  set. `raise` is what tells the workspace this is the operator's own click:
  it opens its window for it, where a request from a surface it embeds is
  dropped while the window is closed. Only `focus_session` sets it; the
  daemon clears it from a `workspace_view` request. The answer carries
  `focused_session.workspaces`.
- **Otherwise, a terminal.** The terminal the operator used last on the
  session's private tmux server is switched to it (`tmux.Switcher`, with the
  registry validation and the one-owner rule of `amx attach`), and the answer
  names it in `focused_session.client`. With no terminal attached the answer
  is `no_viewer`: the daemon attaches nothing and starts nothing.

The second path selects the session **in the terminal**. The daemon knows a
tmux client by its tty and nothing about the emulator that draws it, so it
cannot bring that terminal's window forward: with no app running, a click
leaves the right session selected and raising the window to the operator.

A companion snoozes notifications with one `notification_snooze` request whose
`snooze.mode` is `until` (with a Unix-second `until` that must lie in the
future), `indefinite`, or `clear`; an empty mode reads the snooze back. The
snooze lives in the notifier's memory only: it never rewrites `config.toml`,
and a daemon restart forgets it. Its gate runs right after quiet hours, so a
notification inside both is suppressed as `quiet_hours`, otherwise as
`snoozed`; a permission notification that was already debouncing when the
snooze was set is held too. Every change rings the companion hub, the health
answer's `notification` block carries `snoozed_until` / `snooze_indefinite`,
and the companion snapshot's `notification` block carries the same snooze. The
operator surface is the same request:

```bash
amx notifications snooze                # the snooze in force; changes nothing
amx notifications snooze 1h             # or: until 08:30 | indefinite | off
amx notifications snooze off --json     # {"snoozed":false,"indefinite":false}
amx notifications test                  # reports "suppressed" while snoozed
```

A companion that subscribes with `notification_backend` and
`notifications_authorized` becomes the notification backend; the newest such
subscriber holds the role. Every notification still passes every gate first.
The daemon then sends the backend a `notification` frame —
`{notification_id, window_id, navigable, title, subtitle, action, run_id,
session_id, actions[]}`, where `actions` offers `mute`, `snooze`, and (for an
ASL question) `answer`, and a permission prompt offers none, or `respond` alone when its
session runs on tmux — and waits up to
3s for a `notification_disposition` request reporting `delivered`. A notice
about a runtime session also carries `runtime_session_id` and `backend`;
`window_id` is 0, and the notice is navigable by its runtime session through
`focus_session`. Without a delivery confirmation, the notice is retracted,
`dispatch:fallback:app_fallback` is recorded, and
Alerter shows the notification instead; with no app connected Alerter is used
exactly as before. After `delivered` the app reports one terminal disposition:
`clicked` (the app sends `focus_session` and the daemon records the click as
`focus:suppressed:focus_delegated`), `dismissed`, `action_mute`,
`action_snooze`, `action_answer`, `action_respond`,
or `failed`. `action_respond` follows the one action a `permission` notice can
offer, `respond`, and only for a tmux runtime session: the app shows the
session's screen and the operator answers the prompt there through `amx send`.
The daemon records the choice and writes nothing to the session itself.
Reports are idempotent: the first terminal disposition wins and an unknown
`notification_id` is answered ok. A `notification_retract` frame
`{notification_id, reason}` withdraws a notice that is still showing when a
newer notification for its window is dispatched (`superseded`), when the
window moves on (`superseded`), when it closes (`window_closed`), or when the
interaction window runs out. Journal dispositions carry the backend in
`evidence.backend`.

```bash
printf '%s\n' '{"type":"notification_disposition","disposition":{"notification_id":"n-0123456789abcdef","disposition":"clicked"}}' \
  | nc -U ~/.local/share/agent-mux/daemon.sock
amx notifications test   # "confirmed via app" when the app delivered, else Alerter
```

`amx doctor` reports each connected companion app as `companion_app` (info,
with its client version) and raises `companion_build_mismatch` (warning) when
the `amx_build` an app reported is not the daemon's artifact. `amx debug
subscribe` is not an app and is never reported.

### In-flight dispatches

Each notification that passes every gate runs in its own goroutine, which
blocks until the notification's interaction settles: up to 3s waiting for the
app's `delivered`, then up to the dispatch timeout (Alerter's 5-minute
protocol timeout plus a 1-minute grace) for a click or a dismissal. There is
deliberately no global cap on how many may be blocked at once. The count is
already bounded by the cooldown: a `(window, action)` pair dispatches at most
once per `cooldown_seconds` (default 10), so the worst case is
`windows × actions × dispatch_timeout / cooldown`, and in practice a turn-end
notification needs a new turn before it can recur. A goroutine waiting on the
app holds no process; one waiting on Alerter holds one `alerter` process.
Every dispatch is cancelled with the daemon's context, so a stop or restart
ends them all at once (`cancelled/daemon_shutdown`), and an app notice is
retracted when a newer notification for its window passes the gates.

A cap was considered and rejected. When full, it would have to drop either the
newest notification, which is the one the operator most needs, or the oldest,
which kills an Alerter banner the operator may be about to click. Neither is
better than letting the cooldown bound the set. Revisit this if `alerter`
processes are seen accumulating in practice (`pgrep -fl alerter`), and
prefer a per-window supersede (as the app backend already does) over a global
count.

### Mute lag

A session is muted with `amx mute-toggle --runtime-session` (or from inside an
attached session with no selector) or from the dashboard. The mute is stored on
the tmux pane, and the daemon reads it during runtime reconciliation
(`observeRuntimeMutes`). The notifier and companion snapshot pick up the change
on that pass. Hooks do not claim mute state; the pane value is authoritative.

## Rendered state of a tmux session

A tmux session's state is rendered into two options on the agent's pane:
`@amx_status` (the state icon and markers ahead of the session's title) and
`@amx_status_color` (the session's color as `#rrggbb`). The private server's
configuration builds the attached
terminal's title (`set-titles-string`) and its one-line status
(`status-format[0]`) from them and from nothing else, so the daemon is still
the only writer of rendered state. `amx attach` decides only whether the line
is shown: on for a plain terminal, off in a terminal Agent Mux.app hosts
(which sets `AMX_WORKSPACE_HOSTED` for the `amx attach` it runs).

A status is written once the runtime checkpoint holds the state it renders,
which settles the slot, and again when the
session's title or color changes. Everything a maintenance pass has to write
goes out in one tmux client per server, each status guarded by the server
generation it was rendered for; a pass with nothing new starts none, so a
quiet pass is still the one reconciliation client. A write the server refused
is not repeated by the next pass: everything is written once more thirty
seconds later.

Sessions are listed in one order everywhere (`session.Order`: pins first, then
`[defaults] sort`; sessions that tie use runtime-session ID). The companion
snapshot uses this same order.

## Investigating hook event flow

`amx daemon export-events` writes a schema v13 JSON snapshot for investigation.
Version 13 adds the screen classifier's live decline counters under
`watchdog.screen_declines`: one entry per probe source that currently holds a
streak, carrying the streak, whether the declined frame's fingerprint is stable,
the veto set, and the status line that caused the refusal. Windows whose probe
settles or was never eligible carry no entry at all, which is the ordinary case.
Version 12 establishes the compatibility boundary for the shadow's
`busy_since_at` busy-stretch anchor. Version 11 adds bounded terminal-stop-hold evidence: synthetic
`TerminalStopHold` events, the per-event `stop_hold_outcome`,
`stop_hold_waited_ms`, and `stop_confirmed_by` fields, and the shadow's
`pending_terminal_stop` section. Every value comes from a fixed vocabulary.
Version 10 adds the safe `ph`/`pp`/`pt` harness-process fingerprint to eligible
source facts, enriched `HarnessProcessExited` events, and process-exit counters
under `watchdog`. The opaque start token detects PID reuse; command lines,
prompts, arguments, and paths are never included. Version 9 adds causal
trace/request/source-event identities, sanitized reducer
inputs and outcomes, routing evidence, request-scoped saturation, bounded
render generations, notification dispositions, and child-probe links. Raw
payloads, prompts, tool arguments, and transcript text are not included.
Pass `--schema 12` for the pre-decline-counter contract, `--schema 11` for the
pre-busy-anchor contract, `--schema 10` for the
pre-stop-hold contract, `--schema 9` for the previous complete causal contract,
or `--schema 8` for the pre-causal contract. All five are explicit compatibility
projections: schema 12 and every schema below it cannot expose the v13 decline
counters, schema 11 cannot expose the v12 busy-stretch anchor, schema 10
cannot expose v11 stop-hold fields and drops `TerminalStopHold` rows entirely,
schema 9 cannot expose v10 process fields, and schema 8 cannot expose v9 or v10
fields.

Version 8 retains every v7 field and adds producer-pane origins to events
(`events[].origin_window_id`) and source facts (`shadow.facts[].ow`), plus the
acknowledgement timestamp `shadow.facts[].aa` for acknowledged facts. Version 7
retains every v6 field and adds the interactive-ownership `pm` flag to
pipe-mode source facts (`shadow.facts[].pm`) plus the reducer-wide causal order
`o` used to compare equal-timestamp facts across sources (`shadow.facts[].o`).
Each schema-v13 `shadow.facts[]` entry serializes the reducer's
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
  "schema_version": 13,
  "daemon": { "pid": 1234, "uptime_seconds": 97, "ready": true, "build": { ... }, "metrics": { ... } },
  "cli_build": { ... },
  "windows": [
    {
      "window_id": 42,
      "stats": { ... },
      "durable": { ... },
      "renderer": { "desired_icon": "bolt", "state_set": true, "render_failures": 0, "queued_gen": 7, "rendered_gen": 7, "generations": [ { "generation": 7, "source_event_ids": ["run-1:2"], "stage": "rendered", "outcome": "success" } ] },
      "shadow": { "effective_icon": "bolt", "effective_looping": true, "busy_since_at": 1700000000, "facts": [ { "k": "harness", "y": "claude:sess-pipe", "i": "bolt", "o": 10, "ow": 91, "ph": "claude", "pp": 4321, "pt": "Mon Jul 13 10:11:12 2026", "pm": true }, { "k": "harness", "y": "claude:sess-1", "i": "bolt", "o": 11, "ow": 42, "ph": "claude", "pp": 4322, "pt": "Mon Jul 13 10:15:20 2026" }, { "k": "harness", "y": "claude:sess-old", "i": "bolt", "o": 9, "ak": true, "aa": 1700000000, "ow": 91 } ] },
      "watchdog": { "last_event": "HarnessProcessExited", "last_reason": "pipe_process_exited", "process_exits": 1, "last_process_exit_reason": "pipe_process_exited", "last_process_exit_observation": "missing", "screen_declines": [ { "source": "codex_screen_prompt", "streak": 61, "stable_fingerprint": true, "fingerprint": 4369, "vetoes": "working_status_line", "status_line": "Working (12s · esc to interrupt)", "at": 1700000000 } ] },
      "live": { "found": true, ... },
      "events": [ { "at": 1700000001, "event": "Stop", "action": "stop", "result_icon_state": "stop", "trace_id": "…", "request_id": "…", "source_event_id": "run-1:2", "parse_outcome": "decoded", "normalization_outcome": "normalized", "source_kind": "harness", "source_key": "claude:sess-1", "route_decision": "owner", "origin_window_id": 91, "reducer_decision": "applied", "render_generation": 7, "render_outcome": "success" } ]
    }
  ]
}
```

The full export can exceed 1 MiB on a busy daemon; the IPC client accepts
responses up to 64 MiB, and `--window`/`--current-window` bound the payload to
one slot when investigating a single session.

Durable metadata can outlive a live runtime session; `live.found` flips to
`false` when the session no longer appears in the live survey.
`stats.suppression_reasons` aggregates suppressed transitions by reason, while
`renderer` reports desired icon/loop state and render generations. `shadow`
reports the reducer's effective state and live facts; `busy_since_at` is the
unix-second start of its current active-class stretch and is absent while the
window is idle. Event rows keep
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
`authoritative idle (<harness>) settled visible state "…"→"…" (tier=<tier>
samples=<n> generation=<fingerprint>)`. The suffix is diagnostics, not schema:
`tier` is the grade of evidence the screen produced (`terminal_explicit`,
`terminal_shape`, `quiescent`, or `none` for a producer with no tier concept),
`samples` is the run length that grade required, and `generation` is the
evidence fingerprint every sample in the run had to agree on. Nothing parses
it — it is there so `amx daemon export-events` answers *why* a tab settled.
These rows no longer carry the legacy
`result_icon_state`/`current_icon_state` fields. The three are told apart by
event name and source key:

- **Codex** — `event: "ScreenStopProbe"`, `source: "probe:codex_screen"`. Fires
  when an active Codex tab still shows `⚡` but its latest visible status is a
  completion marker — `Worked for …` on older builds, or the `Completed` line
  Codex 0.149.x draws between two horizontal rules — or the TUI interrupted
  marker, with an empty composer and no visible prompt. The bare `Completed`
  line is honoured **only** when framed by those rules; the word alone is
  ordinary prose and is not evidence. Codex's vetoes are computed first and
  unconditionally, exactly as Claude's are: a live status line, a generic
  `<key> to interrupt/cancel` hint, a blocking prompt, a busy or unrecognized
  composer, and a **running background terminal** — Codex's
  `<n> background terminal(s) running` footer, which is the one case where the
  terminal marker is honest and settling is still wrong, since a detached
  process the pane started is still going. That veto does not depend on the
  composer being recognized, and its non-zero count is what keeps transcript
  prose about background terminals from vetoing anything. It needs **two consecutive samples that agree
  on the same normalized screen fingerprint** — a completed-turn read makes the
  classification unambiguous but cannot answer whether the pane is still
  repainting, which is a different question and the one the second sample
  settles.
- **Claude** — `event: "ScreenStopProbe"`, `source: "probe:claude_screen"`
  (same event name as Codex, distinct source). Fires on a stuck Claude `⚡`
  whose composer is idle, or that shows Claude's interruption marker. The
  classification itself lives in `internal/screenstate`, which computes every
  veto first and unconditionally — a live status line (progress ellipsis plus a
  parenthesized elapsed timer), a background-task hint or a background shell
  still counted on the mode line (`· 1 shell`; not the finished line's
  `1 shell still running`, which is transcript, and not `← 1 agent`, which
  every pane with an agent view shows), a blocking prompt, a
  context-limit marker, or a busy composer — and only then consults positive
  terminal evidence. Every tier needs **at least two consecutive samples that
  agree on the same normalized screen fingerprint**, so a pane still repainting
  an elapsed timer can never settle regardless of what the classifiers
  conclude — two is a floor rather than a fixed count, because below it the
  fingerprint is never compared against anything and stabilization is inert.
  Claude spends more samples on weaker evidence: **two** for positive terminal
  evidence (an interruption marker, or a `<word> for <duration>` completion
  line) and **three** for a quiescent screen — an empty composer with no
  terminal marker at all, which is what Claude shows after `/clear`, after
  `/compact`, at session start, and after a completion line has scrolled away.
  The pane title never vetoes the screen
  read — Claude leaves a marker and its task description behind after a turn, so
  a title gate would strand idle panes on `⚡` forever. A title still carrying a
  marker adds **one** sample on top of the screen's own tier, so the slowest
  Claude path is four consecutive samples and the fastest is two. Native
  `Notification/idle_prompt` is the primary Claude
  stop; this screen probe is the interruption/no-hook fallback. The positive half
  uses the same reducer source and records `event: "ScreenActiveProbe"` when a
  marker over a task description restores an empty managed Claude tab to `⚡`,
  once per description. It never replaces `🛑` or another explicit icon, repeats
  an event for an existing `⚡`, or changes the independent ASL lease.
- **Copilot** — `event: "TranscriptStopProbe"`,
  `source: "probe:copilot_transcript"`. Reads no screen: it consults the
  interactive session transcript's latest state-bearing event and settles only
  on a terminal record. Native `agentStop`/`sessionEnd` remain the primary
  Copilot stop.

A pane that never stabilizes — a live progress bar, an animated plugin, a
harness redrawing something on every frame — never reaches its required run and
keeps `⚡`. That is the doctrine-sanctioned direction (a stale `⚡` is
acceptable; a false `🛑` is not), and it is bounded on two sides. When a native
terminal hook *was* received and is being held for probe confirmation, the hold
rides to its deadline and **commits anyway** at `stop_confirm.timeout` (15s by
default), so the fingerprint gate can never indefinitely suppress a real
terminal stop. The one exception is background work: a held stop whose pane the
probe reads as still running it (the `background_task` veto: Claude's
`· 1 shell`, Codex's `1 background terminal running`) has its deadline moved a
full timeout past each such reading. The turn ended, but nobody has anything to
answer while that work runs, so the session keeps `⚡`, the active watchdog leaves
it alone, and the export marks the hold `background_work`. Once the work is off
the screen, the probe confirms the stop or the turn the finished work starts
supersedes it. A pane that can no longer be read stops pushing the deadline,
and the stop paints a timeout later. When there was no hook at all, only the watchdog clears the tab:
`sweepStale` clears a stale session to the **empty** icon and never to `🛑`,
firing its one-shot "Session idle" notification. The sweep always runs: with
no `[defaults.watchdog]` threshold configured, `active` defaults to 600 seconds
(`subagent` to 1800, `compact` to 180), so an unstabilizable pane holds `⚡` for
no longer than that threshold plus one sweep.

Codex exposes edit approvals and `request_user_input` waits through
TUI/server-request flows. A native `PreToolUse(request_user_input)`, when
available, is only early intent: Agent Mux keeps the session active until its
screen probe confirms that the question UI is visibly
answerable. The probe owns ❓ and its notification, and withdraws both its fact
and guard when the prompt disappears even if no `PostToolUse` arrives. It
recognizes both edit approvals and the bottom-pane question UI while preserving
terminal, warning, and error states. A separate conservative stop-only fallback
recognizes Codex's cleared idle UI after `/clear` when full pane scrollback no
longer contains a transcript. The per-harness stop fallbacks described above
(Codex/Claude `ScreenStopProbe`, Copilot `TranscriptStopProbe`) repair a turn
that finished without a native terminal hook so the icon settles on `🛑`
instead of staying `⚡`. The stop paths fire only on a stuck `bolt` session and never
downgrade protected attention states (`key`, `question`, `error`, `warning`,
`subagent`, `compact`), or retire pipe-mode, subagent, or live ASL facts. Claude's
busy-title active recovery is likewise narrow: it only fills an empty icon. The
daemon runs these probes on a periodic loop (every 2s). It reads each tmux
session's pane on the private server with no terminal attached:
the Claude context-limit banner, prompt label, and busy-title recovery, the
Codex prompt and cleared screen (`ScreenClearProbe`), the Copilot transcript
probe, and for all three the evidence that settles a stuck `⚡` or confirms a
held stop. A capture is used only when it is of the registered pane, under the
server generation the session was registered on, while its agent is running;
anything else, and a read that fails, is no evidence for that tick. The title
Claude sets on its pane is read with the screen and judged with it. The pane's
history, which the cleared-screen check reads second, counts only when the same
server still answers for the pane afterwards. A session showing no icon is
read every fifth tick, and so is a stopped Codex session, whose only question
is whether `/clear` was typed; a working one is read every tick, and a Claude
or Copilot session at rest on `🛑` not at all.

Each screen observation is checked against the registered tmux pane and server
generation before it can change reducer state. A stale or failed read supplies
no evidence for that probe tick.

The primary debug workflow starts from persisted evidence and remains useful
when the daemon is unavailable:

```bash
amx debug status
amx debug tail --last 25
amx debug bundle --mode share
amx debug replay agent-mux-debug-*-share.tar.gz
```

`amx debug probe` makes a live, read-only transport and routing check for a
runtime session. It uses `AMX_RUNTIME_SESSION_ID` in an attached session or
accepts `--runtime-session ID`; a daemon slot number is not a valid probe
target. `export-events` remains the compatibility and daemon snapshot surface.

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

`--current-window` selects the runtime session named by
`AMX_RUNTIME_SESSION_ID`; it fails when that variable is absent.
`--runtime-session ID` selects a registered session from anywhere, and
`--window ID` selects a daemon slot from an unrestricted export. A runtime
entry carries `runtime_session_id` and `backend`; its `window_id` is the
daemon's slot number. `--output PATH` writes the JSON to a file instead of
stdout. The destination's parent directory must exist. The file is replaced
atomically with mode `0600`, and a symlink destination is refused. The export
is read-only and does not auto-start the daemon, so the
buffer of a still-running investigation session is never replaced.

## Diagnostic and native-log privacy

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
not be shared. See [Debugging event delivery](debugging.md) for quotas,
capture expiry, bundle modes, and sharing guidance.
