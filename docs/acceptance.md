# Product acceptance

The AgentMux 0.7.0 product acceptance gate is:

```bash
just check
```

It runs `go test ./...`, the race-sensitive transcript, plan, handoff,
worktree, daemon, and TUI suites, `go vet ./...`, every committed TUI
scenario, and `golangci-lint`. `just acceptance` is the same gate without the
optional linter dependency.
All persistence tests use temporary homes, transcript sources, plan roots, and
cache paths.

The cross-repository release gate is:

```bash
just --justfile /Users/sglavoie/scripts/justfile --working-directory /Users/sglavoie/scripts test-all
just --justfile /Users/sglavoie/scripts/justfile --working-directory /Users/sglavoie/scripts lint
just --justfile /Users/sglavoie/scripts/justfile --working-directory /Users/sglavoie/scripts verify-asl-install
```

These commands cover acknowledged ASL delivery and installation without
changing `/Users/sglavoie/scripts/claude/.claude/settings.json`.

## Diagnostic regression acceptance

`status`, `tail`, `explain`, and `bundle` use schema v2 and share
`journal_snapshot` scan accounting. Their journal reads are immutable snapshots:
they merge segment and spool evidence in memory while the daemon alone owns
60-second promotion plus bounded compaction and 15-minute retention. Status
collects all profile last-seen evidence during its single journal traversal,
before applying display selectors or limits. `tail --last` applies its limit
after event-time ordering. Exact explain queries default to `--graph-depth 2`,
`--max-nodes 250`, and `--max-edges 1000` and publish explicit truncation
metadata. `replay` remains schema v1 while accepting both v1 and v2 diagnostic
bundles and causal-record wrappers.

| Reproduced failure | Automated regression | Installed/live evidence |
| --- | --- | --- |
| A delivered alerter notification became `failed/send_failed` at 300 seconds | `internal/daemon` pins the five-minute protocol timeout, one-minute wrapper grace, structured interaction result, cancellation, and exactly one terminal disposition | An untouched disposable notification records `sent/send_success` and later `suppressed/interaction_timeout`, never `failed/send_failed`; explain returns `notification_suppression`. Backend/wrapper failures separately return `notification_failure`. |
| `tail --last` returned stale append-order evidence | `internal/diagnostics` and `internal/debugger` cover delayed spool records, merged deduplication, deterministic event-time ordering, and follow cursors across promotion | The installed tail's last record has the newest matching `observed_at`. |
| Exact explain expanded into an unbounded unrelated graph | `internal/debugger` covers typed breadth-first causal traversal, contextual non-frontiers, high fan-out, depth/node/edge pruning, and deterministic output | A bounded exact query stays within its requested limits and reports every applied truncation reason. |
| A large debug read held the journal lock and forced writer spool bursts | Snapshot concurrency tests hold parsing open while writers append, prove descriptors use fixed byte ceilings, and assert reader paths are mutation-free | Baseline and post-read spool counts do not increase because of `status`, `tail`, `explain`, or bundle journal reads; renderer evidence continues during the read. |
| Multi-profile hook status scanned the full journal once per profile | A temporary-root acceptance fixture creates 2,000 promoted segments, instruments status traversal, and verifies identical profile verdict/last-seen digests before and after compaction | One status report records one traversal while its visitor observes hook evidence for every profile before filtering. Synthetic before/after scan counts and timings are informational, not pass/fail thresholds. |
| Short-lived hooks accumulated thousands of tiny immutable segments | The same fixture runs bounded maintenance to convergence, verifies a 2,000-to-1 file-count reduction, and proves all 2,000 unique records remain observable. `internal/diagnostics.TestJournalCompactPublishesValidRecordsBeforeQuarantiningCorruptInput` separately proves corrupt originals are moved into quarantine byte-for-byte intact. | `journal.maintenance` reports startup/periodic compaction input/output files and bytes plus duplicates omitted. |
| Payloads above 256 KiB were replaced with empty JSON and collapsed reducer identity | Command-level acceptance delivers mixed large Claude/Codex events as `json_compact`, verifies distinct source keys and session-specific terminal clearing, then proves `payload_oversized` returns strict exit 2 versus legacy fail-open without reaching the daemon | Payloads through 256 KiB remain byte-identical; accepted compact payloads preserve semantic identity through 1 MiB; larger input never invokes the reducer. |
| Doctor permanently reported `last_seen_missing` for an active claude profile because hook evidence was credited to the routed target window's profile | `internal/indicator` stamps `config_dir` on every hook stage and `producer_config_dir` on requests; `internal/hooks` keys status evidence by `(harness, home)` with legacy fallback and shared-home crediting; `internal/daemon` decision-trace tests separate producer and target identity | Strict doctor reports no `last_seen_missing` for profiles with live sessions; an end-to-end trace shows `evidence.producer_config_dir` beside the routed `route.effective_profile` with tab-owner routing unchanged. |
| `Append` lost its 25 ms lock to the compaction commit window every maintenance minute, spilling spool bursts at :54 | `internal/diagnostics` proves the compaction commit window does not block `Append` (dedicated `append.lock` sidecar, `sealedMu`/`appendMu` split, 512-file input bound, 2 s maintenance lock timeout); `internal/daemon` runs maintenance against four hot appenders and asserts zero lock-timeout records | `amx debug tail --follow --json` across a maintenance minute shows no `journal.maintenance … journal lock timeout` records and no spool-fallback bursts; both `events.lock` and `append.lock` sidecars persist in the journal directory. |
| ~13 journal records per hook event wiped retained history in minutes, making the hook-silence check's 4 h window fiction | `internal/indicator` covers standard-detail sampling (success-class intermediate stages suppressed, `hook.delivery` always written with payload HMAC/size, errors never sampled) plus the `journal_detail = "full"` escape hatch; `internal/daemon` and `internal/debugger` cover coalesced renderer records and explain over sampled journals | Journal growth drops roughly 5× under multi-session load and `journal.health.earliest_observed_at` holds a multi-hour horizon after soak. |
| Promoted spool files were counted as plain segments and dropped deliveries had no doctor visibility | `internal/diagnostics` covers `promoted_spool_files_read` and the `earliest_observed_at` horizon over mixed layouts; `internal/hooks` covers the shallow-horizon versus missing distinction and the trailing-hour delivery-failure window; `internal/debugger` and `cmd` cover the stable `hook_delivery_failures` finding and the warning-only strict-doctor line | `journal_snapshot.promoted_spool_files_read` is present in schema-v2 reads; `last_seen_horizon` wording appears only when retained history is genuinely shallower than the verification window; dropped deliveries surface as a warning for up to an hour after a daemon restart. |

## Workflow coverage

This table is an exact, manually maintained inventory. It is not exhaustively
enforced by `cmd.TestReleaseDocumentationPinsVersionAndDiagnosticWorkflow`, which
checks only the required `2,000-to-1`, `json_compact`, `strict exit 2`, and
`informational` substrings; omitting a workflow row will not make that test fail.

| Workflow | Automated coverage |
| --- | --- |
| Focus or resume an exact conversation | `internal/actions` continuity tests and `cmd` session command tests cover live focus, native resume, selector errors, and schema-v1 output. |
| Native and portable forks | `internal/actions` covers native Claude/Codex forks and portable Copilot handoff forks without mutating the source conversation. |
| Isolated worktree fork | `internal/worktree`, `internal/actions`, and `cmd` cover clean/dirty repositories, linked worktrees, collision handling, launch compensation, and explicit confirmation. |
| Cross-harness handoff | `internal/handoff` covers every source/target harness pair, private standalone artifacts, edited drafts, bounded context, and partial launch failure. |
| Past Sessions | `internal/transcript` and `tui` cover cold and warm cache loading, source refresh, filtering, viewer actions, deletion hiding, pruning, stale-result guards, and last-good rows. |
| Plans | `internal/plans` and `tui` cover filesystem discovery, source fallback, search-sidecar repair, lazy warmup, selection continuity, and last-good rows. |
| Narrow and wide TUI layouts | All JSON scenarios under `docs/tui-testing/scenarios` cover the live dashboard, overlays, Past Sessions, Plans, and their viewers. |
| TUI continuity | The hermetic `session-fork-continuity` scenario enters Past Sessions and launches a native fork through the shared action registry while recording, rather than executing, the Kitty launch. |
| Diagnostic pipeline | `cmd.TestAcceptanceDiagnosticPipeline` drives the real hook command adapter through journal persistence and Unix IPC into an in-process daemon with fake Kitty, then checks duplicate acknowledgement, renderer writes, notification suppression, compact large-payload source isolation and terminal clearing, strict/legacy oversize behavior, a schema-v2 snapshot-backed share bundle and offline replay, and a bounded ASL source-event explanation. `internal/debugger.TestAcceptanceSinglePassStatusSurvivesTinySegmentCompaction` records synthetic status timings and proves one traversal plus lossless 2,000-segment convergence. `internal/debugger.TestReplayShareBundleIsPortableAndPreservesReplaySafeEvidence` rewrites the bundle as schema v1 and verifies v1/v2-compatible replay. |
| Harness process lifecycle | `cmd.TestAcceptanceHarnessProcessLifecyclePipeline` uses a real Unix socket plus controllable `ps` and fake Kitty boundaries to prove cross-harness pipe and interactive exits, parent/child and concurrent isolation, attention preservation, native-hook races, reset and ASL exclusions, schema-v4 restart settlement, schema-v10 diagnostics, and linked renderer completion. The automated/live boundary and optional installed drill are documented in [Process-Lifecycle Acceptance](acceptance-testing.md). |
| Terminal stop confirmation | `internal/daemon/terminal_stop_hold_test.go` proves a native terminal is withheld at `⚡` until both the quiet period elapses and an idle probe confirms, that the deadline commits an unconfirmed hold, and that a new prompt supersedes one; the bypass matrix (pipe mode, no idle probe, live ASL lease, no Kitty client, `enabled = false`) and a verified process exit all skip the confirmed-idle wait, restoring the immediate `🛑` for every bypass except the live ASL lease, whose stop the reducer goes on masking until the loop finishes. `internal/daemon/handler_stop_hold_export_test.go` pins the schema-v11 `stop_hold_outcome`/`stop_confirmed_by`/`pending_terminal_stop` projection, its removal under schema v10, and the live hold reaching `daemon export-events`. `internal/config/config_stop_confirm_test.go` covers `[defaults.stop_confirm]` defaults and validation, and `cmd.TestInvariantStopHoldStuckWarnsPastTwiceTheTimeout` pins the `stop_hold_stuck` warning without changing the healthy verdict. Live per-harness confirmation against an installed binary is operator work, recorded in [Daemon State Acceptance](daemon_state_acceptance.md). |
| Claude stale pane-title recovery | `internal/daemon.TestProbeClaudeScreenStop_RestingBrailleDoesNotPromoteEmptyWindow` proves a resting Braille title never creates a phantom ⚡ or busy anchor, `internal/daemon.TestProbeClaudeScreenStop_RestingBrailleRequiresFourIdleSamples` pins the four-sample exit from an existing phantom, and `internal/daemon.TestProbeClaudeScreenStop_BusyBrailleDescriptionVetoesIdleScreen` preserves the active-description veto for a genuinely working pane whose screen appears idle. |
| Dashboard run timer | `internal/daemon` proves the busy-stretch anchor the timer reads: `TestBusySinceStampedOnActiveClassEntry` and `TestBusySinceSurvivesActiveClassChurn` start it once per stretch and hold it across a `⚡`/`🤖`/`🌀` handoff, `TestBusySinceClearedByEveryNonActiveIcon` clears it on stop and on a permission pause as well as every other non-active icon, and `TestSweepStale_ClearsBusySinceWithNilReducer` clears it on a stale sweep and removes it from the export. It survives a daemon restart at its original instant through `TestUserVarV4RoundTripPersistsFactsProcessAndEffective`, `TestRestoreFromUserVarsKeepsLivePersistedBusySince`, and `TestRestoreFromUserVarsStampsBusySinceForFactlessPayloads`, with `TestUserVarV1IgnoresBusySinceAndSelfHealsOnRestore` pinning the pre-upgrade self-heal, and reaches the dashboard through `TestHandler_StateQueryReportsBusySinceAndOmitsItWhenIdle` plus `tui.TestApplyDaemonStatesStampsBusySince`. `TestHandler_ExportProjectsBusySinceAndOmitsItWhenIdle` pins current-schema export inclusion and idle omission, while `TestProjectExportEventsPayloadV11IdentityProjection` pins schema-v11 exclusion and schema-v12 inclusion. `tui.TestFormatRunElapsedBoundaries`, `tui.TestActivityCellTextWarningRequiresTimer`, `tui.TestRenderListActivityColumnHiddenByDefault`, and the `run-timer-narrow-fit`/`run-timer-wide` scenarios cover the rendered bands, the `⏳` warning belonging only to rows with a live timer, and both widths. Live per-harness confirmation against an installed binary is operator work, recorded under [Dashboard run timer](#dashboard-run-timer). |
| Debug workflow | `cmd.TestReleaseDocumentationPinsVersionAndDiagnosticWorkflow` pins AgentMux 0.7.0, schema v12 export documentation, the schema-v2 snapshot contract, event-time tail semantics, explain graph defaults, v1/v2 bundle replay, the `amx debug status` → `amx debug explain` → `amx debug bundle` → `amx debug replay` workflow, and every required failure-matrix row including distinct notification failure and suppression. |
| Bundle secrecy and traversal | `internal/debugger` tests plant a secret corpus, verify share/forensic redaction, exclude raw sources, reject symlink publication, build flat deterministic archives, and replay portable share evidence. |
| Restart and replay equivalence | `internal/daemon` restart-prefix tests and `internal/debugger` golden replay tests cover restored reducer state including exact process ownership, daemon sequence resets, ordering, routing, ASL phases, duplicate requests, coalesced renders, and explicit schema-v8/v9 limitations. |
| ASL delivery correlation | `/Users/sglavoie/scripts/tests` covers stable UUIDv5 request IDs, bounded retry/backoff, duplicates, permanent failures, legacy fallback, missing binaries, exhaustion, metrics/tail/Workbench visibility; `internal/debugger.TestExplainCorrelatesASLSourceDeliveryDaemonAndRenderEvidence` pins the cross-repository graph. |
| ASL human-actionable questions | A worker wait remains bare 🔁 until the explicit `asl outbox wait` supervisor handoff proves an actionable open set. `internal/hookevent` recognizes the body-free question/reply/resolution schema; `internal/agentloop` validates the fixed-size v2 compatibility projection and rejects v1/stale/malformed/foreign evidence; `internal/daemon` covers immediate ❓🔁 plus desktop notification, duplicate idempotence, F8 dismissal until a newer question, lease/trusted-Stop fallback ordering, anonymous/pipe-worker stop rejection, heartbeat resistance, aggregate reply/resolution fast clearing, stale-closure cutoffs, multiple-question final closure, reply/zero-projection/finish fallbacks, restart-safe reducer state, and notification behavior. Wait/reply reconciliation, stale-sweep exemption, explicit paused questions, sanitized detail preview, TUI attention grouping, `awaiting response` gloss, and `]q` navigation remain covered. |

## Filesystem persistence coverage

| Acceptance case | Automated evidence |
| --- | --- |
| Corrupt metadata recovers without losing sources | Transcript and plan filesystem tests quarantine malformed metadata, rebuild from authoritative files, and retain usable snapshots alongside warnings. |
| Concurrent writers preserve discovered entries | Transcript and plan race tests synchronize through stable lock sidecars, reload under lock, and merge dirty snapshots before atomic replacement. |
| Search sidecars are disposable | Transcript and plan search tests reconstruct missing or invalid sidecars from source content and retain last-good inline text when persistence fails. |
| Startup remains source-backed | TUI warmup and hermetic tests prove Past Sessions and Plans initialize from private filesystem caches and do not create retired platform state. |
| Handoffs remain standalone | Handoff artifact and workflow tests verify private Markdown output, local metadata, hash validation, edit handling, and launch-independent persistence. |
| Retired stacks cannot return silently | `cmd.TestSourceTreeHasNoRetiredPlatformStacks` rejects obsolete package directories or imports and forbids the retired state-file pathname in production Go sources. |
| Warm and partial UI refreshes retain useful rows | `tui.TestAcceptanceWarmPastSessionsStayVisibleAndPartialRefreshPublishesSuccessfulRows`, plan last-good acceptance, and filesystem partial-discovery tests keep warm or successful rows visible while surfacing warnings. |
| Continuity assets obey launch lifetime | Handoff workflow tests retain private final artifacts after successful and failed target launches; action/worktree tests retain successful Git worktrees and compensate only the newly created worktree after launch failure. |
| No command path recreates SQLite | TUI, daemon, doctor, and session-command acceptance tests run under temporary homes and assert the complete retired artifact allowlist remains absent. Production-source invariants independently reject the old database pathname. |

Filesystem metadata and search sidecars are disposable caches. Harness transcript
files, plan directories, handoff artifacts, Git repositories/worktrees, and
`config.toml` remain authoritative. Cache files and lock sidecars use private
or source-appropriate modes, and explicit destructive actions retain their
confirmation boundaries.

## Installed release smoke

After both repository gates pass, install and validate the same artifact:

```bash
just install-restart-smoke
amx config hooks-sync
amx config hooks-status
amx doctor --strict
```

`install-restart-smoke` builds and atomically installs the binary, restarts the
daemon, waits for `ready: true`, and runs strict doctor against matching
CLI/daemon build identities. Hook synchronization happens afterward because a
public hook contract changed: the safe Claude observers must be installed
before the final strict-doctor record is retained.

The release uses schema v12 for `amx daemon export-events` by default. Acceptance
also retains `--schema 11`, `--schema 10`, `--schema 9`, and `--schema 8` coverage
as explicit compatibility projections: schema 11 omits the v12 busy-stretch
anchor, schema 10 omits v11 stop-hold evidence, and schemas 9 and 8 additionally
omit v10 process fields. For first-line diagnosis use:

```bash
amx debug status
amx debug explain --source-event <id>
amx debug bundle --mode share -o agent-mux-debug.tar.gz
amx debug replay agent-mux-debug.tar.gz
```

For the installed-artifact diagnostic record, save `amx debug status --json`,
`amx debug tail --last 50 --json`, `amx debug probe --json`, and one exact
`amx debug explain --source-event <id> --graph-depth 1 --max-nodes 25
--max-edges 50 --json`. Assert the explanation has at most 25 nodes and 50
edges and that its `limits`, `truncation`, and `journal_snapshot` fields match
the invocation. Create a share bundle, inspect `manifest.json`, and replay the
same archive. The CLI/daemon build match from status and strict doctor must
identify the exact artifact installed by `just install-restart-smoke`.

Before the reads, record `.journal.health.spool_files` and the newest matching
`observed_at`; record both again afterward. A read-only workflow must not create
a spool burst, and `tail --last` must retain the newest event-time evidence even
when delayed spool records exist.

The failure matrix, native-log privacy defaults, bundle sharing policy, ASL
delivery outcomes, and provider drill are in [Debugging event delivery](debugging.md).

After `just acceptance` succeeds, an upgrade from a legacy SQLite build uses a
manual, one-off deployment flow:

1. Build the new binary, stop the installed daemon, and verify its socket and
   process have exited.
2. Preserve `transcripts.json`, `plans.json`, both search directories,
   `handoffs/`, `worktrees/`, `launch_overrides.json`, and ordinary daemon lock
   files.
3. Remove only `agent-mux.db`, its `-wal` and `-shm` files,
   `agent-mux.db.recovery.lock`, `agent-mux.db.rebuild`,
   `agent-mux.db.rebuild-startup.lock`, and the database `recovery/` directory.
4. Install atomically, restart the daemon, and run `amx daemon status`,
   `amx doctor --strict`, and `amx session list`. Open Past Sessions and Plans.
5. Confirm the retired artifacts remain absent. No automatic cleanup or
   migration is added for later runs.

Record `just acceptance`, optional `just check`, installed daemon/doctor/session
smoke, cache population, and the post-smoke filesystem inventory in the release
handoff.

### Dashboard run timer

Automated coverage stops at the daemon boundary and the hermetic TUI scenarios,
so the timer's behaviour against a real harness is operator-attended work on the
artifact `just install-restart-smoke` installed, after strict doctor reports no
new reducer or renderer findings and matching CLI/daemon build identities:

1. Send a long prompt to a live session. The dashboard row shows ⚡ with a
   timer counting up in `refresh_interval` steps — 2 seconds by default, so the
   seconds digit advances in visible jumps rather than smoothly.
2. At a permission prompt (🔑) the timer clears to `—`; approving it restarts
   the timer from `0:00`.
3. After a turn finishes (🛑) the timer stays `—`.
4. Restart the daemon mid-turn. The timer resumes from the original anchor
   rather than from zero; confirm the restored window with
   `amx daemon export-events --current-window --last 50`.
5. Temporarily lower `[defaults.watchdog].active` and restart the daemon to
   force a stale sweep. ⏳ appears beside the still-running timer, then the icon
   and the timer clear together.
6. Across a subagent handoff (⚡ → 🤖 → ⚡) the timer does not reset.

## Automated versus live-provider evidence

The automated diagnostic fixture has no model usage. It substitutes a local
fake Kitty executable and never contacts Claude, Codex, Copilot, a real Kitty
instance, or a notification backend. It is the completion gate for deterministic
delivery, replay, bundle, and correlation behavior.

Actual provider smoke is a separate operator-attended record. For each installed
provider, record the CLI version and intentionally exercise prompt, tool,
permission/elicitation, terminal, and failure paths in a managed Kitty session.
Retain a schema v12 export or `amx debug explain --source-event <id> --json` for
each path. Do not infer that this drill ran from an automated pass, and do not
incur provider/model usage merely to complete the automated gate.

Native-notification timeout evidence is a separate provider-free live check:

1. Start the daemon with a restricted launchd-style `PATH` and require
   `amx daemon status` plus `amx doctor --strict` to report Alerter 26 or newer,
   its resolved executable, and interactive capability. This verifies discovery
   from the standard installation directories rather than the caller's shell.
2. Launch two disposable, unfocused Kitty windows/tabs with unique
   AgentMux-managed session identities. Record the target window ID, its Kitty
   socket, the UTC start time, and baseline spool count; never reuse an active
   user session.
3. Send one terminal lifecycle event from the target pane, click its native
   notification, and require Kitty to become foreground with that exact OS
   window, tab, and pane focused. Confirm the notification uses Kitty's sender
   identity. Post another notification, close its target before clicking it,
   and require a
   `focus_target_stale` suppression with no fallback to the other disposable
   window.
4. Send a second terminal lifecycle event from the disposable target and leave
   the native notification untouched for the complete five-minute alerter
   protocol timeout. Keep the window alive so daemon renderer activity
   continues.
5. Query that exact window and time range. Require dispatch
   `sent/send_success`, then focus `suppressed/interaction_timeout`, with no
   `failed/send_failed` or `failed/backend_timeout`. A bounded exact explanation
   must classify the no-interaction result as `notification_suppression`.
6. Confirm renderer events continued, the final spool count did not show a
   reader-induced burst, and `tail --last` ends at the newest event time.
7. Close only the disposable Kitty windows after preserving the status, tail,
   explain, bundle, and replay evidence.
