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
`--json` for the stable schema-versioned result. `tail --follow` follows only
new persisted records; it does not require a running daemon.

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
Kitty connection or daemon socket.

## Storage, defaults, and privacy

Diagnostic state lives below `$XDG_STATE_HOME/agent-mux`, falling back to
`~/.local/state/agent-mux`:

| Path | Contents | Raw content? |
| --- | --- | --- |
| `journal/` | Bounded causal metadata for hook, IPC, daemon, renderer, notification, probe, and ASL stages | No; payloads are represented by byte count and a local-key HMAC |
| `daemon/` | Structured operational daemon logs | No hook payload bodies |
| `native/<launch-id>/` | Harness-native debug output for an AgentMux-managed launch | Yes; private local data |
| `captures/` | Explicit temporary hook-payload captures | Yes; only after `amx debug capture start --include-hook-payloads` confirmation |
| `spool/` | Emergency journal records awaiting reconciliation | Causal metadata only |

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
native_logging = true
native_keep_days = 14
native_max_mib = 1024
native_session_max_mib = 128
```

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
`request_user_input` waits are not command-hook events in supported versions;
the daemon uses conservative Kitty-screen probes instead. Copilot
`notification.agent_idle` describes a background agent and does not stop the
interactive session. Unknown future events are retained as rejected evidence
without reducer mutation.

`amx daemon export-events` remains the compatibility snapshot. It emits schema
v9 by default, adding trace/request/source-event identities, sanitized reducer
input and outcome evidence, routing evidence, saturation attribution, render
generations, notification dispositions, and child probe links. Consumers that
need the previous shape can request `--schema 8`; schema v8 is an explicit
projection and never receives v9-only fields accidentally.

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
| Quarantined identity | `quarantined` decision with synthesized fallback identity and low confidence | Fix missing/malformed session identity; do not infer ownership from titles |
| Reducer suppression | Decision reason such as reordered, duplicate source sequence, guard, or protected-state precedence | Inspect before/after reducer evidence in `debug explain --json` |
| Render failure | `daemon.renderer.failed`, retained dirty generation, and doctor renderer drift | Restore Kitty remote control; the same generation remains retryable |
| Notification suppression | `daemon.notification.decision` with `muted`, `pipe_mode`, `config_disabled`, `cooldown`, or `unsupported_action` | Change notification policy only if the suppression was unintended |
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
   schema v9 `amx daemon export-events --window <id>` snapshot.
5. Create a share bundle only if the evidence must leave the machine; inspect
   its manifest first. Do not mark a live provider row complete unless an
   operator intentionally incurred and recorded the provider/model usage.
