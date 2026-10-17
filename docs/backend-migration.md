# Backend migration baseline

This document is the Session 1 contract for the desktop migration: moving
AgentMux from a Kitty-only terminal integration to backend-neutral runtime
sessions served by either Kitty or a private tmux server, with a native
workspace built on embedded Ghostty. It records three things later sessions
must not lose:

1. where AgentMux depends on Kitty today, and which half of each dependency
   belongs to a terminal backend versus AgentMux itself;
2. the workflow acceptance matrix: every existing user workflow, the evidence
   that covers it on Kitty today, the session that makes it backend-neutral,
   and the check that proves it;
3. the protocol and persistence compatibility requirements old clients,
   persisted state, and the paired ASL release depend on.

`TestBackendMigrationMatrixAssignsEveryWorkflow` (repository root) keeps the
matrix honest: every row names an owning session and an acceptance check, every
dependency area appears, and baseline status is only ever `pass` or `pending`.
It does not prove the checks exist yet; later sessions add them.

## Ownership rule

| Owner | Owns | Must not own |
| --- | --- | --- |
| AgentMux daemon | Projects and profile isolation, conversation identity `(harness, session_id)`, runtime-session identity and registry, hook reduction, attention and unseen bits, notifications, ASL association and leases, persisted reducer checkpoints, diagnostics vocabulary. | Terminal topology formats, match expressions, socket addresses, pane or window integer IDs as identity. |
| Terminal backend (Kitty adapter, tmux backend) | Discovery of its own instances, launch into a terminal target, termination, screen capture, terminal metadata (titles, colors, user vars where supported), focus and presentation operations it supports, exit observation. | Deciding what an observation means for a session, attention, or a conversation. |
| App (TUI dashboard, overview, native workspace, menu bar companion) | Presentation, navigation, attachment views, utility views. | Reducer state, backend commands other than through the daemon or action seams. |

An operation a backend cannot perform is a structured capability error, never a
silent no-op or a fabricated identifier.

## Kitty dependency inventory

Inventory taken at revision `5645f028`. Non-test Go files importing
`internal/kitty`: `internal/daemon` (20 files) and its children `activity`,
`probes`, `notification`, `protocol`, `companion`, `rendering`, `events`;
`internal/session` (14); `internal/actions` (4); `internal/handoff`,
`internal/indicator`, `internal/cli`, `internal/debugger`; `tui` (10),
`tui/commands` (5), `tui/overview` (3), `tui/customization` (2); and the
command packages `cmd/session`, `cmd/dashboard`, `cmd/debug`, `cmd/daemon`,
`cmd/project`, `cmd/worktree`. Kitty environment variables (`KITTY_WINDOW_ID`,
`KITTY_LISTEN_ON`, `KITTY_PID`) are read in `internal/indicator`,
`internal/session/env.go`, `internal/kitty`, `internal/cli/kitty.go`,
`cmd/hooks`, `cmd/session`, `cmd/dashboard`, `cmd/daemon`, `cmd/project`, and
`tui/commands/registry.go`. `tui/customization` additionally depends on
`internal/kittykeys`.

| Area | Kitty dependency today | Backend-owned after migration | AgentMux-owned after migration | Session |
| --- | --- | --- | --- | --- |
| launch | `internal/session/launch.go` lists Kitty windows for project/profile dedup, then `LaunchTab` with `amx_*`/`cc_*` user vars and `KITTY_LISTEN_ON` in the env; `LaunchFresh` bypasses dedup. Resume, fork, restart, profile replacement, re-anchor, close, focus, resort, and lock go through `internal/session` and `internal/actions` with Kitty match expressions. Handoff and worktree compensation (`internal/handoff/workflow.go`, `internal/actions/worktree.go`) run around a Kitty launch. | Create a terminal target with cwd, env, and argv; terminate; report launch failure. | Dedup policy, launch identity, runtime-session registration, harness invocation, worktree/handoff compensation, backend selection. | 3, 4, 7, 8 |
| hooks | `amx hooks indicator` resolves its window from `KITTY_WINDOW_ID` (`internal/indicator/indicator.go`) and forwards `KITTY_LISTEN_ON`; `protocol.Message.WindowID` and `KittyListenOn` carry them to the daemon. Ack stays prompt-free. | Nothing beyond exporting a runtime-session reference into the agent's environment at launch. | Runtime-session identification of a hook, legacy Kitty translation, routing, provenance. | 5 |
| state | Reducer state is `map[int]*state.WindowState` keyed by Kitty window ID; tombstones, subprocess ownership, subagent attribution, and ASL routing (`handler_asl_routing.go`, `kitty.FindTabTitleOwner`) assume one integer window. | Validated targets and process fingerprints for its own instances. | Runtime-session keyed reducer, ownership, attribution, stale-target rejection. | 3, 5 |
| persistence | Reducer checkpoints are written to the Kitty user var `amx_state` (schema v4 payload, `internal/daemon/state/persist.go`) and restored from user vars at startup (`state_restore.go`, `renderer.go`); tab titles and colors are the rendered state. | Optional metadata storage where the backend has it. | Backend-neutral versioned checkpoint files with atomic replacement; reading existing Kitty `amx_state`. | 6 |
| capture | `GetText` over `kitten @ get-text` feeds the Claude/Codex prompt probes, Copilot transcript probe, idle and context-limit probes, `activity/screentext.go`, `probes/freshness.go`, the overview tile scheduler (`tui/commands/capture.go`), and `debug capture`. | Bounded screen capture with dimensions and freshness. | Classification, freshness validation, scheduling, the rule that missing or stale capture is never success evidence. | 9 |
| tui | Dashboard refresh lists Kitty directly as fallback and consumes `amx_dashboard_select`; focus is `FocusTab`; overlays (dashboard singleton, pickers, keybindings, pager over `@screen_scrollback`) are `LaunchOverlay`; the overview reads Kitty and focuses; `tui/customization` reads Kitty key config. | Presentation operations it supports (focus, overlay, scrollback). | Selection, action availability, portable utility views, capability-driven fallbacks. | 11, 14 |
| companion | `CompanionSnapshot` v1 carries a `kitty` block (socket, sockets, ready) and per-session `window_id`, `tab_id`, `os_window_id`, `kitty_socket`; `focus_window` requests address `{kitty_socket, window_id}`. The Swift app decodes these in `macos/AgentMux/Daemon/CompanionModels.swift` and `WireModels.swift`. | Focus of its own targets. | Snapshot negotiation, runtime-session references, unchanged v1 for old clients. | 6, 13 |
| diagnostics | `amx doctor` and `debug status` report the Kitty topology coordinator (`kitty_snapshot_stale`), `daemon.kitty_snapshot` journal rows, `daemon export-events --current-window` resolves the window from `KITTY_WINDOW_ID`, `debug capture`/`probe` read Kitty text. | Availability and version reporting for its own dependency. Done in Session 18 for tmux: `amx backend status` is the report, and `doctor`, `debug status`, and bundles carry it with the daemon's `runtime` health (reconciliation, attachment, screen reads, tmux clients) and the `daemon.runtime_reconcile` journal rows. | Stable finding identifiers, journal schemas, redaction. | 18 |
| asl | ASL run association (`amx_asl_run_id` user var), supervisor question attention, action receipts, and notification targeting resolve to a Kitty window; attention clearing follows Kitty focus observation. | Nothing ASL-specific. | Run association to runtime sessions, lease and stale-token contracts, notification dedup, contract-set pairing. | 10 |

## Workflow acceptance matrix

`Baseline` is the state of the current Kitty evidence at Session 1: `pass`
means the named automated evidence passed in this session's `just check`;
`pending` means the evidence is live-only (real Kitty, installed binaries, real
ASL, Xcode) and was not run, because routine verification must not replace the
installed daily-driver binary, restart its daemon, or touch live sessions.
`Owner` is the session that makes the workflow backend-neutral and must add the
acceptance check; `Desktop` is the session that makes it reachable from the
native workspace (Session 14's completion gate is that every row is reachable).

| ID | Workflow | Area | Kitty baseline evidence | Baseline | Owner | Desktop | Acceptance check |
| --- | --- | --- | --- | --- | --- | --- | --- |
| W01 | Launch a configured project with project/profile dedup | launch | `internal/session` launch tests; `internal/kitty.TestRemoteControlBaseline` pins the `launch --type=tab` argv | pass | 8 | 14 | Launch on Kitty and on isolated tmux yields one agent per project/profile key; JSON and exit codes identical across backends. |
| W02 | Fresh launch bypassing dedup (new handoff, explicit new instance) | launch | `internal/session` `LaunchFresh` and `LaunchPolicyAlwaysNewInstance` tests | pass | 8 | 14 | Fresh launch on either backend creates a new runtime session even when the project already runs. |
| W03 | Focus a live conversation exactly | launch | `internal/actions` continuity tests, `cmd/session` focus and JSON contract tests | pass | 8 | 13 | Focus resolves the conversation to its runtime session; Kitty activates the tab, tmux attaches; ambiguous selectors still return candidates. |
| W04 | Resume a closed conversation | launch | `internal/actions` continuity tests, `cmd/session` JSON contract tests | pass | 8 | 14 | Resume through the selected backend creates a new runtime identity bound to the same `(harness, session_id)`. |
| W05 | Native and portable forks | launch | `internal/actions` fork tests | pass | 8 | 14 | Forks on both backends leave the source conversation and runtime session untouched. |
| W06 | Isolated worktree fork with launch compensation | launch | `internal/worktree`, `internal/actions`, `cmd/worktree` tests | pass | 8 | 14 | A backend launch failure removes only the worktree created by the request on both backends. |
| W07 | Cross-harness handoff | launch | `internal/handoff` workflow tests including partial launch failure | pass | 8 | 14 | Handoff artifacts survive a failed tmux or Kitty launch; no duplicate agent is created. |
| W08 | Restart and profile replacement | launch | `internal/session` restart and restart-with-account tests; overview profile-replacement scenario | pass | 8 | 14 | Restart and replacement launch the new agent before closing the old one on either backend and never leave two live agents for one key. |
| W09 | Close a session and undo from the overview | launch | `internal/session` close tests; `tui/overview` acceptance close/undo scenarios | pass | 8 | 14 | Close terminates the recorded backend target only; closing a viewer detaches without terminating. |
| W10 | Tab presentation: title, color, move, resort, lock, mute | tui | `internal/session` sort/resort/mute tests; `internal/daemon/rendering`; `TestRemoteControlBaseline` pins the argv | pass | 11 | 13 | Kitty keeps rendering tabs; tmux sessions expose presentation through the daemon snapshot and answer unsupported operations with capability errors. |
| W11 | Hook ingestion, prompt-free acknowledgement, and routing | hooks | `internal/indicator` tests; `cmd.TestAcceptanceDiagnosticPipeline` | pass | 5 | — | Hooks carrying a runtime-session reference and legacy `KITTY_WINDOW_ID` hooks both route correctly; ack performs no backend discovery. |
| W12 | Duplicate, delayed, reordered, and post-termination hooks; subprocess and subagent attribution | state | `internal/daemon/state` reducer, subagent, and process-exit tests; `cmd.TestAcceptanceHarnessProcessLifecyclePipeline` | pass | 5 | — | No hook affects another runtime session, including mixed Kitty and tmux instances and reused pane IDs. |
| W13 | Reducer checkpoint persistence and daemon restart restore | persistence | `internal/daemon` `state_restore` tests, `internal/daemon/state.TestUserVarV4RoundTripPersistsFactsProcessAndEffective`; `just crash-restart` | pass | 6 | 16 | Restart restores Kitty sessions from existing `amx_state` and tmux sessions from versioned files; freshly received events are never overwritten. |
| W14 | Discovery reconciliation, stale targets, and window-ID reuse | state | `internal/daemon/handler_watchdog_tombstone_test.go` and restore tests | pass | 3 | — | Registry rejects reused pane IDs and PIDs through backend-instance generation and process fingerprints. |
| W15 | Topology outage is survivable and legible | diagnostics | `internal/daemon/kitty_fault_acceptance_test.go`, `kitty_snapshot_journal_test.go`, `internal/kitty` list coordinator tests | pass | 7 | 18 | A wedged or vanished backend degrades rather than empties the session list, for Kitty and for a disappearing tmux server. |
| W16 | Screen-based detection: prompt, permission, busy, completion, failure | capture | `internal/screenstate` committed captures; daemon prompt and transcript probe tests | pass | 9 | — | Detection passes against both Kitty and tmux fixture sets; missing or stale capture is never completion evidence. |
| W17 | Context-limit detection, stop confirmation hold, idle probe | capture | `internal/contextlimit`; `internal/daemon/terminal_stop_hold_test.go`; `internal/daemon/screen_idle_reason_test.go` | pass | 9 | — | Hold and context-limit outcomes match across backends, including a tmux pane with no capture. |
| W18 | Dashboard live list, refresh, selection, and focus handoff | tui | `tui` model tests; `tui/commands/fetch_test.go`; every scenario under `docs/tui-testing/scenarios` | pass | 11 | 14 | Narrow and wide scenarios pass with Kitty, with tmux, and with no Kitty present. |
| W19 | Session-action menu from dashboard and overview | tui | `tui/sessionactions` tests; overview acceptance action scenarios | pass | 11 | 14 | Availability follows backend capabilities; unsupported actions explain themselves. |
| W20 | Live session overview grid | tui | `tui/overview` suite; `tui/commands/fetch_overview_test.go` read-only guard | pass | 11 | 14 | Overview previews tmux sessions through capture without acquiring input or resize ownership. |
| W21 | Dashboard singleton, overlay pickers, keybindings reference, scrollback pager | tui | `cmd/dashboard` overview and singleton tests; `agent_mux_conf_test.go` | pass | 11 | 14 | Without Kitty these open as portable utility views; with Kitty native overlays remain. |
| W22 | Past Sessions, Plans, and Worktrees views | tui | `internal/transcript`, `internal/plans`, `tui` tests and scenarios | pass | 11 | 14 | Views and their launch actions work without Kitty. |
| W23 | Live Kitty acceptance of dashboard, actions, and overview | tui | Operator records in [Product acceptance](acceptance.md) (`Session actions in a live Kitty`, `Live session overview`) | pending | 11 | 19 | Repeated against a real isolated Kitty and a real isolated tmux backend. |
| W24 | Companion snapshot v1 and `focus_window` | companion | `internal/daemon/protocol` `TestCompanionSnapshot_Golden` and tolerance tests; companion hub tests | pass | 6 | 13 | Old clients receive the unchanged v1 shape with Kitty data only; negotiated clients receive runtime-session references; no Kitty IDs are fabricated for tmux. |
| W25 | Menu bar companion: attention cycling, notifications, ASL answer panel | companion | `just mac-test` (`macos/AgentMuxTests`); manual checklist in [Product acceptance](acceptance.md#menu-bar-companion) | pending | 13 | 13 | Native tests pass with mixed-backend snapshot fixtures; tolerant decoding of both snapshot versions. |
| W26 | Desktop notification targeting and attention clearing on focus | asl | `internal/daemon/notification` tests; daemon unseen and focus-refresh tests | pass | 10 | 13 | Notifications deliver once while no viewer is connected; attention clears on the backend-appropriate focus observation. |
| W27 | ASL run association, leases, supervisor questions, and action receipts | asl | `internal/agentloop`, `internal/aslbridge`, `internal/aslcontract`, daemon ASL handler tests | pass | 10 | 14 | Isolated ASL integration covers tmux and Kitty targets including app absence and daemon restart, without misrouted actions. |
| W28 | Paired ASL installation and overseer integration | asl | `just asl-integration`, `just overseer-integration` | pending | 10 | 19 | Both recipes pass against the installed pair with tmux-backed sessions. |
| W29 | Doctor, debug status, and stable finding identifiers | diagnostics | `cmd/debug` doctor tests; `cmd/debug.TestDoctorJSONCommandSurfaceIsVersioned` | pass | 18 | — | Existing identifiers unchanged; backend availability, reconciliation, capture freshness, and viewer attachment reported. |
| W30 | Event export, debug capture, probe, bundle, and replay | diagnostics | `cmd/daemon/export_events_shape_test.go`; `internal/debugger` bundle and replay tests | pass | 18 | — | `--current-window` style selection works by runtime session; bundles redact prompts and credentials by default. |
| W31 | Install, restart smoke, and real Kitty cockpit smoke | diagnostics | `just install-restart-smoke`, `just cockpit-smoke` (the Kitty smoke is `just kitty-smoke` since Session 21) | pending | 17 | 19 | Desktop release installs on a clean Mac; updates preserve detached agents. |
| W32 | Paired release gate and evidence record | diagnostics | `internal/release` tests; `just paired-release-gate` | pending | 19 | 19 | Core gate unchanged and Xcode-free; a separate desktop gate records its own evidence. |

## Session 19 acceptance record

One row per workflow of the matrix above, against its acceptance check as
worded. `passed` means the cited tests exist and passed in this session's
gates, and the cited logs record what was observed. `pending` means part of
the check has no evidence yet, and the row says which part: a missing check is
never counted as a passing one. `TestBackendMigrationSession19RecordCoversEveryWorkflow`
keeps the table whole and its citations real; it does not prove a cited test
passed.

The gates behind the cited tests: `just check`, `just crash-restart`,
`just reliability-stress`, and `just asl-integration` on the Mac Studio
(`docs/acceptance/ghostty-desktop/session-19/go-*.log`), and the hosted suite
through `just mac-release-gate` on the MacBook Pro. Everything that shows or
drives UI ran on the MacBook Pro. `docs/acceptance/ghostty-desktop/session-19.json`
is the summary record, and says which run each log is of.

| ID | Session 19 | Evidence |
| --- | --- | --- |
| W01 | passed | One agent per key: `TestLaunchOnTmuxRegistersOneLiveRuntimeSession`, `TestLaunchOnTmuxConcurrentDedupLaunchesOnce`, `TestLaunch_DedupUsesEffectiveProfile`. JSON and exit codes: `TestProjectLaunchJSONAndExitCodesMatchAcrossBackends` runs six launches (first, repeat, same title on another profile, ambiguous title, unknown project, unusable directory) on Kitty and on a real private tmux server and requires the same exit code and the same envelope from both, apart from the keys that say where the session is (`backend`, and `tab_id` and `window_id` on Kitty or `runtime_session_id` on tmux, each present only on its own backend). `TestProjectLaunchOnTmuxReportsTheRuntimeSession`, `TestProjectLaunchOnKittyReportsTheBackend`; live in `docs/acceptance/ghostty-desktop/session-19/live/isolated-tmux.log` (W2) and `docs/acceptance/ghostty-desktop/session-19/live/isolated-kitty-legacy.log` (L2). |
| W02 | passed | `TestLaunchOnTmuxRegistersOneLiveRuntimeSession` (always-new-instance leg), `TestLaunch_AlwaysNewInstancePolicyBypassesDedup`, `TestLaunchFreshIncludesHandoffInstruction`; live in `docs/acceptance/ghostty-desktop/session-14.json` (all-baseline-workflows-without-kitty). A Kitty launch makes a second tab, not a runtime-session record. |
| W03 | passed | `TestFocusOnAPortableSessionAttachesInsteadOfFocusingATab`, `TestContinueFocusesOnlyAnExactLiveConversation`, `TestContinueRefusesAnAmbiguousLiveConversation`, `TestBareSessionIDResolvesAcrossHarnessesAndRefusesToGuess`; live: Kitty tab made active in `docs/acceptance/ghostty-desktop/session-19/live/isolated-kitty-legacy.log` (L8), tmux attach and a live conversation attached instead of resumed in `docs/acceptance/ghostty-desktop/session-19/live/isolated-tmux.log` (W3, W7). |
| W04 | passed | `TestLaunchContinuityOnTmuxRefusesSecondResume`, `TestContinueLaunchesNativeResumeForEachSupportedHarness`, `TestDuplicateRegistrationIsRejected`; live: a closed conversation resumed on tmux as a new runtime session carrying its conversation ID in `docs/acceptance/ghostty-desktop/session-19/live/isolated-tmux.log` (W7). |
| W05 | passed | `TestForksOnTmuxLeaveTheSourceSessionAndConversationUntouched` forks a live conversation on a real private tmux server both ways, natively (Claude) and portably (Copilot, through the real handoff workflow and its pack), and requires a second live agent while the source's registry record is byte-identical, its pane is the same pane, its transcript is unchanged, and it still holds its conversation. `TestLaunchContinuityOnTmuxRefusesSecondResume`, `TestForkUsesNativeSupportAndPreservesSource`, `TestForkUsesPortableHandoffForCopilot` (Kitty); live native fork on tmux with the source left running in `docs/acceptance/ghostty-desktop/session-19/live/isolated-tmux.log` (W8). |
| W06 | passed | `TestForkWorktreeOnTmuxCompensatesOnlyItsOwnWorktree`, `TestForkWorktreeCleansUpWhenNativeLaunchFails`, `TestForkWorktreeUsesPortableLaunchDirectoryAndCleansUpFailure`. |
| W07 | passed | `TestWorkflowLaunchesOnSelectedRuntime`, `TestWorkflowRetainsFinalArtifactWhenLaunchFails`, `TestHandoffReportsFinalArtifactAfterLaunchFailure`, `TestPartialLaunchFailureLeavesNothing`. |
| W08 | passed | Order: `TestRestartLaunchesBeforeClosingOnKitty` and `TestRestartLaunchesBeforeClosingOnTmux` record the order the backend is asked in, for a restart and for a profile replacement: the launch comes first and only the replaced session is closed, on tmux both agents are alive at the moment of the close, and a launch that fails closes nothing and leaves the original running. End state: `TestRestartOnTmuxLaunchesBeforeClosingAndEndsTheOriginal`, `TestRestartWithAccountOnTmuxReplacesProfile`, `TestRestartFollowsRecordedBackend`, `TestRestart_ClosesAndRelaunches`; live: one agent before and after a restart in `docs/acceptance/ghostty-desktop/session-19/live/isolated-tmux.log` (W6) and `docs/acceptance/ghostty-desktop/session-19/live/isolated-kitty-legacy.log` (L7). |
| W09 | passed | `TestSessionActionsFollowTheRecordedBackend`, `TestRetainedExitUntilExplicitClose`, `TestFaultViewerCrashLeavesAgentRunningAtItsSize`, `TestAttachRealClientKeepsAgentRunning`, `testClosingTheWindowDetachesEveryTerminalAndReopeningReattaches`; live: each row closed through its own backend in `docs/acceptance/ghostty-desktop/session-19/live/isolated-kitty-tmux.log` (T5), and the app quit and killed with both agents unchanged in `docs/acceptance/ghostty-desktop/session-19/live/packaged-workspace.log`. |
| W10 | passed | `TestUnsupportedErrorMatches`, `TestBackendContract`, `TestRemoteControlBaseline`, `TestRuntimeSessionsAreLabeledForCompanions`, `TestResortWithoutKittySaysSessionsAreAlwaysInSortOrder`, `TestPortablePickersChooseTheirSessionWithoutGuessing`; live: Kitty tab titles through clear, active, lock, and mute in `docs/acceptance/ghostty-desktop/session-19/live/isolated-kitty-legacy.log` (L5), lock and mute in tmux pane metadata and the Resort Tabs refusal in `docs/acceptance/ghostty-desktop/session-19/live/isolated-tmux.log` (W4, W5, W11). Since then the order is one function (`session.Order`, `TestOrderMatchesSortRunningForAnyCarrier`, `TestProject_V2ListsSessionsInTheDashboardsOrder`), so without Kitty Resort Tabs answers that sessions are always in sort order instead of refusing; a tmux launch records its profile's color (`TestLaunchOnTmuxRecordsTheProfileColor`); and the daemon renders state, title, and color into the session's terminal (`TestSettledTmuxStateIsRenderedOnceIntoItsSessionsStatus`, `TestRenderedStatusReachesARealTmuxPaneInOneClientPerPass`, `TestAnAttachedTerminalShowsTheRenderedStatus`). |
| W11 | passed | `TestManagedHooksRouteByRuntimeSessionAcrossMixedBackends`, `TestManagedHooksWithoutRegistryAreRetryableAndLegacyStillRoutes`, `TestManagedHookAcknowledgementNeverWaitsOnBackendDiscovery`, `TestLegacyKittyHookIsUnchangedWithoutRuntimeSession`. |
| W12 | passed | `TestManagedHooksRouteByRuntimeSessionAcrossMixedBackends`, `TestDuplicateAndReorderedHooksStayInsideTheirRuntimeSession`, `TestPipeChildAndSubagentsStayWithTheirRuntimeSession`, `TestPostTerminationHooksCannotReachASuccessorOnTheSameTarget`. |
| W13 | passed | `TestRuntimeCheckpointSurvivesDaemonRestart`, `TestRuntimeCheckpointFreshEventsWinOverRestoredState`, `TestRestoreFromUserVarsRestoresReducerFactsLeasesAndGeneration`, `TestFaultDaemonRestartLeavesTmuxAgentAndItsOwnerUnchanged`; `just crash-restart` in `docs/acceptance/ghostty-desktop/session-19/go-crash-restart.log` (19 scenarios). |
| W14 | passed | `TestBackendGenerationChangeMakesTargetsStale`, `TestPIDReuseDoesNotReviveASession`, `TestValidateOutcomes`, `TestRuntimeCheckpointDropsStaleTargets`. |
| W15 | passed | Kitty: `TestBlockedKittySurvivesAsOneOutageAcrossEveryDaemonSurface`. tmux, the list: `TestAFailedTmuxDiscoveryKeepsTheLastListedSessions` (a tmux that stops answering keeps the rows last listed, with and without Kitty, and the refresh says they are kept), `TestAVanishedTmuxServerLeavesItsSessionsListedAsLost` (a real server killed under two agents: both stay listed, marked lost, are not counted as running, and closing one dismisses it), `TestClosingEverySessionEmptiesTheListWithoutAnOutage`, `TestANewServerReplacesLostRows`, `TestPortableDiscoveryFailureIsTotalOnlyWithoutKitty` (nothing listed yet), `TestHealthReportsKeptTmuxSessions`, `TestFeedbackNamesATmuxOutageWhileItsTilesAreKept`. tmux, the daemon: `TestRuntimeReconcileBacksOffWhileItFails`, `TestFaultTmuxServerLossIsRetiredAsLostAndNothingIsRelaunched`, `TestServerDisappearance`; live in `docs/acceptance/ghostty-desktop/session-18.json` (backend-failure-diagnosis). |
| W16 | passed | `TestBackendFixtureParity`, `TestTmuxFixtureClassification`, `TestTmuxExitedPaneScreenIsNotAFinishedTurn`, `TestScreenProbesNeverSettleOnWithheldObservations`, `TestCheckScreen`. |
| W17 | passed | `TestHeldStopIsConfirmedByTheIdleScreenOnEitherBackend`, `TestHeldStopOfAnUnreadablePaneIsReleasedAtTheDeadlineOnEitherBackend` (the tmux pane with no capture), and `TestContextLimitBannerIsDetectedOnEitherBackend` run each scenario on a Kitty window and on a tmux runtime session in a daemon with no Kitty, through the production probe tick, with the same assertions for both. `TestTmuxScreensFromAnotherGenerationOrAnExitedPaneAreNoEvidence`, `TestTmuxStopIsHeldOnlyWhenItsPaneCanConfirmIt`, `TestRuntimeProbesSpareSessionsThatAreNotWorking`; `TestRuntimeScreenProbeReadsARealTmuxPane` reads a real private tmux server through the production reader. `TestScreenProbesNeverSettleOnWithheldObservations`, `TestTerminalStopHold_ProbeConfirmsHeldStop`, `TestReadScreenNeverSettlesAWithheldCapture`. The three probes that first read Kitty only run on tmux through the same bodies: `TestClaudePaneTitleRecoversAndPacesAStuckTurnOnEitherBackend` (busy-title recovery and the idle title bump, from `backend.Screen.Title`), `TestCodexClearedScreenClearsAStoppedSessionOnEitherBackend` (the cleared-screen check over `Capture(ExtentAll)`), `TestCopilotHeldStopIsConfirmedByItsTranscriptOnEitherBackend` and `TestCopilotTranscriptSettlesAndRecoversAStuckTurnOnEitherBackend` (the transcript probe, its stop hold, and its recovery route from the slot table), over the captures `claude_working_streaming_titled`, `claude_idle_completed_titled` and `codex_cleared` in `internal/screenstate/testdata/tmux` and the existing Copilot ones. `TestTmuxPaneTitleFromAnotherGenerationRecoversNothing`, `TestTmuxCodexClearReadsTheHistoryOfTheRegisteredPaneOnly`; `TestRuntimeProbesReadTitleAndHistoryOfARealTmuxPane` and `TestObserveReportsTheTitleThePaneProgramSet` read a real private tmux server. |
| W18 | passed | `TestExampleScenariosParseAndPass`, `TestTUITestRunCommittedScenariosEndToEnd`, `TestRefreshWithoutKittyListsPortableSessionsWithTheirDaemonState`, `TestKittyAndPortableSessionsShareOneList`, `TestAKittyOnlySurfaceIsUnchanged`; live with Kitty, with tmux inside Kitty, and with no Kitty in the three logs under `docs/acceptance/ghostty-desktop/session-19/live`. |
| W19 | passed | `TestResortWithoutKittySaysSessionsAreAlwaysInSortOrder`, `TestActionsMenuEnterRefusesAnUnavailableEntry`, `TestUnavailableActionIsRefusedBeforeItStarts`, `TestAnUnavailableEntryReportsItsReasonAndRunsNothing`; live: the actions menu on a tmux row and on a Kitty row in `docs/acceptance/ghostty-desktop/session-19/live/isolated-tmux.log` (W4, W5) and `docs/acceptance/ghostty-desktop/session-19/live/isolated-kitty-tmux.log` (T3). |
| W20 | passed | `TestTmuxTilesAreScheduledLikeKittyOnes`, `TestUnavailableAndStaleTmuxCapturesPaintNothing`, `TestReadingAPaneNeitherAttachesNorResizes`; live: previews with no terminal attached in `docs/acceptance/ghostty-desktop/session-19/live/isolated-tmux.log` (W10) and `docs/acceptance/ghostty-desktop/session-19/live/isolated-kitty-tmux.log` (T4). |
| W21 | passed | Pickers: `TestColorPickerOpensAsAPortableViewForATmuxSession`, `TestIconPickerOpensAsAPortableViewForATmuxSession`, `TestPortablePickersChooseTheirSessionWithoutGuessing` (outside Kitty, over a real private tmux server, with every Kitty seam failing the test if reached), `TestColorPicker_ApplyHookReceivesThePickInsteadOfKitty`, `TestIconPickerSetTitleHookReplacesTheKittyTab`; with Kitty, `TestPickersInsideKittyRemainKittyOverlays`. Dashboard and keybindings without Kitty: `TestDashboardOutsideKittyOpensInThisTerminalAndAsksKittyNothing`, `TestKeybindingsOutsideKittyOpenInThisTerminal`; with Kitty the singleton stays, `TestFocusExistingDashboardRequestsSourceTabSelection`, `TestOpenDashboardTwiceLeavesExactlyOneDashboard`; in the workspace, `testTheDashboardAndOverviewAreAlwaysListedAndOpenedViewsAreUnique`. Pager, shell, editor: `TestWorkspaceSurfacesRunOnTmuxWithoutKittyAndRouteToTheWorkspace`, `TestHostedUtilityViewsGoToTheWorkspace`, `TestHostedUtilityViewsFallBackToThisTerminal`, `testShellEditorAndPagerOpenAsViewsAndGoBackWhenTheyEnd`; live: utility views in `docs/acceptance/ghostty-desktop/session-19/live/isolated-tmux.log` (W4, W11) and the native Kitty overlay pager in `docs/acceptance/ghostty-desktop/session-19/live/isolated-kitty-legacy.log` (L6). Without Kitty there is no second dashboard to find, so each invocation is a view of its own; the picked color is drawn by the dashboard and by the app's sidebar (`WorkspaceRowView` paints the row on the snapshot's `tab_color`), and a session nobody picked a color for is drawn in its profile's. Since then a plain terminal attached to a tmux session has the dashboard, the overview, the reference, and the pickers as popups over it and the function keys from the private tmux configuration: `TestTmuxFunctionKeysMatchKitty`, `TestFunctionKeysRunAmxForTheSessionAndTerminalThatPressedThem`, `TestPopupDashboardOpensOnItsSessionAndSwitchesTheTerminal`, `TestOpenDashboardOutsideKittyOpensAPopupOverTheLastTerminal`; going to another session is `TestSwitchClientMovesATerminalAndNeverLeavesTwoOwners` and `TestFocusManagedSwitchesTheNamedClientInListOrder`. |
| W22 | passed | `TestFocusingALiveTmuxWorktreeSessionReportsAttach`, `TestAPortableLaunchLeavesTheDashboardRunning`, `TestOpenActionsNeedNoKitty`, `TestWorktreeShellAndEditWorkWithoutKitty`, `TestWorktreeLiveSessionsComeFromTmux`, `TestSessionActionsNameTheBackendTheyLack`; live: Past Sessions, Worktrees, and Plans and their launch actions with no Kitty in `docs/acceptance/ghostty-desktop/session-19/live/isolated-tmux.log` (W7, W8, W9). |
| W23 | passed | Session 19, on the MacBook with the packaged amx and tmux: real Kitty 0.48.2 in `docs/acceptance/ghostty-desktop/session-19/live/isolated-kitty-legacy.log` (31 passed, 0 failed) and `docs/acceptance/ghostty-desktop/session-19/live/isolated-kitty-tmux.log` (30 passed, 0 failed); the tmux backend with no Kitty in `docs/acceptance/ghostty-desktop/session-19/live/isolated-tmux.log` (101 passed, 0 failed). |
| W24 | passed | `TestCompanionSnapshot_Golden`, `TestCompanionSnapshotV2_Golden`, `TestCompanionSnapshotVersionIsNegotiated`, `TestProject_V1HidesRuntimeSessions`, `TestProject_V2ListsRuntimeSessionsWithoutKittyIDs`, `testV2SnapshotDecodesRuntimeIdentity`. Runtime-session focus is its own request, `focus_session`: `TestFocusSessionAsksAWorkspaceBeforeAnyTerminal` (a connected workspace is sent a raised session view; with none the terminal used last is switched; a notification target for a runtime slot is the runtime session, never the slot), `TestFocusSessionSwitchesTheTerminalUsedLast` (a real private tmux server, no Kitty, no workspace), `TestNotifier_ClickShowsARuntimeSessionWithoutKitty`, `testFocusSessionOutcomes`. With no app running the session is selected in the terminal; raising that terminal's window is left to the operator. |
| W25 | passed | `TestCompanionSnapshot_ToleratesUnknownFields`, `TestCompanionSnapshot_OmittedFieldsDecodeToZero`, `testNextAttentionCyclesAcrossBackendsInSidebarOrder`, `testMenuListsTmuxSessionsWithTheirAnswerItems`, `testDecodingToleratesUnknownMissingAndMistypedFields`; the hosted suite run by the desktop gate, Debug and Release, in `docs/acceptance/ghostty-desktop/session-19/desktop-release/gate-run.log`. |
| W26 | passed | Focus observation on tmux: `TestAttachingATerminalClearsATmuxSessionsUnseenAttention` (a real private tmux server and real terminals: attaching clears the session's unseen attention and no other's, a terminal that is merely still attached clears nothing, typing in it does, and the clear survives a daemon restart), `TestViewerObservationLeavesKittySlotsAndSeenSessionsAlone`, `TestReconcileReportsWhoIsViewedWithoutCountingItAsAChange`. On Kitty: `TestFocusRefreshClearsOnlyTheFocusedPane`. Notification and routing with no viewer: `TestASLRunStaysRoutedWithNoKitty`, `TestASLRunInTmuxSessionRoutesToItsRuntimeSlot`, `TestOperatorClearReachesARuntimeSlot`, `TestAppNoticeForANonKittyRuntimeSessionCarriesNoKittyTarget`. |
| W27 | passed | `TestASLRunInTmuxSessionRoutesToItsRuntimeSlot`, `TestASLRunInKittyRuntimeSessionRoutesToItsOwnWindow`, `TestASLRunStaysRoutedWithNoKitty`, `TestInstalledBridgeRoutesRunsByRuntimeSession`, `TestInstalledBridgeServesTheContractSet`; `just asl-integration` against the installed asl in `docs/acceptance/ghostty-desktop/session-19/go-asl-integration.log`. |
| W28 | passed | An isolated installed pair on the Mac Studio (its own HOME; this checkout's amx installed by `just install`; a clone of the registered agent-session-loop source at 9f2e4318e224261a616e25b0d18033ee5f4eeb0e linked as its `asl`): `just asl-contract-sync` against the registered source, `just asl-integration` against the installed clone (`TestInstalledBridgeServesTheContractSet`, and `TestInstalledBridgeRoutesRunsByRuntimeSession` with a run in a private tmux session), and `just overseer-integration` (11 tests) all exit 0 in `docs/acceptance/ghostty-desktop/session-19/pair/pair.log`, with the source and the operator's own amx, asl, configuration, and daemon unchanged. The operator's installed tools were not the pair judged. |
| W29 | passed | `TestBackendFindingIdentifiersAreStable`, `TestDoctorJudgesTheDaemonsRuntimeWork`, `TestDoctorNamesEveryBackendProblemByItsStatusCode`, `TestDoctorJSONCommandSurfaceIsVersioned`, `TestRuntimeHealthReportsSessionsAttachmentAndReconciliation`; live in `docs/acceptance/ghostty-desktop/session-18.json`. Screen reads are reported as counts and failures, not as an age. |
| W30 | passed | Selection by runtime session: `TestEventsExportSelectsByRuntimeSession`, `TestEventsExportRefusesAnUnknownRuntimeSession` (daemon), `TestDaemonExportEventsCommand_CurrentWindowResolvesRuntimeSession` (in a tmux session `--current-window` is the session, and the inherited Kitty window is not believed), `TestDaemonExportEventsCommand_RuntimeSessionFlag`, `TestDaemonExportEventsCommand_RuntimeSessionRefusals`, `TestDaemonExportEventsCommand_CurrentWindowResolvesKittyEnv`; an exported tmux slot's live half is read from its pane by runtime session with Kitty never asked. Probe and capture by runtime session: `TestDebugProbeTargetsARuntimeSession`, `TestDiagnosticProbeNamesARuntimeSession` (claims no slot, names no window), `TestDebugCaptureScopesToTheCurrentRuntimeSession`, `TestCaptureScopedToARuntimeSession`. Doctor's Kitty findings only where Kitty is in use: `TestDoctorKittyIsNotAFailureWhereItIsNotInUse`. Redaction: `TestShareBundleKeepsBackendHealthAndDropsWhatItCouldCarry`, `TestShareBundleExcludesSecretCorpusRawSourcesAndIsDeterministic`, `TestForensicBundleRemainsRedactedAndCarriesLocalOnlyWarning`. |
| W31 | passed | Session 19 on the MacBook: the Session 17 installation acceptance run again against this session's Release bundle, `docs/acceptance/ghostty-desktop/session-19/packaged/install-acceptance.log` (107 passed, 0 failed), and the installed app as a workspace, `docs/acceptance/ghostty-desktop/session-19/live/packaged-workspace.log` (36 passed, 0 failed). The bundle is ad-hoc signed; a Developer ID signature and notarization are not shown. `just install-restart-smoke` and `just cockpit-smoke` were rehearsed in an isolated world only (`docs/acceptance/ghostty-desktop/session-19/live/isolated-install-cockpit.log`). |
| W32 | passed | `TestMacAppRecipesStayOutOfTheGates`, `TestJustMacReleaseGateIsTheDesktopGateAndNothingElse`, `TestTheTwoGatesStayApart`, `TestDesktopGatePassesAndRecordsWhatItBuilt`, `TestDesktopGateReportsMissingInfrastructureAsPending`; the desktop gate run as a rehearsal with its record in `docs/acceptance/ghostty-desktop/session-19/desktop-release`, and two runs that reported pending in `docs/acceptance/ghostty-desktop/session-19/desktop-release/pending-runs.log`. `just paired-release-gate` itself was not run: it installs tools and restarts the operator's daemon. |

## Kitty (optional legacy backend)

Kitty is where Agent Mux started and it stays supported, behind
`internal/backend`, as a backend an operator opts into. No Kitty code was
removed. What changed is that nothing else depends on it:

| Surface | Without Kitty | To use Kitty |
| --- | --- | --- |
| Install | `just install` builds and installs `amx` and seeds `config.toml`; it creates no `~/.config/kitty` and links nothing | `just install-kitty` links `agent-mux.conf`; `kitty.conf` includes it and enables remote control. `just uninstall` removes the link if there is one |
| Launch | `launch_backend = "tmux"` or `--backend tmux`; the dashboard in a terminal that is not Kitty launches on tmux unasked | `launch_backend = "kitty"` (or unset, until the default moves; see below) or `--backend kitty` |
| A running session | reached on the backend it was launched on; a *recorded* empty backend means Kitty, so sessions and checkpoints from before the tmux backend read as they always did | the same |
| Paired release gate | no step runs or names Kitty; `cockpit-smoke` is a real-tmux smoke that runs from any terminal and fails without a usable tmux | `just kitty-smoke`, from a Kitty window, in no gate |
| Evidence record | schema 2: no `kitty-version` step, `environment.kitty` absent | schema-1 records, which name a Kitty, still load |
| Agent Mux.app | **Launch projects in** defaults to *Workspace*; *Follow CLI configuration* is optional | **Launch projects in** › *Kitty*; **Show in Kitty** and the Kitty socket preference are unchanged |
| `amx doctor` | `kitty`, `kitty_snapshot_stale`, and `renderer_drift` are raised only where Kitty is in use | unchanged |

Still Kitty-shaped inside, and out of scope here: `kitty.UserVars` and
`kitty.OSWindow` as the shared data model, Kitty launches not being registered
in the runtime-session registry, the `internal/kittykeys` package name, and the
TUI scenarios built on Kitty fixtures. The compatibility requirements below
are unchanged: companion v1, `focus_window`, legacy `KITTY_WINDOW_ID` hooks,
`amx_state` restore, and the ASL `kitty_window_id` contract.

**The default.** `launch_backend` unset still means Kitty. Requirement 9 below
holds it there until the daily-driver trial passes
(`docs/acceptance/ghostty-desktop/session-20.json`, `docs/daily-driver-trial.md`).
When that record says `passed` with `operator_confirmed: true`,
`config.LaunchBackendOrDefault` returns tmux and
`TestLaunchBackendDefaultsToKitty` is replaced; `--backend kitty` and
`launch_backend = "kitty"` keep working. Until then an installation with no
Kitty sets `launch_backend = "tmux"`: left unset, doctor counts Kitty as in
use and raises `kitty` as a warning (never a `--strict` failure) saying so.

## Session 21 record

Session 21 is the session that stopped requiring Kitty (plan "Make tmux a
complete backend so Kitty is needed for nothing", session 6). It follows the
five sessions that gave every Kitty-only workflow a tmux path.

Done, with what holds it:

- **Install.** `just install` no longer creates `~/.config/kitty` or links the
  snippet; `just install-kitty` does. `cmd/release.TestJustInstallCreatesNothingOfKittys`.
- **Release gate.** `kitty-version` is gone from the plan and `cockpit-smoke`
  is the real-tmux smoke; the Kitty one is `just kitty-smoke`.
  `internal/release.TestGateRequiresNoKitty` (and a fixture whose `kitty` and
  `kitten` fail if run), `cmd/release.TestJustCockpitSmokeFailsRatherThanSkips`,
  `cmd/release.TestJustKittySmokeIsOptInAndFailsRatherThanSkips`.
- **Evidence record.** `schema_version` 2 with `environment.kitty` optional;
  schema 1 still loads. `internal/release.TestLoadEvidenceStillReadsASchemaOneRecord`.
- **App.** The explicit default is Workspace; the retired automatic value migrates
  to it. `BackendSetupTests.testFollowingCLIUsesTheReportedDestinationAndNeverGuesses`
  covers the optional CLI-following choice and the preserved default.
- **Coverage.** `portable_tmux` scenarios, narrow and wide, for resort, open
  shell and editor, the launch picker, and an action naming the tmux server
  (`docs/tui-testing/README.md`).
- **Docs.** The README, `CLAUDE.md`, usage, companion app, paired releases,
  testing, and acceptance pages describe tmux as the backend and Kitty as
  optional.

Pending, and not counted as done:

- **The default flip.** Not made. On 2026-10-13
  `docs/acceptance/ghostty-desktop/session-20.json` is `pending` with
  `operator_confirmed: false` and no workday logged, so `launch_backend` unset
  still launches into Kitty and `TestLaunchBackendDefaultsToKitty` stands.
- **The hosted app suite.** The app and its test target compile
  (`xcodebuild build-for-testing`); `just mac-test` on the remote Mac has not
  run the changed `BackendSetupTests`, and the launch-target default has not
  been driven live.
- **The gate on a machine with no Kitty.** `just cockpit-smoke` was run from a
  terminal that is not Kitty, with every `KITTY_*` variable unset, on a machine
  that has Kitty installed. `just paired-release-gate` on a machine without
  Kitty has not been run.

## Baseline fixtures

These pin current Kitty behavior so later sessions can prove they preserved it:

- `internal/kitty/testdata/remote_control_baseline_v1.json`, asserted by
  `internal/kitty.TestRemoteControlBaseline`: the exact `kitten @` argv of every
  `KittyClient` method, including socket selection, flag order, sorted env and
  user vars, the `--` separator, and the match-expression spellings. The test
  fails if a method is added to `KittyClient` without a pinned entry.
- `internal/daemon/protocol/testdata/companion_snapshot_v1.json`, asserted by
  `TestCompanionSnapshot_Golden`: the v1 companion wire shape (existing).
- `internal/session` `launch_user_vars_test.go`: the `amx_*`/`cc_*` user-var
  vocabulary written at launch (existing).
- `testdata/kitty_ls*.json` and `internal/screenstate/testdata`: Kitty topology
  and screen captures (existing).

## Protocol compatibility requirements

1. The common JSON envelope `{"schema_version":1,"data":...}` and structured
   errors are unchanged. New fields are additive; unsupported backend
   operations are structured capability errors.
2. Companion snapshot v1 stays byte-compatible with its golden for clients that
   do not negotiate a newer version, and carries only Kitty sessions with their
   real Kitty identifiers. A tmux session is never given a fabricated
   `window_id`, `tab_id`, or `kitty_socket`.
3. The Swift client keeps tolerant decoding: unknown keys ignored, omitted keys
   decode to zero.
4. `focus_window` keeps its `{kitty_socket, window_id}` request and refusal codes
   for Kitty targets; runtime-session focus is a new request, not a reuse.
5. Legacy hooks carrying `KITTY_WINDOW_ID` and `KITTY_LISTEN_ON` keep routing
   through a validating translation layer; hook acknowledgement stays
   prompt-free and performs no backend discovery.
6. Existing Kitty `amx_state` checkpoints remain readable; new persistence is a
   versioned file with atomic replacement and the existing lock conventions.
   No database is introduced.
7. `daemon export-events` schema versions, `debug` report schema versions, and
   doctor finding identifiers are unchanged; new data is additive under new
   schema versions where the shape changes.
8. The ASL contract set, bridge protocol 2, and lifecycle hook schema 2 are
   unchanged by backend work; the paired release gate stays Xcode-free.
9. Kitty launch defaults keep working; tmux is selected explicitly until its
   acceptance sessions pass, and actions on an existing session follow its
   recorded backend.

## Session 1 baseline record

Automated: `just check` (gofmt, `go test ./...`, `race-core`, `go vet`, every
TUI scenario, `golangci-lint`) passed on 2026-10-09 with the baseline fixtures
above.

Pending (live, not run in Session 1): `just cockpit-smoke`,
`just install-restart-smoke`, `just asl-integration`,
`just overseer-integration`, `just paired-release-gate`, `just mac-test`, and
the operator checklists in [Product acceptance](acceptance.md). Each is assigned
in the matrix above and re-run by Session 19.
