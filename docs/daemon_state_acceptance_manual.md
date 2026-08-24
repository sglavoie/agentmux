# Daemon State Manual Acceptance Evidence

Manual drills and dogfood records supplementing [daemon_state_acceptance.md](daemon_state_acceptance.md).

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
### Session 7 evidence record

Environment under test: `amx version 0.5.0`, git `07d22b6`; installed harnesses
`codex-cli 0.144.3`, `Claude Code 2.1.195`, `GitHub Copilot CLI 1.0.70`.

| Evidence | Result | What to retain |
| --- | --- | --- |
| `just check` (2026-07-13) | **passed — exit 0**; `go test ./...`, `race-core`, `go vet ./...`, every TUI acceptance scenario, and `golangci-lint run` (0 issues) | full gate output and diff identity |
| `just install-restart-smoke` (2026-07-13) | **passed** — installed `amx` 0.5.0 (revision `07d22b6b4b95-dirty`, sha256 `7107ed680042`), daemon restarted (pid 52418), `ready: true`, strict `doctor` **overall: clean** at uptime 0s: kitty ok, all 7 profiles `in_sync` (codex/claude/copilot), `kitty sockets: unix:/tmp/kitty-689`, renderer drift none, no recent suppressions (only one `acknowledged_state` info note on window 75) | install/restart output, ready wait, strict-doctor result |
| Live interactive drills (normal completion, interruption, prompt-after-stop, protected states, live ASL between-turn) for all three harnesses | **passed** (operator-attended, 2026-07-13) — see [§Session 8.2 live cross-harness stop-convergence drills](#session-82-live-cross-harness-stop-convergence-drills-2026-07-13) for per-harness window IDs, event provenance, desired/live convergence, and the live daemon audit | per-harness date, version, window ID, and `amx daemon export-events` capture showing the probe source and reduced result |

The interactive live drills were completed operator-attended on 2026-07-13
against the installed binary in a real Kitty session, with physical
interruptions of each harness and live ASL leases; the detailed evidence is
recorded in the Session 8.2 section below. The automated gate proves the
reducer/renderer/probe convergence deterministically; the live rows below are
the operator confirmation that the installed interactive TUIs match those
classifiers. **Scope note:** these drills confirmed the stop-convergence matrix
(normal completion, confirmed interruption, prompt-after-stop, protected active
states, and a live ASL lease). They are **not** a completion of the separate
five-transition (bolt/key/stop/error/question) *Manual live evidence log* above,
whose unexercised transitions remain `not run`.

### Session 8.2 live cross-harness stop-convergence drills (2026-07-13)

Repair session 8.2 (finding `F2-live-acceptance-not-run`). Operator-attended
live drills against the installed binary confirmed the reliable stop-state
convergence across Codex, Claude, and Copilot, resolved the outstanding
per-harness dialect questions carried from sessions 3–7, and fixed the
installed-client compatibility mismatches discovered in the process.

Environment under test (2026-07-13): `amx version 0.5.0`, revision
`07d22b6b4b95-dirty`; installed harnesses `codex-cli 0.144.3`,
`Claude Code 2.1.195`, `GitHub Copilot CLI 1.0.70`; Kitty socket
`unix:/tmp/kitty-689`. Scratch managed windows: Codex **91**, Claude **92**, and
a clean cold-start Copilot **101**. All rows below are actual live observations
from that session; probe provenance is from `amx daemon export-events --window
<id>` and title convergence from the live tab titles.

| Harness / window | Normal completion & prompt-after-stop | Confirmed interruption (installed key) | Fallback probe provenance | Convergence |
| --- | --- | --- | --- | --- |
| **Codex 91** | `UserPromptSubmit`→`bolt`, native `Stop`→`stop`; reprompt restored `bolt` | **Esc** during a real `sleep 30` tool turn produced the installed marker `Conversation interrupted - tell the model what to do differently` plus the dim composer placeholder `Find and fix a bug in @filename`, and **no native `Stop`** | `ScreenStopProbe`, `source: probe:codex_screen`, `reason: authoritative idle (codex) settled visible state "bolt"→"stop"` | desired/live title converged to `stop`; `mismatch_count 0`, `render_failures 0` |
| **Claude 92** | `UserPromptSubmit`→`bolt`, native `Stop`→`stop`; reprompt restored `bolt` | **Esc** during a real `sleep 30` tool turn returned to an empty composer containing `U+00A0` (non-breaking space) and emitted **no native `Stop`** | `ScreenStopProbe`, `source: probe:claude_screen`, `reason: authoritative idle (claude) settled visible state "bolt"→"stop"` | desired/live title converged to `stop`; `mismatch_count 0`, `render_failures 0` |
| **Copilot 101** | cold start reproduced `userPromptSubmitted` **before** a late same-session `sessionStart(source:new)`; the new handler recorded that `sessionStart` as `action idle`, `preserves_icon true`, `result preserve`, `current bolt`, `reset_flow false`; the subsequent `agentStop(end_turn)` produced `stop`; a following reprompt restored `bolt` and converged to `stop` | a real `sleep 300` tool turn showed **remote `Esc` did not abort** installed Copilot 1.0.70 while a tool was running; **`Ctrl+C` did**. Transcript `~/.copilot/session-state/6f00b502-035c-4d9d-83b2-687f3eff69c6/events.jsonl` ends with `type: abort` / `reason: user_initiated`, **no native `agentStop`** | `TranscriptStopProbe`, `source: probe:copilot_transcript`, `reason: authoritative idle (copilot) settled visible state "bolt"→"stop"` | desired/live title matched; `mismatch_count 0`, `render_failures 0` |

The three `reason` strings quoted above are verbatim from that 2026-07-13 run
and are kept as the historical record. The applied reason has since gained a
diagnostic suffix, so the current form of the Codex row's cell is
`authoritative idle (codex) settled visible state "bolt"→"stop"
(tier=terminal_explicit samples=2 generation=<fingerprint>)`, and the Claude and
Copilot rows read the same way with their own tier, run length, and evidence
fingerprint. Read a fresh drill against that shape, not the pre-suffix one.

Protected / live-loop evidence (unmodified live ASL windows, observed but not
disturbed): live window **11** retained its ASL lease with title `bolt+loop`,
ASL heartbeat `result preserve`, two `pm:true` facts, desired/live agreement,
`mismatch 0`, `render_failures 0`; live window **30** likewise held an active
ASL lease, `looping true`, multiple `pm:true` facts, and desired/live
`bolt+loop` agreement. Blocking permission (`🔑`) and subagent (`🤖`)
transitions remain covered by the dated **2026-07-10** installed acceptance
above; they were not re-exercised here.

Live daemon audit for the run: `amx daemon audit --duration 30s --json` ran from
epoch `1783985676` to `1783985705`, **15 samples, `healthy: true`, exit 0**;
only informational `closed_tombstone`/`acknowledged_state` findings.

Installed smoke after the fixes: `just install-restart-smoke` passed on installed
binary sha256 `a211873d8765` with `ready: true` and strict `doctor`
**overall: clean**.

Repository gate after the live validation and compatibility fixes:
`just check` **passed — exit 0** (`go test ./...`, `race-core`, `go vet ./...`,
all 33 TUI acceptance scenarios, and `golangci-lint run` with 0 issues).

**Interruption-key correction (recipe fix):** the confirmed-interruption drill
is **Esc** for Codex and Claude, and **`Ctrl+C` during a running tool** for
installed Copilot 1.0.70 (remote `Esc` does not abort a running Copilot tool).
Any recipe claiming an Esc pass for Copilot is wrong for this installed client.

**Installed-client compatibility fixes made during the drills** (each pinned by
a fixture/test, validated by the automated gate below):

- Codex — the installed interrupted screen renders the dim composer placeholder
  `Find and fix a bug in @filename`; the composer/idle detection now tolerates it
  (`internal/screenstate/codex.go`, `codex_test.go`; the regex lived in
  `internal/daemon/codex_prompt.go` when the drill ran). Codex 0.149.x renamed
  that placeholder to `Ask Codex to do anything`, which is now accepted
  alongside it — the list is version-specific by design and an unknown
  placeholder withholds settling rather than guessing.
- Claude — the installed 2.1.195 empty composer contains a non-breaking space
  (`U+00A0`); the composer regex now matches it
  (`internal/screenstate/claude.go`, `claude_test.go`; the regex lived in
  `internal/daemon/claude_prompt.go` when the drill ran).
- Copilot — a late same-session `sessionStart(source:new)` arriving after
  `userPromptSubmitted` no longer resets the interactive flow or drops the
  pending `bolt` (pinned by
  `TestHandler_CopilotLateInitialSessionStartPreservesPromptAndAllowsStop`).
- Readiness — the startup smoke exposed a bug where maintenance successfully
  reconciled after the 400ms startup-snapshot timeout but never called
  `MarkReady`; `internal/daemon/renderer.go` now clears startup degradation after
  the first successful maintenance snapshot
  (`internal/daemon/readiness_test.go`).

**Dialect questions resolved** (carried open from sessions 3–7):

- Codex interrupted-turn marker — `Conversation interrupted - tell the model
  what to do differently` (installed 0.144.3, unchanged in 0.149.1), plus the
  dim placeholder above.
- Codex completed-turn marker — `Worked for Ns` up to 0.144.3; 0.149.x prints no
  duration and instead frames a bare `Completed` line between two horizontal
  rules. Both are recognized; the framed form is honoured only with its frame.
- Claude interruption / empty composer — the installed 2.1.195 idle composer is
  a bare `❯` followed by `U+00A0` (no dim placeholder ghost text on normal
  completion); the `U+00A0` had been defeating the composer regex.
- Copilot user abort — `type: abort`, `data.reason: user_initiated`, with no
  native `agentStop`, re-confirmed live against 1.0.70.

### Stabilized screen-probe drills — OPERATOR-PENDING (2026-08-24)

The screen-probe stabilization work (positive terminal tiers plus a
normalized-screen fingerprint fed to `AuthoritativeIdle.Generation`) is fully
covered by the automated gate, but its four live drills have **not** been run
against an installed binary. They are recorded here as operator work, not as
evidence. Run them against an installed build of 0.8.0 or newer:

1. **Regression.** Put a Claude pane mid-turn (`✽ …ing… (…)` visible, and *no*
   `esc to interrupt`, which Claude 2.1.x no longer renders). `⚡` must hold for
   30s. The pre-fix behaviour was `🛑` in 4–8s.
2. **Recovery still works.** Esc a Claude turn → `🛑` within ~4s (the
   two-sample explicit-marker tier at the 2s probe cadence). Finish a turn with
   `Stop` hooks disabled → `🛑` within ~6s (the three-sample quiescent tier).
   Claude's unmasked footer wall clock can add one further 2s tick when a minute
   boundary lands mid-run; that is expected, not a failure.
3. **Codex parity.** Both of the above against a Codex pane.
4. **Read the decision.**
   ```bash
   amx daemon export-events --current-window --last 50 \
     | jq '.events[] | select(.event=="ScreenStopProbe") | {at,action,source,reason}'
   ```
   Each `reason` must name `tier`, `samples`, and `generation`. For a pane that
   declines to settle, run the daemon at debug level and grep
   `screen stop probe: not settleable` for the named vetoes and the fingerprint.

Drill 3's blocker is CLEARED. It read: `codexComposerLineRE` did not recognize
codex-cli 0.149.1's `Ask Codex to do anything` composer placeholder, and 0.149.1
emits no `Worked for Ns` line, so a real 0.149.1 pane could not settle a
synthetic stop at any sample count. Both halves are now supported
(`internal/screenstate/codex.go`):

- the composer placeholder list accepts `Ask Codex to do anything`, still as a
  whole-line match against a known string — arbitrary text after the chevron is
  rejected, because a transcript prompt line reading as an empty composer would
  satisfy the positional composer scan mid-turn;
- `codexCompletedMarkerLineRE` accepts 0.149.x's `Completed` marker, but only
  when a horizontal rule stands above and below it. `Completed` alone is one
  ordinary word with no duration to anchor on; the frame is what makes it
  harness-authored.

Every other gate is unchanged: the composer must still follow the marker, a
later live-work line still clears it, a visible prompt still outranks it, and
the settle still costs two samples agreeing on one screen fingerprint. Real
captures now drive all three paths —
`internal/screenstate/testdata/codex_idle_completed_t0.txt` (terminal),
`codex_interrupted_no_background.txt` (interruption recovery, captured against
codex-cli 0.149.1 with nothing left running) and `codex_cleared.txt` (clear
recovery) — pinned by `TestCodexCurrentScreenContract`
(`internal/screenstate/codex_test.go`) and `TestProbeCodexCurrentCompletedScreenSettles`
(`internal/daemon/codex_idle_samples_test.go`).

One known gap remains and belongs to repair group `codex-background-task-veto`,
not here: `internal/screenstate/testdata/codex_interrupted.txt` reports
`1 background terminal running` and nothing vetoes that footer, so such a pane
settles today. Drill 3 is otherwise runnable as written.

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
