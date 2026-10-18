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
