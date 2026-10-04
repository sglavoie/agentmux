# Daemon

The daemon owns tab-state transitions and notifications:

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
reports it. A stopped daemon is a successful answer with `"alive": false`.

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

The daemon is the only writer for state icons and the `🔁` loop marker. Its
state lives in each Kitty window's `amx_state` user var, so a restart restores
the intended state and the renderer converges the live title on its next pass.
`amx_state` schema v4 persists each eligible fact's validated harness-process
fingerprint (`ph` harness, `pp` PID, and opaque `pt` start token) alongside the
schema-v3 producer-pane origin (`ow`) and acknowledgement time (`aa`). A compact
payload marker (`pv:1`) records that process tokens were rendered under the
pinned UTC timezone. The optional compact `pc` field is the Unix-millisecond
time of the last effective icon or unseen transition; omitting it is the
backward-compatible zero value. Readers retain v1-v4 compatibility; v1-v3 facts
and early v4 payloads without that marker restore without lifecycle ownership
and continue using watchdog fallback.
Acknowledged nonterminal replay cutoffs expire after one hour and are capped at
the newest 64; unacknowledged facts and live ASL leases are never removed by
that cap.

The state-query response projects each window as `icon_state`, `looping`,
`last_action`, `last_event_at`, and `guard_action`, plus optional
`busy_since_at`, `presentation_changed_at_ms`, `unseen`, `reason`, `branch`,
`dirty`, and `conversation`. `presentation_changed_at_ms` uses Unix
milliseconds and is omitted when unknown, including for restored pre-field
payloads and daemons predating the field. Dashboard clients must clear a stale
local value when the member or whole window entry is absent; the timestamp is
an ordering signal only and does not replace the busy-stretch or activity
clocks.
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

- **F8 (`user_clear`)** clears the tab's state icon and any loop marker without
  a live ASL lease. On a managed tab it is
  retargeted to the title owner from whichever pane pressed the binding —
  owner, managed non-owner, or unmanaged sibling — so the wipe is never dropped
  as an owner mismatch. On an ownerless tab, it clears a recognized state icon
  or stale loop marker
  from the source pane, including terminal icons left by subprocesses such as
  `claude -p`; an icon-free ownerless tab remains a no-op. A live ASL lease keeps
  `🔁` while executing, waiting for a mailbox reply, or between turns. Pausing
  ends the loop process and clears `🔁`; old paused runs are not restored onto
  new tabs. The base title, harness emoji,
  `★`, and `🔇` are preserved. On a managed Claude pane,
  the clear is transient while Claude's current pane title still carries its
  busy marker: the next 2-second probe tick restores `⚡` from that positive
  activity evidence. This recovery never creates `🔁`; only a live ASL lease
  owns the loop marker.
- **F11 (`user_active`)** is the operator saying "this tab is working". It
  routes exactly like F8 (owner from any pane; an icon-free ownerless tab is a
  no-op) and acknowledges every visible fact the same way, then shows `⚡` from
  its own synthetic `probe:user_active` source. That `⚡` lasts until the next
  real hook or ASL event for the window — which retires it before applying, so
  a Stop shows `🛑`, a permission `🔑`, and a prompt the harness's own `⚡` —
  until F8, or until the ordinary ten-minute active staleness. An ASL
  supervisor question and a verified harness exit retire it the same way. The
  screen idle probe keeps reading the pane under it but holds the mark against
  the idle screen it was pressed on: only once the probe has seen the pane
  working since the press does a later stabilized terminal screen settle `🛑`
  over it (recorded as `bolt→stop`), which is what clears the mark on a tab
  whose harness delivers no hooks. A native Stop withheld under the mark is
  still confirmed by the probe rather than waiting out the hold. Each F11
  forgets work observed under the previous one. The mark keeps any promotion
  hold an earlier F8 placed, so a stale Claude busy title cannot re-promote `⚡`
  once the mark ages out. The dashboard
  and overview offer the same action as "Mark active" (`b`).
- **Unmanaged producer routing** lets the active unmanaged pane borrow exactly
  one managed target. A direct user prompt is authoritative focus evidence even
  if the cached Kitty snapshot lags; an exact live ASL candidate also authorizes
  its source pane. Once accepted, the producer/session/origin/target route stays
  valid through that session's terminal event after focus moves. A new inactive
  unmanaged session, an ambiguous target, or CWD/harness similarity alone is
  rejected as `owner_mismatch`.
- **Non-interactive runs are excluded.** A background `claude -p` or
  `codex exec` in an unmanaged pane is not a session the dashboard reports on,
  so prompt/focus evidence no longer lets it borrow the managed owner, and in an
  ownerless tab it creates no state on the plain pane. Such hooks are suppressed
  as `unmanaged_pipe_source`, which is distinct from `owner_mismatch` and never
  feeds the doctor mismatch findings. An exact live ASL candidate still
  authorizes its pane, and ASL lifecycle events keep their `🔁` marker. The same
  exclusion keeps `codex exec` out of the Codex foreground screen probe. Pipe
  mode inside a *managed* pane is unaffected.

For the acceptance record, required live-client transitions, and dogfood
checklist, see [Daemon State Acceptance Evidence](daemon_state_acceptance.md).
The hermetic process-exit fixture and optional installed drill are documented in
[Process-Lifecycle Acceptance](acceptance-testing.md).

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
change with the clock alone (Kitty snapshot staleness, quiet hours).

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
`snapshot`, `heartbeat`, and `notifications`.

Every later line is a frame `{v, seq, type, data}`. `v` is the protocol version,
`seq` rises by one per frame on one stream, and `data` depends on `type`:

| `type` | `data` | When |
| --- | --- | --- |
| `snapshot` | the `CompanionSnapshot` (its own `version`, today 1) | on subscribe, then whenever the projection changes |
| `notification` | `{notification_id, window_id, navigable, title, subtitle, action, run_id, session_id, actions[]}` | to the notification backend only |
| `notification_retract` | `{notification_id, reason}` | a notice still showing is withdrawn |
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
| `focus_window` | 3s | three sequential Kitty remote-control calls |
| `notification_test` | 5s | waits up to 3s for the app to confirm delivery |
| `asl_snapshot`, `asl_question` | 3s | the bridge read timeout (2s) plus 1s |
| `asl_action`, `asl_workbench` | 6s | the bridge action timeout (5s) plus 1s |
| `subscribe` ack (client side) | 2s | after the ack the stream has no overall deadline |

### Focus, snooze, and notification delivery

A companion focuses a session with one `focus_window` request naming a Kitty
socket and a window ID. The daemon dials the socket only if it already knows it
— a per-window hook socket, its effective listen address, or the socket its
published snapshot was read through — and otherwise answers `unknown_socket`
without dialing anything. It lists that instance, answers `stale_target`
without writing when the window is gone, and then focuses the tab and the
window, which is the same sequence a notification click uses
(`notification.FocusKittyWindow`). All of it runs outside `stateMu`, under a 3s
connection deadline. A successful answer names the window's tab and OS window:

```bash
printf '%s\n' '{"type":"focus_window","focus_window":{"kitty_socket":"unix:/tmp/kitty-123","window_id":7}}' \
  | nc -U ~/.local/share/agent-mux/daemon.sock
```

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
ASL question) `answer`, and a permission prompt offers none — and waits up to
3s for a `notification_disposition` request reporting `delivered`. Without it
the notice is retracted, `dispatch:fallback:app_fallback` is recorded, and
Alerter shows the notification instead; with no app connected Alerter is used
exactly as before. After `delivered` the app reports one terminal disposition:
`clicked` (the daemon focuses the pane exactly as an Alerter click does),
`dismissed`, `action_mute`, `action_snooze`, `action_answer`, or `failed`.
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

The mute lives in Kitty: `amx mute-toggle` writes the window's `cc_mute` user
var (and mirrors a glyph into the tab title), and the daemon's mute cache is
recomputed from every Kitty listing. The maintenance loop lists Kitty every
render cadence (2s), and a hook reads the same cached listing, so a mute made
from the CLI or the companion app reaches the notifier's mute gate and the
companion snapshot within about 2s. A notification raised inside that window
is not yet suppressed.

A `mute_set` request that updates the cache under `stateMu` was evaluated and
not added. The lag is bounded by a cadence the daemon already runs, and is
short beside the time it takes an operator to mute after deciding to. More
importantly, the daemon deliberately never trusts a client's claim about mute
(hook requests carry a legacy `Muted` field it ignores), because the user var
is the authority and a cache seeded from a client could disagree with it until
the next listing anyway. If the lag becomes observable, the change to make is
a request that asks the daemon to refresh its Kitty listing now, which keeps
Kitty the only source.

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
one window when investigating a single tab.

Durable metadata can outlive the live Kitty join after a tab closes;
`live.found` flips to `false` when Kitty/session lookups are no longer
available. `stats.suppression_reasons` aggregates suppressed transitions by
reason, while `renderer` reports desired icon/loop state (`looping_set` shows
when the daemon has explicitly taken ownership), the known Kitty
socket, and any consecutive render failure with its last error. `shadow`
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
  parenthesized elapsed timer), a background-task hint, a blocking prompt, a
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
  The Kitty pane title never vetoes the screen
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
terminal stop. When there was no hook at all, only the watchdog clears the tab:
`sweepStale` clears a stale window to the **empty** icon and never to `🛑`,
firing its one-shot "Session idle" notification. With no `[defaults.watchdog]`
threshold configured there is no such sweep, so an unstabilizable pane can hold
`⚡` indefinitely.

Codex exposes edit approvals and `request_user_input` waits through
TUI/server-request flows. A native `PreToolUse(request_user_input)`, when
available, is only early intent: Agent Mux keeps the tab active until its
best-effort Kitty screen probe confirms that the question UI is visibly
answerable. The probe owns ❓ and its notification, and withdraws both its fact
and guard when the prompt disappears even if no `PostToolUse` arrives. It
recognizes both edit approvals and the bottom-pane question UI while preserving
terminal, warning, and error states. A separate conservative stop-only fallback
recognizes Codex's cleared idle UI after `/clear` when full Kitty scrollback no
longer contains a transcript. The per-harness stop fallbacks described above
(Codex/Claude `ScreenStopProbe`, Copilot `TranscriptStopProbe`) repair a turn
that finished without a native terminal hook so the icon settles on `🛑`
instead of staying `⚡`. The stop paths fire only on a stuck `bolt` tab and never
downgrade protected attention states (`key`, `question`, `error`, `warning`,
`subagent`, `compact`), or retire pipe-mode, subagent, or live ASL facts. Claude's
busy-title active recovery is likewise narrow: it only fills an empty icon. The
daemon runs these
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
