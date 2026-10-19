# Desktop workflow improvements

Current completion-visibility status: **F-001 resolved by operator confirmation
on 2026-10-18**. See [the recorded build and observations](acceptance/workspace-consistency/observations.json).
Earlier pending statements below describe the evidence available during those
passes. The separate five-workday migration trial remains pending.

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

## Reading and workflow polish on 2026-10-16

This pass completes six improvements to existing workflows:

- [x] History renders headings, lists, inline formatting, and fenced code blocks
  with a copy action. Search highlights and message anchors survive formatting;
  unfinished code fences remain readable while a conversation is being written.
- [x] Restarting the app restores both workspace panes, the active side and
  divider, and an embedded Changes review. History remembers its query, scope,
  selection and message position; Changes remembers each checkout's base and
  file, with scroll positions per comparison. Agent attachments retain ownership
  checks; ended utility programs require explicit Reopen.
- [x] TUI action notices retain their time and target. Select a notice to copy or
  dismiss it, inspect its original session, check health, or open configuration.
  An unconfirmed operation is never repeated by inspecting its notice.
- [x] Close recovery lasts 30 seconds after confirmation. The offer says Resume
  when the exact conversation is known and Relaunch otherwise. A missing or
  invalid resume source reports an error without starting a fresh conversation.
- [x] Searchable native choices keep typing, arrow navigation and Return on the
  same available choice. Escape preserves selection and returns focus to the
  opener. Existing review shortcuts and pane focus behavior remain covered.
- [x] Waiting cards wrap long names and place actions on their own row. Review
  toolbars give the content more room, primary actions stand out, and minimum
  windows remain readable in both appearances.

The focused coverage lives in `WorkspaceReadingPolishTests`,
`WorkspaceContextPolishTests`, `tui/feedback_test.go`, and
`tui/commands/recovery_test.go`, alongside the existing workflow, ownership and
recovery tests. Hosted input tests exercise Return and Escape through AppKit;
restoration tests create fresh models and views from isolated preferences.

Reviewed fixtures: [formatted transcript, light](acceptance/workspace-reading-polish/transcript-light.png),
[dark](acceptance/workspace-reading-polish/transcript-dark.png),
[waiting actions, light](acceptance/workspace-reading-polish/waiting-light.png),
[dark](acceptance/workspace-reading-polish/waiting-dark.png),
[minimum History](acceptance/workspace-reading-polish/history-dark-minimum.png),
[minimum Changes](acceptance/workspace-reading-polish/changes-dark-minimum.png),
[TUI notices, 120×32](acceptance/workspace-reading-polish/feedback-120x32.txt),
[60×18](acceptance/workspace-reading-polish/feedback-60x18.txt).
[Provenance and hashes](acceptance/workspace-reading-polish/manifest.json) identify
the fixture sources and review criteria. Captures use fixture data, without live
agent sessions. Earlier acceptance captures remain unchanged.

Validation for this pass:

- `macos/scripts/test-app.sh`: 822 hosted tests passed, zero failures or skips,
  including six screenshot tests. The app and hosted tests build together.
- `env -u AMX_RUNTIME_SESSION_ID just check`: passed the full Go test suite,
  core race checks, vet, 179 TUI scenarios and lint with zero issues. An initial
  run encountered a transient failure in the existing real-tmux switcher test;
  its focused rerun and the subsequent complete gate passed.
- Captures were inspected in light and dark mode; saved artifact hashes and
  documentation links were verified. `git diff --check` passed.

## Review refinement on 2026-10-17

All five follow-up improvements are implemented:

- [x] Refresh the selected transcript without discarding its preview or reading
  anchor. Updated prose replaces the old content; newly appended messages get an
  explicit navigation button. Failed reads retain the preview and the offer.
  Diff refreshes follow matching source text, with a nearby source-line fallback;
  file switches and restarts retain the existing comparison positions.
- [x] Compact the Changes heading, comparison controls, statistics, and file
  toolbar. The populated 720×450 fixture has 259 points of diff viewport height;
  a hosted geometry assertion requires at least 240, without enlarging the window.
- [x] Separate the diff gutter from selectable text. Copying selected source strips
  diff markers and preserves partial Unicode selections. The context menu copies
  unified diff text; the toolbar menu copies the displayed patch or changed source
  lines. Wrapping and text size are remembered across new models/app launches.
- [x] Highlight backend-parsed search terms in result snippets, including quoted
  phrases. Empty results offer concrete scope/query/automation actions; the backend
  supplies an actual hidden-automated-match count under the active scope.
- [x] Filter exact conversations and projects before transcript text reads, load
  native results in pages of 50, preserve selection while appending, and cancel
  superseded read processes. Previously loaded result counts are restored with
  the saved selection. CLI/JSON paging exposes counts and continuation metadata.

### Verification

- `env -u AMX_RUNTIME_SESSION_ID just check`: passed all Go tests, core race
  suites, vet, 185 TUI scenarios, and lint (zero issues).
- `macos/scripts/test-app.sh`: 849 hosted tests passed, zero failures or skips.
  This includes actual subprocess cancellation, copy/Unicode behavior, refreshed
  transcript anchors, result pagination, restored preferences and selections,
  shifted diff anchors, minimum-window geometry, and own-window screenshots.
- Final focused run: 55 tests passed, zero failures or skips, covering the last
  process-wait, gutter-rendering, and singular/plural wording refinements alongside
  refreshed captures. All nine `WorkspaceWorkflowPreviewTests` passed.
- `git diff --check`: passed.

### Performance evidence

On this Mac (Apple M2 Ultra), `BenchmarkHistoryArchiveScope` used 10,000 synthetic
conversations with real warm search sidecars, five iterations per case. Searching
all conversations averaged 210.82 ms with 21.3 MB allocated; exact-conversation
scope averaged 0.010 ms with 1,192 bytes allocated. These are local observations,
not service-level guarantees. An earlier run exposed a 3.4 MB allocation for an
exact lookup; direct indexed lookup removed it.

The hosted large-archive fixture measured about 339 ms from query change to
settled results, including the 250 ms debounce, and about 22 ms to select/read a
conversation. Decoding all 10,000 result rows averaged 41.11 ms; a 50-row page
averaged 0.22 ms. This fixture measures native model/rendering and decoding with
an injected command, separately from the Go sidecar benchmark. It does not claim
end-to-end installed-application latency.

### Reviewed captures

Fresh fixtures use only test data; earlier acceptance images remain unchanged.

- [Compact Changes, minimum window](acceptance/workspace-review-refinement/changes-dark-minimum.png)
- [Larger wrapped diff text](acceptance/workspace-review-refinement/changes-wrapped-large.png)
- [Search result highlighting](acceptance/workspace-review-refinement/history-dark-minimum.png)
- [Actionable empty search](acceptance/workspace-review-refinement/history-empty-scoped.png)
- [New messages without losing the reading position](acceptance/workspace-review-refinement/history-new-messages.png)
- [Capture provenance and hashes](acceptance/workspace-review-refinement/manifest.json)

Offset pages describe a changing archive, not a frozen snapshot. The native view
removes duplicate identities across pages; refresh obtains a fresh ordering.
Source text anchors for diff refresh are memory-only and are never saved in
preferences. Neither tests nor captures launch real agents or install the app.

## Daily workflow polish on 2026-10-17

The six follow-up improvements are implemented:

- [x] Waiting uses the space left by its controls and other rows for context,
  with proportional supervisor questions and monospace terminal output. Window ›
  Waiting (⌘⇧I) explicitly focuses its reader: arrows read items, Return opens the
  existing answer panel, N advances, Space toggles context, and page keys scroll.
  Changed questions remain disabled. Escape restores the previous keyboard focus
  without attaching a detached session, and a pane's default Reattach button
  cannot consume the reader's Return.
- [x] History combines its heading and actions, compacts result metadata and
  message navigation, and retains fixed status slots. Its minimum 720×450 window
  reserves at least 240 points for transcript reading, including at 24-point
  prose size and while refreshing. Earlier/later paging remains available in the
  message navigation menu; the heading's tooltip retains the checkout path.
- [x] History's advertised ⌘R invokes the same transcript read as its refresh
  button, including after a search failure. It preserves selection, focus and
  the reading anchor, and ignores duplicate refreshes while reading. The shortcut
  audit also aligned Activity and Runs refresh shortcuts with their busy buttons,
  and Activity's focus shortcut with its available focus action.
- [x] Activity offers Mark selected read and Mark all N results read, explicitly
  including loaded rows below the viewport. Undo restores the prior persisted
  markers and selection. Failed saves retain the existing state and a failed Undo
  stays retryable; late arrivals remain unread.
- [x] History, Activity and Waiting share remembered prose sizing, independent
  of code/diff/terminal-preview sizing. Open readers update immediately. Fixture
  captures check large text with reachable primary controls in both appearances.
- [x] An empty workspace offers Launch Project, Conversation History and Dashboard
  directly. Launch reuses the existing project menu and action route, with the
  effective destination/source shown in the empty pane. Configuration guidance,
  pending project reads and failed reads are distinct. Existing daemon recovery
  and session restoration retain their own actions.

The new interaction coverage is in `WorkspaceDailyPolishTests` and
`WorkspaceActivityTests`; `WorkspaceWorkflowPreviewTests` captures large-text
History, Waiting, Activity and the empty workspace in light and dark appearances.

Reviewed references:

- [History, large reading text](acceptance/workspace-daily-polish/history-large-dark.png)
- [Waiting, proportional context and keyboard reader](acceptance/workspace-daily-polish/waiting-large-dark.png)
- [Activity, large reading text](acceptance/workspace-daily-polish/activity-large-light.png)
- [Empty workspace actions](acceptance/workspace-daily-polish/empty-workspace-light.png)
- [Capture provenance and hashes](acceptance/workspace-daily-polish/manifest.json)

Validation:

- `macos/scripts/test-app.sh`: 865 hosted macOS tests passed, zero failures or
  skips. The Debug app and tests build together. This includes real key dispatch,
  detached-session focus restoration, stale-question rejection, larger-text
  geometry, read-marker persistence/Undo, and the existing lifecycle suites.
- After removing default-argument actor-isolation warnings, a final focused build
  and run of `WorkspaceDailyPolishTests`, `WorkspaceActivityTests`, and
  `WorkspaceWorkflowPreviewTests` passed all 24 tests, zero failures or skips.
  There were no actor-isolation warnings or layout constraint conflicts.
- `env -u AMX_RUNTIME_SESSION_ID go test . ./cmd/dashboard`: passed, including
  the repository's native menu/function-key contract checks.
- Eight new fixture captures were inspected in light/dark appearances; artifact
  hashes and fixture provenance were verified. `git diff --check` passed.

No installation or release was performed; captures contain fixture data only.

## Review clarity polish on 2026-10-17

This pass refines five existing workflows:

- [x] History gives transcripts most of a narrow window. Its draggable divider
  remembers the preferred width, clamped to the current window without overwriting
  that preference. The sidebar button and ⌘⌥S collapse results and restore keyboard
  focus to the selected result on reopening. Selection and reading position remain
  intact; Down from search also reveals the results. Empty-search actions remain
  readable in the narrower column.
- [x] Runs keeps progress, blockers, and controls above the scrolling evidence.
  Resume and Answer have primary emphasis when applicable. Paths, identifiers,
  timestamps, and verification records live in expandable sections. Confirmation
  buttons say Abort Run or Stop After Current Session and retain the existing
  target and authorization checks.
- [x] Native feedback retains the newest 30 outcomes in memory for the app session,
  shared by History, Changes (including embedded reviews), Runs, and the plan form.
  Every entry keeps its target, time, message, and recovery action, with Copy and
  Dismiss. Recovery reads the original conversation, checkout, or plan options,
  selects the original run when still listed, or explicitly reports its absence.
  Receipt recovery can only check the same pending operation, never a later one.
- [x] Search, transcript, comparison, diff, run-list, and plan-detail refresh times
  are independent. Failed or superseded reads do not advance them. Retained content
  shows its age; clicking the time reveals the exact timestamp and timezone.
  Agent activity timestamps keep their separate meaning.
- [x] Sidebar, Waiting, and menu-bar reasons share readable permission, question,
  completion, and prompt-kind labels. The session inspector preserves raw reason
  codes for diagnostics. Unknown states remain visible with readable separators.

New coverage lives in `WorkspaceClarityPolishTests` and the existing Changes,
Runs, RunPlan, sidebar, and menu tests. The preview suite exercises large History
text, collapsed results, minimum-size Runs, multiple retained failures, and aged
content in both appearances. All captures use fixture data; this pass does not
install the app or claim completion of the operator's daily-driver trial.

Validation:

- `macos/scripts/test-app.sh`: final run passed all 884 hosted tests, with zero
  failures or skips. It builds the Debug app and exercises the existing lifecycle,
  authorization, receipt, keyboard, and restoration suites alongside the new tests.
- `env -u AMX_RUNTIME_SESSION_ID go test . ./cmd/dashboard`: passed.
- Ten new fixture captures were reviewed in light and dark appearances. The
  final geometry check verifies that a long Runs session list scrolls below the
  pinned action area while leaving useful evidence space at minimum size.
- `git diff --check`: passed. No source changes followed the final hosted run.

During validation an existing Ghostty wheel-input test failed once, and a later
run was terminated externally before completion. The final complete run passed
both. A screenshot OCR assertion also missed a visibly rendered Copy button;
the capture was inspected and that brittle label assertion removed while keeping
the feedback layout, target, message, and recovery assertions.

Reviewed references:

- [History with 24-point prose](acceptance/workspace-clarity-polish/history-dark.png)
- [Collapsed History results](acceptance/workspace-clarity-polish/history-collapsed-light.png)
- [Runs at minimum size](acceptance/workspace-clarity-polish/runs-dark.png)
- [Retained action outcomes](acceptance/workspace-clarity-polish/feedback-light.png)
- [Retained-content age](acceptance/workspace-clarity-polish/stale-dark.png)
- [Capture provenance and hashes](acceptance/workspace-clarity-polish/manifest.json)

## Event navigation and workflow polish on 2026-10-17

- [x] Activity opens History at the last timestamped assistant output at or before
  the selected event, including older messages outside the recent window. A short
  highlight identifies the location; absent evidence is explained. Closing this
  History window returns focus to Activity when it remains open. Failed reads can
  retry the same event without losing the requested location.
- [x] Runs shows progress as “8 of 10 sessions complete” and puts the failed check
  above expandable evidence. The failure is scoped to the selected session's
  latest completed verification attempt; a later success cannot expose an old
  failure as current. Missing evidence remains explicit.
- [x] History, Activity, Runs, and Changes share a compact read-status toolbar.
  The popover retains independent read times, errors, retained-result labels, and
  recovery actions. Delayed loading feedback avoids flashing text and moving the
  reading area. Existing action-feedback history remains available.
- [x] TUI filters explain ignored state tokens, suggest `!attention` for `!attn`,
  and explain unfinished quoted project filters without changing their matching
  semantics. Narrow/wide dashboard scenarios and launcher geometry cover hints.
- [x] Preferences lists resolved destinations and sources for app launches, CLI
  launches, and plans in the app. Follow CLI is offered when destinations differ.
- [x] The daily-use log and friction register capture recurrence, recovery effort,
  builds, evidence, and subsequent verification without inventing trial results.
  The operator's `tt` completion-indicator report is recorded as F-001.
- [ ] Five real workdays and post-fix recurrence evidence remain outstanding.

The reported Codex issue exposed a separate launch problem: a shared server's
new conversations have no exact runtime binding after `/new`, so their hooks
are correctly rejected but the terminal loses completion visibility. Managed
interactive Codex launches now select `--no-daemon` when the launched CLI
advertises support, preserving the terminal's hook environment. Explicit remote
transport choices and older CLIs retain their behavior. This prevention applies
after installation and relaunch; no current client was stopped or rebound.

Validation and evidence:

- `macos/scripts/test-app.sh`: 892 tests passed, zero failures or skips. This
  includes event navigation/retry/cancellation, retained reading positions,
  verification-attempt scoping, launch summaries, and minimum-window screenshots.
- Executed launch commands in `sh` and `zsh` with stand-in CLIs: supported/older
  CLI, failed capability read, explicit local/remote transport, profile home,
  runtime ownership, resume arguments, and literal shell-sensitive prompts pass.
- `just check`: passed the full Go suite, core race checks, vet, TUI scenarios,
  formatting, and lint (zero issues). The gate ran detached from the supervising
  Codex server so process-discovery acceptance fixtures had their intended
  ancestry; `AMX_RUNTIME_SESSION_ID` was unset for this test process only.
- `git diff --check`: passed. Screenshot and fixture-source hashes were verified.
- Nine fixture screenshots were visually reviewed. They are fixture evidence,
  not daily-use acceptance: [event location](acceptance/workspace-navigation-polish/event-location-dark.png),
  [independent reads](acceptance/workspace-navigation-polish/read-details-light.png),
  [failed check](acceptance/workspace-navigation-polish/runs-dark.png),
  [destinations](acceptance/workspace-navigation-polish/launch-settings.png), and
  [capture provenance and hashes](acceptance/workspace-navigation-polish/manifest.json).

Changes are prepared in the repository; no installation or release was performed.

## Status, review, and refresh polish on 2026-10-18

This pass implements the six follow-up recommendations against the existing
workflows. Real-use confirmation of the Codex interruption remains a separate,
uncompleted acceptance item.

- [x] Shared review status gives failures precedence over retained results and
  says **Refresh failed**. Slow reads say **Refreshing…** after the existing
  delay; the popover retains previous-content explanations and independent read
  times. The fixed toolbar slot and recovery actions are preserved.
- [x] Runs emphasizes Answer for questions, Inspect Failed Check for verification
  pauses, and Resume for other resumable pauses. The affected pending plan row
  says **Incomplete · verification failed**. Fresh authorization, confirmations,
  and exact operation receipts remain unchanged.
- [x] Diffs highlight changed words in paired replacement lines, omit redundant
  file headers by default, and show the visible hunk number beside navigation.
  **Show patch headers** restores the headers; full-patch copying is unchanged.
  Mode changes, binary evidence, and no-newline markers remain visible. Unicode
  selections, line numbers, reading anchors, and keyboard navigation are covered.
  Intraline work is bounded; large or unpaired blocks keep whole-line shading.
- [x] Runs still polls visible, active summaries every five seconds, but reads
  unchanged plan details every thirty seconds. Changed summaries, explicit
  refresh, and returning to an active visible window trigger fresh reads.
  Hidden/inactive windows suspend the timer. Failed reads retry, skipped reads
  keep their original timestamp, and polling never performs mutations.
- [x] Current documentation and generated TUI help agree on close recovery:
  thirty seconds, Resume for an exact conversation, otherwise Relaunch. The
  binding declaration supplies the shared **recover closed** hint. Historical
  acceptance records retain the behavior they originally observed. The companion
  guide documents the new status, diff, action, and refresh behavior; separate
  app/CLI launch preferences and the migration's compatibility default remain
  explicit.
- [x] The completion-indicator fix is exercised through shell launch selection,
  isolated hook delivery for resumed and new conversations, production reducer
  and screen probes on both backends, an eight-hour simulated observation gap,
  and daemon checkpoint restoration after a conversation change. Codex CLI
  0.163.0 on this Mac advertises `--no-daemon`. Shared-server hooks still refuse
  ambiguous/unbound owners instead of borrowing another terminal's identity.
- [ ] F-001 still needs an installed-build observation across `/new`, resume,
  app reopening, and physical sleep/wake. Tests do not count as operator use;
  the five-workday trial remains pending. No live session was relaunched.

### Validation

- `macos/scripts/test-app.sh`: 901 hosted tests passed, zero failures or skips.
  The first runs exposed outdated screenshot wording expectations and a hunk
  navigation regression; both were corrected before this successful full run.
- `env -u AMX_RUNTIME_SESSION_ID just check`: passed Go tests, core race checks,
  vet, 188 TUI scenarios, and lint with zero issues. The first gate found a
  timestamp-comparison lint issue in configuration polling; it now uses
  `time.Time.Equal`.
- Final focused hosted run: 28 tests passed, zero failures or skips. This covers
  Runs controls, refresh/cancellation, navigation, and the final safeguard that
  unknown verification reasons are not relabeled as failures. An unhosted SwiftUI
  state assertion was removed; hosted loading screenshots retain that coverage.
- `just mac-app Release`: built and signed `macos/build/Agent Mux.app`, bundling
  this checkout's CLI. [Candidate hashes](acceptance/workspace-finishing-polish/candidate.json)
  identify the prepared executables. No installation or release was performed.
- `git diff --check`: passed.
- Six minimum-window captures were inspected in both appearances:
  [diff light](acceptance/workspace-finishing-polish/diff-light.png),
  [diff dark](acceptance/workspace-finishing-polish/diff-dark.png),
  [Runs light](acceptance/workspace-finishing-polish/runs-light.png),
  [Runs dark](acceptance/workspace-finishing-polish/runs-dark.png),
  [refresh failure light](acceptance/workspace-finishing-polish/refresh-failed-light.png),
  [refresh failure dark](acceptance/workspace-finishing-polish/refresh-failed-dark.png).
  [Provenance and hashes](acceptance/workspace-finishing-polish/manifest.json)
  identify the fixture source and full test result.
The new focused coverage is in `WorkspaceFinishingPolishTests`,
`codex_completion_lifecycle_test.go`, and the expanded isolated-hook tests.

The real-subprocess fixture issued two status processes for twelve unchanged
snapshots (initial plus thirty-second fallback), instead of twelve. A changed
summary's read took 10.17 ms locally, in addition to the normal polling delay of
up to five seconds. With automatic refresh suspended, no snapshot or subprocess
was requested during 5.2 seconds; whole-XCTest-host CPU time was 18.28 ms during
that interval. These fixture measurements are not installed-app CPU or latency
claims. The injected-clock test separately covers five minutes of suspended
polls, immediate reactivation, rotating authorization tokens, and explicit reads.

### Remaining installed-build check for F-001

After installing this candidate and relaunching a managed Codex client, complete
one response in each of these paths: resume an existing conversation; `/new` in
that same terminal; close and reopen the app; sleep and wake the Mac. Record the
installed CLI/app build, whether the completed indicator remains visible before
acknowledgement or the next prompt, and any failure's runtime/conversation IDs.
Use `amx debug bundle` for a recurrence. Do not reinterpret a deliberate
acknowledgement or a new prompt as a lost completion.

The `/new` reset itself has a separate expectation: the previous 🛑 must clear
before another prompt is sent. A Codex 0.163.0 capture reproduced a missed reset
when a narrow footer hid `Context 0% used`. The follow-up recognizes the strict
empty startup layout in both the screen and history, retains the receipt checks
against concurrent activity, and tests reset/recompletion on Kitty and tmux.
Unknown layouts and retained transcript content still withhold the clear.
The prepared candidate hashes above predate this reset fix; `just update`
rebuilds and installs the current checkout.

The automated tests establish shell selection, ownership, reducer persistence,
and simulated observation gaps. They do not establish physical sleep behavior
or recurrence in real use. F-001 and the separate five-workday trial therefore
remain open until the operator supplies those observations.

## Review usability on 2026-10-18

This pass implements the five interface/documentation refinements and prepares
an updated build for the remaining completion-visibility check:

- Changes and Runs have draggable, remembered sidebars and **⌘⌥S** toggles.
  Reopening restores focus to the selected row; **⌘⇧F** reveals a hidden list.
  Narrow windows clamp widths without overwriting the wider preference. Changes
  omits its empty worktree toolbar. Collapsing files focuses the native diff text
  view, so Page Down works immediately without changing the file or reading anchor.
- **Inspect Failed Check** and **⌘⇧I** expand recorded verification and scroll to
  the selected session's current failed attempt, visibly highlighting its evidence.
  **Open Session** remains separate. Missing evidence is explained; summary-only
  evidence uses **Inspect Verification**. A refreshed successful attempt clears the
  old selection. Inspection never focuses another session or runs a control/check.
- Runs names the failed check once in its primary summary, with readable brown/amber
  warning colors and a symbol. The plan row says **Incomplete**; secondary stop and
  abort controls have less visual weight while retaining their confirmations.
- Code blocks retain language and Copy beside their text. Shared text sizing and
  **Wrap code and diff lines** live in the reader menu. Successful copy feedback
  clears after two seconds; another copy restarts that interval, and failures remain
  visible. Literal Unicode and newlines reach the clipboard unchanged.
- The companion guide now organizes current behavior by workflow, with one shortcut
  table. Its Codex limitation explains supported managed-launch isolation, explicit
  remote/older CLI behavior, and the remaining installed-build observation. Earlier
  acceptance records remain historical evidence.

The focused interaction coverage is `WorkspaceReviewUsabilityTests`; the preview
suite captures expanded/collapsed sidebars, selected verification evidence, and
multiple code blocks in both appearances. A hosted keyboard check caught and fixed
an initial SwiftUI-wrapper focus bug. Copy timing is verified through real fixture
window clicks, an isolated pasteboard, and own-window capture; SwiftUI's virtual
button was not exposed through the original in-process accessibility-tree lookup.

F-001 remains open until the operator confirms completion visibility on the updated
installation across resume, `/new`, app reopening, and physical sleep/wake. The
separate five-workday migration trial is unchanged. Automated checks and fixture
screenshots do not supply those observations.

Validation:

- `env -u AMX_RUNTIME_SESSION_ID just check`: passed the full Go suite, core race
  checks, vet, 188 TUI scenarios, and lint with zero issues.
- `macos/scripts/test-app.sh`: 908 hosted tests passed, zero failures or skips.
  The final focused run passed 34 tests with zero failures or skips, including
  summary-only evidence handling, native diff keyboard focus, clipboard timing,
  Runs authorization/receipt regressions, and all 14 workflow preview tests.
- Focused Codex checks in `internal/harness`, `internal/indicator`,
  `internal/screenstate`, `internal/daemon`, and `internal/session` passed with
  `-run Codex -count=1` and the inherited runtime-session variable removed.
- Ten new fixture captures were inspected in both appearances. Representative
  references: [expanded Changes](acceptance/workspace-review-usability/diff-light.png),
  [collapsed Changes](acceptance/workspace-review-usability/diff-collapsed-dark.png),
  [Runs](acceptance/workspace-review-usability/runs-light.png),
  [selected failed check](acceptance/workspace-review-usability/inspected-check-dark.png),
  and [code blocks](acceptance/workspace-review-usability/code-light.png).
  [Provenance and hashes](acceptance/workspace-review-usability/manifest.json)
  identify the fixture sources and final focused test result.
- Local guide links and `git diff --check` passed.

Installation: `just update` built and signed the Release candidate, updated the
CLI, and found the hooks already current. Its first app installation attempt
stopped at the quit confirmation for six utility-view programs. After the operator
authorized quitting and continuing, `macos/scripts/install-app.sh` installed the
verified candidate and relaunched the app; `just restart-verify` passed. The
installed app executable matches the verified candidate hash. The agent server
retained generation `4085-1792330813` and all six agents in six sessions. The daemon
and ASL bridge report ready, with the companion connected. Strict doctor returned
exit 0, overall OK with two warnings: Kitty is unreachable from this environment,
and the Claude hook evidence is stale against the newer CLI contract.
[Candidate and installed-artifact hashes](acceptance/workspace-review-usability/candidate.json)
record the completed installation and the earlier interrupted attempt. F-001's
real-use observations remain pending; installation and automated verification do
not establish completion visibility across physical sleep/wake.

## Lifecycle, Waiting, and setup consistency on 2026-10-18

This pass implements the six review recommendations against existing workflows:

- Completion visibility: the installed app hash and CLI version match the recorded
  F-001 fix. The operator confirmed **“Tested all paths; they work”** for resume,
  `/new` reset before a prompt, recompletion, app reopening, and physical sleep/wake.
  The friction register and trial log record resolution; no trial workday is added.
- Lifecycle language: Dashboard and Overview menus, palettes, confirmation headings,
  status footers, pending/success/failure notices, and recovery offers use End Session.
  The native app distinguishes End Session, Detach Terminal, and Close View. Existing
  command identifiers, `amx close`, aliases, preferences, and authorization stay intact.
  Preferences and guides explain the native/TUI confirmation scopes.
- Waiting: category and ordering share a row; next-item navigation and pending updates
  share another. Update list is hidden when there is nothing to apply. Reader hints
  follow actual keyboard focus. Holding an item while reading still disables changed
  questions immediately. The native Context button's cell now has its own accessible
  label, matching the Answer button.
- Review status: the fixed toolbar slot displays the oldest successful content update
  as seconds/minutes/hours/days, so a fresh list cannot disguise older detail. Failures,
  warnings and delayed progress retain priority; the popover keeps separate timestamps
  and retries. Runs says Show in Finder and Stop After Session.
- Setup: README supplies a complete minimal configuration, verified with `config
  validate` in an isolated directory. The shipped template puts the minimal profile
  and project first. ASL entry points and configuration reload guidance now match the
  implementation. The valid `a`, then `O` editor shortcut stays documented.
- Accessibility: hosted checks cover native accessible Waiting actions, question
  reading, answer routing, keyboard return to the original terminal, changed-question
  refusal, and narrow layouts. Code blocks form named accessibility groups with an
  identified Copy action; the action uses control-sized, primary-colored text.
  Increased-contrast fixtures cover large prose/code in both appearances. The operator
  waived the manual VoiceOver walk-through with **“skip voiceover check”**. The hosted SwiftUI Copy lookup does not
  expose its virtual controls in this test host, and system Accessibility access is
  unavailable. Its explicit test skip is not recorded as a pass.

The current [observation record](acceptance/workspace-consistency/observations.json)
separates confirmed completion-state behavior from the waived accessibility check.

For an optional future check on the prepared build, use VoiceOver to find a waiting agent,
read its question, open the answer panel and submit an intended answer, inspect a
failed run's check, copy a transcript code block, and return to the original terminal.
Repeat navigation with large reading text and Increase Contrast; confirm the selected
question/action is announced correctly, focus is visible, controls remain reachable,
and copying preserves literal Unicode/newlines. Record the build, date, paths tested,
and any failure in the observation record. Do not change system privacy permissions
or count fixture screenshots as this observation.

Validation for this pass:

- `env -u AMX_RUNTIME_SESSION_ID just check` passed the full Go suite, core race
  checks, vet, all 188 TUI scenarios, and lint with zero issues. The final help
  wording also passed `go test ./tui ./internal/config` with that variable removed.
- `macos/scripts/test-app.sh` executed 914 tests: 913 passed, one accessibility
  lookup skipped as described above, zero failures. Copy feedback, literal Unicode
  clipboard contents, repeated-copy timing, and expiry passed. The screenshot
  assertion accepts Vision's known Cyrillic look-alike spelling of the fixed Copy
  label; clipboard comparisons remain exact.
- The minimal README configuration passed validation in an isolated directory.
  Local documentation links and `git diff --check` passed.
- Four fixture captures were inspected: [Waiting light](acceptance/workspace-consistency/waiting-contrast-light.png),
  [Waiting dark](acceptance/workspace-consistency/waiting-contrast-dark.png),
  [Reading light](acceptance/workspace-consistency/reading-contrast-light.png), and
  [Reading dark](acceptance/workspace-consistency/reading-contrast-dark.png).
  [Provenance and validation](acceptance/workspace-consistency/manifest.json)
  record source hashes and the final test result bundle.

`just mac-app Release` built and signed `macos/build/Agent Mux.app`; bundle
verification passed. This pass did not install it. The
[candidate record](acceptance/workspace-consistency/candidate.json) identifies the
app and bundled CLI hashes. All requested implementation work is complete; the
manual VoiceOver check is waived, with no claim that it passed.

## Navigation, recovery, and setup polish on 2026-10-19

- Diff Previous/Next follows the visible hunk after manual scrolling, while
  repeated shortcuts still advance when adjacent hunks already fit on screen.
- History, Changes, Activity, and Runs restore native saved window placement.
  The shared placement policy fits restored frames to connected displays.
- Changes cancels obsolete comparison, diff, and managed-review reads. Diff
  parsing and word matching run with the background read. Landing retains its
  existing mutation lifecycle and confirmation checks.
- Read failures expose an inline explanation and Retry beside the affected
  content, including initial failures. Retained results are explicitly labeled;
  the toolbar still supplies independent timestamps and full diagnostics.
- Dashboard queue and inspector accept g/G and Ctrl+D/U, matching the navigation
  vocabulary in History, Plans, and Worktrees without expanding the footer.
- Setup offers profile-specific hook syncing and summarizes CLI/dashboard
  destinations, executable availability, backend health, and remaining steps.
  An unanswered hook prompt does not save configuration. Hook failures preserve
  the saved config and print a retry command; authentication is not inferred.

Verification:

- Hosted macOS suite: 923 tests, zero failures, one existing accessibility lookup
  skip. Result: `Test-AgentMux-2026.10.19_16-12-36--0600.xcresult`.
- Follow-up Changes suite: nine tests passed, including actual termination of a
  superseded child process, queued cancellation, and thirty file selections over
  a roughly 1.8 MB patch. The background read assertion and final selected diff
  passed. Result: `Test-AgentMux-2026.10.19_16-16-16--0600.xcresult`.
- Follow-up navigation and finishing-polish suites: eleven tests passed,
  including adjacent visible hunks, manual scrolling, wrapping navigation, and
  existing diff/read-status checks. Result:
  `Test-AgentMux-2026.10.19_16-17-36--0600.xcresult`.
- Minimum-window captures cover populated, empty, loading, retained failures,
  and initial failures in both appearances. Forty captures passed geometry and
  text checks; representative History, Changes, Runs, and Activity failures were
  visually inspected. Capture attachments remain in the hosted test result.
- `env -u AMX_RUNTIME_SESSION_ID just check` passed: the full Go suite, core race
  checks, vet, all 188 TUI scenarios, and lint with zero issues. Final setup-only
  tests also passed after refining the destination-specific readiness wording
  and quoting retry commands. `git diff --check` passed.

No installation or release was performed.

## Waiting, applied settings, and search polish on 2026-10-19

Waiting context now has explicit loading, failed, unavailable, and loaded states,
with Refresh/Retry, retained-content notices, and read times. ASL questions use the
shared formatted prose reader; terminal captures retain their fixed-width text.

Settings compares effective saved configuration with daemon startup fingerprints,
reports changed groups, and verifies the result of its existing restart action.
Project-only edits no longer imply that daemon settings need reapplying. Doctor
uses the same comparison; older daemons report that verification is unavailable.

The inspector translates prompt reasons and shows the winning state observation's
source and time. Terminal Find offers Search full conversation, transferring the
literal query into the selected conversation's History scope and restoring focus
when History closes. Large-diff work measures styling and layout separately and
checks reading-position preservation alongside selection latency.

The [implementation and acceptance record](acceptance/workspace-seams-polish/README.md)
contains validation, fixture captures, and the pending real-work observations.
The user waived VoiceOver acceptance for this pass. Real-work observations are
not supplied by automated tests.

Validation: the full Go gate passed, including 188 TUI scenarios and zero lint
issues. The final native suite executed 934 tests with zero failures and one
existing accessibility lookup skip. The Release candidate built and passed
strict signature verification; it has not been installed.
