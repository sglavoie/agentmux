# Consistent workspace workflows

Polish pass requested on 2026-10-23. This extends the recovery/review pass with
session recognition, calmer presentation, shared confirmation policy, one primary
conversation browser, and evidence-based feedback resolution.

## Completion evidence

| Requirement | Implementation | Evidence |
| --- | --- | --- |
| Recognize similar sessions | Duplicate display names gain a unique branch or runtime suffix across sidebar, switcher, Waiting, inspector, and dialogs; middle truncation preserves it | `WorkspaceConsistencyPolishTests`, sidebar/window regression tests; light/dark fixture captures |
| Clear visual hierarchy | Neutral rows, small profile markers, system-accent selection, textual status, native completion checkmark | Hosted sidebar capture and interaction checks |
| Predictable confirmations | Follow config.toml or explicit app override, shared with already-open embedded management views; legacy Boolean migration; missing/unreadable policy confirms | Go live-policy tests, native preference and asynchronous target-revalidation tests |
| One conversation starting point | ⌘Y and the older history shortcut open native History; specialized terminal actions retain exact conversation and search; native reader remains intact | Native History and argument tests; Go initial-cache/search integration test |
| Resolved failures | Typed operation/target keys, explicit evidence, persisted resolution, recurrence reopening; unrelated success cannot resolve an issue | Feedback persistence/isolation tests, readiness integration test, existing run receipt and recovery tests |
| Daily-use prioritization | The task checklist below feeds the existing daily log and friction register | Operator observations pending; automated fixtures are not workdays |

## Real-work evaluation

Continue the existing [daily log](../ghostty-desktop/session-20/trial-log.md) and
[friction register](../ghostty-desktop/session-20/friction.md). Use the build actually
installed, recording its revision and any local changes. Do not count staged fixture
runs or inferred outcomes as operator observations.

During ordinary work, capture these when they occur:

1. Identify one of three similar sessions, switch to it, and answer its question.
   Record wrong selections, truncated identities, and extra inspection steps.
2. Scan working, completed, and waiting sessions in the appearance you normally use.
   Record missed completions or ambiguity between selection, profile, and state.
3. End a session from both native controls and the embedded dashboard. Check the
   chosen confirmation policy; record unexpected prompts or missing prompts.
4. Find an earlier conversation, resume or fork it, and use Handoff / More when
   needed. Return with ⌘Y; record lost selection, query, reading position, or focus.
5. Recover from a real failure. Record whether its notice correctly becomes resolved,
   remains actionable when still broken, and survives reopening without confusion.

For each interruption, record **build, task, expected/observed behavior, extra steps,
recovery time (measured or estimated), recurrence, evidence, and fix/recheck**. A day
without friction still gets a log entry. Rank recurring wrong selection and lost
context above isolated cosmetic preferences; use total recovery effort to break ties.

The existing five-workday migration trial, process-survival requirements, rollback
conditions, and operator confirmation remain in force. This pass neither changes
launch defaults nor marks that trial complete.

## Verification status

- `env -u AMX_RUNTIME_SESSION_ID just check`: passed, including Go tests, core
  race suites, both vet builds, the TUI acceptance scenarios, and both lint modes
  (zero issues). Source: `/tmp/amx-polish-check3.log`.
- `macos/scripts/test-app.sh`: 992 tests passed, zero failures. Source:
  `/tmp/amx-polish-native5.log`. The final short-identity adjustment and receipt
  resolution assertions then passed 27 focused native tests; the final rule that
  a later fork cannot resolve an earlier fork's unknown outcome passed all seven
  History tests. Sources: `/tmp/amx-polish-native-focused.log` and
  `/tmp/amx-polish-history-final.log`.
- Native light/dark sidebar captures were visually reviewed: profile markers,
  system-accent selection, explicit state wording, and visible identity suffixes.
  [Light](sidebar-light.png) · [Dark](sidebar-dark.png). These are synthetic fixture
  windows with a blank stand-in terminal, not installed-app observations.
- `just mac-app Release`: passed. The candidate is `macos/build/Agent Mux.app`;
  its signature passed `codesign --verify --deep --strict`. It has not been installed.
- An isolated smoke of the built `amx config confirmation --json` passed with
  inherited true and false settings and schema-v1 output; no agent was launched.
- [Candidate, source, and capture hashes](manifest.json) identify the uncommitted
  worktree used for this pass.
- `git diff --check`: passed.

Installation and real-work evaluation have not been performed as part of this pass.
The real-work trial remains pending and no launch defaults were changed.
