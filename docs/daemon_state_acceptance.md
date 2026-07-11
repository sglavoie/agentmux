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

### Reliability-reconstruction installed acceptance (2026-07-10)

Session 14 of the 14-session reliability-reconstruction plan. All entries
below are actual command outcomes and live observations from this run against
the installed binary (`amx version 0.5.0`, revision `e7b8b5444b3f-dirty`,
binary sha256 `32548362f81a…` — CLI and daemon hashes identical per
`amx daemon status` and strict doctor).

Automated gates:

| Evidence | Result |
| --- | --- |
| `just check` | passed — `go test ./...`, `go vet ./...`, `golangci-lint run` (0 issues) |
| `go test -race ./internal/daemon -count=10` | passed (~34s) |
| ASL full suite (`python3 -m unittest -q` in `~/scripts`) | passed — 1264 tests in 183.5s |
| `git diff --check` (both repositories) | clean |
| `just install-restart-smoke` | ok — atomic install, restart, `ready: true`, strict doctor clean |
| `amx config hooks-status` | all 7 profiles `in_sync` after one `hooks-sync` (drift was serialization-order only; semantic diff empty) |
| `amx daemon audit --duration 30m --json` | see audit record below |

Live scenarios observed (tab-title polling plus `amx daemon export-events`
window logs retained in the session transcript):

- **Claude turn transitions** — new managed tab: `⚡` on prompt submit, `🛑`
  at turn end (`UserPromptSubmit→active→bolt`, `Stop→stop→stop`).
- **Three concurrent subagents with interleaved parent activity** — three
  `SubagentStart`/`SubagentStop` pairs with parent `PostToolUse` interleaved;
  `🤖` held for the whole subagent window (precedence over the parent bolt)
  and resolved to a clean `🛑` with no stranded robot icon.
- **Codex turn transitions** — `⚡` → `🛑` within ~2s on a trivial prompt.
- **Codex manual compaction** — `/compact`: `PreCompact→compact` (`🌀`),
  `PostCompact→stop` (`🛑`).
- **Codex automatic compaction** — with `model_auto_compact_token_limit=8000`
  the session compacted repeatedly mid-turn: `PreCompact→compact` then
  `PostCompact→active` (`🌀` → `⚡`, trigger-classified, never `🛑` mid-turn).
- **Stale-compaction watchdog** — a compaction interrupted mid-flight (Esc
  during auto-compact) left `🌀` with no follow-up hook; the watchdog expired
  it at exactly the 180s threshold (`SourceExpired` + `WatchdogStale`
  recorded, icon cleared).
- **Two overlapping ASL runs in one workspace** — two `asl run` processes in
  one Kitty window produced two run-keyed leases; the quick run's finish left
  the long run's fact and `🔁` untouched; after both finished, the loop marker
  dropped while another still-active source kept its icon (the plan's
  "finished: 🛑 without 🔁 unless another source remains active").
- **Verification spanning two heartbeats** — a 70s verify recipe produced
  `AgentSessionLoopVerificationStarted`, two 30s-cadence
  `AgentSessionLoopVerificationHeartbeat` events, then
  `VerificationFinished`/`Finished`; `🔁` held throughout.
- **Nested ASL tests with notifier isolation** — `tests.test_asl_run_gates`
  (33 tests, includes nested real runs) executed from inside a live managed
  window; the window's ASL fact set was byte-identical before and after (no
  leaked lifecycle events, no spurious leases).
- **F8 acknowledgement during active ASL work** — `user_clear` on the active
  `⚡🔁` window applied non-suppressed (icon cleared), the ASL turn heartbeat
  preserved `🔁`, and the next genuine tool event re-asserted `⚡` within ~3s.
  Method note: invoked as the exact command the F8 binding launches
  (`amx hook-indicator user_clear --window-id <owner>`); remote-control
  `kitten @ launch --type=background` hits a kitty `Bad file descriptor` bug,
  and the physical-keypress path was operator-verified 2026-07-09.
- **Daemon restart during active work** — three restarts while a live ASL
  session (this one) was mid-turn; `⚡🔁` restored from `amx_state` each time
  and subsequent hooks kept flowing. The first two restarts exposed and fixed
  two restore defects (see notes below).
- **Copilot turn transitions** — three live turns observed:
  `userPromptSubmitted→active→bolt` (`⚡`), `agentStop→stop` (`🛑`), twice on
  trivial prompts and once through a real blocking tool permission. The first
  cold launch refused with "Please use /login" despite valid keychain
  credentials (one-time initialization; a relaunch authenticated cleanly —
  the first launch stamps `loggedInUsers` into `~/.copilot/config.json`).
- **Copilot blocking permission** — with correct harness classification the
  live sequence `preToolUse` → `permissionRequest` (pre-permission signal,
  icon preserved) → `notification(permission_prompt)` surfaced `🔑`. Two
  launch-environment caveats discovered: (1) a hand-launched tab carrying
  only the legacy `cc_account` var is classified as claude
  (`EffectiveHarness()` fallback), which routes the permission through the
  Claude auto-approve heuristic and suppresses the key — amx-launched tabs
  set `amx_harness` and are unaffected; (2) current Copilot emits
  `preToolUse` BEFORE the permission and does not re-emit it after approval,
  so after approving, `postToolUse`/`agentStop` deliberately preserve the
  pending `🔑` and it stays up until the next prompt or tool start resolves
  it (verified: the next `userPromptSubmitted` cleared it instantly to
  `⚡`→`🛑`). The sanitized replay trace models the older
  notification-then-preToolUse order; see open question in the session-14
  handoff.

Defects found and fixed during this acceptance run (each now pinned by a
regression test):

1. Full events-export of the long-running production daemon exceeded the IPC
   client's 1 MiB response cap, degrading doctor/audit to `daemon_export`
   failures. The client response ceiling is now 64 MiB with a dedicated 5s
   export deadline (`TestClient_ExportEventsAboveLegacyCapRoundTrips`).
2. Restored windows never republished reduced state: a terminal fact that
   aged past the tombstone TTL while the daemon was down pinned a permanent
   `shadow_current_mismatch` (stale scalar icon vs empty reducer). Restore
   now compacts + reduces + publishes
   (`TestRestoreFromUserVarsRepublishesReducedStateAfterTombstoneExpiry`).
3. Restore never stamped `Shadow.EffectiveIcon`/`EffectiveLooping` metadata,
   so every restored window with facts was a false-positive
   `shadow_current_mismatch` until its first live event (same test).
4. Caught by the first 30-minute audit: every non-event publish path
   (watchdog sweeps, ASL reconciliation, screen probes) updated the renderer
   scalars through `publishEffectiveLocked` but left the shadow export
   metadata at the last event-stamped value — a watchdog expiry produced a
   persistent `shadow_current_mismatch` until the window's next real event.
   `publishEffectiveLocked` now stamps the shadow metadata itself, so every
   publish path moves both together
   (`TestSweepStale_StampsShadowMetadata`).

### 30-minute monitored audit records (2026-07-10)

**Audit 1** (`amx daemon audit --duration 30m --json`, 20:22–20:52 local, 880
samples, run concurrently with all live scenarios above): **unhealthy** — two
failure findings, both `shadow_current_mismatch` on watchdog-expired windows
(the codex window after the 180s compact expiry and the ASL window after the
600s bolt expiry), plus one pre-existing-window `harness_drift` warning and
two informational `acknowledged_state` notes. Both failures were the same
root cause: defect 4 above. Fixed, regression-tested, reinstalled via
`just install-restart-smoke` (clean at daemon uptime 0s, including the
previously mismatching windows).

**Audit 2** (30m against the fixed installed build, 20:59–21:29 local, 879
samples): **healthy — exit 0, zero failure or warning findings**; the only
findings were three informational `acknowledged_state` notes. The window
included continuous live ASL session traffic, a Claude turn with two parallel
subagents, a Codex turn with compaction cycling interrupted mid-flight, an
F8-equivalent `user_clear` on an active `⚡🔁` tab (observed clearing the icon
while preserving `🔁`), a fresh toy ASL run, and that run's worker-bolt
watchdog expiry at exactly the 600s threshold — the same expiry class that
failed audit 1 — now with `desired == shadow == "stop"` at the moment of
expiry.

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
| 2026-07-10 | session-14 autonomous acceptance (ASL worker) | ~2h installed run: 4 daemon restarts under live work, Claude/Codex/Copilot turn drills, Copilot blocking permission, manual+auto Codex compaction, 3-subagent fan-out, overlapping ASL runs, 2-heartbeat verification, nested-test isolation, F8-equivalent clear, two 30m monitored audits | this document (2026-07-10 sections) + `amx daemon export-events` logs in the session transcript | audit 1 found defect 4 (fixed same session); audit 2 clean (exit 0) |
|  |  |  |  | not run |

Record failures as well as successful observations. Do not mark this table
complete prospectively.
