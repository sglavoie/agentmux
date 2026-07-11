# Product acceptance

The project-workbench acceptance gate is:

```bash
just acceptance
```

It runs `go test ./...`, race-sensitive datastore and daemon suites, `go vet
./...`, and every committed TUI scenario. `just check` adds `golangci-lint` and
is the final repository gate.

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

All command JSON tests use schema version 1. Selector tests require exact IDs,
paths, or unique names; ambiguous matches return ordered candidates and never
select one implicitly.

## Compatibility-release rollback policy

`transcripts.json`, `plans.json`, and their search sidecars remain read-only
rollback artifacts. Normal SQLite import, status, and rebuild operations never
rewrite or delete them. Removal is deferred until:

1. every supported pre-SQLite release can upgrade directly;
2. field experience shows database fallback is no longer needed;
3. a documented database backup/export and restore workflow exists; and
4. one compatibility release has elapsed after those conditions are met.

The SQLite database, handoff artifacts, and Agent Mux data directory must retain
private `0600`/`0700` permissions. `amx doctor --strict` and `amx index status`
enforce this operationally.
