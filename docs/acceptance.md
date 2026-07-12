# Product acceptance

The project-workbench acceptance gate is:

```bash
just acceptance
```

It runs `go test ./...`, race-sensitive datastore, workspace, daemon, and TUI
datastore-lifecycle suites, `go vet ./...`, and every committed TUI scenario.
`just check` adds `golangci-lint` and is the final repository gate. Every test
in the persistence acceptance matrix uses a temporary file-backed datastore
and isolated compatibility paths.

## Workflow coverage

| Workflow | Automated coverage |
| --- | --- |
| Focus an exact live conversation | `internal/actions.TestContinueFocusesOnlyAnExactLiveConversation`; `cmd.TestSessionLaunchAndFocusCLIUseWorkspaceAndLiveTabSelectors` |
| Resume a closed conversation | `internal/actions.TestContinueLaunchesNativeResumeForEachSupportedHarness`; `cmd.TestSessionContinueCLIUsesExactNativeInvocation` |
| Native Claude/Codex fork | `internal/actions.TestForkUsesNativeSupportAndPreservesSource` |
| Portable Copilot fork | `internal/actions.TestForkUsesPortableHandoffForCopilot`; `cmd.TestSessionForkCLIUsesPortableCopilotHandoff` |
| Isolated worktree fork | `internal/actions.TestForkWorktreeLaunchesNativeForkInManagedContext`; `internal/worktree.TestCreateTracksIsolatedWorktreeAndRemoveUnusedCleansIt` |
| Cross-harness handoff | `internal/handoff.TestWorkflowLaunchesEverySourceTargetHarnessCombination`; `cmd.TestSessionHandoffCLICreateOnlyAndPartialJSON` |
| Explicit plan linking | `cmd.TestPlanLinkAndUnlinkUseExplicitSelectorsAndSchemaV1`; ambiguity is fail-closed in `cmd.TestPlanLinkAmbiguityReturnsCandidatesAndNeverGuesses` |
| Activity persistence and restart dedupe | `internal/daemon.TestMeaningfulActivityPersistsAfterReducerAndExcludesHookNoise`; `internal/daemon.TestMeaningfulActivityDedupesAcrossDaemonRestartByRequestID` |
| Discovery and safe adoption | `internal/workspace` discovery tests; `internal/config` concurrent-change/rollback tests; `cmd.TestWorkspaceAdoptPreviewsExactBlockThenRequiresExplicitYes` |
| Legacy migration and database fallback | `internal/transcript.TestEnableSQLiteIndexFallbackRetainsLegacyReadPathAndReason`; `internal/plans.TestEnableSQLiteIndexFallbackRetainsLegacyPlans`; datastore migration rollback/future-version tests |
| Safe derived rebuild | `cmd.TestIndexRebuildPreservesDurableStateAndRefreshesDerivedData`; `internal/plans.TestSQLiteRefreshPreservesReferencedMissingPlanAndExplicitLink` |
| Narrow and wide TUI layouts | all JSON scenarios under `docs/tui-testing/scenarios`, including workspace split/drill-down, palette, activity, multi-profile, and worktree layouts |

## Release-candidate persistence coverage

| Acceptance case | Automated evidence |
| --- | --- |
| Concurrent fresh daemon, TUI, and CLI startup | `tui.TestAcceptanceConcurrentFreshStartupAcrossDaemonTUIAndCLI` opens three independent process-role pools against one fresh file while the daemon persists activity and the TUI publishes its complete service snapshot. `internal/datastore.TestConcurrentOpenPathInitializesAndUpgradesIndependentPools` expands this across every supported prior schema. |
| Long transcript refresh retains the dashboard snapshot | `tui.TestAcceptanceLongTranscriptRefreshRetainsLastGoodDashboardSnapshot` blocks a real SQLite-backed transcript discovery pass and proves warmed transcript rows plus the last workspace snapshot remain published until completion. |
| Warm startup performs no sidecar-body reads | `internal/transcript.TestImportLegacyIndexEquivalentAndIdempotent` and `internal/plans.TestImportLegacyIndexIsIdempotentAndReadOnly` instrument the compatibility readers and write transactions; the second unchanged sync performs neither. |
| Periodic workspace refresh performs zero datastore writes | `tui.TestAcceptancePeriodicWorkspaceProjectionPerformsZeroDatastoreWrites` executes three real projections and observes SQLite `data_version` from an independent connection. `internal/workspace.TestProjectSnapshotRemainsReadOnlyBesideUnrelatedWriter` also proves projection remains available beside a writer. |
| Multi-profile and multi-harness project search | `internal/workspace.TestBuildGroupsProfilesAndPersistsGeneratedWorkspaceID` synchronizes launch variants then filters the unified project result by a non-default harness/profile while retaining scalar defaults. |
| Unavailable placeholders and explicit repair | `internal/datastore.TestInspectClassifiesRepairableAndUnavailableMissingDocumentsReadOnly` classifies missing sources without mutation; `cmd.TestIndexRebuildPreservesDurableStateAndRefreshesDerivedData` explicitly repairs reconstructable documents, reports unavailable handoffs, verifies FTS, and preserves protected state. |

All command JSON tests use schema version 1. Selector tests require exact IDs,
paths, or unique names; ambiguous matches return ordered candidates and never
select one implicitly. New index health and rebuild fields are additive inside
the schema-v1 data envelope; existing scalar search `harness` and `profile`
fields remain unchanged while project membership arrays are additive.

## Compatibility-release rollback policy

`transcripts.json`, `plans.json`, and their search sidecars remain read-only
rollback artifacts. Normal SQLite import, status, and rebuild operations never
rewrite or delete them. Incremental import records per-entry metadata and
sidecar fingerprints in SQLite, reads sidecar bodies only for changed entries,
and retains durable rows when rollback entries disappear. Removal is deferred
until:

1. every supported pre-SQLite release can upgrade directly;
2. field experience shows database fallback is no longer needed;
3. a documented database backup/export and restore workflow exists; and
4. one compatibility release has elapsed after those conditions are met.

The SQLite database, handoff artifacts, and Agent Mux data directory must retain
private `0600`/`0700` permissions. `amx doctor --strict` and `amx index status`
enforce this operationally.
