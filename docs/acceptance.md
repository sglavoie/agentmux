# Product acceptance

The AgentMux 0.8.0 product acceptance gate is:

```bash
just check
```

It runs `gofmt -l` over every tracked Go file, `go test ./...`, the
race-sensitive transcript, plan, handoff, worktree, daemon, and TUI suites,
`go vet ./...`, every committed TUI scenario (inside `go test ./...`, through
both the CLI and the in-package runner), and `golangci-lint`. `just
acceptance` is the same gate without the optional linter dependency. The
formatting check leads because it is the cheapest of them: a formatting drift
reported after the race suite is a long wait for a one-command fix.
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
| Payloads above 256 KiB were replaced with empty JSON and collapsed reducer identity | Command-level acceptance delivers mixed large Claude/Codex events as `json_compact`, verifies distinct source keys and session-specific terminal clearing, then proves `payload_oversized` returns strict exit 2 versus legacy fail-open without reaching the daemon | Payloads through 256 KiB remain byte-identical; accepted compact payloads preserve semantic identity through the 16 MiB hook read limit; larger input never invokes the reducer. |
| Doctor permanently reported `last_seen_missing` for an active claude profile because hook evidence was credited to the routed target window's profile | `internal/indicator` stamps `config_dir` on every hook stage and `producer_config_dir` on requests; `internal/hooks` keys status evidence by `(harness, home)` with legacy fallback and shared-home crediting; `internal/daemon` decision-trace tests separate producer and target identity | Strict doctor reports no `last_seen_missing` for profiles with live sessions; an end-to-end trace shows `evidence.producer_config_dir` beside the routed `route.effective_profile` with tab-owner routing unchanged. |
| `Append` lost its 25 ms lock to the compaction commit window every maintenance minute, spilling spool bursts at :54 | `internal/diagnostics` proves the compaction commit window does not block `Append` (dedicated `append.lock` sidecar, `sealedMu`/`appendMu` split, 512-file input bound, 2 s maintenance lock timeout); `internal/daemon` runs maintenance against four hot appenders and asserts zero lock-timeout records | `amx debug tail --follow --json` across a maintenance minute shows no `journal.maintenance … journal lock timeout` records and no spool-fallback bursts; both `events.lock` and `append.lock` sidecars persist in the journal directory. |
| ~13 journal records per hook event wiped retained history in minutes, making the hook-silence check's 4 h window fiction | `internal/indicator` covers standard-detail sampling (success-class intermediate stages suppressed, `hook.delivery` always written with payload HMAC/size, errors never sampled) plus the `journal_detail = "full"` escape hatch; `internal/daemon` and `internal/debugger` cover coalesced renderer records and explain over sampled journals | Journal growth drops roughly 5× under multi-session load and `journal.health.earliest_observed_at` holds a multi-hour horizon after soak. |
| Promoted spool files were counted as plain segments and dropped deliveries had no doctor visibility | `internal/diagnostics` covers `promoted_spool_files_read` and the `earliest_observed_at` horizon over mixed layouts; `internal/hooks` covers the shallow-horizon versus missing distinction and the trailing-hour delivery-failure window; `internal/debugger` and `cmd` cover the stable `hook_delivery_failures` finding and the warning-only strict-doctor line | `journal_snapshot.promoted_spool_files_read` is present in schema-v2 reads; `last_seen_horizon` wording appears only when retained history is genuinely shallower than the verification window; dropped deliveries surface as a warning while the running daemon was up to miss them, and as a note once they all predate its start. |

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
| Starting a run of a plan | A plan is started only when agent-session-loop says it may be, and by launching a supervising session, never by running a loop. `internal/planrun` reads `asl status`, `asl preflight`, and `asl supervisor-guide` through one seam: `TestCheckReportsWhatASLSays` covers ready, never run, not ready, running, and complete, `TestCheckFailsWhenPreflightGivesNoAnswer` keeps a silent ASL from reading as readiness, and `TestPromptIsTheHarnessInvocationWithTheRunArguments` pins the per-harness skill invocation and its shell quoting. The shared `plan.run` action is `internal/actions/plan_test.go`: `TestPlanRunStartsASupervisingSessionInThePlansDirectory`, `TestPlanRunOnTmuxReportsTheRuntimeSession`, `TestPlanRunStartsNothingForAPlanThatMayNotBeStarted` (each refusal by code, with nothing launched), `TestPlanRunCheckReportsReadinessAndStartsNothing`, and `TestPlanRunNeverGuessesTheProject`. `cmd/plans` pins the command surface, including that a refused plan is the data envelope with a quiet nonzero exit (`TestPlanRunReportsAPlanThatMayNotBeStartedAsData`) and that a plan name several plans begin with lists candidates. The form is `tui/runform` and `tui/plan_run_form_test.go`, with `plan-run-form-wide` and `plan-run-refused-narrow-fit` as its rendered scenarios. No test starts a real run: live confirmation against an installed agent-session-loop is operator work. |
| Narrow and wide TUI layouts | All JSON scenarios under `docs/tui-testing/scenarios` cover the live dashboard, overlays, Past Sessions, Plans, and their viewers. |
| TUI continuity | The hermetic `session-fork-continuity` scenario enters Past Sessions and launches a native fork through the shared action registry while recording, rather than executing, the Kitty launch. |
| Diagnostic pipeline | `cmd.TestAcceptanceDiagnosticPipeline` drives the real hook command adapter through journal persistence and Unix IPC into an in-process daemon with fake Kitty, then checks duplicate acknowledgement, renderer writes, notification suppression, compact large-payload source isolation and terminal clearing, strict/legacy oversize behavior, a schema-v2 snapshot-backed share bundle and offline replay, and a bounded ASL source-event explanation. `internal/debugger.TestAcceptanceSinglePassStatusSurvivesTinySegmentCompaction` records synthetic status timings and proves one traversal plus lossless 2,000-segment convergence. `internal/debugger.TestReplayShareBundleIsPortableAndPreservesReplaySafeEvidence` rewrites the bundle as schema v1 and verifies v1/v2-compatible replay. |
| Harness process lifecycle | `cmd.TestAcceptanceHarnessProcessLifecyclePipeline` uses a real Unix socket plus controllable `ps` and fake Kitty boundaries to prove cross-harness pipe and interactive exits, parent/child and concurrent isolation, attention preservation, native-hook races, reset and ASL exclusions, schema-v4 restart settlement, schema-v10 diagnostics, and linked renderer completion. The automated/live boundary and optional installed drill are documented in [Process-Lifecycle Acceptance](process-lifecycle-fixture.md). |
| Terminal stop confirmation | `internal/daemon/terminal_stop_hold_test.go` proves a native terminal is withheld at `⚡` until both the quiet period elapses and an idle probe confirms, that the deadline commits an unconfirmed hold, and that a new prompt supersedes one; the bypass matrix (pipe mode, no idle probe, live ASL lease, no Kitty client, `enabled = false`) and a verified process exit all skip the confirmed-idle wait, restoring the immediate `🛑` for every bypass except the live ASL lease, whose stop the reducer goes on masking until the loop finishes. `internal/daemon/handler_stop_hold_export_test.go` pins the schema-v11 `stop_hold_outcome`/`stop_confirmed_by`/`pending_terminal_stop` projection, its removal under schema v10, and the live hold reaching `daemon export-events`. `internal/config/config_stop_confirm_test.go` covers `[defaults.stop_confirm]` defaults and validation, and `cmd.TestInvariantStopHoldStuckWarnsPastTwiceTheTimeout` pins the `stop_hold_stuck` warning without changing the healthy verdict. Live per-harness confirmation against an installed binary is operator work, recorded in [Daemon State Acceptance](acceptance/daemon-state.md). |
| Claude stale pane-title recovery | `internal/daemon.TestProbeClaudeScreenStop_RestingBrailleDoesNotPromoteEmptyWindow` proves a resting Braille title never creates a phantom ⚡ or busy anchor, and `internal/daemon.TestProbeClaudeScreenStop_RestingBrailleRequiresMarkerBumpedRun` pins the exit from an existing phantom: the screen's own tier requirement plus the marker-title sample bump, three samples for a terminal-shape resting screen. |
| Claude stale description titles | `internal/daemon.TestProbeClaudeScreenStop_BusyBrailleDescriptionSettlesAfterMarkerRun` proves an idle-at-prompt pane whose title still carries a task description settles on the four-sample marker run and stays settled, while `TestProbeClaudeScreenStop_BusyPaneTitleKeepsBolt` and `TestProbeClaudeScreenStop_BlockingPromptWithDescriptionTitleKeepsBolt` prove a working pane and an open permission prompt with the same title never settle. `TestProbeClaudeScreenStop_StaleDescriptionSurvivesMarkerRotation` pins one-shot promotion keyed on the description body, and `TestProbeClaudeScreenStop_HookEventResetsDescriptionMarkerRun` pins the hook-driven streak reset the dropped title veto relies on. |
| Copilot pane refuses a transcript settle | A Copilot pane waiting on a permission prompt is not a transcript event, so the screen is the only evidence it exists. `internal/daemon.TestProbeCopilotTranscriptStop_ScreenRefusesTerminalSettle` drives the real probe entry point over the committed `internal/screenstate/testdata` captures and proves terminal transcript evidence plus a permission prompt, a trust prompt, a composer holding queued input, a live status line, or an unreadable pane leaves the tab at `⚡` with no title write, no `TranscriptStopProbe` row, and — the cost that cannot be undone — no "Session stopped" notification. Its subtests pin the two directions the refusal must respect: a veto discards the pending stabilization streak rather than skipping one observation, and recovery of a stranded synthetic `🛑` stays transcript-driven, because the refusal is forward-only and a blocked pane must not be left wearing a stop its own transcript disagrees with. `TestProbeCopilotTranscriptStop_ScreenAllowsVetoFreeTerminalSettle` is the positive twin and asserts the pane was actually read, so deleting the wiring fails it rather than passing vacuously; a finished Copilot turn renders no completion marker at all, so a clause that vetoed the ordinary screen would strand every pane on a permanent `⚡`. |
| Backend-neutral screen observations | Screen probes read `backend.Screen` observations (text plus instance generation, pane size, alternate-screen and exit state, capture time) and only through `probes.CheckScreen`. `internal/screenstate/tmux_fixture_test.go` registers 39 real tmux captures of Claude Code 2.1.296, codex-cli 0.162.0 (the cleared screen: 0.162.1) and Copilot CLI 1.0.95 (`testdata/tmux`, taken with `capture.sh`), pins each one's state, tier and veto set, holds the two captured Claude pane titles and the cleared Codex screen with its history to the traps they were taken for (`TestTmuxClaudeTitleCaptures`, `TestTmuxCodexClearedCapture`), holds its geometry and screen mode to what tmux reported, proves the styled capture reads like the plain one and the narrow re-wrapped permission dialog reads like the wide one, and `TestBackendFixtureParity` holds prompt, permission, busy, completion and failure, and the Codex cleared-screen verdict, to the same reading on the Kitty and tmux sets for all three harnesses. The daemon's `…OnEitherBackend` tests (`internal/daemon/runtime_screen_probes_test.go`, `runtime_probe_parity_test.go`) run the stop hold, the context-limit banner, Claude's pane-title recovery, the Codex cleared screen, and the Copilot transcript probe on a Kitty window and a tmux runtime session with the same assertions. `internal/daemon.TestScreenProbesNeverSettleOnWithheldObservations` feeds the real stop probes a real finished-turn capture and proves it settles only when the observation is fresh: a failed read, a missing capture instant, a stale capture, another pane, another backend, and an exited program each leave the tab at `⚡` with no stop fact. `internal/daemon/probes/screen_test.go` and `pane_test.go` pin every verdict, the per-pane epochs for non-Kitty discoveries, and that a tmux server restart invalidates every pane that reuses an ID; `internal/tmux/observe_test.go` runs `Observe` against a real server for wrap, colour, the alternate screen, an exited pane, and a restarted server; `tui/overview/capture_backend_test.go` holds tmux tiles to the overview scheduler's bounds and shows an unavailable or stale capture paints no content. |
| Dashboard run timer | `internal/daemon` proves the busy-stretch anchor the timer reads: `TestBusySinceStampedOnActiveClassEntry` and `TestBusySinceSurvivesActiveClassChurn` start it once per stretch and hold it across a `⚡`/`🤖`/`🌀` handoff, `TestBusySinceClearedByEveryNonActiveIcon` clears it on stop and on a permission pause as well as every other non-active icon, and `TestSweepStale_ClearsBusySinceWithNilReducer` clears it on a stale sweep and removes it from the export. It survives a daemon restart at its original instant through `TestUserVarV4RoundTripPersistsFactsProcessAndEffective`, `TestRestoreFromUserVarsKeepsLivePersistedBusySince`, and `TestRestoreFromUserVarsStampsBusySinceForFactlessPayloads`, with `TestUserVarV1IgnoresBusySinceAndSelfHealsOnRestore` pinning the pre-upgrade self-heal, and reaches the dashboard through `TestHandler_StateQueryReportsBusySinceAndOmitsItWhenIdle` plus `tui.TestApplyDaemonStatesStampsBusySince`. `TestHandler_ExportProjectsBusySinceAndOmitsItWhenIdle` pins current-schema export inclusion and idle omission, while `TestProjectExportEventsPayloadV11IdentityProjection` pins schema-v11 exclusion and schema-v12 inclusion. `tui/dashboard.TestFormatRunElapsedBoundaries`, `TestActivityCellTextWarningRequiresTimer`, `TestRenderListDrawsTheRunTimerInTheTimeColumn`, `TestActivityWarningIsStyledWhereItIsStillDrawn`, `TestInspectorWatchdogWarningMatchesTheActivityColumn`, and the `run-timer-wide` scenario cover the rendered bands, the right-aligned Time column, and the `⏳` warning belonging only to rows with a live timer — which the inspector's `Watch` line is now the thing that draws. `run-timer-narrow-fit` is the narrow half: at 57 cells every row keeps its aligned timer and the `⏳` is nowhere, because that width is below the split floor and the inspector that carries it is not drawn. Live per-harness confirmation against an installed binary is operator work, recorded under [Dashboard run timer](#dashboard-run-timer). |
| Durable presentation-transition time | `internal/daemon.TestPresentationChangedAtTracksOnlyVisiblePublicationTransitions`, `TestPresentationChangedAtStampsUnseenOnlyPublicationChange`, and `TestFocusClearUsesSnapshotObservationTimeAndDoesNotRestamp` pin the one-stamp-per-effective-transition rule. `internal/daemon/state.TestPresentationChangedAtRoundTripsThroughUserVarInMilliseconds` and `TestPresentationChangedAtAbsentKeyRestoresZeroAndIsOmitted` cover restart persistence and pre-field compatibility; `internal/daemon.TestHandler_StateQueryReportsPresentationChangedAtInMillisecondsAndOmitsZero`, `internal/daemon/protocol.TestLegacyStateSnapshotReaderIgnoresPresentationChangedAt`, and `tui.TestApplyDaemonStatesOverlaysAndClearsPresentationChangedAt` cover the wire/client boundary. `tui.TestTriageIntegrationFixturePinsReleaseMatrix` carries zero, equal, and distinct values in one hermetic world. |
| Dashboard triage ordering | `tui/dashboard.TestSortRowsOrdersEverySubtypeWithinItsGroup`, `TestSortRowsUsesPresentationRecencyThenStableFallback`, `TestSortRowsPreservesSortRunningPinAndConfiguredFallback`, and `TestCompareRowPriorityIsTransitive` pin the severity/recency comparator. `tui.TestSessionsMsgPresentationReorderPreservesSelectionAndPreviewTarget` covers refresh continuity; `tui.TestTriageIntegrationFixtureRendersPriorityAndScopeOrder` plus `triage-default-wide-ordering` prove the integrated order and tallies over every rank. |
| Dashboard project filter and switcher | `tui/dashboard.TestParseProjectSelectors`, `TestSerializeProjectSelectorRoundTrips`, `TestProjectCompletionUsesContextAndCanonicalLabels`, and `TestProjectCompletionInsertionQuotesAndEscapes` cover identity, DSL, and completion. `TestBuildProjectScopesAggregatesPriorityCasingTalliesAndAll`, `TestBuildProjectScopesEmptyContextStillOffersAll`, `TestProjectScopeKeyFoldsCaseAndAllIsEmpty`, and `TestFilterProjectScopesKeepsAllAndFoldsCase` cover the contextual aggregation and its case-folded identity; `TestRenderProjectSwitcherClampsWithoutBreakingUnicode`, `TestRenderProjectSwitcherTalliesLineUpUnderTheirHeadings`, `TestRenderProjectSwitcherSeparatesCursorFromAppliedScope`, `TestRenderProjectSwitcherHitsFollowTheVisibleWindow`, and `TestRenderProjectSwitcherSearchRowAndNoMatch` cover the overlay's rendering, its tally alignment, the cursor/applied distinction, and the hit rectangles. `tui.TestProjectSwitcherOpensOnTheAppliedScopeAndNavigates`, `TestProjectSwitcherAppliesOneComponentAndPreservesTheRest`, `TestProjectSwitcherQuotesLabelsThatNeedIt`, `TestProjectSwitcherCancelsWithoutTouchingTheFilter`, `TestProjectSwitcherSearchNarrowsAndKeepsAllReachable`, `TestProjectSwitcherSelectionSurvivesARefreshThatReordersScopes`, `TestProjectSwitcherLeavesTheSessionSurfaceAlone`, and `TestProjectSwitcherSurvivesAResizeAndPaintsInsideTheBody` cover root state, and `tui.TestProjectSelectorIsAlwaysOfferedInTheHeader` pins the entry point at every size. The `triage-contextual-project-completion`, `triage-quoted-scope-round-trip`, `triage-switcher-scroll`, `triage-switcher-at-narrow-width`, `triage-switcher-contextual-counts`, `triage-selector-survives-width-change`, `triage-horizontal-preview`, `triage-view-round-trip`, `project-switcher-keyboard-apply`, `project-switcher-narrow-fit`, `project-switcher-over-split`, and `project-scope-survives-resize` scenarios cover the integrated rendered contract. |
| Dashboard mouse parity | `tui.TestViewEnablesMouseOnlyForLiveDashboard`, `TestMouseSessionSelectionDoubleClickAndInterveningTarget`, `TestMouseStaleAndReorderedSessionTargets`, `TestMouseProjectSwitcherHeaderChipAndScopeRow`, `TestMouseProjectSwitcherIgnoresEverythingButItsOwnRows`, `TestMouseGeometryWrappedChipsAndPreview`, and `TestMouseIgnoresUnsupportedEventsAndOutsideCoordinates` pin render-owned hit maps, identity revalidation, double-click, pane routing, and the one overlay that publishes click targets. The committed `mouse-session-select-focus`, `mouse-switcher-scope`, `mouse-switcher-ignores-wheel`, `mouse-chip-removal`, `mouse-header-controls`, and `mouse-list-wheel` scenarios drive those paths only through `View.OnMouse`. Live terminal selection remains operator-attended and is recorded under [Dashboard triage scopes and mouse](#dashboard-triage-scopes-and-mouse). |
| Dashboard Needs Attention drains | A finished turn leads the dashboard exactly once — until the user has looked at it. `internal/daemon.TestUnseenPublishTransitionTable` and the `TestFocusRefresh*` family pin the daemon-owned bit itself (raised only on an unfocused transition into rest, cleared by any non-stop icon and by the focus observation, never set by a refresh, unchanged across a `stop`→`stop` republish), with `internal/daemon.TestUnseenNotRaisedByRestartRepublish` and `internal/daemon/state.TestSchemaV1PayloadRestoresAsSeen` pinning the inversion that makes a restart and a pre-field payload quiet rather than loud. `internal/daemon.TestFocusClearPersistsUnseenAcrossRestart` pins the other half of the restart story — that the clear itself is durable rather than only in memory: an already-converged unseen stop that the user then focuses advances the renderer generation exactly once (and not again for as long as the pane keeps focus), the render worker writes the cleared bit to `amx_state`, and a fresh daemon restoring that payload puts the row in Idle, with the pre-focus payload restored beside it as the control that still groups into Needs Attention. `internal/daemon.TestHandler_StateQueryReportsUnseenAndOmitsItWhenSeen` pins the wire projection *and* the omitted key, and `tui.TestApplyDaemonStatesOverlaysUnseen` plus `tui.TestApplyDaemonStatesDecodesAnOlderDaemonAsSeen` pin the overlay in both directions, the reset in the no-entry branch, and the older-daemon decode. `tui.TestStateGroupClassification` covers the grouping rule — an unseen `stop` in Needs Attention, a seen one in Idle whatever its activity, and the bit ignored on every other state name — and `tui.TestStateGroupSorting` pins the resulting order. The `unseen-stop-grouping-narrow-fit`/`unseen-stop-grouping-wide` scenarios render two identically-iconed `🛑` rows split across the two groups at both widths, and assert at both that only the unread one wears the gutter dot. Attention navigation is deliberately blind to the bit: `n`/`N` walk the Needs Attention group, so an unseen `stop` is on their path and a seen one — already demoted into Idle by the grouping rule above — is not, and no key reaches it by way of its unseenness. Ordinary `j`/`k` still reaches it, which is what the wide scenario asserts by stepping onto the demoted row with two presses; arriving there marks nothing and clears nothing. The `[defaults.jump].attention_states` set that the retired bracket chords consulted is out of this path entirely — it is accepted for compatibility and read by nothing, which `internal/config.TestLoad_JumpIsReportedAsDeprecated` pins as a notice rather than a behavior. The two transitions no scenario can reach are Go tests over that same fixture and driver, and each is here because of a property of the harness rather than of the dashboard. `tui.TestClearingTheUnseenBitDemotesTheRowOnTheNextRefresh` answers one refresh with the bit cleared and every other fact held constant, then asserts the row leaves Needs you for Idle and loses its marker while the icon, the title, and the two rows beside it do not move; a fixture replays one overlay identically on every tick, so a change of mind needs the overlay swapped between them. `tui.TestAFailedFocusLeavesTheRowUnacknowledged` installs a Kitty client whose `FocusTab` records the attempt and then refuses it, presses enter on the unread row, and asserts all three halves of a refusal: the attempt was dispatched, the error is reported, and the marker and the group position both survive. It is a Go test because the recording client every scenario runs answers a focus with nil and a successful focus quits the program, so the refused path is the only one with a model left to assert against — and an optimistic acknowledgement would throw away the one signal saying the session is still waiting on a human. |
| Dashboard unread completion marker | An unread completion says so on its own row, and keeps saying so until the operator actually looks at the session. `tui/dashboard.TestUnreadStopMarksItsRowAndASeenStopDoesNot` and `TestUnseenBitOnANonStopStateDrawsNothing` pin the leaf rule in both directions — stopped *and* unseen, never one without the other — and `tui/dashboard.TestPulseOutranksTheUnreadDotAndTheDotOutlivesIt` pins the precedence and its fallback in one pass: a pulsing unread row draws the pulse glyph and not the dot, and the dot is still there when the pulse decays, because seenness is daemon-owned. `tui.TestUnreadDotSurvivesTheKeypressThatClearsThePulse` is the same claim through the root's pulse-clearing keypress, which is the way an operator could most easily be shown a cleared marker they never earned. Aggregation is item-aware rather than row-aware: `tui/dashboard.TestCollapsedFamilyRollsUpUnreadAndAnExpandedOneLeavesItToTheLeaf` proves a folded family's summary carries its hidden members' news and an unfolded one leaves it to the leaf, so the marker relocates instead of being printed twice on adjacent lines; `TestAFoldedMemberPulseIsNeitherDrawnNorReservedFor`, `TestFilteredAndSingletonFamiliesMarkOnlyWhatTheyProject`, and `TestUnreadNoticeIgnoresInvalidSummaryMembers` pin the three ways a summary could speak for a line it does not project. The column itself is `TestMarkedAndUnmarkedRowsShareOneGutterColumn`, `TestGutterIsRemovedEntirelyWhenNoLineHasNews` (measured by its cost in cells, so a blank reservation cannot pass as an absent one), `TestOffScreenNoticeReservesTheGutterSoScrollingDoesNotShiftColumns`, and `TestGutterSitsOutsideTheGroupBorderAndSurvivesNarrowWidths`, which pins the prefix order and the ANSI-aware clamp down to one cell. `TestNoticeGutterChangesNoBucketOrOrder` and `TestNoticeGutterRendersDeterministically` are the two things the gutter must not do. Rendered end to end, `family-unread-collapsed` and `family-unread-expanded` fold and unfold a three-checkout family holding one unread stop beside a seen sibling and an active one, and prove the marker moves between summary and leaf while a family with nothing unread stays unmarked in both folds; the `unseen-stop-grouping-*` pair covers the unfamilied leaf at two widths. Nothing on the render path writes seenness: clearing stays with the daemon's Kitty focus observation, which is the durability half recorded in the Dashboard Needs Attention drains row above. |
| Dashboard blocked reason | A waiting row says what it is waiting for. `internal/daemon.TestHandler_StateQueryReportsPromptKindAndOmitsItWhenUnlabelled` pins the wire projection *and* the omitted key, so a blocked window no screen probe labelled decodes exactly as one from a daemon too old to carry the field; `tui.TestApplyDaemonStatesOverlaysReason` and `tui.TestApplyDaemonStatesDecodesAnOlderDaemonAsUnlabelled` pin the overlay in both directions, the reset in the no-entry branch, and the older-daemon decode. The kind-to-status mapping lives in `tui/dashboard` with the other presentation decisions, and the redesign moved it out of a separate tag cell and into the row's own Status column: `tui/dashboard.TestReasonStatusesCoverEveryPromptKind` is the totality guard the untyped shared field cannot give (every Claude and Codex kind must reach a status phrase, and no phrase may exist for a kind no harness can stamp), `TestBlockedStatusIsRefinedByItsPromptKind` pins the many-to-one collapse that reads a Codex and a Claude permission prompt as the same `Needs permission` while an unlabelled blocked row still says `Awaiting reply` rather than going blank, and `TestUnknownPromptKindIsNotRenderedRaw` pins that a kind this build does not know never reaches the screen verbatim. `TestStatusTextSaysWhatTheRowIsDoing` and `TestStatusVocabularyCoversTheSameStatesAsTheFooterGloss` pin the phrase set and its agreement with the footer, `TestStatusEmphasisIsSemanticNotPerState` pins that emphasis follows meaning rather than enumerating states, and `TestQueueRowDropsTimeBeforeStatus` pins the drop order that keeps the phrase on the row after the timer has gone. `MinRowWidth()` is the compact band's floor and excludes every conditionally-reserved column, so the split-view threshold cannot move with the contents of the current view; `tui.TestCrossModeDashboardListWidthAcrossFixture` and `tui.TestResponsiveSplitNeverStarvesTheListColumn` confirm it across the whole width matrix. The `blocked-reason-column-wide` scenario renders four identically-iconed blocked rows — two harnesses' permission prompts, a request for input, and one nothing labelled that reads `Awaiting reply` rather than going blank, with no raw prompt kind anywhere on screen. `blocked-reason-column-narrow-fit` renders the same four at 34 cells, where each whole Status phrase survives beside the name after the Time column has gone, and the profile, harness, and directory are nowhere — that width is below the split floor, so there is no inspector to carry them. |
| Dashboard branch column | Two worktrees of one repository are told apart — by name on the row, and by branch in the inspector. `internal/gitstate.TestProbeTable`, `TestProbeRunsTheDocumentedCommands`, and `TestProbeWithoutAnExecutorIsSilent` pin the advisory probe itself — a branch and a dirty bit read independently, with a non-repository, a missing `git`, a timeout, and a detached HEAD all collapsing to an empty branch rather than to an error; `internal/daemon.TestGitStateRefreshStampsTrackedWindows`, `TestGitStateRefreshDropsAResultForAPaneThatMoved`, `TestGitStateDisabledRunsNoProbe`, and `TestGitStateRefreshTakesNoLockAcrossASubprocess` pin the write-back, the staleness drop, the `git_probe = false` off-switch, and the discipline that keeps git off `stateMu`. `internal/daemon.TestHandler_StateQueryReportsGitBranchAndDirtyAndOmitsThemWhenUnprobed` pins the wire projection, the independence of the dirty bit from the branch, both omitted keys, and that the probe's bookkeeping directory stays off the wire; `tui.TestApplyDaemonStatesOverlaysBranchAndDirty` and `tui.TestApplyDaemonStatesDecodesAnOlderDaemonAsUnbranched` pin the overlay in both directions, the reset in the no-entry branch, and the older-daemon decode. Rendering lives in `tui/dashboard`, and the redesign moved the branch off the row entirely: it is the selected session's, drawn once in the inspector, rather than a column every row reserves. `TestBranchTextRendersTheDirtyMarkerOnlyOnANamedBranch` pins the `*` marker and its absence on an unnamed branch, `TestBranchReachesTheSelectedSessionContextRatherThanTheRow` pins that relocation directly, `TestBranchIsUncoloredAlongsideTheDirectory` pins that it reads as metadata rather than as state, and `TestBranchLongEnoughToHaveNeededACapIsStillDrawnWhole` pins what the move bought — a branch that the retired column would have truncated is now drawn in full. `tui/dashboard.TestDuplicateCheckoutsAreToldApartByBranch` and `TestOneMissingBranchDoesNotLeaveASiblingBare` keep the row-level use the branch still has, which is disambiguating two checkouts that share a name. `tui.TestCrossModeDashboardListWidthAcrossFixture` drives the real list renderer at 36/79/80/119/120/160 — both sides of both band boundaries — over a fixture where every third row carries a branch and one of them is longer than the column cap. The `branch-column-wide` and `branch-column-unreserved` scenarios render two worktrees of one repository, a dirty checkout, a non-repository row that pads rather than collapsing, and a filtered view with no branch at all where the column is not reserved. `branch-column-narrow-fit` renders 66 cells instead, where no branch is on screen at all: that width is below the split floor, so the inspector that carries it is not drawn and the two checkouts are told apart by name alone. Live confirmation against real checkouts is operator work, recorded under [Dashboard branch column](#dashboard-branch-column). |
| Dashboard responsive presets | Width bands survive as a footer input and nothing else, and the height budget agrees with what is drawn. `tui/dashboard.TestBandForBoundariesAndUnmeasuredWidth` pins the 80/120 boundaries and the deliberate reading of an unmeasured width as standard rather than as scarcity, while `TestBandsNoLongerReachTheSessionRow` and `TestQueueRowIsContinuousAcrossTheRetiredBandBoundaries` pin the retirement itself: the queue row is one shape at every width, spending its budget down rather than switching presets. `tui/dashboard.TestQueueRowDropsTimeBeforeStatus` is that drop order — Time before the Status phrase, and the name last — and what the row no longer carries is the inspector's now: `TestInspectorMetadataGeometryIsFixed`, `TestInspectorNamesEveryMetadataFact`, `TestInspectorUnknownFactsKeepTheirLine`, and `TestInspectorNoSelectionDrawsNoMetadata` pin the fixed metadata block in all four of its situations, and `TestProjectCardWearsTheInspectorGeometry` pins that the family card the pane swaps to keeps the same geometry. In the root, `tui.TestResponsiveViewFitsItsViewport` and `TestResponsiveListPaneFitsItsColumn` render the whole frame across 36/79/80/119/120/160 × three heights × both views × inspector on and off and assert no line exceeds its pane and no pane exceeds its budget; `TestResponsiveSplitNeverStarvesTheListColumn`, `TestResponsiveGeometryIsStableUnderRepeatedResolution`, and `TestResponsiveBandsAndSplitGeometry` pin the split fallback at the row floor and the width-only geometry that keeps it from oscillating, with `TestInspectorSplitWidthBoundaryIsExact`, `TestInspectorSplitHeightBoundaryIsExact`, and `TestAShortTerminalDropsToTheQueueRatherThanPromotingTheInspector` pinning both boundaries exactly rather than approximately; `TestResponsiveInspectorDrawsExactlyWhatItReserved`, `TestResponsiveChromeBudgetMatchesRenderedChrome`, and `TestResponsiveCursorStaysVisibleInShortTerminals` pin budget/render agreement and the cursor arithmetic that depends on it. The footer is `tui.TestCompactFooterIsOneRowAndKeepsTheRequiredTokens`, `TestCompactFooterFallsBackWholesaleRatherThanMixingSpellings`, and `TestStandardAndSpaciousOutspendCompactWithinTwoRows`; `tui.TestHeaderNamesTheActiveView` pins the orientation the narrowest widths cost the most. The `size-60x15-triage`, `size-80x24-triage`, `size-100x24-triage`, `size-120x30-triage`, and `size-160x45-triage` scenarios render the same queue at all five reviewed terminals; `size-split-floor-99-no-split` and `size-split-floor-100-split` assert the split's width floor from both sides of it; `size-extreme-short-window` pins the four-row viewport; and `asl-question-pending-narrow-fit`, `blocked-reason-column-narrow-fit`, `branch-column-narrow-fit`, and `run-timer-narrow-fit` are the rendered sub-floor widths end to end. |
| Dashboard footer budget | The dashboard footer is exactly one row at every width, so the rows it does not spend reach the list, and no binding it drops becomes unreachable. `tui.TestListFooterIsExactlyOneRowAtEveryWidth` asserts the single row — and that it never overruns its width — across empty, attention, active, idle, summary, and one- and two-chip filter states at 20/40/60/79/80/119/120/160/300, and `TestListFooterIsOneRowAtEveryWidth` re-asserts it at the band boundaries while proving the retired bracket chord's own vocabulary (`jump next:`, `jump prev:`, `esc cancels`) survives nowhere in the row. `TestListFooterKeyHintsAlwaysPresent` is the reachability half: `a actions`, `: commands`, `/ filter`, and `? help` are offered in every state group, and the actions route still reaches the peek, launch-another, clear-icon, and close entries the retired direct keys used to name. `TestListFooterPaletteOrder` pins that a drop shortens the fixed palette rather than reordering what is left, and `TestListFooterZeroWidthEmitsOneUnwrappedRow` pins the pre-resize path that budgets against no width at all. `TestListFooterTruncationKeepsTokensWhole` proves the budget drops whole bindings instead of hard-clamping a row mid-key, `TestListFooterFallsBackToBareKeysWholesale` proves a narrowed row never mixes the labelled spelling with the bare-key one, and `TestListFooterMarksOverflowWhenTruncated` proves the `? more` marker is honest in both directions. `TestListFooterReclaimsListCapacity` at 80x24 and 120x32 and `TestListFooterBudgetMatchesRenderedRowsAcrossStates` prove the reclaimed rows reach the list rather than merely disappearing, by pinning the position/visible-height fixed point against the rendered footer. The overflow promise is `tui.TestDashboardHelpDocumentsEveryFooterBinding`, which is the parity authority behind `? more`, with `TestDashboardHelpOmitsNonBindingCandidates` keeping the position fragment and the state gloss out of the keymap they compete with for space. The exact row counts and the visible-capacity fixed point stay in those Go tests; the `footer-*` scenarios are the rendered half, and they are chosen to be the states where the budget is decided rather than merely exercised. `footer-cap-79-compact-fit` and `footer-cap-80-standard` straddle the standard boundary and assert the same labelled row on both sides of it with no `? more`, because at those widths nothing was dropped; `footer-cap-80-standard` additionally asserts the `]/[ jump chord` hint is gone. `footer-cap-119-standard` and `footer-cap-120-spacious` straddle the spacious boundary, where the surplus buys `n/N needs you` and `tab switch pane` ahead of the marker, and both assert the retired `r restart` is not what the extra cells bought. `footer-cap-short-height` is the reclamation seen from the list side — a 14-row viewport still spends exactly one row, carrying the `1–4 of 7` pagination token within it, and four list rows plus the more-indicator are what that bought. `footer-filter-chips-active` proves two chips add `esc clear filters` without a second row and without advertising any per-chip removal key, `footer-gloss-narrow-fit` proves a 30-column terminal still lays out, and `footer-survives-inspector` proves an open inspector keeps the footer and its `i inspector` and `tab switch pane` hints rather than moving the toggle into the header. Each `footer-cap-*` scenario also asserts the retired `Sessions: N ·` summary line is absent, and the one-row status bar below the footer stays pinned in all of them. |
| Dashboard key affordance | A key the operator has to press is painted like a key wherever it is named. The rule came out of reviewing styled fixture renders rather than ANSI-stripped ones, which is the only way either gap below could be seen at all: the text was already correct in both, and every assertion covering them strips the styling before it reads. `tui/dashboard.TestEmptyHeroPaintsItsKeysLikeKeys` covers the empty dashboard across its three sentences — no sessions, loading, everything filtered out — where the two keys that lead out of a screen with no rows on it were the same dim as the sentence around them. `tui.TestHelpKeymapPaintsItsKeysLikeKeys` covers the help overlay, the one screen whose whole subject is which key does what, and asserts three things: the key column wears the footer's key style at its padded width, a parenthetical aside and a wrapped continuation line keep the quiet treatment because neither binds anything, and the icon reference stays dim because its left column is a glyph the daemon paints rather than one anybody presses. It also pins that no key is spelled past the ten-cell column, measured in display cells rather than bytes: the split is positional, so a wider key would be cut in half with the remainder painted as prose — a corruption every stripped assertion in the file reads straight through. The copy is unchanged by any of this. The double spaces that set a key apart are in the sentence rather than in the styling, so a terminal that drops color reads exactly what it read before, which is what lets the existing text assertions stand unmodified beside the new styling ones. |
| Dashboard keymap migration | The dashboard answers twelve command-backed bindings (`Enter`, `s`, `t`, `S`, `/`, `Esc`, `l`, `i`, `p`, `u`, `?`, `q`/`Ctrl+C`) plus its cursor and structure keys (`a`, `:`, `n`/`N`, `Tab`, and paging), and every capability the retired keys named is still reachable. `tui/dashboard_bindings.go` is the single declaration, which is what makes the claim checkable in one place: `tui.TestEveryDashboardBindingNamesACatalogCommand` rejects a binding whose command does not exist, `TestPaletteShortcutsComeFromTheBindingTable` pins that no catalog row carries a shortcut of its own, and `TestCommandCatalogDashboardShortcutBindings` maps every catalog command to the key it advertises — exhaustively in both directions, so a retirement that left a stale shortcut on a palette row fails there rather than reaching the screen. `TestEveryRetiredDashboardKeyIsInertAtTheList` presses every retired spelling in each of the seven swept model states and requires the model to come back unchanged, measured as a whole-struct comparison rather than a list of effects, so a key that starts doing something *new* fails as loudly as one that never stopped; `TestEveryFinalDashboardBindingDispatches` is its complement, since a table of uniformly inert keys would satisfy the sweep perfectly. `TestEveryCatalogCommandIsAvailableSomewhere` is the reachability guard the retirement made necessary — the palette is now the only route to most of these, so a command that is never available there is a capability that left the build. `TestDirectKeyAndPaletteAgree` and `TestUncheckedBindingsRunTheirCommandRegardless` pin what each surviving key does against the palette entry that runs the same command, and the per-family retirement tests (`TestRetiredDirectActionKeysDoNotDispatch`, `TestRetiredChipKeysDoNotDispatch`, `TestRetiredDetailKeyIsInertAtTheList`, `TestRetiredPeekKeyIsInertAtTheList`) keep each retirement's own reasoning beside it; `s`/`S`, `u`, `g`/`G`, and `Ctrl+D`/`Ctrl+U` were retired and later restored, so they are covered by the binding and dispatch tests rather than by retirement tests. `needs-you-reveals-folded-target` is the rendered half of the one capability with no predecessor key: `n`/`N` unfold a family to reveal a waiting member and move the cursor onto it while asserting zero `FocusTab` calls, because finding a waiting session is not answering it. Configuration compatibility is `internal/config.TestLoad_JumpIsReportedAsDeprecated` and `TestLoad_NoJumpTableIsNotDeprecated`: `[defaults.jump]` still loads and still validates, is reported once as a dismissible notice, and is read by nothing. The old→new mapping operators read is the table in [Upgrading](upgrading.md#upgrading-from-the-old-dashboard-shortcuts), and the per-key record of where each capability went is [the keymap inventory](dashboard-keymap-inventory.md). |
| Backend migration baseline | `internal/kitty.TestRemoteControlBaseline` pins the exact `kitten @` argv of every `KittyClient` method against `internal/kitty/testdata/remote_control_baseline_v1.json`, and `TestBackendMigrationMatrixAssignsEveryWorkflow` keeps every row of the workflow acceptance matrix in [Backend migration baseline](backend-migration.md) assigned to an owning session with an acceptance check. |
| Debug workflow | `cmd.TestReleaseDocumentationPinsVersionAndDiagnosticWorkflow` pins AgentMux 0.8.0, schema v13 export documentation, the schema-v2 snapshot contract, event-time tail semantics, explain graph defaults, v1/v2 bundle replay, the `amx debug status` → `amx debug explain` → `amx debug bundle` → `amx debug replay` workflow, and every required failure-matrix row including distinct notification failure and suppression. |
| Bundle secrecy and traversal | `internal/debugger` tests plant a secret corpus, verify share/forensic redaction, exclude raw sources, reject symlink publication, build flat deterministic archives, and replay portable share evidence. |
| Unified causal diagnostics | One bundle explains the causal path across both systems, and AgentMux reads only ASL's public sanitized export to do it. `scripts:tests/test_asl_diagnostics_export.py` is the producing half: bounded, deterministic, control-plane only, with an omission ledger of counts, digests, and named reasons, and canaries proving no question body, reply, prompt, model output, raw bridge stderr, or secret reaches the document. `internal/aslexport` is the boundary: `TestFetchDropsFieldsThisBuildDoesNotImplement` proves the typed decode drops fields this build does not implement, so a newer ASL cannot inject one into a bundle unreviewed, and `TestFetchNamesEveryDegradedCondition` covers every survivable failure — absent, predating the subcommand, hung, oversize, unreadable, silent, unnamed, newer schema, foreign contract set, self-reported failure — each resolving to a named availability rather than an error. `internal/debugger/bundle_asl_test.go` proves the join: both acceptance counters, both build identities *and* both binary hashes, contract-set agreement, operation and closure IDs, delivery outcomes matched against journal records by request ID with unmatched deliveries called out, `TestBundleDoesNotReadASLPrivateStateFormats` pinning the manifest's explicit exclusion of `state.json`/`events.jsonl`/`mailbox.jsonl`, `TestASLBundleMembersAreDeterministicAndCanaryFree` pinning byte-identical repeated capture over frozen fixtures and canary-free output, `TestShareBundleKeepsTheASLCorrelationReadable` failing whenever a new field is added without extending the share-mode key allowlist, and `TestBundleSurvivesEveryASLUnavailableCondition` proving bundle creation still succeeds and still names the reason when ASL, the bridge, or the integration is unavailable. `cmd/debug_bundle_asl_test.go` pins `--include-asl` wiring: no subprocess without the flag or with `[defaults.asl] enabled = false`, resolution failures carried as availability, and the configured command, state root, run selector, and contract-set ID reaching the export. |
| Restart and replay equivalence | `internal/daemon` restart-prefix tests and `internal/debugger` golden replay tests cover restored reducer state including exact process ownership, daemon sequence resets, ordering, routing, ASL phases, duplicate requests, coalesced renders, and explicit schema-v8/v9 limitations. |
| ASL delivery correlation | `/Users/sglavoie/scripts/tests` covers stable UUIDv5 request IDs, bounded retry/backoff, duplicates, permanent failures, legacy fallback, missing binaries, exhaustion, metrics/tail/Workbench visibility; `internal/debugger.TestExplainCorrelatesASLSourceDeliveryDaemonAndRenderEvidence` pins the cross-repository graph. |
| ASL run presence and lease reconciliation | Presence comes from the strict nested `agent_mux` projection and nothing else in ASL's state. `internal/agentloop` pins the exact-member-set decode (missing member, unknown member, unknown phase, relative plan dir, zero generation, untrimmed string, malformed timestamp, process on a resting phase), unsupported contract sets, and the tri-state verdict: paused is present without a PID, finished and an exited process are confirmed absent, and unreadable/missing/misplaced state, an unverifiable process, and a recycled PID are all indeterminate. `TestDiscoverIsolatesOneCorruptPlan` proves one corrupt plan neither hides healthy plans nor reports itself absent, and `TestDiscoverTreatsStateWithoutAProjectionAsIndeterminate` pins the paired cutover. `internal/agentloop.TestAuthenticateProcessOnlyAssertsAbsenceWhenThePIDIsGone` and `TestCompareStartTokens` pin PID-reuse rejection and the timezone-rendering tolerance that stops a healthy run being read as recycled. `internal/daemon` covers startup plus ten-second reconciliation, two consecutive confirmed absences before retirement, indeterminate evidence neither advancing nor resetting a miss streak, an absent state root and a failed sweep preserving every lease, rejection of observations that went backwards on either monotonic counter, and the `asl_reconcile` metrics reaching the health surface. `internal/config` pins `defaults.asl.state_root` acceptance and relative-path rejection. The sweep's cost is held off the readiness path: `internal/daemon.TestStartupRestoreLeavesTheSweepToTheFirstMaintenanceTick` proves the bounded initial snapshot performs no sweep and the very next tick does, `TestReconcileReusesOneProjectionCacheAcrossSweeps` proves one cache spans sweeps, and `internal/agentloop` projection-cache tests pin reuse of unchanged documents, re-decode after a rewrite, memoized failures, read-through when the file is gone or no cache is supplied, and eviction of plans that left the state root. |
| ASL contract set and compatibility refusal | AgentMux and ASL are versioned together by one contract-set ID, a digest over the canonical corpus ASL owns and this repository vendors byte for byte at `internal/aslcontract`. `internal/aslcontract` re-derives that ID from the embedded bytes, pins every per-file digest, excludes the manifest from the identity it carries, and proves fixtures carry the placeholder identity only; `internal/aslbridge.TestInstalledBridgeServesTheContractSet` is the live half, where a running ASL must name the same ID and `assertTheDigestSpansEveryComponent` proves that one equality vouches for the lifecycle and projection fixtures too. The two halves separate the two drifts: a locally edited fixture fails `just check`, a stale vendored copy fails `just asl-integration`. Bridge protocol 2 is the only protocol and hook schema 2 the only lifecycle schema; there is no v1 path in either. `internal/aslbridge` covers the server-first hello carrying the ID, a retired protocol and a missing `session.handshake`/`runs.snapshot` capability each ending the child, and a foreign set ending supervision without restart — mismatch is terminal, not transient. `internal/aslbridge/contract_gate_test.go` covers every way the mutation gate closes: a foreign set named by a snapshot or refused by a handshake, a grant against an older digest and a withheld grant both leaving reads working, an undecodable receipt disabling mutations as ambiguous, and a new projection withdrawing the previous grant. `internal/daemon` gates lifecycle-hook ingestion on the same identity: `TestTheGatedVocabularyIsTheDeclaredOne` pins the sixteen boundaries against the schema's own enum, `TestEveryCanonicalLifecyclePayloadPassesTheGate` and `TestEveryInvalidLifecycleFixtureIsRefusedAtItsOwnLayer` run the vendored corpus and assign each negative case to the decoder, the gate, or the schema, and `TestAnIncompatibleLifecycleHookIsQuarantinedWithoutReduction` proves a retired schema or foreign set is quarantined with a stable non-retryable reason that quotes nothing the producer chose. Availability is the other half: `TestHarnessHooksAreUnaffectedByTheLifecycleGate` proves Claude/Copilot/Codex hooks carry no compatibility block, are never gated, and reduce normally, so a refused ASL event never costs AgentMux its core function. `internal/indicator` proves the contract caps a lifecycle payload far below the compaction threshold and that compaction carries the block anyway, so a size limit can never present as a version mismatch. |
| ASL human-actionable questions | A worker wait remains bare 🔁 until the explicit `asl outbox wait` supervisor handoff proves an actionable open set. `internal/hookevent` recognizes the body-free question/reply/resolution schema; `internal/agentloop` validates the fixed-size v2 compatibility projection and rejects v1/stale/malformed/foreign evidence; `internal/daemon` covers immediate ❓🔁 plus desktop notification, duplicate idempotence, F8 dismissal until a newer question, lease/trusted-Stop fallback ordering, anonymous/pipe-worker stop rejection, heartbeat resistance, aggregate reply/resolution fast clearing, stale-closure cutoffs, multiple-question final closure, reply/zero-projection/finish fallbacks, restart-safe reducer state, and notification behavior. Wait/reply reconciliation, stale-sweep exemption, explicit paused questions, sanitized detail preview, TUI attention grouping, the `awaiting response` gloss, and `n`/`N` attention navigation — the successor to the retired `]q` chord — remain covered. |
| ASL pairing | The daemon is spawned detached and never inherits an operator's `PATH`, so a bare `asl` that works in every terminal is unreachable from the one process that needs it. `internal/aslbridge/resolve_test.go` pins sibling-before-`PATH` resolution, a path-bearing command taken at its word, relative paths made absolute, every not-found wrapping `exec.ErrNotFound`, the auto-mode re-probe backoff (5s/15s/60s to a five-minute ceiling, with no restart charged) against required mode's terminal stop, and `Status.ExecutableSource`. `cmd/config_asl_sync_test.go` covers the command that ends it: `--check` writes nothing and migrates nothing; active incompatible runs refuse whole-root with pid, run, and plan named, before and after the migration pass; contract-set and bridge-protocol mismatches refuse before any write; a successful apply persists the absolute path, the state root, and `enabled = true`; and every post-write failure — a failed restart, a daemon that never reached ASL, a daemon that paired with a different executable — restores the previous bytes (or removes a file it created) and restarts onto them. `internal/config/asl_pairing_edit_test.go` pins the surgical TOML edit that makes that promise keepable: comments, blank lines, key order, trailing newlines, and `#` inside paths all survive, dotted-key and duplicate-key ambiguity is refused with the edit that fixes it, new keys land inside `[defaults.asl]` rather than under whatever header came last, and re-pairing is byte-identical. `internal/atomicfile` pins mode preservation and that a failed replacement leaves no temporary file beside an authoritative artifact. `scripts:tests/test_asl_agent_mux_pairing.py` is the ASL half: the survey classifies every plan, resolves its own executable to an absolute non-canonical path, and refuses to migrate around a live run. |
| ASL integration diagnosis | One `asl: unavailable` line was true for six unrelated conditions with six different fixes. `cmd/doctor_asl_test.go` pins the schema-v2 `asl_integration` block: six named dimensions in a stable order, each with its own stable finding identifier, and severity that follows the declared mode and nothing else. Shell resolution warns for a bare name even when this shell resolves it, because the successful lookup here does not predict the daemon's; daemon resolution reports separately, so shell-ok plus daemon-failed is itself the diagnosis; a contract mismatch fails in every mode, from either the bridge or the state root; projection errors stay warnings even in required mode so one corrupt plan cannot block a release check; freshness covers a stale snapshot and a sweep lagging past twice its interval; and delivery reports quarantined lifecycle hooks, which are otherwise invisible because the hook path acknowledges every producer it refuses. `internal/daemon/handler_asl_delivery_test.go` pins those counters through the real ingestion path, including that a producer cannot grow the reason map and that an unreadable harness payload is never charged to ASL. Disabled mode answers every dimension `not_applicable`, and so does an unconfigured machine with no `asl` installed — absence is not a diagnosis. `asl_unavailable` and `asl_read_only` keep their exact pre-v2 names and meanings. |
| Screen classifier patterns and decline health | The harness UI copy the classifier matches on is data, overridable per pattern set, and a classifier that has stopped matching now reports itself. `internal/screenstate/patterns_test.go` pins the schema and its bounds (unknown key, uncompilable regex, oversized pattern and phrase list, empty required fields, the three adapter boolean shapes, deterministic warning order) and every load outcome: a missing override is not a warning, an unreadable or invalid one keeps the embedded default, a valid one shadows it, and a non-absolute config directory is refused rather than read from the daemon's working directory. `internal/screenstate/patterns_clauses_test.go` walks the adapter clause constants so a clause renamed in the TOML fails the suite instead of degrading silently to "no match", and `TestCodexPromptClausesGateTheSettlePaths` pins the two fail-open clause families that refuse outright rather than classify weakened. `internal/contextlimit/patterns_test.go` and `internal/screenstate/patterns_contextlimit_test.go` prove the one override directory covers the set that lives in another package, including that a bad override there degrades in both packages. `internal/daemon/run_screen_patterns_test.go` drives `screenstate.Load` for real through daemon startup and pins the health block for a clean start, a valid override, a broken override, and a daemon that never loaded. `internal/daemon/screen_decline_streak_test.go` pins the counter itself — streak and fingerprint stability, the atomic idle-sample reset, non-colliding per-source streaks, purge on window close, and the whole thing driven through the real Codex probe — and `screen_decline_export_test.go` pins its publication, retraction, and absence from schema v12. `cmd/doctor_screen_patterns_test.go` covers the per-set source lines, that an applied override is source-only, that all four failing warning kinds raise `screenstate_override_invalid` without failing `--strict`, and the older-daemon "unreported" line; `cmd/doctor_screen_decline_test.go` covers the other severity, where a persistent decline fails `--strict` and benign declines do not. `cmd.TestDoctorJSONCommandSurfaceIsVersioned` pins the doctor report schema version and the uniqueness of every stable finding identifier. Live confirmation that a deliberately non-matching override makes a real pane hold `⚡` while the streak climbs and `--strict` exits 2 is operator work, recorded in [Screen classifier health](screen-classifier-health.md). |
| Live session overview | Every managed session at once, as a grid of live pane previews, plus the one thing it shares with the dashboard: `a` opens the same session-action menu, under the same availability and confirmation rules, declared once in `tui/sessionactions` (see [Session actions across surfaces](#session-actions-across-surfaces)). Browsing the grid itself is still read-only against Kitty except for the single focus call. `tui/overview` is a standalone Bubble Tea program with its own suite: `grid_test.go` pins the geometry at every shipped terminal size and the single-tile floor, `nav_test.go` pins the spatial/sequential split and that navigation is total over an empty list and a gridless terminal, `mouse_test.go` pins the pointer — the hit test agrees with the drawn tile cell for cell, a left click focuses the session drawn there through the digits' single-flight path, a click on chrome or an empty slot does nothing, the wheel only turns pages, and nothing reaches the grid under an overlay — `order_test.go` pins the project/family grouping that decides which tile is where, `capture_test.go` pins the read scheduler — four in flight, one per window, FIFO with coalescing, slots released by request ID and painting gated by generation, plus every way a reply can be wrong (duplicate, unsolicited, resized, closed, superseded anchor) — and `view_test.go` pins the rendering, including that styling from one capture cannot reach the tile beside it and that a tile too small for a box drops the box rather than the session. `TestTheRenderingHalfPerformsNoIO` and `TestTheLifecycleHalfReachesTheWorldOnlyThroughItsSeams` are the structural guards that keep those two halves apart. `tui/overview/acceptance_test.go` is the integrated half: eighteen scenarios drive the real `Update`/`View` loop over an isolated TOML config, a frozen clock, and a recording Kitty world whose nine non-read methods record a violation rather than acting — each write is permitted explicitly, per scenario, for exactly the action under test. They cover a multi-profile universe larger than the screen, paging and resizing while reads are out, a session disappearing under a capture and under a focus alongside a Kitty that refuses the focus outright, which the surface has to answer the same way, repeated topology failures and recovery, a cold outage that still owes its source pane, dismissal and clean reopening, and a 200-tick burst that checks the bound, the scheduler/world agreement, and the retention and queue bounds on every single iteration, then that the reads followed the visible page rather than refreshing tiles nobody is looking at, before proving the keyboard still moves. The action half adds: a close that leaves the tree and is restored by `u`; that browsing every menu writes nothing; that every session is reachable and readable by keyboard alone; each of the four shipped terminal sizes carrying 8–20 sessions with CJK, emoji, grapheme-cluster, and over-long identities, walked through the menu, an unavailable entry and its reason, the profile picker, a destructive confirmation, and the help overlay, in normal and compact mode, asserting every row is exactly the promised width and leaks no styling; a close with `confirm_destructive = false`, which must skip the prompt and still offer the undo; a close aimed at the tab the overview is running in, which must say so and offer no undo; a profile replacement driven end to end, which must launch under the chosen profile before it closes the old tab; and a restart whose relaunch Kitty refuses, which must name the refusal and leave the world byte-for-byte unchanged. None of them sleeps: effects are parked and delivered by the test. The read-only boundary is `tui/commands/fetch_overview_test.go`, where `RefreshOverview` is driven against a client whose ten write methods panic — `TestRefreshOverviewStaysReadOnlyOnAFreshSnapshot`, `TestRepeatedOverviewRefreshesStayReadOnly` — and `TestOverviewRetrievalLeavesTheRequestForTheDashboardToConsume` proves three overview reads across both topology sources leave a pending `amx_dashboard_select` request intact for the dashboard to consume and clear afterwards. The singleton rule is `cmd/dashboard/overview_test.go`, including the two ways this surface is stricter than the dashboard and the overlay pickers: `TestOverviewListFailureIsReportedWithoutASecondSurface` and `TestOverviewFocusFailureIsReportedWithoutASecondSurface`. Because the singleton check reads a foreground argv, which spellings count as a running overview is part of the rule: `TestOverviewFocusesAnExistingInstanceStartedWithPersistentFlags` pins that the root's inherited `--debug` is recognized after the subcommand as well as before it, on either side of `--window-id`, and `cmd/root_test.go`'s `TestTheOverviewDetectorAcceptsEveryRunnableOverviewArgv` closes that loop against the assembled root, so an argv the CLI accepts cannot be one the detector misses. `agent_mux_conf_test.go` pins the shipped `F2` line against the catalog the `F1` reference is generated from, so the snippet and the reference cannot disagree. `just race-core` names `./tui/overview` and `./tui/commands` explicitly, because `./tui` does not include them. Live confirmation in a real Kitty is operator work, recorded under [Live session overview](#live-session-overview). |
| Kitty topology outage is survivable and legible | A Kitty that stops answering `kitten @ ls` must degrade the dashboard rather than empty it, must not be answered with more subprocesses, and must leave evidence an operator can find afterwards. `internal/kitty` pins the per-socket coordinator: concurrent reads collapse into one subprocess, waiters are counted but never charged as attempts, error classes are the fixed `timeout`/`command`/`decode` vocabulary, and the observer fires only on health edges. `internal/daemon/snapshot_coordination_test.go` proves the four listing subsystems share one subprocess through a wedged read; `internal/daemon/kitty_fault_acceptance_test.go` is the cross-layer scenario, where a warm daemon meets a Kitty that first parks and then refuses: `state_query` is still answered from the cache while every listing path is blocked, the last-good topology and generation survive the failure that follows, the coordinator reports the streak, and recovery clears both the staleness and the streak. `internal/daemon/kitty_snapshot_journal_test.go` pins the durable record — one `daemon.kitty_snapshot` row per outage and one per recovery however many attempts each spans, evidence limited to the error class, the counters and the latencies, and no socket, argv, payload, stderr, or raw error anywhere in the envelope — and drives a real `run()` against a failing runner to prove the observer is installed before the bounded startup restore performs the first list. `cmd/debug/kitty_snapshot_test.go` pins the reporting split: `amx debug status` describes the snapshot without moving its daemon verdict, `amx doctor` judges it, and the two share one renderer so their four states (fresh, stale, cold, unreported) cannot drift. `kitty_snapshot_stale` is warning severity, so it leaves `healthy` true and `--strict` exiting zero; a daemon too old to carry the block is unknown rather than a failure. `cmd.TestDoctorJSONCommandSurfaceIsVersioned` pins the doctor report at schema v4 for it. On the dashboard side, `tui/commands/fetch_test.go` pins the daemon-first refresh and its direct-listing fallback, `tui/model_topology_staleness_test.go` pins the nonfatal warning over a working list against the red cold error, and `tui/model_topology_fault_test.go` runs the real refresh against a wedged Kitty to prove the in-flight gate keeps a burst of ticks from multiplying fallback listings. |
| tmux backend health is legible, bounded, and private | A backend that fails has to be diagnosable with the daemon down, the work done for a fleet has to stay bounded as it grows, and no diagnostic may carry what a pane printed. `internal/tmux/gate_test.go` pins the client bound: no more than `MaxClientCommands` tmux clients run at once however many callers ask, a caller that cannot get a slot within its own timeout is told so, thirty panes read at once against a real server stay inside it, and `FailureClass` is a fixed vocabulary that never carries a path or an argument. `internal/daemon/runtime_health_test.go` pins what health publishes (`protocol.RuntimeHealth`: passes, failures and their class, live sessions and how many have a terminal attached, screen reads that reached a backend, tmux client activity) against a real private server; that a failing streak is two `daemon.runtime_reconcile` journal records (the failure and the recovery) and a stopped agent one; that a pass runs on every maintenance tick and backs off to thirty seconds while it fails; that a tmux slot settles through the runtime checkpoint and is never queued for the Kitty writer, so doctor's `render_generation_drift` is not raised for it unless the checkpoint could not be written; and `TestHandler_ConcurrentHealthDuringRuntimeMaintenance`, in the stress set, asks health while maintenance reconciles and the probes read. `cmd/debug/doctor_backend_test.go` pins the `backend:`, `runtime sessions:`, `screen captures:`, and `versions:` lines, every finding identifier and its severity (`backend_*` by the codes of `amx backend status`, `runtime_reconcile_failing`, `runtime_reconcile_stale`, `screen_capture_failing`, `tmux_client_timeouts`), the unknown-not-broken answer for a daemon that predates the block, and that an absent Kitty is a note only where launches go to tmux outside any Kitty window; `cmd.TestDoctorJSONCommandSurfaceIsVersioned` pins the report at schema v7 for them. `internal/debugger/bundle_backend_test.go` plants a prompt, a credential, and a home directory in every free-form field of the backend, runtime, and journal inputs and proves a share bundle keeps the counts, classes, codes, and releases and none of the canaries. `internal/daemon/performance_fleet_test.go` measures one, ten, and thirty tmux agents, idle and printing, and holds the bounds (one client per pass, at most four at once) and the budgets (pass, screen read, one tick's work, server memory per session) under `just reliability-stress`. The recorded measurements and the live failure runs (a server stopped, a server killed, no tmux and no daemon) are in `docs/acceptance/ghostty-desktop/session-18.json`. |
| tmux sessions show their state, color, and order without Kitty | One function orders sessions for every surface: `internal/session.TestOrderMatchesSortRunningForAnyCarrier`, `internal/daemon/companion.TestProject_V2ListsSessionsInTheDashboardsOrder` (v2 lists Kitty and tmux sessions in the dashboard's order, v1 keeps tab order), `tui.TestResortWithoutKittySaysSessionsAreAlwaysInSortOrder` and the `portable-resort-always-sorted` scenario. A tmux launch records its profile's color: `internal/session.TestLaunchOnTmuxRecordsTheProfileColor`. The daemon renders settled state into the session's pane options: `internal/daemon.TestSettledTmuxStateIsRenderedOnceIntoItsSessionsStatus`, `TestUnsettledTmuxStateIsNotRendered`, `TestFailedStatusWriteIsRepeatedLaterNotEveryPass`, and against a real private tmux server `TestRenderedStatusReachesARealTmuxPaneInOneClientPerPass` (a rendering pass is the reconciliation client and one more, a quiet pass the reconciliation alone). `internal/tmux.TestWriteStatusesIsOneClientAndEachPaneStandsAlone` and `TestAnAttachedTerminalShowsTheRenderedStatus` hold the write and what a real client's terminal receives: the title, the status line on the session's color, text never read as tmux styling, and no line in a terminal the workspace hosts. |
| Navigation, overlays, and the one dashboard in a plain terminal | Switching a terminal: `internal/tmux.TestSwitchClientMovesATerminalAndNeverLeavesTwoOwners`, `TestSwitchClientRefusesExitedAndMissingTargets`, `TestSwitcherValidatesTheSessionAndMovesTheOwnershipRecord` (real terminals on a private tmux server: the owner of the target is detached in the move, an exited or lost agent is refused and recorded, the registry's owner follows the terminal). Commands: `cmd/session.TestFocusManagedSwitchesTheNamedClientInListOrder`, `TestFocusManagedUsesTheCurrentSessionAndItsTerminalWhenNothingIsNamed`, `TestFocusNextGoesToTheNextSessionWaitingOnTheOperator`, `TestFocusNeverReachesAnExitedOrLostSession`, `TestFocusHostedByTheWorkspaceAsksItToShowTheSession`, `TestMuteToggleWithoutAFlagMutesTheCurrentRuntimeSession`, `TestLiveConversationsListBothBackends`, with every Kitty seam failing the test if reached. Keys: `internal/kittykeys.TestTmuxFunctionKeysMatchKitty` holds the tmux table to `agent-mux.conf`'s, and `internal/tmux.TestFunctionKeysRunAmxForTheSessionAndTerminalThatPressedThem` types every key into a real attached terminal and reads what amx was run with. Popups: `internal/tmux.TestPopupRunsAProgramOverAnAttachedTerminalWithTheSessionsEnvironment`, `cmd/dashboard.TestPopupDashboardOpensOnItsSessionAndSwitchesTheTerminal`, `TestDashboardSelectsARuntimeSessionWithoutBecomingAPopup`, `TestOpenDashboardOutsideKittyOpensAPopupOverTheLastTerminal`, `tui/commands.TestFocusInAPopupSwitchesTheTerminalInsteadOfAttaching`, `TestASurfaceOpensOnTheRowOfTheRuntimeSessionItWasStartedOn`, `tui.TestAPopupDashboardEndsOnceItsTerminalIsSwitched`, `tui/overview.TestAPopupOverviewEndsOnceItsTerminalIsSwitched`. |
| Nothing in install, the gates, or the app's default requires Kitty | Install: `cmd/release.TestJustInstallCreatesNothingOfKittys` (`just install` touches no Kitty directory or snippet; `install-kitty` links it; `uninstall` still removes a link). Paired gate: `internal/release.TestGateRequiresNoKitty` (no step runs or names Kitty, and the fixture's `kitty` and `kitten` fail if anything runs them, so `TestGateRecordsSignedEvidenceForACleanPair`'s passing record proves none was), `cmd/release.TestJustCockpitSmokeFailsRatherThanSkips` (a real-tmux smoke on a socket of its own, naming no Kitty, mutating nothing), `TestJustKittySmokeIsOptInAndFailsRatherThanSkips` (the Kitty smoke is unchanged in what it requires and is a dependency of no gate). Record: `internal/release.TestLoadEvidenceStillReadsASchemaOneRecord` (schema 2 omits `environment.kitty`; schema 1 still loads; other versions are refused). App: `BackendSetupTests.testFollowingCLIUsesTheReportedDestinationAndNeverGuesses`. Dashboard without Kitty: the `portable-open-here-*`, `portable-launch-picker-*`, `portable-session-action-names-tmux-*`, and `portable-resort-always-sorted*` scenarios. The launch default is not part of this row: `internal/config.TestLaunchBackendDefaultsToKitty` still holds it on Kitty until the daily-driver trial record passes ([Backend migration](backend-migration.md#session-21-record)). |

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
tmux cockpit smoke — then writes a signed JSON record naming both revisions,
the artifact hashes, the contract set and manifest SHA-256, the OS and harness
versions, and every command with its result and duration. No step needs Kitty
(`internal/release.TestGateRequiresNoKitty`); `just kitty-smoke` is the opt-in
smoke for an operator who launches there. The procedure, the
evidence schema, the verification command, tagging, rollback, and maintenance
installation are documented in
[Paired releases](paired-releases.md).

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
  `just cockpit-smoke` fails rather than skips when tmux is absent. Both close
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
event rather than reporting a pair it never compared. No hosted runner has an
installed pair and its daemon, so `cockpit-smoke` is operator-only by
construction.

### Dashboard branch column

The probe runs real `git` against real checkouts, which no hermetic test does,
so confirming it is operator-attended work on the artifact
`just install-restart-smoke` installed:

1. Open two managed worktrees of one repository plus the repository's own
   checkout. Each row shows its own branch between the run timer and the
   directory, and the two worktree rows differ there even after the shared
   directory prefix has truncated away.
2. Touch a tracked file in one of them. Within one probe TTL (10s) that row's
   branch grows a trailing `*`; reverting the change drops it again.
3. Open a shell tab in a directory that is not a repository. Its branch cell is
   blank and its directory still starts in the same column as the others.
4. Set `git_probe = false`, restart the daemon, and confirm the column
   disappears entirely rather than rendering a row of blanks — and that
   `ps` shows the daemon forking no `git` at all.
5. Detach HEAD in one checkout. The branch cell goes blank while the row stays
   otherwise unchanged; the dirty state of that checkout is not claimed.

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

### Dashboard triage scopes and mouse

Release-session record, 2026-09-04: the hermetic `triage-*` and `mouse-*`
scenario sets passed through `amx tui-test run`, including both preview
directions and the 105/106-cell width boundary. The live portion below
remains explicitly environment-bound: Kitty is installed and an operator-owned
instance is reachable, but this autonomous run has no attended GUI pointer or
Shift-drag input channel. It did not inject events into the operator's desktop
or treat remote-control keypresses as mouse evidence.

| Live check in an isolated disposable Kitty and daemon environment | Record |
| --- | --- |
| Switcher and list clicks | not run — requires attended pointer input against disposable windows |
| Double-click session focus | not run — requires attended timing and foreground-focus confirmation |
| Wheel routing over list and inspector | not run — requires attended pointer placement |
| Horizontal and vertical preview directions | not run live — both passed hermetically as `triage-horizontal-preview` and `triage-vertical-direction-ignored` |
| Project scope survives a resize | not run live — the old rail's exact 105/106 boundary passed hermetically as `triage-switcher-at-narrow-width` and `triage-selector-survives-width-change`, and scope retention as `project-scope-survives-resize` |
| Presentation recency survives daemon restart | not run live — restart persistence is covered by `internal/daemon/state.TestPresentationChangedAtRoundTripsThroughUserVarInMilliseconds` |
| Shift-drag terminal text selection | not run — Kitty reserves this modified drag for native selection and it requires attended GUI confirmation |

This table is a residual record, not a live pass. An operator completing the
release drill should replace each `not run` entry with the Kitty version,
isolated socket/config roots, disposable tab/window IDs, and observed result.

### Session actions across surfaces

A session action is offered from three places — the dashboard's per-row menu
(`a`), the command palette (`:`), and the F2 overview's own menu (`a`) — and what
makes that safe is that all three read one declaration, `tui/sessionactions`.
The surfaces are allowed to word an action differently; they are not allowed to
disagree about whether it exists, whether the captured row may run it, whether it
confirms, or what its outcome was.

This matrix is the record that each cell is actually covered. The **Declaration**
column is the rule itself, stated once and tested where it lives; the three
surface columns are the proof that the surface obeys it. A cell reading *(same)*
means the named test covers both surfaces in one assertion, which is stronger
than two tests that could drift apart. Package prefixes are `sessionactions` for
`tui/sessionactions`, `tui` for the root, and `overview` for `tui/overview`.

| Property | Declaration | Dashboard menu `a` | Command palette `:` | F2 overview `a` |
| --- | --- | --- | --- | --- |
| Every declared action is offered | `TestDefinitionsInventory`, `TestMenuDefinitionsAreTheRowActionsInMenuOrder`, `TestMenuMembershipIsOneFact` | `tui.TestOverlayOffersEveryMenuDefinition` | `tui.TestPaletteOffersEverySessionDefinition` | `overview.TestOverviewMenuOffersEveryDefinition`, `overview.TestEveryMenuActionReachesItsEffectInBothModes` |
| Availability is one rule, not three | `TestAvailabilityMatrix`, `TestAvailabilityWithoutATarget`, `TestZeroDefinitionIsPermissive` | `tui.TestBothSurfacesAgreeOnAvailability`, `tui.TestActionAvailability*` | *(same test)* | `overview.TestAnUnavailableEntryReportsItsReasonAndRunsNothing` |
| A disabled target refuses instead of acting | `TestUnavailableActionIsRefusedBeforeItStarts`, `TestActionWithNoEffectIsRefused` | `tui.TestActionsMenuEnterRefusesAnUnavailableEntry`, `tui.TestActionsOverlayPeekUnavailableNoDispatch`, `tui.TestOpenDirActionUnavailableWithoutMatchingConfig` | `tui.TestCommandRunRejectsDisabledAndHiddenCommands` | `overview.TestAnUnavailableEntryReportsItsReasonAndRunsNothing`, `overview.TestAcceptanceEveryShippedTerminalDrawsEveryOverlayIntact` |
| `confirm_destructive` gates the same three actions | `TestBeginConfirmsDestructiveAction`, `TestUnconfirmedDestructiveActionDispatchesImmediately`, `TestDestructiveAndNeedsProfileAgree`, `TestSecondConfirmIsNoOp` | `tui.TestBothSurfacesShareConfirmationPolicy`, `tui.TestRestartConfirmsUnderConfirmDestructive`, `tui.TestConfirmedCloseRunsTheSameEffectAsAnUnconfirmedOne` | *(same test)* | `overview.TestAcceptanceWithConfirmationOffACloseRunsAtOnceAndStillOffersTheUndo` |
| The prompt wraps its consequence rather than truncating it | `TestRenderPromptWrapsTheConsequence`, `TestRenderPromptNeverExceedsTheContentWidth`, `TestRenderPromptStatesVerbAndSubject`, `TestPromptBoxWidthLeavesTwoCellsOnEitherSide`, `TestPromptForCarriesTheDecidedSentences` | `tui.TestHostTabConfirmationSaysTheDashboardClosesWithIt` | *(same declaration)* | `overview.TestAcceptanceClosingTheHostingTabWarnsAndOffersNoUndo`, `overview.TestAcceptanceEveryShippedTerminalDrawsEveryOverlayIntact` |
| Profile replacement picks first, then confirms | `TestProfileReplacementPicksThenConfirms`, `TestChoosingCurrentProfileIsNoOp`, `TestLaunchAnotherNeedsProfileButNeverConfirms` | `tui.TestProfileReplacementConfirmsAfterTheChoice`, `tui.TestChoosingTheCurrentProfileStaysANoOp`, `tui.TestActionsOverlayDispatchConfigPicker` | `tui.TestPaletteRestartMatchesActionsOverlay` | `overview.TestAcceptanceAProfileReplacementRestartsUnderTheChosenProfile`, `overview.TestChoosingTheProfileARowAlreadyRunsIsANoOp` |
| Cancelling returns to where the action was chosen | `TestCancelRestoresOrigin`, `TestCancelNestedProfileThenConfirmRestoresOnce`, `TestCancelWithNothingPendingIsNoOp` | `tui.TestCancellingReturnsToTheActionsOverlay`, `tui.TestCancellingTheProfilePickerReturnsToItsOrigin` | `tui.TestCancellingReturnsToThePaletteQuery`, `tui.TestCancellingRestoresThePaletteSelectionByIdentity` | `overview.TestTheSelectionAndPageSurviveMenuVisitsAndCancellation` |
| The target is captured at selection and never retargeted | `TestCaptureIsASnapshot`, `TestCaptureRecordsTabAndWindowIdentity`, `TestStaleTargetsAreRefused`, `TestStaleTargetIsRefusedAtTheProfilePickerToo` | `tui.TestStaleTargetIsRefusedRatherThanRetargeted` | `tui.TestCommandExecutionUsesCapturedTarget` | `overview.TestALiveRefreshCannotRedirectTheCapturedTarget`, `overview.TestACapturedTargetSurvivesReorderingIsStillActedOn` |
| A lock taken or dropped after capture is decided by both readings | `TestALockTakenAfterCaptureIsCaughtAtTheLiveRow`, `TestALockTakenUnderAConfirmationIsCaughtBeforeDispatch`, `TestALockReleasedAfterCaptureStillRefuses` | *(same declaration)* | *(same declaration)* | *(same declaration)* |
| A failure is reported as a failure, distinct from a refusal | `TestNonSuccessOutcomesAreDistinct`, `TestResultCarriesActionRequestAndTarget`, `TestCompleteCorrelatesWithItsDispatch`, `TestCompleteIgnoresForeignCompletions` | `tui.TestActionDoneCorrelatesWithItsDispatch`, `tui.TestUnrelatedConfirmationsStillReachTheirOwnHandlers` | *(same test)* | `overview.TestCompletionRoutingMapsEachOutcomeToItsDestination`, `overview.TestAcceptanceARefusedRestartSaysWhyAndChangesNothing` |
| Close recovery is offered only after a confirmed close, for thirty seconds | `TestTheWindowStartsAtConfirmationRatherThanDispatch`, `TestUnconfirmedAndFailedClosesLeaveNothingBehind`, `TestAClaimedEntryIsNotOfferedTwice`, `TestExpiredEntriesAreNeitherOfferedNorClaimable`, `TestRepeatedClosesKeepIndividualDeadlinesAndUndoNewestFirst` | `tui.TestCloseIsRecoverableWithConfirmationEnabled`, `tui.TestUnconfirmedCloseOffersNoRecovery`, `tui.TestFailedCloseOffersNoRecovery`, `tui.TestUndoLastConsumesNewestEntry`, `tui.TestFailedRelaunchIsRetryableUntilTheOriginalDeadline` | `tui.TestUndoCloseRunsFromTheCommandPalette`, `tui.TestUndoKeyAndPaletteAgree` | `overview.TestOnlyAConfirmedCloseIsRecoverable`, `overview.TestAConfirmedCloseIsOfferedAnUndoAndRelaunches`, `overview.TestAFailedRelaunchIsOfferedAgainForWhatWasLeftOfItsWindow`, `overview.TestAnExpiredOfferIsWithdrawn`, `overview.TestAcceptanceAClosedSessionLeavesTheTreeAndIsRestoredByUndo` |
| The hosting tab is named in the prompt and is never recoverable | `TestHostHostsOnlyItsOwnTab`, `TestAnUnreconstructableSnapshotIsNeverRemembered` | `tui.TestHostTabConfirmationSaysTheDashboardClosesWithIt`, `tui.TestHostTabIsTrackedIndependentlyOfSelection`, `tui.TestClosingTheHostingTabOffersNoRecovery` | *(same host rule)* | `overview.TestADestructiveActionAimedAtTheHostingTabExplainsItself`, `overview.TestClosingTheHostingTabIsNeverRecoverable`, `overview.TestAcceptanceClosingTheHostingTabWarnsAndOffersNoUndo` |
| Opening and browsing a menu changes nothing | `TestDefinitionsAreIndependentValues`, `TestInFlightIsCopied`, `TestMutationsDoNotWriteThroughACopiedRecovery` | `tui.TestActionsMenuOwnsTheKeyboard`, `tui.TestActionsOverlayEscReturnsToList`, `tui.TestActionsOverlayQReturnsToList` | `tui.TestCommandCatalogAvailability`, `tui.TestCommandCatalogConditionalVisibility` | `overview.TestBrowsingTheMenuMutatesNothing`, `overview.TestAcceptanceBrowsingTheMenusWritesNothing`, `overview.TestAnOverlaySwallowsTheGridsOwnKeys` |
| A fresh menu opens where `Enter` on the row already went | `TestInitialSelectionOpensTheMenuOnOpenSession`, `TestMenuIndexOf` | `tui.TestActionsMenuEnterOnInitialSelectionFocusesTheSession`, `tui.TestActionsMenuEnterRunsTheSelectedEntry` | n/a — the palette has no per-row cursor | `overview.TestAFreshMenuOpensOnTheEntryEnterAlreadyRan` |
| Mnemonics stay distinct from the navigation keys | `TestMenuDefinitionsAreBanded`, `TestMenuGroupString` | `tui.TestMenuMnemonicsMatchDashboardKeyConstants`, `tui.TestMenuNavigationKeysShadowNoMnemonic` | `tui.TestCommandCatalogDashboardShortcutBindings` | `overview.TestAnOverlaySwallowsTheGridsOwnKeys`, `overview.TestCtrlCLeavesTheProgramFromEveryOverlay` |
| Every terminal size draws every overlay intact | — | the `actions-menu-*`, `actions-popup-fit`, `config-picker-fit`, and `combined-actions-during-refresh` scenarios in `docs/tui-testing/scenarios/` | the `command-palette-*` scenarios | `overview.TestAcceptanceEveryShippedTerminalDrawsEveryOverlayIntact` (four shipped sizes × 8–20 sessions × CJK/emoji/long identities, normal and compact) |

The changed-lock row reads *(same declaration)* across all three surfaces for a
reason worth stating rather than hiding behind a repeated cell: none of them
decides it. A lock is the one guard an operator applies from somewhere else while
a menu or a confirmation is open here, and `Controller.validate` asks both
readings — the captured snapshot and the live row — on every entry point, so a
surface has nowhere to put a rule of its own. `SameSession` deliberately ignores
the lock so that a lock change is refused as a lock rather than misreported as a
replaced session, and the two directions are asymmetric on purpose: a lock taken
after capture is caught live, and a lock released after capture still refuses,
because failing closed there costs one keypress and failing open restarts a
session someone locked.

Two gaps in that matrix are deliberate and are not owed a cell. The palette has
no per-row cursor, so "where a fresh menu opens" is not a claim it can make. And
the dashboard's `tui-test` scenario driver stays pointed at the dashboard: the
overview is a separate program with its own `Update`/`View` loop, and its
equivalent evidence is the package-level acceptance suite named above rather than
a JSON scenario.

The overlay compositor underneath all of them has its own regression:
`layout.TestComposeOverlayKeepsTheWidthWhenACutLandsInsideAWideGlyph`. A centred
modal lands wherever the content underneath happens to be, so both of its
vertical edges can fall inside a double-width glyph; `ansi.Cut` cannot halve one,
so an uncorrected splice returned a row one cell too wide or one too narrow and
pushed every column to its right out of place for the rest of the frame. That is
exactly a grid of CJK-titled sessions with any modal over it, which is how the
four-size sweep above found it.

### Session actions in a live Kitty

The matrix above is entirely hermetic: every one of those tests substitutes the
Kitty client, so none of them proves that a real pane is focused, that a real
editor is launched, or that a relaunch that really fails is reported as a failure
rather than swallowed. That half is operator-attended and runs in a **disposable
Kitty instance**, for the same reason the overview smoke does — the surfaces act
on every managed session in the instance they run inside, so a smoke against the
operator's instance would mutate the operator's work. The four isolation roots
are the ones described under *Live session overview* below, and the standing
constraint is that automated verification must not replace the installed binary,
restart the working daemon, or mutate live sessions.

Two properties of the harness are worth stating because getting either wrong
silently invalidates the run. `kitten @ send-key` normalizes a bare uppercase
letter to its lowercase spelling, so a mnemonic that differs only by case (`o`
open shell versus `O` open editor) must be delivered with `send-text`;
`send-key` stays for named keys. And `NO_COLOR` inherited from the driving shell
reaches the program inside the isolated instance and makes every frame colorless,
which reads exactly like a styling regression — the launcher unsets it, and a
capture with zero color sequences means the harness leaked, not that the footer
stopped distinguishing available keys from unavailable ones.

Release-session record, 2026-09-17 — kitty 0.48.2, `amx` 0.8.0 (revision
`35c02a070db4-dirty`, sha256 `7a76ab64eac2`), isolated roots
`HOME=/tmp/amx-live-…/home`, `AMX_CONFIG=/tmp/amx-live-…/cfg/config.toml`,
`PATH=/tmp/amx-live-…/bin:$PATH`, socket `unix:/tmp/amx-live-…/kitty.sock`,
`--instance-group amxlive`; twenty disposable managed tabs across two profiles
(`live-a` claude, `live-b` codex) with CJK, Hangul, emoji, and long-ASCII
identities, plus a stub editor on `PATH`:

| Live check | Observed |
| --- | --- |
| Focus | a digit and `Enter` each focused the named tab and left the overlay gone in the same listing; the twenty tabs were otherwise untouched |
| Open shell | `o` opened a new tab whose working directory was the session's; the session's own tab was unchanged |
| Open editor | `O` (delivered as text) launched the configured `editor` in that directory. The unset-`editor` fallback chain (`$VISUAL`, then `$EDITOR`, then `vi`) was not exercised live; it is covered in `internal/session`'s own tests |
| Disabled target | on a locked session the menu's restart entry rendered its reason and ran nothing, and `Enter` on it refused rather than acting |
| Profile replacement | `c` opened the picker on the session's **current** profile; choosing the other one raised the confirmation, whose consequence sentence wrapped over three lines at 80 columns rather than truncating; `y` ran the replacement through to a relaunch under the chosen profile |
| Close and undo | a confirmed close removed the tab and offered `u`; `u` within the five-second window relaunched it. The same `u` issued after the window had elapsed was correctly inert — the expiry is the contract, not a miss |
| Failed restart | renaming a profile in the config so a live window's `amx_profile` no longer resolved produced `restart failed for svc03: profile "live-b" not found in config`; the session stayed live and was not closed. Deleting a session's directory is *not* a usable failure injection — Kitty falls back and the relaunch succeeds |
| Hosting tab | closing and restarting the tab the surface itself ran in each carried the extra consequence sentence naming that the surface goes with it, and the close offered no undo |
| Chrome | the help overlay, the compact toggle, and the empty grid all drew intact against the same corpus |

The four-size ANSI and geometry sweep was run against that same twenty-session
corpus — grid, help overlay, actions menu, a menu with an unavailable entry, the
profile picker, and the confirmation, at each supported size — by capturing
`kitten @ get-text --extent=screen --ansi` and normalizing it: strip OSC, CSI,
and other escapes, then measure east-asian display width per row.

| Size | Frames | Rows over the column budget | Widest row | Leftover escapes | Malformed CSI | Color sequences |
| --- | --- | --- | --- | --- | --- | --- |
| 80x24 | 6 | none | 80 | 0 | none | 66–154 |
| 100x24 | 6 | none | 100 | 0 | none | 146–174 |
| 120x30 | 6 | none | 120 | 0 | none | 163–219 |
| 160x45 | 6 | none | 160 | 0 | none | 248–290 |

Widest-row-equals-the-budget at every size is the positive half of that result:
a frame that merely did not overflow could have been silently narrow. Note that
`get-text --extent=screen` trims trailing blank rows, so a captured frame is
routinely one or more lines shorter than the terminal height; that is a property
of the capture and not of the render.

Teardown removed only test resources: the isolated daemon was stopped through its
own `HOME`, the Kitty instance was killed by PID, and the temporary root was
removed. The operator's daemon reported the same running build before and after
(`0.8.0`, revision `8f0f79ce912d-dirty`, sha256 `96f99d959d0e`).

### Live session overview

The hermetic suite in `tui/overview` drives the program's own `Update`/`View`
loop, but every seam it reaches the world through is a test double: no test in
this repository proves that Kitty's `F2` resolves to the overview, that a real
pane's text arrives in a tile, or that `Enter` moves a real operator's focus.
That half is operator-attended, and it is run in a **disposable Kitty instance**
rather than the operator's own — the surface lists every managed session in the
instance it runs inside, so a smoke against a live instance would both read the
operator's work and be read by it.

The isolation is four separate roots, and all four matter. A private `HOME`
is what isolates the daemon, because `ipc.SocketPath()` is derived from it;
`AMX_CONFIG` points at a temporary `config.toml`; `PATH` puts the test build
first so `amx` inside Kitty is the artifact under test rather than the installed
one; and the instance gets its own `listen_on` socket and `--instance-group` so
`kitten @` cannot reach the operator's windows. Copy the tracked
`agent-mux.conf` beside the temporary `kitty.conf` so its `include` resolves —
symlinking the installed one would test the installed snippet instead.

Managed tabs do not require real harnesses. Launch each with
`kitten @ launch --type=tab --title svcNN sh -c "printf …; exec cat"` and stamp
it with `kitten @ set-user-vars amx_profile=… amx_harness=… amx_project=…
amx_directory=… amx_launch_id=…`. The non-empty `amx_launch_id` is the load-
bearing one: `internal/session/query_fallback.go` requires a live harness
process only when the launch ID is absent, so without it the tabs are visible to
Kitty and invisible to the overview. The `exec cat` is what makes the live-output
check possible — `kitten @ send-text` then appends to the pane on demand.

Two checks are worth driving through Kitty's own machinery rather than by
running `amx overview` directly. Dump the parsed keymap with Kitty's own parser
(`kitty +runpy` over `load_config`) to prove the binding resolves —
`kitty --debug-config` does not exist in 0.48.2 — and then invoke the exact
definition it reports with `kitten @ action …`, so the key-to-action and
action-to-behavior halves are each proven rather than assumed.

Release-session record, 2026-09-11 — kitty 0.48.2, `amx` 0.8.0 (revision
`9d0c6e7ba3fd-dirty`, sha256 `6f35c2c0e039`), seven managed tabs `svc01`–`svc07`
across three profiles and three harnesses, OS window 170x48 cells:

| Live check | Observed |
| --- | --- |
| `F2` resolves to the overview | `SingleKey(key=57365)` → `launch --type=overlay --allow-remote-control amx overview --window-id @active-kitty-window-id`; `SingleKey(key=57364)` → `amx keybindings` |
| That action opens the grid | overlay window on the invoking tab, header `Live sessions  7`, all seven tiles drawn |
| Source selection | invoked from `svc05`'s tab, `svc05` carried the heavy border; reinvoked from `svc04`'s tab, `svc04` did |
| Live pane output | `send-text` into `svc01` and `svc05` appeared in both tiles within one `refresh_ms` (400), selected and unselected alike |
| Repeated `F2` is a singleton | window set unchanged (no second surface), focus moved from the unrelated tab to the existing overlay, and the selection it already held was not disturbed |
| Resizing | 170x48 → 110x30 re-laid the grid from 3x3 to 2x2, `page 2/2` appeared, and the selection survived — the page followed `svc05` rather than the selection following the page |
| Pagination | `PgUp` → `page 1/2` selecting `svc01`; `Home`/`End` jumped to the first and last tiles across pages; `h`/`j`/`k`/`l` and `Tab` moved within the page as documented |
| `Enter` focus | selected `svc04`, `Enter` focused tab 5 and the overlay was gone in the same listing |
| `q` dismissal | overlay closed, focus left where it was, all seven managed tabs untouched |
| `F1` reference | its bound action opened the reference, which lists `f2  open or focus the live session overview`, and closed on `q` |
| Ordinary dashboard unchanged | opened in the same instance, listed all seven sessions, and its inspector still showed the full six-fact block the overview's three-line header is a subset of |

Teardown removed only test resources: the isolated daemon was stopped through
its own `HOME`, the Kitty instance was killed by PID, and the temporary root was
removed. The operator's daemon reported the same running build before and after.

### Menu bar companion

The native app in `macos/` has its own hosted XCTest suite, `just mac-test`,
which runs against fake daemon sockets and a fake `amx` and needs Xcode. It is
deliberately outside `just check`, `just acceptance`, and the paired release
gate (`TestMacAppRecipesStayOutOfTheGates`), so a companion change is accepted
by `just check` for the daemon half, `just mac-test` for the app half, and the
manual checklist below for everything only a real Kitty, a real Notification
Center, and a real window server can show. See
[Menu bar companion](companion-app.md).

Releasing the app has a gate of its own for the same reason:
`just mac-release-gate` builds the Release bundle, checks it, runs the hosted
suite against the Debug and the Release app, and writes its own evidence
record, separate from the pair's. Where there is no Xcode to build with it
reports `pending` and fails, never a pass. See
[Desktop releases](desktop-releases.md).

Daemon-side evidence, after `just install`:

```bash
just crash-restart             # includes a subscriber killed mid-write
just install-restart-smoke
amx doctor --strict            # companion_app present, no companion_build_mismatch
amx debug subscribe --frames 5 # ack, then snapshot and heartbeat frames
```

Then `just mac-install` and, in a real Kitty with at least two managed
sessions, check each item and record the app version, `amx --version`, and the
macOS and Kitty versions beside the result:

- [ ] **Counts.** The status item's glyph counts track live sessions: a
      session that stops, asks for permission, or errors moves into Needs
      attention within a few seconds, and leaves it once handled.
- [ ] **Row click.** Clicking a row focuses that session's tab and window and
      brings Kitty forward, from another app and from another Space.
- [ ] **Dashboard shortcut.** ⌃⌥⌘A from another app brings the running
      dashboard forward, or opens one when none runs.
- [ ] **Attention shortcut.** ⌃⌥⌘N cycles through every waiting session,
      in the workspace's sidebar order, and beeps when nothing waits.
- [ ] **Actions.** Open Dashboard, Launch Project, Recent (including a
      conversation already live, which focuses instead of resuming twice), ⌥
      Mute and Unmute on a row, every Notifications snooze and Resume Now,
      Start daemon (with the daemon stopped), and Preferences each do what
      `macos/README.md` says.
- [ ] **Native notification.** A session notification arrives as a native
      banner, `amx notifications test` reports it confirmed via the app, and
      clicking the banner focuses the pane. Mute Session and Snooze All for
      1 Hour on the banner work, for a tmux session as for a Kitty window.
- [ ] **Fallback.** With the app quit, the next notification goes through
      Alerter, and `amx debug tail` names the `alerter` backend.
- [ ] **ASL question.** An ASL run waiting on its supervisor shows ❓ and an
      Answer… item; a reply sent from the panel is accepted, and ❓ clears.
- [ ] **Daemon restart.** `amx daemon restart` shows `reconnecting` briefly
      and returns to `ready` without relaunching the app; a snooze set before
      the restart is restored.
- [ ] **Sleep and wake.** After sleep, the feed reconnects at once and the
      counts are current.
- [ ] **Two Kitty instances.** Sessions from both instances are listed; a row
      click and ⌃⌥⌘N focus the right instance and bring that one forward; Mute
      acts only in the row's own instance.
- [ ] **Older daemon.** Against a daemon built before the companion protocol,
      the menu shows "daemon too old — run `amx daemon restart`", polls
      health, and greys out daemon actions instead of failing.
- [ ] **Missing `listen_on`.** In a Kitty without `listen_on`, the menu
      explains the `kitty.conf` lines it needs instead of acting.

Known limits are documented rather than tested: macOS may decline to switch
Spaces for a cooperative activation (see the companion guide's
troubleshooting), and a mute reaches the daemon's notification gate within
one 2s render cadence ([Daemon › Mute lag](daemon.md#mute-lag)).

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
