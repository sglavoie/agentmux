# Daily-driver trial log

Written by the operator, one entry per workday of real use, on that day. See
[docs/daily-driver-trial.md](../../../daily-driver-trial.md) for what to cover
and how the trial closes. Nothing here is filled in by a test run.

No day has been recorded yet.

## Reported interruption: 2026-10-17

This is an operator report and bounded diagnostic investigation, not a completed
trial workday.

- F-001: operator initially reported lingering Codex bolts, then clarified that
  the bolt eventually disappears without a stop icon, specifically in `tt`.
- Installed CLI/daemon: 0.8.0, revision `47f7e166053a-dirty`, built
  `2026-10-17T21:34:55Z`. App build was not recorded for this observation.
- Read-only inspection found a completed response and Ready composer in `tt`.
  The runtime registry retained the original resumed conversation, whereas the
  displayed response was in a newer native conversation. Journal records for
  the newer conversation reported `codex_conversation_unbound`; its runtime
  slot had no completion event or effective icon.
- This explains the missing completion state. The original bolt's delay was not
  timed, and its exact clearing transition was not retained in this snapshot.
- [Sanitized diagnostic evidence](f-001-evidence.json) retains the blank runtime
  state, the newer conversation's rejected Stop, and the registry binding,
  without terminal contents or unrelated sessions.
- Fix prepared: supported managed Codex launches use `--no-daemon`, preserving
  terminal ownership across conversation changes. Existing clients require a
  relaunch after installation. No live client was stopped during investigation.
- Recurrence, recovery duration, installed fix build, and subsequent operator
  confirmation remain unrecorded.

## Trial preparation: 2026-10-30

This is an agent-observed setup check, not an operator workday. The trial
record remains pending; no dates or case completions are inferred from it.

- Installed CLI at preflight: `amx` 0.8.1, revision `bfddead72b72-dirty`.
- The app already had **Workspace (tmux)** selected and **Open the workspace
  at launch** enabled. CLI configuration had no `launch_backend` key, so its
  effective destination was Kitty. Added `launch_backend = "tmux"` under
  `[defaults]`, preserving the rest of the configuration and saving an exact
  private backup beside it as `config.toml.before-desktop-trial-*.bak`.
- `amx config validate` and `amx backend status --check` passed after the
  change. The same private server was running, with seven agents in eight
  sessions. Its previously selected tmux executable remained in use.
- Preflight doctor completed with warnings: three configured project
  directories were missing, Copilot hook activation was unverified, and 179
  Codex hook deliveries in the trailing hour reported an unbound conversation.
  Those deliveries named two conversations absent from the current runtime
  registry; their ownership has not been established. These observations do
  not prove loss or misrouting of a managed session. Deliberate hook refusals
  outside Kitty were also present and are excluded by doctor's delivery-failure
  counter.
- Native validation on the previously used MacBook host compiled the test
  target but could not execute it: XCTest could not connect to
  `com.apple.testmanagerd.control`, and the test account had no GUI login.
  The operator then selected the local Mac Studio for hosted tests.
  `just mac-test` and `just mac-test Release` each passed 1,021 tests with
  zero failures. Local full logs are in `macos/DerivedData/test-Debug.log`
  and `macos/DerivedData/test-Release.log`.
- F-002: the operator reported that the green sidebar state-change flash
  was hard to notice and requested twice the duration. The pulse was extended
  to 1.6 seconds total, or one 0.8-second pulse with Reduce Motion.
  Reveal in Sidebar retains its
  original duration. This is implementation evidence, not operator confirmation
  that state-change visibility is sufficient during daily work. Recurrence
  and recovery effort have not been measured; confirmation on the installed
  change is pending.
- `just check` exposed a notification handoff race: an obsolete notice could
  escape withdrawal when cancellation arrived before the app backend returned
  from delivery. Registering it before delivery and deferring withdrawal until
  acceptance fixes that interval. Deterministic tests cover cancellation both
  before and after backend acceptance and preserve `FinishCancellation`'s
  deferred-effects contract. The repaired `just check` passed, including the
  daemon race suite and both lint builds.
- The Release app was built, signed, and installed. Its executable SHA-256
  is `5f27024a5df4882e8c11a6b9e5d900fc95834a02ab345db49c959c6dc04695e3`.
  `just install-restart-smoke` installed the matching CLI and restarted the
  daemon successfully. The standalone and app-bundled CLI have the same Go
  build ID; signing gives them different file hashes. Installation preserved
  the session-server generation and the count of seven running agents.
- After reopening the app against the updated CLI, `amx doctor --strict`
  reported healthy with no companion-build mismatch and no hook deliveries
  lost since the daemon restart (eight minutes observed). The existing
  Copilot activation and missing-project-directory warnings remained.
  CLI launches still selected tmux; the app retained its workspace destination
  and open-at-launch preference.

## Workday template

Use a stable friction ID (`F-001`, `F-002`, …) when the same interruption recurs.
Record each occurrence below, then update [the friction register](friction.md).
Record zero-friction days too; do not reconstruct dates or elapsed time from
memory as measured evidence.

<!--
## YYYY-MM-DD

- Build: `amx --version` and the app's version
- Profiles and harnesses used:
- Cases touched (multiple-profiles, all-harnesses, permission-prompts,
  long-running-jobs, asl, overview, conversation-continuity, kitty-fallback):
- Friction occurrences:

  | ID | Task and entry point | Expected / observed | Occurrences today | Extra steps | Recovery time (measured or estimated) | Evidence |
  | --- | --- | --- | --- | --- | --- | --- |
  | | | | | | | |

- Context lost (conversation message, selected event, file/hunk, pane focus):
- Launch destination: expected / actual / was the source clear?
- Keyboard review: task and any pointer-only detour:
- Recovery: was the original failure still readable and the next step clear?
- Polish observations (destination clarity; retained failures; keyboard review):
- Failures (what was on screen; bundle or export saved as):
- Fixes installed today (commit; checks run again):
-->

## F-001 confirmation, 2026-10-18

The operator answered **“Tested all paths; they work”** when asked specifically
about completion after resume, app reopening, physical sleep/wake, clearing the
previous completion on `/new` before another prompt, and completion after the
new response. The installed CLI and app were checked against the previously
recorded fix before asking. [Build identity and confirmation](../../workspace-consistency/observations.json)
close F-001. This is a defect-verification observation, not a completed trial
workday or confirmation of the five-workday migration trial.
