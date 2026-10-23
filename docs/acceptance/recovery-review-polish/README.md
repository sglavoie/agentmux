# Recovery and review polish

Implementation record for the five follow-ups requested on 2026-10-23.
This pass changes existing recovery, review, Waiting, feedback, and setup flows.

## Implemented behavior

| Item | Behavior | Verification |
| --- | --- | --- |
| Native recovery | Ended/lost sessions offer Resume Conversation. The native panel reads the exact source, displays profile and workspace destination, rechecks source identity/profile/checkout on submission, and uses History's live-focus/resume path. Opening or cancelling never launches. | `WorkspaceRecoveryTests`, `WorkspaceResumeTests`, native panel geometry and fixture captures; existing CLI continuity coverage in `just check` |
| Large-diff restoration | Initial display keeps visible-region layout. Deep restoration, wrapping changes and restored-view resizing prepare a detached TextKit graph on a worker queue and transfer it to a new native text view. Newer requests invalidate pending layouts. Reading anchors and unchanged-source selections survive replacement; offsets are clamped when wrapped lines become shorter. | `WorkspaceSeamsPolishTests`: deep reopening, 12 repeated returns with resize/wrap changes, 30 rapid selections, final-hunk navigation, superseded preparation and main-loop timing; existing diff regression suites |
| Waiting hierarchy | Expanded cards remove repeated reason text, put Refresh beside the answer/context controls, move freshness below the question, and lighten the background. Failed reads keep visible Retry and retained-content status. Refresh hit testing no longer opens the session. | Waiting context/lifecycle tests, narrow light/dark captures, stale-question refusal and keyboard focus coverage |
| Durable failures | Up to 30 outcomes; unresolved errors/warnings persist in app preferences. Identical notices coalesce with count/time; successes cannot evict failures. Dismissal persists. Restored recovery reads the original target; old receipts open fresh run state. | `WorkspaceFeedbackPersistenceTests`, existing feedback tests, restored-feedback captures |
| Guided setup | The empty project catalog offers Set Up First Project. The embedded utility runs existing config setup with session identities removed, keeps its summary/errors until Return, then refreshes projects and backend status in the native workspace. No automatic launch. | `WorkspaceResumeTests`, `WorkspaceDailyPolishTests`, Go setup/wrapper tests and an isolated CLI smoke through `workspace-view setup` → `project list --json` |

The shared code-copy control is now a native button. Hosted accessibility tests
read its role/label and invoke its action through the native accessibility API,
then verify the isolated pasteboard. This is automated control coverage, not a
claim of a manual VoiceOver walkthrough.

## Performance

The 6,000-line long-source fixture retains the same deep source line through
wrapping, resizing and reopening. Full layout still takes time, but that work
runs on a detached graph with no displayed text view and automatic background
layout disabled, following Apple's [TextKit threading guidance](https://developer.apple.com/library/archive/documentation/Cocoa/Conceptual/TextLayout/Concepts/LayoutManager.html).

Final full-suite measurements on this Mac (Debug):

| Fixture | Measured result |
| --- | --- |
| Deep wrap change and reopening | Maximum main-loop gap **34.1 ms**; same source line and selection retained |
| Twelve returns with resize/wrap changes | Maximum main-loop gap **35.3 ms**; source anchors retained |
| Thirty rapid initial selections | Maximum main-loop gap **41.9 ms**; final hunk reached |
| Attributed text construction | 17.2 / 17.6 ms, unwrapped / wrapped |
| Initial visible-region layout | 6.0 / 2.9 ms, unwrapped / wrapped |
| Complete standalone TextKit layout | 232.1 / 383.0 ms, unwrapped / wrapped; restoration performs this work off-main |

These are fixture measurements, not installed-app or cross-machine guarantees.
The interaction tests enforce a 120 ms main-loop budget for deep restoration and
repeated returns, in addition to their source-anchor and selection assertions.

## Visual references

Captures contain synthetic data, made through the test process's own windows.
The capture tests check geometry and visible labels with OCR. The images are
review references, not pixel-perfect goldens.

- Native recovery: [light](resume-light.png), [dark](resume-dark.png).
- Waiting: [loaded light](waiting-light.png), [loaded dark](waiting-dark.png),
  [retained failure](waiting-failed-dark.png).
- Restored feedback: [light](feedback-light.png), [dark](feedback-dark.png).
- [Capture and source hashes](manifest.json).

## Verification

- `env -u AMX_RUNTIME_SESSION_ID just check`: passed, including Go tests, core
  race checks, vet, TUI scenarios, and lint in both build modes (zero issues).
  A pre-existing render-equivalence test now fixes its clock so crossing an age
  boundary between equivalent renders cannot fail the race suite.
- Isolated `go run . workspace-view setup` with a temporary configuration,
  profile and checkout: passed. It saved exactly one Codex project; the subsequent
  native catalog command returned its expected profile and directory. Hooks were
  explicitly declined; no agent was launched. Missing-tmux readiness remained
  visible in the setup summary.
- `macos/scripts/test-app.sh`: **983 tests passed**, zero failures. Result bundle:
  `macos/DerivedData/Logs/Test/Test-AgentMux-2026.10.23_09-03-29--0600.xcresult`.
  This includes all native workflow captures, recovery, feedback persistence,
  actual accessibility copy action, and diff selection/restoration regressions.
- `just mac-app Release`: passed; built and signed
  `macos/build/Agent Mux.app`. The build's signature verification passed.
- `git diff --check`: passed. Captured-image and source hashes match the manifest.

## Operator observations

No installation or real-work trial is claimed. The existing daily-use and backend
migration trials remain pending; their required workdays and operator confirmation
cannot be supplied by fixture tests. Launch defaults remain governed by that trial.
