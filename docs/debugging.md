# Debugging event delivery

Agent Mux 0.6.0 keeps a private causal journal and native harness logs so an
intermittent icon or notification problem can be investigated after the fact.
Start with the offline-first workflow. `tail` and `explain` read persisted state
without contacting the daemon. `status` performs a read-only daemon health call
without autostarting it, while `probe` sends a side-effect-free diagnostic
request; neither mutates reducer or Kitty state:

```bash
amx debug status
amx debug tail --last 50
amx debug probe
amx debug explain --source-event <source-event-id>
```

All four commands accept the same selectors: `--harness`, `--profile`,
`--window`, `--session`, `--asl-run`, `--event`, `--trace`, `--request`,
`--source-event`, `--stage`, `--outcome`, `--after`, and `--before`. Use
`--json` for the stable schema-versioned result. `status`, `tail`, and `explain`
reports use schema v2; bundle manifests and causal-record wrappers also use
schema v2. `probe`, `capture`, and `replay` remain schema v1. `tail --follow`
follows only new persisted records and does not require a running daemon.

When the first report is inconclusive, capture the exact selector and preserve
a redacted artifact:

```bash
amx debug explain --request <request-id> --json
amx debug bundle --mode share -o agent-mux-debug.tar.gz
amx debug replay agent-mux-debug.tar.gz
```

Bundle creation gathers the same read-only live health evidence as `status`
plus a doctor snapshot, so it may contact the daemon and Kitty. It does not
mutate either one.

`replay` is strictly offline. It accepts an immutable journal directory, debug
bundle, or `export-events` file and reconstructs routing, reducer decisions,
restart boundaries, duplicate suppression, and render groups without opening a
Kitty connection or daemon socket. Its schema-v1 report accepts both v1 and v2
diagnostic bundles and causal-record wrappers.

## Read-only snapshots, ordering, and graph limits

Every diagnostic journal read captures fixed byte prefixes from the active
segment, immutable segments, and emergency spool files under a short shared
lock. File parsing happens after the lock is released. The merged snapshot
deduplicates overlapping segment/spool evidence, tolerates corrupt lines, and
does not rename, promote, reconcile, rotate, quarantine, or delete storage.
`amx debug status` also gathers last-seen hook evidence for every configured
profile while that same snapshot is traversed, before display selectors and
limits are applied. Health, selected recent events, hook verdicts, and hook
silence therefore require one journal traversal, regardless of profile count.
Readers remain useful while the daemon is offline because unpromoted spool
records are included directly in memory.

Only the daemon performs durable maintenance. At startup it runs spool
promotion, one bounded compaction pass, then rotation/retention. Every 60-second
promotion tick is followed by one compaction pass; the 15-minute retention
schedule is unchanged. Each operation receives its own five-second context, so
compaction cannot consume the next operation's budget. Compaction selects at
most 64 MiB of the oldest sealed `events-*.jsonl` inputs and incrementally
rewrites them into approximately 16 MiB immutable segments. It never rewrites
the active journal, spool/claim files, quarantine evidence, or unrelated
temporary files. Valid records from corrupt inputs are published before the
original damaged segment is moved intact into quarantine, and duplicate
old/new segments left by an interrupted cleanup remain safe because snapshot
traversal deduplicates them.

Each pass records `journal.maintenance` evidence with a distinct startup or
periodic reason. In addition to promotion, corruption, quarantine, rotation,
and retention fields, the record includes `compaction_input_files`,
`compaction_input_bytes`, `compaction_output_files`,
`compaction_output_bytes`, and `compaction_duplicates_omitted`. Existing
journals require no migration or blocking rewrite; repeated bounded passes
converge as ordinary maintenance runs.

Schema-v2 `status`, `tail`, `explain`, and bundle evidence carry the shared
`journal_snapshot` object. It reports `segment_files_read`,
`promoted_spool_files_read`, `spool_files_read`, `bytes_scanned`,
`records_scanned`, `records_matched`, `duplicates_omitted`, and
`corrupt_lines`. `records_matched` is counted before an output limit is
applied. Promoted `events-spool-*` files live in the journal directory and are
counted as segments; `promoted_spool_files_read` is the promoted subset, while
`spool_files_read` covers only not-yet-promoted spool-directory files. Journal
health additionally reports `earliest_observed_at` beside `latest_observed_at`
so consumers can judge how far back the retained history actually reaches.

When the journal horizon is shorter than a profile's hook verification window,
hook status reports the `last_seen_horizon` issue instead of claiming
`last_seen_missing`: pruned history cannot prove that hooks are silent. The
status hooks report also carries `delivery_failures` (`count`, `latest_at`),
counting one terminally failed delivery per request in the trailing hour,
surfaced as the stable warning finding `hook_delivery_failures` and as a doctor
summary line. Each request is terminalized exactly once: `hook.delivery` is the
authoritative terminal record and its outcome decides success or failure, while
an error-outcome `hook.failure` marks a request failed only as a legacy fallback
when no correlated `hook.delivery` exists. Intermediate error-outcome
`hook.ipc_attempt` records are ignored, so a transient transport error that a
later successful or duplicate terminal delivery resolves never contributes to
the count. Expect the warning to fire for up to an hour after a daemon restart:
the daemon client performs bounded auto-start retries within the hook
invocation's auto-start budget, but once that invocation exits there is no
durable retry queue, so deliveries that never reached a live daemon in time are
dropped by design.

`tail --last N` merges all captured evidence, sorts by `observed_at` with stable
identity tie-breakers, and then selects the newest N matching records. This is
event-time ordering, not file append order: a delayed emergency spool write
cannot push a newer segment event out of the result. Follow mode uses
inode/offset cursors plus stable record identities so promotion or legacy claim
replacement does not replay an already emitted record.

Exact-ID explain selectors choose graph seeds, not an unbounded transitive
closure. The defaults are `--graph-depth 2`, `--max-nodes 250`, and
`--max-edges 1000`; accepted ranges are 1–4, 1–2,000, and 1–8,000. Values are
validated before the journal scan. Schema v2 publishes `limits`, `truncation`,
and `journal_snapshot`; a text report beginning `warning: evidence graph
truncated` lists the limiting reasons and directly pruned node/edge candidate
counts.

## Tracing a harness process exit

When `spec commit` (or another pipe-mode invocation) leaves a bolt behind,
capture the Kitty window ID before reproducing it, then trace both discovery
and settlement:

```bash
spec commit
amx debug tail --stage hook.process_detection --window "$KITTY_WINDOW_ID" --last 20 --json
amx debug tail --stage daemon.process_lifecycle --window "$KITTY_WINDOW_ID" --last 20 --json
amx debug explain --window "$KITTY_WINDOW_ID" --json
amx daemon export-events --window "$KITTY_WINDOW_ID" --last 100 --pretty=false |
  jq '.windows[] as $w | $w.events[] | select(.event == "HarnessProcessExited") as $exit | {exit: $exit, render: [$w.renderer.generations[]? | select(.generation == $exit.render_generation)]}'
```

`hook.process_detection` records `complete_identity`, `incomplete_identity`, or
`identity_absent`. A committed `daemon.process_lifecycle` record and matching
`HarnessProcessExited` event use one of the public reasons
`pipe_process_exited`, `interactive_process_exited`, or
`reset_process_exited`. The event carries the exact safe process fingerprint,
missing-versus-replaced observation, affected reducer sources, actual
before/after effective state, withdrawn/cleared counts, terminal-hook evidence,
and `render_generation`. Match that generation against
`renderer.generations[]` to prove the title update completed. The
`process_lifecycle_recovery` explanation explicitly reports when no terminal
hook was observed. Command lines, prompts, arguments, payloads, transcripts,
and arbitrary paths are absent from this evidence.

## Storage, defaults, and privacy

Diagnostic state lives below `$XDG_STATE_HOME/agent-mux`, falling back to
`~/.local/state/agent-mux`:

| Path | Contents | Raw content? |
| --- | --- | --- |
| `journal/` | Bounded causal metadata for hook, IPC, daemon, renderer, notification, probe, and ASL stages | No; payloads are represented by byte count and a local-key HMAC |
| `daemon/` | Structured operational daemon logs | No hook payload bodies |
| `native/<launch-id>/` | Harness-native debug output for an AgentMux-managed launch | Yes; private local data |
| `captures/` | Explicit temporary hook-payload captures | Yes; only after `amx debug capture start --include-hook-payloads` confirmation |
| `spool/` | Emergency journal records awaiting daemon-owned promotion | Causal metadata only |

Directories are `0700`; files and generated bundles are `0600`. Managed paths
reject symlinks, archive output is a flat controlled member set, and bundle
publication uses a private sibling temporary file plus atomic rename.

Journaling and native logging are enabled by default. Native harness logs are
raw and may contain prompts, tool arguments, filesystem paths, source text, and
provider output. Keep them local. The defaults retain journal and native data
for 14 days, cap the journal at 256 MiB, cap all managed native logs at 1024
MiB, and cap one launch at 128 MiB. Age and quota maintenance protects active
launches and applies a grace period to newly created launch directories.

```toml
[defaults.diagnostics]
journal_enabled = true
journal_keep_days = 14
journal_max_mib = 256
journal_detail = "standard"
native_logging = true
native_keep_days = 14
native_max_mib = 1024
native_session_max_mib = 128
```

At the default `journal_detail = "standard"`, the hook client samples healthy
traffic: success-class intermediate stages (`hook.kitty_detection`,
`hook.window_resolution`, `hook.payload_read`, `hook.validation`,
`hook.process_detection`, `hook.capture`, `hook.ipc_attempt`,
`hook.acknowledgement`) are not persisted, so one healthy hook event costs a
few journal records instead of a dozen. `hook.delivery` is always written and
carries the payload byte count and HMAC, and every error, rejection, or
degraded outcome on any stage is always written. Renderer stages likewise emit
one record per (generation, stage); coalesced trace/request IDs live in the
`trace_ids`/`request_ids` evidence lists and exact-ID selectors match through
them. As a consequence, `amx debug tail --stage hook.validation` (or any other
intermediate stage) shows few or no rows on healthy traffic — failures still
appear there. Set `journal_detail = "full"` to restore every per-stage success
record while reproducing a problem.

Set `native_logging = false` to opt out of AgentMux-managed raw logging. An
explicit harness logging flag or environment setting always wins; `amx debug
status` and `amx doctor` classify coverage as `managed`, `external`,
`degraded`, or `disabled`. External paths are not governed by Agent Mux
permissions or retention.

Raw hook payload capture is separate and off by default:

```bash
amx debug capture start --window 42 --duration 10m --include-hook-payloads
amx debug capture status
amx debug capture stop
```

Non-interactive capture start also requires `--yes`. Captures expire after at
most 60 minutes, share a fixed 64 MiB quota, are purged after 24 hours, and are
never inputs to a bundle.

Hook payload transport has two accepted encodings without changing delivery
protocol v1. Payloads through 256 KiB are forwarded byte-for-byte with
`payload_encoding=json`. Larger payloads through 1 MiB are validated in their
original form, retain original byte-count/HMAC and authorized raw-capture
evidence, then forward only an allowlisted semantic representation with
`payload_encoding=json_compact`; bulk transcript or message text is omitted.
Payloads above 1 MiB are rejected before process discovery, IPC, or reducer
mutation with reason `payload_oversized`. Legacy hook entry points remain
fail-open, while `hook-indicator --strict` returns exit status 2 for that
permanent rejection.

## Bundle modes and sharing

`amx debug bundle` defaults to `--mode share`. Share bundles use field
allowlists, HMAC identifiers, path aliases, and controlled native-log excerpts.
They exclude raw native logs and raw hook captures. Review `manifest.json`
before sharing; it records every included, transformed, dropped, or unavailable
source.

`--mode forensic` includes broader scrubbed diagnostic text but is explicitly
`LOCAL ONLY`. It is not a less-redacted sharing mode. Raw captures and raw
native logs are still excluded. Never share a forensic bundle.

## Event contracts and intentional exclusions

Run `amx config hooks-status` after a CLI upgrade and `amx config hooks-sync`
when generated definitions drift. `amx debug status` combines the installed
hook definitions, trust/disable settings, known CLI-version contract, and
last-seen journal evidence. A newer unreviewed harness version is reported as
unverified rather than silently trusted.

Agent Mux installs only documented command-hook events that are safe observers.
Claude observer receipts such as `InstructionsLoaded`, `CwdChanged`, and
`WorktreeRemove` are recorded as intentional suppressions. `FileChanged` and
`WorktreeCreate` are intentionally not installed because they require matcher
or response semantics Agent Mux does not own. Codex edit approvals and
`request_user_input` waits are screen-authoritative: an early native tool-start
receipt remains active, and the daemon's conservative Kitty-screen probe alone
raises or retracts the visible question. Copilot
`notification.agent_idle` describes a background agent and does not stop the
interactive session. Unknown future events are retained as rejected evidence
without reducer mutation.

`amx daemon export-events` remains the compatibility snapshot. It emits schema
v11 by default, adding bounded terminal-stop-hold evidence to the schema-v10
harness-process lifecycle fields and intervention counters, themselves layered
on the schema-v9 trace/request/source-event identities, sanitized
reducer input and outcome evidence, routing evidence, saturation attribution, render generations,
notification dispositions, and child probe links. Consumers can request
`--schema 10` for the pre-stop-hold shape, `--schema 9` for the previous
complete causal shape, or `--schema 8` for the pre-causal shape; all three are
explicit projections and never receive later fields accidentally.

## ASL delivery outcomes

`agent-session-loop` derives one stable request ID from each persisted
`source_event_id` and invokes `amx hook-indicator --strict --json`. AgentMux
returns one of four outcomes:

| Outcome | Meaning | ASL behavior |
| --- | --- | --- |
| `accepted` | The daemon admitted the original logical event | Success |
| `duplicate` | The same request was already applied | Success; no second reducer mutation |
| `retryable_failure` | Transport or daemon availability may recover | At most three attempts, with 100 ms and 300 ms backoffs |
| `permanent_failure` | Payload, routing precondition, or daemon acknowledgement is terminal | Stop retrying that event |

ASL persists exactly one sanitized `agent_mux_delivery` outcome containing the
bounded attempt summaries. Delivery remains fail-open: exhaustion, a missing
`amx` binary, legacy downgrade, or outcome-recording failure never changes ASL
run control. Older AgentMux binaries that reject the acknowledgement flags are
downgraded once per executable to the legacy unacknowledged invocation.

## Failure matrix

Use the first-line command in the final column, then narrow with a trace,
request, or source-event ID from `tail`.

| Failure | Durable evidence or verdict | First response |
| --- | --- | --- |
| Disabled or untrusted hook | `debug status` activation finding; explanation `hook_disabled` or `hook_untrusted` | Correct the effective harness/profile setting, then run `amx config hooks-sync` |
| Invalid payload | `hook.validation` rejected and quarantined `daemon.decision`; raw body absent unless temporary capture was authorized | `amx debug explain --request <id>` |
| Missing Kitty | `hook.kitty_detection` rejected with `outside_kitty` | Confirm the hook came from a Kitty-managed session and that `KITTY_LISTEN_ON` is present |
| Daemon unavailable | Numbered `hook.ipc_attempt` and `hook.daemon_autostart` stages ending in retryable delivery failure | Check `amx debug status`, daemon log, and binary identity |
| Retry exhaustion | ASL `agent_mux_delivery` contains all attempts and final retryable outcome | Repair availability; ASL itself remains fail-open |
| Duplicate delivery | Hook outcome `duplicate` plus `request_replayed` daemon decision; no reducer mutation | Treat as successful idempotency unless duplicates grow unexpectedly |
| Queue saturation | Request-scoped `ingress_queue` or `dedupe_cache` saturation evidence and doctor metrics | Inspect burst source and daemon latency with `debug explain` |
| Route mismatch | `owner_mismatch` routing evidence with rejected alternatives | Compare producer and managed-owner identity; resync stamped hooks |
| Non-interactive run in a plain pane | `unmanaged_pipe_source` suppression with no reducer mutation | Expected and benign: `claude -p` or `codex exec` in an unmanaged pane must not drive icons |
| Quarantined identity | `quarantined` decision with synthesized fallback identity and low confidence | Fix missing/malformed session identity; do not infer ownership from titles |
| Reducer suppression | Decision reason such as reordered, duplicate source sequence, guard, or protected-state precedence | Inspect before/after reducer evidence in `debug explain --json` |
| Missing terminal hook / process exit | `hook.process_detection`, a committed `daemon.process_lifecycle`, `HarnessProcessExited`, and explanation `process_lifecycle_recovery` share the exact fingerprint and render generation | Use the tracing commands above; distinguish `missing` from `replaced_instance`, then confirm the linked renderer generation completed |
| Render failure | `daemon.renderer.failed`, retained dirty generation, and doctor renderer drift | Restore Kitty remote control; the same generation remains retryable |
| Notification dispatch failure | `daemon.notification.dispatch` or `daemon.notification.focus` failed with `send_failed`, `backend_timeout`, or `focus_failed`; explanation `notification_failure` | Inspect the backend error and daemon lifecycle; do not treat delivery failure as policy suppression |
| Notification suppression | `daemon.notification.decision` or `daemon.notification.focus` suppressed with `muted`, `pipe_mode`, `config_disabled`, `cooldown`, `backend_unavailable`, `focus_unavailable`, `focus_target_missing`, `focus_target_stale`, `unsupported_action`, `interaction_timeout`, or dismissal; `daemon.notification.dispatch` cancelled with `daemon_shutdown`; explanation `notification_suppression` | Run `amx daemon status` and `amx doctor` for Alerter capability failures. For focus failures, verify the originating Kitty socket and window still exist. Change notification policy only if the suppression was unintended |
| Quota exhaustion | Capture/native status reports the bounded quota and denied or pruned write | Stop/purge capture or reduce retention; active native launches remain protected |
| Restart recovery | Daemon lifecycle boundary plus restored reducer facts and later render generation | Compare CLI/daemon build, then replay the journal or bundle offline |

## Automated and provider smoke boundaries

`just check` includes a deterministic fake-harness acceptance fixture covering
the hook command, journal, Unix IPC, daemon decision, duplicate acknowledgement,
renderer, notification suppression, share bundle, replay, restart-safe evidence,
and ASL correlation. It has no model usage and never contacts a real provider,
Kitty instance, or notification backend.

Actual provider smoke is operator-attended and deliberately separate:

1. Install and verify the exact artifact with `just install-restart-smoke`.
2. Run `amx config hooks-sync`, `amx config hooks-status`, and `amx doctor --strict`.
3. In one managed session for Claude, Codex, and Copilot, record the CLI version
   and exercise prompt, tool, permission/elicitation, terminal, and error paths.
4. For each path, retain `amx debug explain --source-event <id> --json` or a
   schema v11 `amx daemon export-events --window <id>` snapshot.
5. Create a share bundle only if the evidence must leave the machine; inspect
   its manifest first. Do not mark a live provider row complete unless an
   operator intentionally incurred and recorded the provider/model usage.

The native-notification protocol smoke does not require provider/model usage.
Create a disposable, unfocused Kitty window with a unique managed session ID,
send one terminal lifecycle event from that window, and leave the notification
untouched through alerter's five-minute protocol timeout. Keep the disposable
window open so renderer ticks continue. The exact-window tail must contain
`daemon.notification.dispatch` as `sent/send_success`, followed by
`daemon.notification.focus` as `suppressed/interaction_timeout`, with no
`failed/send_failed` or `failed/backend_timeout`. Record the baseline and final
spool counts, newest `observed_at`, and bounded exact-explain node/edge counts,
then close only the disposable window.
