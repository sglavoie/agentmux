# Product acceptance

The product acceptance gate is:

```bash
just acceptance
```

It runs `go test ./...`, the race-sensitive transcript, plan, handoff,
worktree, daemon, and TUI suites, `go vet ./...`, and every committed TUI
scenario. `just check` adds `golangci-lint` and is the final repository gate.
All persistence tests use temporary homes, transcript sources, plan roots, and
cache paths.

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
