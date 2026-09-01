# Product acceptance

The AgentMux 0.8.0 product acceptance gate is:

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
just asl-integration
```

These commands cover acknowledged ASL delivery and installation without
changing `/Users/sglavoie/scripts/claude/.claude/settings.json`.

`just asl-integration` is the live half of the contract-set gate and the only
recipe that talks to a real `asl`. It is deliberately outside `just check`,
which stays hermetic, and it never skips when asked for: an ASL that is absent
or built from a different corpus is exactly the incompatibility it exists to
catch.

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
| Claude stale pane-title recovery | `internal/daemon.TestProbeClaudeScreenStop_RestingBrailleDoesNotPromoteEmptyWindow` proves a resting Braille title never creates a phantom ⚡ or busy anchor, and `internal/daemon.TestProbeClaudeScreenStop_RestingBrailleRequiresFourIdleSamples` pins the four-sample exit from an existing phantom. |
| Claude stale description titles | `internal/daemon.TestProbeClaudeScreenStop_BusyBrailleDescriptionSettlesAfterMarkerRun` proves an idle-at-prompt pane whose title still carries a task description settles on the four-sample marker run and stays settled, while `TestProbeClaudeScreenStop_BusyPaneTitleKeepsBolt` and `TestProbeClaudeScreenStop_BlockingPromptWithDescriptionTitleKeepsBolt` prove a working pane and an open permission prompt with the same title never settle. `TestProbeClaudeScreenStop_StaleDescriptionSurvivesMarkerRotation` pins one-shot promotion keyed on the description body, and `TestProbeClaudeScreenStop_HookEventResetsDescriptionMarkerRun` pins the hook-driven streak reset the dropped title veto relies on. |
| Dashboard run timer | `internal/daemon` proves the busy-stretch anchor the timer reads: `TestBusySinceStampedOnActiveClassEntry` and `TestBusySinceSurvivesActiveClassChurn` start it once per stretch and hold it across a `⚡`/`🤖`/`🌀` handoff, `TestBusySinceClearedByEveryNonActiveIcon` clears it on stop and on a permission pause as well as every other non-active icon, and `TestSweepStale_ClearsBusySinceWithNilReducer` clears it on a stale sweep and removes it from the export. It survives a daemon restart at its original instant through `TestUserVarV4RoundTripPersistsFactsProcessAndEffective`, `TestRestoreFromUserVarsKeepsLivePersistedBusySince`, and `TestRestoreFromUserVarsStampsBusySinceForFactlessPayloads`, with `TestUserVarV1IgnoresBusySinceAndSelfHealsOnRestore` pinning the pre-upgrade self-heal, and reaches the dashboard through `TestHandler_StateQueryReportsBusySinceAndOmitsItWhenIdle` plus `tui.TestApplyDaemonStatesStampsBusySince`. `TestHandler_ExportProjectsBusySinceAndOmitsItWhenIdle` pins current-schema export inclusion and idle omission, while `TestProjectExportEventsPayloadV11IdentityProjection` pins schema-v11 exclusion and schema-v12 inclusion. `tui.TestFormatRunElapsedBoundaries`, `tui.TestActivityCellTextWarningRequiresTimer`, `tui.TestRenderListActivityColumnHiddenByDefault`, and the `run-timer-narrow-fit`/`run-timer-wide` scenarios cover the rendered bands, the `⏳` warning belonging only to rows with a live timer, and both widths. Live per-harness confirmation against an installed binary is operator work, recorded under [Dashboard run timer](#dashboard-run-timer). |
| Debug workflow | `cmd.TestReleaseDocumentationPinsVersionAndDiagnosticWorkflow` pins AgentMux 0.8.0, schema v13 export documentation, the schema-v2 snapshot contract, event-time tail semantics, explain graph defaults, v1/v2 bundle replay, the `amx debug status` → `amx debug explain` → `amx debug bundle` → `amx debug replay` workflow, and every required failure-matrix row including distinct notification failure and suppression. |
| Bundle secrecy and traversal | `internal/debugger` tests plant a secret corpus, verify share/forensic redaction, exclude raw sources, reject symlink publication, build flat deterministic archives, and replay portable share evidence. |
| Unified causal diagnostics | One bundle explains the causal path across both systems, and AgentMux reads only ASL's public sanitized export to do it. `scripts:tests/test_asl_diagnostics_export.py` is the producing half: bounded, deterministic, control-plane only, with an omission ledger of counts, digests, and named reasons, and canaries proving no question body, reply, prompt, model output, raw bridge stderr, or secret reaches the document. `internal/aslexport` is the boundary: `TestFetchDropsFieldsThisBuildDoesNotImplement` proves the typed decode drops fields this build does not implement, so a newer ASL cannot inject one into a bundle unreviewed, and `TestFetchNamesEveryDegradedCondition` covers every survivable failure — absent, predating the subcommand, hung, oversize, unreadable, silent, unnamed, newer schema, foreign contract set, self-reported failure — each resolving to a named availability rather than an error. `internal/debugger/bundle_asl_test.go` proves the join: both acceptance counters, both build identities *and* both binary hashes, contract-set agreement, operation and closure IDs, delivery outcomes matched against journal records by request ID with unmatched deliveries called out, `TestBundleDoesNotReadASLPrivateStateFormats` pinning the manifest's explicit exclusion of `state.json`/`events.jsonl`/`mailbox.jsonl`, `TestASLBundleMembersAreDeterministicAndCanaryFree` pinning byte-identical repeated capture over frozen fixtures and canary-free output, `TestShareBundleKeepsTheASLCorrelationReadable` failing whenever a new field is added without extending the share-mode key allowlist, and `TestBundleSurvivesEveryASLUnavailableCondition` proving bundle creation still succeeds and still names the reason when ASL, the bridge, or the integration is unavailable. `cmd/debug_bundle_asl_test.go` pins `--include-asl` wiring: no subprocess without the flag or with `[defaults.asl] enabled = false`, resolution failures carried as availability, and the configured command, state root, run selector, and contract-set ID reaching the export. |
| Restart and replay equivalence | `internal/daemon` restart-prefix tests and `internal/debugger` golden replay tests cover restored reducer state including exact process ownership, daemon sequence resets, ordering, routing, ASL phases, duplicate requests, coalesced renders, and explicit schema-v8/v9 limitations. |
| ASL delivery correlation | `/Users/sglavoie/scripts/tests` covers stable UUIDv5 request IDs, bounded retry/backoff, duplicates, permanent failures, legacy fallback, missing binaries, exhaustion, metrics/tail/Workbench visibility; `internal/debugger.TestExplainCorrelatesASLSourceDeliveryDaemonAndRenderEvidence` pins the cross-repository graph. |
| ASL run presence and lease reconciliation | Presence comes from the strict nested `agent_mux` projection and nothing else in ASL's state. `internal/agentloop` pins the exact-member-set decode (missing member, unknown member, unknown phase, relative plan dir, zero generation, untrimmed string, malformed timestamp, process on a resting phase), unsupported contract sets, and the tri-state verdict: paused is present without a PID, finished and an exited process are confirmed absent, and unreadable/missing/misplaced state, an unverifiable process, and a recycled PID are all indeterminate. `TestDiscoverIsolatesOneCorruptPlan` proves one corrupt plan neither hides healthy plans nor reports itself absent, and `TestDiscoverTreatsStateWithoutAProjectionAsIndeterminate` pins the paired cutover. `internal/agentloop.TestAuthenticateProcessOnlyAssertsAbsenceWhenThePIDIsGone` and `TestCompareStartTokens` pin PID-reuse rejection and the timezone-rendering tolerance that stops a healthy run being read as recycled. `internal/daemon` covers startup plus ten-second reconciliation, two consecutive confirmed absences before retirement, indeterminate evidence neither advancing nor resetting a miss streak, an absent state root and a failed sweep preserving every lease, rejection of observations that went backwards on either monotonic counter, and the `asl_reconcile` metrics reaching the health surface. `internal/config` pins `defaults.asl.state_root` acceptance and relative-path rejection. The sweep's cost is held off the readiness path: `internal/daemon.TestStartupRestoreLeavesTheSweepToTheFirstMaintenanceTick` proves the bounded initial snapshot performs no sweep and the very next tick does, `TestReconcileReusesOneProjectionCacheAcrossSweeps` proves one cache spans sweeps, and `internal/agentloop` projection-cache tests pin reuse of unchanged documents, re-decode after a rewrite, memoized failures, read-through when the file is gone or no cache is supplied, and eviction of plans that left the state root. |
| ASL contract set and compatibility refusal | AgentMux and ASL are versioned together by one contract-set ID, a digest over the canonical corpus ASL owns and this repository vendors byte for byte at `internal/aslcontract`. `internal/aslcontract` re-derives that ID from the embedded bytes, pins every per-file digest, excludes the manifest from the identity it carries, and proves fixtures carry the placeholder identity only; `internal/aslbridge.TestInstalledBridgeServesTheContractSet` is the live half, where a running ASL must name the same ID and `assertTheDigestSpansEveryComponent` proves that one equality vouches for the lifecycle and projection fixtures too. The two halves separate the two drifts: a locally edited fixture fails `just check`, a stale vendored copy fails `just asl-integration`. Bridge protocol 2 is the only protocol and hook schema 2 the only lifecycle schema; there is no v1 path in either. `internal/aslbridge` covers the server-first hello carrying the ID, a retired protocol and a missing `session.handshake`/`runs.snapshot` capability each ending the child, and a foreign set ending supervision without restart — mismatch is terminal, not transient. `internal/aslbridge/contract_gate_test.go` covers every way the mutation gate closes: a foreign set named by a snapshot or refused by a handshake, a grant against an older digest and a withheld grant both leaving reads working, an undecodable receipt disabling mutations as ambiguous, and a new projection withdrawing the previous grant. `internal/daemon` gates lifecycle-hook ingestion on the same identity: `TestTheGatedVocabularyIsTheDeclaredOne` pins the sixteen boundaries against the schema's own enum, `TestEveryCanonicalLifecyclePayloadPassesTheGate` and `TestEveryInvalidLifecycleFixtureIsRefusedAtItsOwnLayer` run the vendored corpus and assign each negative case to the decoder, the gate, or the schema, and `TestAnIncompatibleLifecycleHookIsQuarantinedWithoutReduction` proves a retired schema or foreign set is quarantined with a stable non-retryable reason that quotes nothing the producer chose. Availability is the other half: `TestHarnessHooksAreUnaffectedByTheLifecycleGate` proves Claude/Copilot/Codex hooks carry no compatibility block, are never gated, and reduce normally, so a refused ASL event never costs AgentMux its core function. `internal/indicator` proves the contract caps a lifecycle payload far below the compaction threshold and that compaction carries the block anyway, so a size limit can never present as a version mismatch. |
| ASL human-actionable questions | A worker wait remains bare 🔁 until the explicit `asl outbox wait` supervisor handoff proves an actionable open set. `internal/hookevent` recognizes the body-free question/reply/resolution schema; `internal/agentloop` validates the fixed-size v2 compatibility projection and rejects v1/stale/malformed/foreign evidence; `internal/daemon` covers immediate ❓🔁 plus desktop notification, duplicate idempotence, F8 dismissal until a newer question, lease/trusted-Stop fallback ordering, anonymous/pipe-worker stop rejection, heartbeat resistance, aggregate reply/resolution fast clearing, stale-closure cutoffs, multiple-question final closure, reply/zero-projection/finish fallbacks, restart-safe reducer state, and notification behavior. Wait/reply reconciliation, stale-sweep exemption, explicit paused questions, sanitized detail preview, TUI attention grouping, `awaiting response` gloss, and `]q` navigation remain covered. |
| ASL pairing | The daemon is spawned detached and never inherits an operator's `PATH`, so a bare `asl` that works in every terminal is unreachable from the one process that needs it. `internal/aslbridge/resolve_test.go` pins sibling-before-`PATH` resolution, a path-bearing command taken at its word, relative paths made absolute, every not-found wrapping `exec.ErrNotFound`, the auto-mode re-probe backoff (5s/15s/60s to a five-minute ceiling, with no restart charged) against required mode's terminal stop, and `Status.ExecutableSource`. `cmd/config_asl_sync_test.go` covers the command that ends it: `--check` writes nothing and migrates nothing; active incompatible runs refuse whole-root with pid, run, and plan named, before and after the migration pass; contract-set and bridge-protocol mismatches refuse before any write; a successful apply persists the absolute path, the state root, and `enabled = true`; and every post-write failure — a failed restart, a daemon that never reached ASL, a daemon that paired with a different executable — restores the previous bytes (or removes a file it created) and restarts onto them. `internal/config/asl_pairing_edit_test.go` pins the surgical TOML edit that makes that promise keepable: comments, blank lines, key order, trailing newlines, and `#` inside paths all survive, dotted-key and duplicate-key ambiguity is refused with the edit that fixes it, new keys land inside `[defaults.asl]` rather than under whatever header came last, and re-pairing is byte-identical. `internal/atomicfile` pins mode preservation and that a failed replacement leaves no temporary file beside an authoritative artifact. `scripts:tests/test_asl_agent_mux_pairing.py` is the ASL half: the survey classifies every plan, resolves its own executable to an absolute non-canonical path, and refuses to migrate around a live run. |
| ASL integration diagnosis | One `asl: unavailable` line was true for six unrelated conditions with six different fixes. `cmd/doctor_asl_test.go` pins the schema-v2 `asl_integration` block: six named dimensions in a stable order, each with its own stable finding identifier, and severity that follows the declared mode and nothing else. Shell resolution warns for a bare name even when this shell resolves it, because the successful lookup here does not predict the daemon's; daemon resolution reports separately, so shell-ok plus daemon-failed is itself the diagnosis; a contract mismatch fails in every mode, from either the bridge or the state root; projection errors stay warnings even in required mode so one corrupt plan cannot block a release check; freshness covers a stale snapshot and a sweep lagging past twice its interval; and delivery reports quarantined lifecycle hooks, which are otherwise invisible because the hook path acknowledges every producer it refuses. `internal/daemon/handler_asl_delivery_test.go` pins those counters through the real ingestion path, including that a producer cannot grow the reason map and that an unreadable harness payload is never charged to ASL. Disabled mode answers every dimension `not_applicable`, and so does an unconfigured machine with no `asl` installed — absence is not a diagnosis. `asl_unavailable` and `asl_read_only` keep their exact pre-v2 names and meanings. |
| Screen classifier patterns and decline health | The harness UI copy the classifier matches on is data, overridable per pattern set, and a classifier that has stopped matching now reports itself. `internal/screenstate/patterns_test.go` pins the schema and its bounds (unknown key, uncompilable regex, oversized pattern and phrase list, empty required fields, the three adapter boolean shapes, deterministic warning order) and every load outcome: a missing override is not a warning, an unreadable or invalid one keeps the embedded default, a valid one shadows it, and a non-absolute config directory is refused rather than read from the daemon's working directory. `internal/screenstate/patterns_clauses_test.go` walks the adapter clause constants so a clause renamed in the TOML fails the suite instead of degrading silently to "no match", and `TestCodexPromptClausesGateTheSettlePaths` pins the two fail-open clause families that refuse outright rather than classify weakened. `internal/contextlimit/patterns_test.go` and `internal/screenstate/patterns_contextlimit_test.go` prove the one override directory covers the set that lives in another package, including that a bad override there degrades in both packages. `internal/daemon/run_screen_patterns_test.go` drives `screenstate.Load` for real through daemon startup and pins the health block for a clean start, a valid override, a broken override, and a daemon that never loaded. `internal/daemon/screen_decline_streak_test.go` pins the counter itself — streak and fingerprint stability, the atomic idle-sample reset, non-colliding per-source streaks, purge on window close, and the whole thing driven through the real Codex probe — and `screen_decline_export_test.go` pins its publication, retraction, and absence from schema v12. `cmd/doctor_screen_patterns_test.go` covers the per-set source lines, that an applied override is source-only, that all four failing warning kinds raise `screenstate_override_invalid` without failing `--strict`, and the older-daemon "unreported" line; `cmd/doctor_screen_decline_test.go` covers the other severity, where a persistent decline fails `--strict` and benign declines do not. `cmd.TestDoctorJSONCommandSurfaceIsVersioned` pins doctor report schema v3 and the uniqueness of every stable finding identifier. Live confirmation that a deliberately non-matching override makes a real pane hold `⚡` while the streak climbs and `--strict` exits 2 is operator work, recorded in [Screen classifier health](operations.md#screen-classifier-health). |

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

The release uses schema v13 for `amx daemon export-events` by default. Acceptance
also retains `--schema 12`, `--schema 11`, `--schema 10`, `--schema 9`, and
`--schema 8` coverage as explicit compatibility projections: schema 12 omits the
v13 `watchdog.screen_declines` counters, schema 11 additionally omits the v12
busy-stretch anchor, schema 10 omits v11 stop-hold evidence, and schemas 9 and 8
additionally omit v10 process fields. For first-line diagnosis use:

```bash
amx debug status
amx debug explain --source-event <id>
amx debug bundle --mode share -o agent-mux-debug.tar.gz
amx debug replay agent-mux-debug.tar.gz
```

When the failure crosses into agent-session-loop, add `--include-asl` to the
bundle. It adds `asl-export.json` (ASL's public sanitized diagnostics export)
and `asl-correlation.json` (the join against this bundle's own journal window).
Neither member can appear without the flag, and neither can fail bundle
creation: an ASL that is absent, older than the subcommand, disabled, hung, or
unreadable leaves the export member out and names the reason in
`manifest.json`.

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

### Paired release evidence

For a release of the Agent Mux / agent-session-loop pair, the handoff above is
produced mechanically instead. `just paired-release-gate asl_root=… signing_key=…`
runs both repositories' full gates, the crash/restart and reliability-stress
gates, the paired bridge integration against the installed wrapper, the staged
install in the documented order (ASL first), a real daemon restart, and a real
Kitty cockpit smoke — then writes a signed JSON record naming both revisions,
the artifact hashes, the contract set and manifest SHA-256, the OS/Kitty/harness
versions, and every command with its result and duration. The procedure, the
evidence schema, the verification command, tagging, rollback, and maintenance
installation are documented in
[Paired releases](operations.md#paired-releases).

These properties make the record admissible as acceptance evidence:

- A step that never ran is recorded as `not-run` rather than omitted, so a
  truncated gate cannot read as a complete one.
- The persisted record declares its own signature — signed or not, the
  namespace, the key, and the detached signature's path — and it declares it
  before `ssh-keygen` runs, so the claim is inside the signed bytes. A record
  that reads as unsigned was not signed, regardless of what sits beside it.
- A run that names no signing key cannot tag. `tag=yes`/`--tag` without a key is
  refused before any step runs, any record is written, or either repository's
  refs are read, so the pair tags that identify a release can only ever have
  been produced alongside a signed record. The rehearsal itself stays
  available — it just leaves evidence rather than history.
- The two pair tags are one transaction. A failure on the second repository's
  tag removes the first — by object, so only what this run created is deleted —
  and if that removal also fails, the leftover ref is recorded as
  `"result": "partial"` with an empty `"tags"` list and a `"tag_failure"` block
  naming the stranded ref and the exact command that clears it. A pair tagged on
  one side can therefore never be read as a released pair, by a consumer of the
  record or by a reader of either repository's tags.
- Every deletion the record publishes is bound to an object. When the read-back
  after a tag write fails, the run knows a ref exists and does not know what is
  under it, so it publishes no command for that ref: it goes into
  `"unidentified"` with read-only inspection commands and the requirement an
  operator must satisfy first, and never `git tag -d <pair-id>`, which would
  delete whatever answers to the name later. Running every command in a record,
  or every command the summary prints, therefore deletes nothing that this run
  did not verifiably create.
- The transaction covers the record that vouches for the tags, not only the two
  refs. A tagging run publishes an unsigned `"result": "tagging"` record before
  its first ref exists, and the release claim is the last step: the record naming
  both tags and the signature over exactly those bytes are staged and signed
  together, then moved into place by two ordered renames — the signature first,
  the record second. That pair is not atomic, and the invariant is directional
  rather than all-or-nothing: a crash between the renames may leave the canonical
  record provisional beside a signature that does not verify it, which is
  admissible because it names no release, but a canonical `"passed"` record is
  never visible without the signature covering it. A failure to write it or to
  sign it retracts both tags by the same compare-and-delete. Release tags
  therefore never outlive the record that describes them, and no record reads as
  a release before the refs it names exist.
- A retracted attempt is recorded as `"result": "not-released"` rather than
  `"passed"`, and `Evidence.Passed()` reports false for any record carrying a
  `"tag_failure"` block at all. A run that returns an error, prints what it took
  back, and releases nothing cannot leave behind a record whose single most-read
  field says the pair passed. The two incomplete outcomes stay distinguishable:
  `"partial"` means a ref survives and the next attempt is refused until it is
  cleared, `"not-released"` means nothing survives and the run can simply be
  repeated.
- That holds when no account of the retraction can be written at all, which is
  the case where the evidence medium itself is what failed. Ordinary publication
  needs the evidence directory; the gate also rewrites the record it already owns
  in place, which needs only that one file. Neither is load-bearing: when both
  fail, what survives is the provisional `"tagging"` record written before the
  first ref existed, which names no release and has no signature beside it. No
  durable record of a retracted attempt reads as passed, satisfies
  `Evidence.Passed()`, or still verifies — and that does not depend on the
  medium that just failed becoming writable again.
- `just crash-restart` counts the scenarios that actually executed and
  `just cockpit-smoke` fails rather than skips when Kitty is absent. Both close
  the gap where a gate reports a boundary as exercised that was never touched.

The gate's own logic is covered by `go test ./internal/release`, which runs the
whole procedure against throwaway repositories, an ephemeral SSH key, and fake
`just`/`kitty`/ASL executables — with real Git and real `ssh-keygen`, so
revision reading, annotated tagging, compare-and-delete tag rollback, and
namespace-scoped signature verification are genuinely exercised without touching
the live installation. Failures that cannot be provoked otherwise are injected
through the same command seam: `TestSecondRepositoryTagFailureLeavesNoPairTag`
fails the second repository's tag write and asserts the refs and the persisted
record agree afterwards, and
`TestUnremovablePairTagIsRecordedAsRecoverablePartialState` additionally fails
the rollback and runs the recovery command the record names.
`TestFinalEvidenceSigningFailureRollsBackPairTags` fails only the signature over
the tagged bytes — every step passes and both tags are genuinely written first —
and asserts the refs, the persisted record down to its `"result"`, the detached
signature, the returned error, and the progress the command prints all agree
that the pair is untagged and not released;
`TestFinalEvidenceWriteFailureRollsBackPairTags` does the same for the
publication that never reaches disk, and asks it twice by sealing the evidence
medium with real filesystem permissions: once with the directory unwritable, so
the account of the retraction is rewritten into the record in place, and once
with the record unwritable as well, so no write of any kind can land after
tagging and the durable state is the provisional `"tagging"` record alone. In
both the durable `"result"`, `Evidence.Passed()`, and `ssh-keygen -Y verify`
agree with the refs that nothing was released. The summary's half of that
agreement is
`cmd.TestReleaseGateSummaryDoesNotSayTaggedWhenTheTagsWereTakenBack`: a run
whose tags were retracted must not print the word an operator scans for, must
lead with `(not-released)` rather than `(passed)`, and must say the same thing
in the JSON a script reads as in the summary a person reads.
`TestUnreadableCreatedTagDoesNotOfferNameOnlyDeletion` fails the read-back of a
tag that was really written, so one ref is identified and rolls back while the
other is stranded without an object; it asserts the run runs no
`git tag -d`, that the substring `tag -d` appears nowhere in the record bytes,
the progress output, or the returned error, and — after planting a replacement
ref under the stranded name — that executing every command the record publishes
leaves that replacement intact.
`cmd.TestReleaseGateSummaryOffersNoDeletionForAnUnidentifiedTag` is the summary's
half: the identified tag keeps its pasteable `clear it with:` command and the
unidentified one gets `UNIDENTIFIED`, `inspect it with:`, and
`before removing it:` instead.

CI coverage is three lanes in `.github/workflows/check.yml`: `linux-portable`
(build, unit suite, vet, gofmt, lint), `macos-full` (`just acceptance` plus
lint, on the release platform), and `cross-repository-contract` (the vendored
corpus is self-consistent, and byte-identical to ASL's canonical corpus). The
cross-repository job records a skipped ASL checkout and fails on a trusted
event rather than reporting a pair it never compared. No hosted runner has a
real Kitty, so `cockpit-smoke` is operator-only by construction.

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
Retain a schema v13 export or `amx debug explain --source-event <id> --json` for
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
