# Desktop workflow improvements

Implementation checklist for the seven improvements requested together. A checked
item requires working UI, appropriate failure handling, and verification; adding
a model or routing to a placeholder is not completion.

- [x] Decision inbox: question/output context, permission/question/error/completed
  filters, optional oldest-first order, and next-waiting navigation after answering.
- [x] Command palette: explicit action mode, launch/resume/terminal/review/run-plan
  actions, shortcuts, unavailable reasons, and existing action authorization.
- [x] Changes: checkout-scoped files and statistics, selectable comparison base,
  readable diff, open in editor, and managed-worktree review/landing workflow.
- [x] Two panes: agent/shell, agent/agent, agent/diff, explicit focus, swap and
  focus-other commands, safe attachment and lifecycle behavior.
- [x] Native history: backend search, transcript preview, conversation/project/all
  scopes, focus live/resume/fork actions.
- [x] Runs: all runs including paused without questions, actual session progress,
  timing, blockers, verification evidence, and permitted controls.
- [x] Since last looked: recorded transitions and excerpts, durable read boundary,
  event navigation to supporting output or conversation.

## Validation

Use hosted macOS tests for model, routing, and view behavior, plus focused Go tests
for any new CLI/daemon contracts. Follow with the full desktop suite and build.
Record actual command outcomes here as implementation proceeds. Preserve existing
attachment ownership, stale-feed handling, and ASL action-token rules.

## Progress on 2026-10-15

Implemented in the working tree:

- Inbox category filters, stable optional oldest-first ordering, read-only expandable
  context, stale-response rejection (including another question on the same ASL run),
  and Next waiting navigation. Answer panels retain their existing authority.
- Command palette action mode (`>` / ⌘⇧P), project launch, conversation focus/resume,
  shell/navigation/settings, native history, checkout review, ASL Runs, and run-plan
  actions.
- Native history window with scoped backend transcript search, previews, earlier
  messages, and focus/resume/fork. Late reads are discarded; duplicate pending
  launches are blocked and failures stay visible.
- Native Changes window and reusable Changes view, plus `amx session changes`.
  Checkout scope, arbitrary commit/ref comparison, statistics, per-file bounded
  diffs, untracked/binary paths, editor opening, managed worktree review and confirmed
  landing are implemented, including review beside the agent in a second pane.
- Two-pane agent/shell, agent/agent, and agent/Changes layouts with visible focus,
  click-to-focus, focus-other, swap, and hide-other actions. Existing terminal views
  move between containers without new attachments. Detached/failed/ended inactive
  sessions remain explicit; utility programs survive hiding. Closing the workspace
  detaches both agents and resets to one pane. Review cannot close during landing.

- Native ASL Runs lists the full bridge projection, including questionless pauses,
  with permitted controls, fresh authorization and operation-ID receipt retries.
  `amx plans status` supplies actual session progress and bounded, run-scoped recorded
  verification. A native plan-launch form previews preflight findings and starts a
  managed supervising session through the existing `plans run` command.

- Since Last Looked uses the durable daemon journal, with explicit read markers,
  retained-history limits, timestamp-matched transcript excerpts, earlier-message
  loading, and exact conversation/live-session navigation. Late-spooled records
  remain unread. No transcript content is persisted by the read-marker store.

The final audit covered all seven integrated entry points, unavailable/stale
states, asynchronous selection changes, and ownership of terminal attachments.
Worktree confirmations now retain and recheck the review shown to the operator;
ASL controls retain their fresh authorization and exact operation receipt logic.

Evidence so far:

- `macos/scripts/test-app.sh`: 778 tests executed, two visual tests skipped, zero
  failures (Debug, local Mac), including pane lifecycle and geometry and Runs tests.
- Final focused Runs/plan-launch run: 16 tests passed, zero failures, covering
  questionless pauses, fresh authorization, changed confirmation targets, exact
  receipt retries, run-scoped details, changed launch options, and ambiguous launches.
- Final pane/window/switcher run: 72 tests passed, zero failures and no constraint
  conflicts, including the added hidden-review recovery test (13 pane tests total).
  Visual capture is still unavailable.
- `go test ./internal/gitstate ./cmd/session`: passed, including actual Git fixtures
  and live-pane directory resolution without attaching.
- `go vet ./internal/gitstate ./cmd/session`: passed.
- `go test ./internal/planrun ./cmd/plans` and matching `go vet`: passed for run
  status, run-scoped verification projection, bounded reads, and disabled integration.
- An isolated smoke test of `go run . plans status --json -- TEMP_PLAN` against the
  installed ASL returned schema v1 with the fixture's actual one complete and one
  pending session. No run was started and no user plan was read or changed.
- `go run . session changes --directory . --base HEAD --json`: schema-v1 result
  naming this checkout and its changed files.
- The two visual fixture tests now use scoped window capture because NSView/layer
  capture omitted SwiftUI-rendered contents. The targeted run skipped both: window
  capture is unavailable in this environment. Visual appearance has not been
  verified by screenshots; earlier incomplete captures are not validation evidence.
  Hosted window, view, routing, and pane geometry tests passed.

No installation, commit, or release has been performed.

## Final validation

- `macos/scripts/test-app.sh`: 787 tests executed, two screenshot tests skipped,
  zero failures. Builds the Debug app and runs hosted XCTest, including all seven
  activity tests and existing terminal lifecycle coverage.
- After the final worktree-confirmation fix, a focused Changes/panes/activity run
  executed 24 tests with zero failures (four Changes, thirteen panes, seven activity).
- `env -u AMX_RUNTIME_SESSION_ID go test ./...`: passed. The initial unsanitized
  run failed existing hook tests because this agent's runtime identity leaked into
  their isolated daemon fixtures; removing that inherited variable resolved them.
- `go vet ./...`: passed.
- Activity CLI fixture tests read a real temporary durable journal without a
  daemon, verify schema-v1 output, and reject unbounded queries. Projection tests
  verify noise filtering before the limit, stable IDs, truncation, and exits.
- `git diff --check`: passed.

All seven requested improvements are implemented. Screenshot inspection remains
an environmental validation limitation. No installation, commit, or release was
performed.

## Follow-up polish on 2026-10-15

This pass improves the existing workflows above. It also resolves the earlier
screenshot validation limitation using current-process ScreenCaptureKit capture.

- [x] History opens a bounded window around the actual matching prose message,
  including old matches outside the latest 50 messages. Terms are highlighted;
  previous/next matching-message navigation and earlier/later paging retain a
  stable message anchor. Refreshing the same search preserves the open preview.
- [x] Changes shows old/new line numbers, addition/deletion shading, and hunk
  boundaries with previous/next controls. Text remains selectable, and each
  checkout/base/file retains its scroll position.
- [x] Waiting groups decisions and errors ahead of completions with section
  counts. Hovering or reading context holds the list in place. Changed questions
  immediately lose their actions; explicit updates apply the fresh order.
- [x] The palette accepts abbreviations and aliases, highlights matched title
  characters, ranks exact names first, and then favors available contextual
  actions. Unavailable entries retain their explanations and action checks.
- [x] The TUI profile picker uses compact profile/harness rows with only the
  selected home expanded below. It fits narrow and short terminals, follows the
  cursor through long lists, and keeps harness-change warnings visible. The
  empty dashboard offers launch, configuration, history, commands, and help.
- [x] History, Changes, Activity, and Runs share heading, loading, error/retry,
  and empty-state treatments. Failed reads are distinct from empty results;
  retained results are labeled. ASL's exact **Retry Receipt** behavior remains.
- [x] Hosted visual tests cover populated, empty, loading, and failed states in
  light/dark appearances with long names and paths. Geometry assertions reject
  unexpectedly enlarged windows; OCR checks that key labels and actions are
  actually rendered. Screenshot review caught and fixed an error-notice layout
  that forced windows taller than their minimum size.

### Reviewed reference captures

Each contact sheet contains History (upper left), Changes (upper right), Activity
(lower left), and Runs (lower right). The images use fixture data and retain
their native pixel dimensions. These are review references, not pixel-exact
goldens; locale, system fonts, and scrolling appearance can vary between Macs.

| State | Light | Dark |
| --- | --- | --- |
| Populated | [Capture](acceptance/workspace-polish/review-populated-light.png) | [Capture](acceptance/workspace-polish/review-populated-dark.png) |
| Empty | [Capture](acceptance/workspace-polish/review-empty-light.png) | [Capture](acceptance/workspace-polish/review-empty-dark.png) |
| Loading with retained results | [Capture](acceptance/workspace-polish/review-loading-light.png) | [Capture](acceptance/workspace-polish/review-loading-dark.png) |
| Failed read with retained results | [Capture](acceptance/workspace-polish/review-failed-light.png) | [Capture](acceptance/workspace-polish/review-failed-dark.png) |

Additional references: [History search](acceptance/workspace-polish/history.png),
[History at minimum size](acceptance/workspace-polish/history-dark-minimum.png),
[Changes](acceptance/workspace-polish/changes.png),
[Changes at minimum size](acceptance/workspace-polish/changes-dark-minimum.png),
[Waiting and palette, light](acceptance/workspace-polish/waiting-palette-light.png),
[Waiting and palette, dark](acceptance/workspace-polish/waiting-palette-dark.png).
The TUI's normalized 80×24 renders show the
[selected profile detail](acceptance/workspace-polish/tui-picker-80x24.txt) and
[empty dashboard](acceptance/workspace-polish/tui-empty-80x24.txt).
[Capture provenance and hashes](acceptance/workspace-polish/manifest.json)
record the fixture source, sizes, and review criteria.

### Verification

- `env -u AMX_RUNTIME_SESSION_ID just check`: passed, including all Go tests,
  core race checks, vet, 175 TUI scenarios, and lint (zero issues). The full gate
  also exposed two existing lint issues in activity projection and verification
  log cleanup, which were corrected without changing their behavior.
- `macos/scripts/test-app.sh`: 802 hosted tests passed, zero failures or skips.
  This builds the Debug app and exercises the existing terminal lifecycle and
  ASL authorization/receipt checks alongside the new interaction coverage.
- Final `WorkspaceWorkflowPreviewTests` run: four tests passed, zero failures
  or skips, producing 38 captures. Minimum sizes include the title bar. The
  references above were inspected in both appearances; overflowing detail bodies
  scroll while their primary actions remain visible.
- Focused interaction tests verify an old match outside the tail, anchored
  paging in both directions, real diff scroll restoration and hunk visibility,
  palette ordering/Unicode highlights, and held-list stale-question disabling.
- Visual tests capture only their own fixture windows. They require macOS 14.4
  or later and skip on older versions; model and interaction tests still run.
- `git diff --check`: passed.
