# Product acceptance

The AgentMux 0.6.0 product acceptance gate is:

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

## Workflow coverage

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
| Diagnostic pipeline | `cmd.TestAcceptanceDiagnosticPipeline` drives the real hook command adapter through journal persistence and Unix IPC into an in-process daemon with fake Kitty, then checks duplicate acknowledgement, renderer writes, notification suppression, a share bundle, offline replay, and ASL source-event explanation. |
| Debug workflow | `cmd.TestReleaseDocumentationPinsVersionAndDiagnosticWorkflow` pins AgentMux 0.6.0, schema v9 documentation, the `amx debug status` → `amx debug explain` → `amx debug bundle` → `amx debug replay` workflow, and every required failure-matrix row. |
| Bundle secrecy and traversal | `internal/debugger` tests plant a secret corpus, verify share/forensic redaction, exclude raw sources, reject symlink publication, build flat deterministic archives, and replay portable share evidence. |
| Restart and replay equivalence | `internal/daemon` restart-prefix tests and `internal/debugger` golden replay tests cover restored reducer state, daemon sequence resets, ordering, routing, ASL phases, duplicate requests, coalesced renders, and explicit schema-v8 limitations. |
| ASL delivery correlation | `/Users/sglavoie/scripts/tests` covers stable UUIDv5 request IDs, bounded retry/backoff, duplicates, permanent failures, legacy fallback, missing binaries, exhaustion, metrics/tail/Workbench visibility; `internal/debugger.TestExplainCorrelatesASLSourceDeliveryDaemonAndRenderEvidence` pins the cross-repository graph. |

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

The release uses schema v9 for `amx daemon export-events` by default. Acceptance
also retains `amx daemon export-events --schema 8` coverage as an explicit
compatibility projection. For first-line diagnosis use:

```bash
amx debug status
amx debug explain --source-event <id>
amx debug bundle --mode share -o agent-mux-debug.tar.gz
amx debug replay agent-mux-debug.tar.gz
```

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

## Automated versus live-provider evidence

The automated diagnostic fixture has no model usage. It substitutes a local
fake Kitty executable and never contacts Claude, Codex, Copilot, a real Kitty
instance, or a notification backend. It is the completion gate for deterministic
delivery, replay, bundle, and correlation behavior.

Actual provider smoke is a separate operator-attended record. For each installed
provider, record the CLI version and intentionally exercise prompt, tool,
permission/elicitation, terminal, and failure paths in a managed Kitty session.
Retain a schema v9 export or `amx debug explain --source-event <id> --json` for
each path. Do not infer that this drill ran from an automated pass, and do not
incur provider/model usage merely to complete the automated gate.
