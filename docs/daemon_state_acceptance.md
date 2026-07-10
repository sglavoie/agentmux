# Daemon State Acceptance Evidence

This is the acceptance record for the daemon-owned tab-state refactor. It
separates repeatable command evidence from the live-client and dogfood evidence
that an operator must collect. A blank or `not run` entry is not a pass.
Mechanical acceptance checks can treat any `not run` cell as incomplete.

## Automated evidence

`just check` passed for the refactor and must remain green for each change.
The remaining commands interact with the operator's installed binary, Kitty,
or daemon, so this documentation-only session did not run them. Capture their
output, date, and environment when preparing a live acceptance run.

| Evidence | Current record | What to retain |
| --- | --- | --- |
| `just check` | passed for this session | command output and commit/diff identity |
| `just install` | not run in this documentation-only session | install output and installed binary path |
| `./amx --version` | not run | version output after installation |
| Hook sync: `amx config hooks-status` | not run | per-profile status showing hooks are in sync |
| `amx daemon restart` | not run | restart result and daemon PID/socket evidence |
| `./amx doctor` | not run | complete diagnostic output, including renderer and suppression status |

Run these against the binary intended for the live checks. Do not substitute a
successful unit-test run for an install, hook-sync, restart, or doctor result.

## Manual live evidence log

For each client, exercise and record every required icon transition: **bolt**,
**key**, **stop**, **error**, and **question**. Record a timestamp, client and
version, tab/window identifier, transition evidence, and any observed mismatch.
Leave the row `not run` until an operator has performed the complete exercise.

| Client | Required transitions | Status | Operator evidence |
| --- | --- | --- | --- |
| Claude Code | bolt, key, stop, error, question | not run | date, version, tab ID, screenshots/export-events, notes |
| Codex | bolt, key, stop, error, question | not run | date, version, tab ID, screenshots/export-events, notes |
| Copilot | bolt, key, stop, error, question | not run | date, version, tab ID, screenshots/export-events, notes |

## Session 4 regression drills

Session 4 was the original daemon-state refactor session that introduced these
live regression drills.

Run these drills in a live Kitty environment and retain the relevant
`amx daemon export-events` output with each result.

- [ ] **Kill recovery** — stop or kill the daemon during an active tab, then
  verify restart and the next cleanup recover the expected state.
- [ ] **Drift convergence** — introduce or observe a title/state mismatch and
  verify the renderer converges it on a subsequent tick, recording any
  transient flicker window observed in the real Kitty session.
- [ ] **Split-tab owner resolution** — exercise a split tab and confirm the
  event is resolved to the tab/window owner that should receive the state.
- [ ] **Hook latency** — record representative hook-to-render latency and note
  any delayed or dropped delivery.

## Residual title writers

The daemon owns the tab-state and loop segments of the title. Mute and star
remain externally owned title segments in this refactor, so a coincident mute
or star write can briefly race with a daemon render. The focused renderer
regression covers latest-live-snapshot preservation: it starts from the latest
title state and verifies that the daemon-owned segments are rewritten while
both external markers are preserved. It does not exercise a true in-flight interleaving
between an external writer and a daemon render. The live drift convergence
drill remains responsible for confirming any transient flicker
window in a real Kitty session and reconvergence on a subsequent renderer
tick. This residual race is intentional for now.

## Checkpoint before dogfood

The daemon-state refactor remains an uncommitted working-tree diff. Before
treating live dogfood as the final acceptance gate, an operator should commit
the diff or otherwise create a durable checkpoint that identifies the exact
version under test. Do not infer that this documentation or automated evidence
creates that checkpoint.

### Checkpoint record

| Status | Method | Commit hash or artifact | Operator | Date |
| --- | --- | --- | --- | --- |
| not run | checkpoint creation pending |  |  |  |

Live dogfood cannot be treated as final acceptance until an operator creates
the checkpoint and completes this record, including the method, commit hash or
artifact, operator, and date. Do not mark the dogfood table complete until the
operator has also recorded the required live evidence.

## Dogfood evidence

The effort is not accepted until an operator fills dated dogfood entries.
Automated checks and one-off live-client rows do not replace multi-day dogfood.

| Date (local) | Operator | Duration / scenarios | Evidence location | Result / follow-up |
| --- | --- | --- | --- | --- |
|  |  |  |  | not run |
|  |  |  |  | not run |

Record failures as well as successful observations. Do not mark this table
complete prospectively.
