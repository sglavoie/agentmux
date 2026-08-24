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

- **F8 (`user_clear`)** clears the tab's state icon. On a managed tab it is
  retargeted to the title owner from whichever pane pressed the binding —
  owner, managed non-owner, or unmanaged sibling — so the wipe is never dropped
  as an owner mismatch. On an ownerless tab, it clears a recognized state icon
  from the source pane, including terminal icons left by subprocesses such as
  `claude -p`; an icon-free ownerless tab remains a no-op. `🔁`, the base title,
  harness emoji, `★`, and `🔇` are all preserved. On a managed Claude pane,
  the clear is transient while Claude's current pane title still carries its
  busy marker: the next 2-second probe tick restores `⚡` from that positive
  activity evidence. This recovery never creates `🔁`; only a live ASL lease
  owns the loop marker.
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

## Investigating hook event flow

`amx daemon export-events` writes a schema v12 JSON snapshot for investigation.
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
Pass `--schema 11` for the pre-busy-anchor contract, `--schema 10` for the
pre-stop-hold contract, `--schema 9` for the previous complete causal contract,
or `--schema 8` for the pre-causal contract. All four are explicit compatibility
projections: schema 11 cannot expose the v12 busy-stretch anchor, schema 10
cannot expose v11 stop-hold fields and drops `TerminalStopHold` rows entirely,
schema 9 cannot expose v10 process fields, and schema 8 cannot expose v9 or v10
fields.

Version 8 retains every v7 field and adds producer-pane origins to events
(`events[].origin_window_id`) and source facts (`shadow.facts[].ow`), plus the
acknowledgement timestamp `shadow.facts[].aa` for acknowledged facts. Version 7
retains every v6 field and adds the interactive-ownership `pm` flag to
pipe-mode source facts (`shadow.facts[].pm`) plus the reducer-wide causal order
`o` used to compare equal-timestamp facts across sources (`shadow.facts[].o`).
Each schema-v12 `shadow.facts[]` entry serializes the reducer's
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
  "schema_version": 12,
  "daemon": { "pid": 1234, "uptime_seconds": 97, "ready": true, "build": { ... }, "metrics": { ... } },
  "cli_build": { ... },
  "windows": [
    {
      "window_id": 42,
      "stats": { ... },
      "durable": { ... },
      "renderer": { "desired_icon": "bolt", "state_set": true, "render_failures": 0, "queued_gen": 7, "rendered_gen": 7, "generations": [ { "generation": 7, "source_event_ids": ["run-1:2"], "stage": "rendered", "outcome": "success" } ] },
      "shadow": { "effective_icon": "bolt", "effective_looping": true, "busy_since_at": 1700000000, "facts": [ { "k": "harness", "y": "claude:sess-pipe", "i": "bolt", "o": 10, "ow": 91, "ph": "claude", "pp": 4321, "pt": "Mon Jul 13 10:11:12 2026", "pm": true }, { "k": "harness", "y": "claude:sess-1", "i": "bolt", "o": 11, "ow": 42, "ph": "claude", "pp": 4322, "pt": "Mon Jul 13 10:15:20 2026" }, { "k": "harness", "y": "claude:sess-old", "i": "bolt", "o": 9, "ak": true, "aa": 1700000000, "ow": 91 } ] },
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
