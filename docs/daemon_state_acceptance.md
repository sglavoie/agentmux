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

### Emoji-regression-repair acceptance (2026-07-09)

Automated and installed-binary evidence collected for the F8/ASL/transport
regression repair (`amx` version 0.5.0). These are actual command outcomes from
this session; the interactive F8/ASL live drills below were **not** performed
here and remain operator work.

| Evidence | Result |
| --- | --- |
| `just check` | passed — `go test ./...`, `go vet ./...`, `golangci-lint run` (0 issues) |
| `just install` | installed `amx` to `~/.local/bin/amx`; `agent-mux.conf` symlinked |
| `amx --version` | `amx version 0.5.0` |
| `amx config hooks-status` | all 7 profiles `in_sync` (codex/claude/copilot) |
| `amx daemon restart` | restarted; new daemon pid 70676 |
| `amx daemon status` | running on `~/.local/share/agent-mux/daemon.sock` |
| `amx doctor` | `overall: clean` — kitty ok, all hooks in_sync, `kitty sockets: unix:/tmp/kitty-88030`, renderer drift none, no recent suppressions |
| `amx daemon export-events --last 50` | 7 windows, all `render_failures: 0`, all sockets `unix:` (no `fd:`), zero `owner_mismatch`; live managed codex loop (window 112) rendered `🛑🔁` (stop-plus-loop) on the stable socket |

Partial live observation: the installed daemon rendered an existing managed
agent-session-loop as stop-plus-loop with a `unix:` socket and zero render
failures, confirming the transport fix and loop-marker preservation on the
installed binary. This is not a substitute for the explicit interactive F8 and
unmanaged-sibling ASL drills, which require physical keybinding presses and a
purpose-built split-pane run.

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
- [x] **F8 owner + sibling routing** — in a managed tab with at least one
  sibling pane, produce a visible state, then press the real F8 binding (not a
  direct state-update request) from the owner pane and again from an active
  sibling pane. For each, confirm the tab loses only its state icon; `🔁`, base
  title, `★`, and `🔇` are preserved; `amx daemon export-events --window
  <owner-id>` records a non-suppressed `user_clear` on the owner; and the
  owner's `renderer` shows an empty `desired_icon`, an empty live icon, a
  `unix:` socket (never `fd:`), and `render_failures: 0`.
- [x] **Active-loop sibling ASL routing** — launch an agent-session-loop from an
  *unmanaged* sibling pane. Record the ASL source window ID and the managed
  owner ID. During tool activity confirm the owner title shows bolt-plus-loop
  (`⚡🔁`); at turn completion confirm stop-plus-loop (`🛑🔁`); after the next
  prompt confirm it returns to `⚡🔁`. Confirm routed events appear under the
  owner and are **not** counted as `owner_mismatch`, and that an unrelated
  sibling event (same CWD, no active loop candidate) stays suppressed. Finish
  the run and confirm `🔁` is removed while the correct terminal state remains.
  Retain `amx daemon export-events --window <owner-id>` and
  `amx daemon export-events --window <source-id>` for both.

**Operator confirmation (2026-07-09):** both interactive drills above were run
in a real Kitty session — the F8 owner/sibling routing drill and the
unmanaged-sibling active-loop ASL drill. Each showed the expected title
behavior.

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
