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

> Every turn transition below was recorded before the confirmed-idle stop hold
> existed, when a native terminal hook painted `🛑` immediately. The hooks and
> the settled icons are unchanged; only the timing moved. Re-running any of
> these drills today, the tab holds `⚡` after the terminal hook and paints `🛑`
> once the harness's own idle probe confirms (or at the hard deadline). See
> [Cross-harness stop convergence](#cross-harness-stop-convergence-session-7).

- **Claude turn transitions** — new managed tab: `⚡` on prompt submit, `🛑`
  at turn end (`UserPromptSubmit→active→bolt`, `Stop→stop→stop`); under the
  stop hold the same `Stop` is withheld and `🛑` lands after the quiet period.
- **Three concurrent subagents with interleaved parent activity** — three
  `SubagentStart`/`SubagentStop` pairs with parent `PostToolUse` interleaved;
  `🤖` held for the whole subagent window (precedence over the parent bolt)
  and resolved to a clean `🛑` with no stranded robot icon.
- **Codex turn transitions** — `⚡` → `🛑` within ~2s on a trivial prompt; under
  the stop hold the same turn converges after the quiet period instead.
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
  trivial prompts and once through a real blocking tool permission; under the
  stop hold `agentStop` is withheld until the transcript probe confirms. The first
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

## Cross-harness route diagnostics (session 6)

The shared `doctor`/`daemon audit` evaluator now turns the previously healthy
OrderBook suppression burst into stable structured findings. The automated
contract uses a 19-event synthetic trace (one prompt plus 18 non-prompt
`owner_mismatch` suppressions on one window) and pins the same check, severity,
window, 15-minute scope, and exit behavior in text doctor, JSON doctor, and
repeated audit samples.

| Stable check | Severity | Trigger |
| --- | --- | --- |
| `owner_mismatch_prompt` | failure | Any direct `UserPromptSubmit` or `userPromptSubmitted` suppressed as `owner_mismatch` within 15 minutes. |
| `owner_mismatch_burst` | warning | Three or more non-prompt `owner_mismatch` suppressions on one window within 15 minutes. |
| `unknown_producer_fact` | warning | An unacknowledged window-scoped legacy fact contributes an icon or live lease without a known producer harness. |
| `acknowledged_fact_pressure` | warning / failure | 49–64 evictable acknowledged nonterminal facts warn; more than 64 fails. Live ASL leases, terminal history, and unacknowledged facts are excluded. |

Automated evidence lives in `cmd/debug/invariants_test.go`,
`cmd/debug/doctor_strict_test.go`, and `cmd/debug/daemon_audit_test.go`. Correctly routed
cross-harness traces carry `route_decision: "owner"`, retain the producer pane
in `origin_window_id`, and produce none of the mismatch findings. Ordinary
acknowledged history remains an informational `acknowledged_state` finding.

A non-interactive harness run in an unmanaged pane (`claude -p`, `codex exec`)
is suppressed as `unmanaged_pipe_source`, not `owner_mismatch`. This is the
expected, benign outcome of a background subprocess in the plain split of a
managed tab, so it is deliberately excluded from both `owner_mismatch_*`
findings and must never contribute to their counts.

For the next installed acceptance run, retain all of the following:

- `amx config hooks-status` showing every generated hook in sync and stamped
  with its producer `--harness` flag;
- `amx doctor --strict` and a repeated `amx daemon audit --duration 10m --json`
  with no new mismatch, unknown-producer, or pressure failures;
- default schema v12 exports for both the managed target and unmanaged producer
  panes, showing trace/request/source-event correlation, producer-derived
  source key/origin, managed target ownership, reducer outcomes, async render
  links, and terminal route continuity after focus moves;
- one `--schema 11` export confirming the v12 busy-stretch anchor is omitted;
- one `--schema 9` export confirming process fields are omitted while causal and
  async-effect fields remain, plus one `--schema 8` export confirming v9/v10
  additions are omitted while retaining the v8 route and producer-origin contract;
- a negative inactive-unmanaged trace that remains `owner_mismatch` without
  causing a prompt failure.

### Cross-harness route repair installed acceptance (2026-07-14)

Session 7 of the cross-harness route-repair plan ran against the real idle
`OrderBook` split tab after installing the cumulative Sessions 1–6 diff. The
installed CLI and daemon both reported version 0.5.0, revision
`e9bc7d16cec0-dirty`, and binary sha256 `6b746e1408e9…`.

| Evidence | Result |
| --- | --- |
| `just check` | passed after repairing the seven lint findings caught by its first run and again after the live retention repair — repository tests, race-core, vet, all 20 committed TUI scenarios, and `golangci-lint run` (`0 issues`) |
| `amx config hooks-sync` then `amx config hooks-status` | updated the shared Codex, both Claude, and Copilot hook files; all 7 configured profiles reported `in_sync`, and generated commands carried their producer `--harness` stamp |
| `just install-restart-smoke` | passed after hook synchronization — atomic install, daemon restart/readiness, exact CLI/daemon build parity, and strict doctor `overall: clean` |
| `amx doctor --strict --json` after the main route | exit 0, `healthy: true`, no findings; daemon metrics showed no rejected, duplicate, or queue-full requests |
| `amx daemon audit --duration 10m --json` | exit 0 — 295 samples from epoch 1784077653 through 1784078252, `healthy: true`, empty findings |
| `git diff --check` | clean |

Live `OrderBook` topology: managed Codex target **28** was stopped, while
unmanaged Claude pane **165** was active. A real Claude prompt ran `sleep 8`
and returned `LIVE_ROUTE_OK`. After the retention repair and final reinstall,
the same real topology ran `sleep 5` and returned `FINAL_ROUTE_OK`.
`UserPromptSubmit` rendered `⚡` in **447 ms**; focus then moved to window 28 at
epoch-ns `1784077629256119000`, before the Claude `Stop` at
`1784077644957944000`. The title and reducer converged to `🛑`/`stop` on target
28.

`amx daemon export-events --window 28 --last 100` returned schema 8 and five
producer-attributed rows (`UserPromptSubmit`, `PreToolUse`, `PostToolUse`,
`PostToolBatch`, `Stop`). Every row used
`source_key: claude:b173b517-d136-4880-8a2f-5e142de7df53`,
`origin_window_id: 165`, and `route_decision: owner`; no row was suppressed.
The routed terminal fact retained `ow: 165`, while durable target metadata
remained `profile: tradition-codex`, `harness: codex`. The producer-pane export
for window 165 remained state-free, confirming that the source did not become
a second renderer. Target 28 retained 7 facts total and only 5 evictable
acknowledged facts, below the 64-fact cap.

Additional installed dogfood:

- Inactive managed window 175 accepted its own stamped Codex prompt as
  `route_decision: source` and reduced to `bolt/bolt` while active owner 28 and
  the shared title stayed stopped. Focusing 175 revealed its bolt; its terminal
  converged to `stop/stop`. The disposable pane was then closed.
- Inactive unmanaged window 166 emitted one stamped Claude `PreToolUse`. Its
  own export recorded `suppression_reason: owner_mismatch`; target 28 stayed
  `stop/stop` at the same effective generation. One non-prompt isolation event
  remained below the burst threshold and produced no doctor finding.
- Disposable ownerless window 176 preserved stamped Codex identity through
  prompt and terminal: durable harness `codex`, source key
  `codex:ownerless-codex-s7`, origin 176, `route_decision: source`. It was
  closed after convergence.
- An independent live exact-ASL sibling route remained active without
  intervention: unmanaged Claude pane 144 carried live run
  `20260714T232728347326Z-27822` into managed Codex target 143. Schema-8 facts
  and ASL heartbeats retained `ow`/`origin_window_id: 144`; ordinary Claude
  tool rows were owner-routed and the target showed `⚡🔁` with zero
  `owner_mismatch` rows.

The first 10-minute audit attempt exposed one otherwise-idle acknowledged fact
on window 112 crossing the one-hour boundary without being compacted. The
watchdog now applies fixed retention compaction on every tick independently of
configurable stale thresholds, advances the reducer receipt, and marks an
invisible retention-only mutation dirty for persistence. The new integration
test is
`TestSweepStale_CompactsAcknowledgedFactsWithoutStaleThresholds`. After the
final reinstall, window 112 exported zero facts and zero render failures.

Two setup outcomes were intentional and resolved, not flaky product failures:
the first installed smoke ran before the required hook upgrade and therefore
failed strict doctor on seven drifted profiles; after `hooks-sync`, the exact
smoke command passed. The first remote prompt injection placed text in Claude's
composer without submitting it; a real remote Enter key submitted the same
prompt and produced the accepted trace above.



## Cross-harness stop convergence (session 7)

Session 7 is the acceptance gate for the reliable stop-state convergence effort
(Codex, Claude, Copilot). The reducer now carries interactive ownership
(`pm`), race-safe authoritative-idle observations, and harness-specific stop
fallbacks. Native hooks remain primary; the fallbacks only repair a stuck `⚡`
that never received a native terminal.

### Exact expected states

| Scenario | Codex | Claude | Copilot |
| --- | --- | --- | --- |
| Normal turn completion | native `Stop` → held at `⚡` until probe-confirmed or the deadline → `🛑`; the confirming evidence is the same completion marker (`Worked for …`, or 0.149.x's rule-framed `Completed` line) + empty composer (two samples at the same screen fingerprint) the `ScreenStopProbe` uses, released as `stop_confirmed_by: probe:codex_screen` | native `Stop` → held at `⚡` until probe-confirmed or the deadline → `🛑`; confirmed by the same idle composer (two samples) the `ScreenStopProbe` uses, released as `stop_confirmed_by: probe:claude_screen` | `agentStop`/`sessionEnd` → held at `⚡` until probe-confirmed or the deadline → `🛑`; confirmed by the same terminal transcript record the `TranscriptStopProbe` uses, released as `stop_confirmed_by: probe:copilot_transcript` |
| Confirmed user interruption | `ScreenStopProbe` after the TUI interrupted marker + empty composer (two samples at the same screen fingerprint) → `🛑` | `ScreenStopProbe` after Claude's interruption marker (two samples at the same screen fingerprint) → `🛑` (no native `Stop` fires on interruption) | `TranscriptStopProbe` after a terminal/abort transcript record → `🛑` |
| Protected active states | `key`/`question`/`error`/`warning`/`subagent`/`compact`, pipe-mode `pm:true` facts, and live ASL leases are **never** downgraded by any stop fallback | same | same |
| New prompt after stop | `UserPromptSubmit` → `⚡` (reducer recency supersedes the stop fact) | `UserPromptSubmit` → `⚡` | `userPromptSubmitted` → `⚡` |
| Live ASL between turns | the loop lease masks `🛑` and shows `🔁`/its active/attention state; `🛑` appears only after `AgentSessionLoopFinished` | same | same |
| Background agent idle | n/a | `agent_idle` → `🛑` (Claude semantics unchanged) | `notification.agent_idle` is **ignored** (background-agent), never a main-session stop; the interactive `⚡` is preserved |

The hold applies to every native terminal that reaches the gate, not just
`Stop`: `SessionEnd`/`sessionEnd`/`agentStop` and Claude's idle notifications
are all withheld the same way. It is bypassed at arm time for pipe-mode panes,
harnesses without an idle probe, windows with a live ASL lease, a daemon with no
Kitty client, and `[defaults.stop_confirm] enabled = false`. A bypass admits the
native terminal to the reducer without the confirmed-idle wait, which restores
the pre-gate `🛑` everywhere except under a live ASL lease: there the lease keeps
masking the stop icon exactly as the *Live ASL between turns* row above
describes, so `🛑` still appears only after `AgentSessionLoopFinished`. A
verified harness-process exit bypasses an already-armed hold and paints `🛑`
instantly; that exit's `HarnessProcessExited` row keeps
`synthesized_stop: true`, because the reducer stop is the exit's own, but reports
`terminal_hook_evidence: observed` — the retired hold is proof the native hook
did arrive. While a hold is armed the window keeps its active icon and fires no
stop notification; a
`TerminalStopHold` row records the `held` outcome, and a `superseded` or
`bypassed` row records a hold that ended without ever painting.

An armed hold is deliberately absent from every other health surface — the
reducer never saw the stop, so nothing is late and nothing mismatches. Its only
projection is `shadow.pending_terminal_stop` (`armed_at`, `quiet_until`,
`deadline`, `confirmed`, `class`), live at export time and gone the moment the
hold releases. `amx doctor` reads it for one **warning**-severity check,
`stop_hold_stuck`: a terminal hold still armed at twice the configured
`[defaults.stop_confirm] timeout` has outlived its own deadline and every
release path, so the window is showing an active icon for a `🛑` that will
never paint. It stays a warning — a withheld stop is not a corrupt one — and
the healthy verdict is unaffected.

Do not read a released hold's `stop_confirmed_by` as a lost hook. It names the
evidence that let a **native** stop paint early, and the fact it commits is the
harness's own. Only a synthetic stop *fact* whose `source` is `probe:<harness>`
means the native hook was lost.

Every synthetic stop records its source (`probe:<harness>`) and the **actual
reduced icon** in `action` (a masked probe records the icon that actually won),
and its `reason` reads `authoritative idle (<harness>) settled visible state
"…"→"…" (tier=<tier> samples=<n> generation=<fingerprint>)` — the grade of
screen evidence, the run length that grade required, and the normalized screen
fingerprint every sample in that run agreed on. The suffix is diagnostics only;
the session-event shape and the `debug` schemas are unchanged. A probe result is
discarded if any reducer event lands between evidence capture and application.

### Automated coverage

These convergence expectations are pinned by the automated gate:

- `internal/daemon/trace_replay_test.go` — full hook-path replay (waypoints,
  duplicate-delivery idempotency, and restart-equivalence after every prefix)
  for native single-turn completion, prompt-after-stop (`claude-stop-then-
  reprompt`, `copilot-stop-then-reprompt`), stale interactive-source
  replacement (`codex-session-replacement`), permission-and-stop, compaction,
  subagents, and ASL lifecycle/overlap across all three harnesses.
- `internal/daemon/authoritative_idle_test.go` — the fallback stop path:
  settling a stale interactive bolt, discard-on-moved-sequence, reset-flow
  block, consecutive-sample stabilization, and protected/background masking
  (live ASL lease, attention icon, pipe-mode child, running subagent).
- `internal/daemon/state/interactive_ownership_test.go` — interactive vs.
  pipe-mode ownership, cross-harness terminal retirement, session replacement,
  and durable `pm` round-trip/restart.
- `internal/daemon/{codex,claude}_prompt_probe_test.go`,
  `internal/daemon/copilot_transcript_probe_test.go` — per-harness completion
  and interruption classification with fail-closed handling of active/unknown
  evidence.

Continued in [daemon-state-manual.md](daemon-state-manual.md).
