# Native workflow views

[Back to the companion guide](../companion-app.md)

### Returning to work

Enable **Settings › Workspace › Show Welcome back** to see a compact **Welcome back**
card at the bottom of the right sidebar. It is off by default; turning it off hides
an existing welcome card immediately. The card shows unread recorded
updates from the past seven days and the live attention count. It appears only
once per app launch, when first opening the workspace. **Activity** and
**Briefing** open the existing review windows; **Next waiting** focuses a waiting
session. Dismissing the card does not mark any activity read. Incomplete history
is labeled “At least”; unavailable reads and disconnected waiting status are
shown explicitly.

Successful prompt submissions and accepted supervisor answers show **Answer sent** in the right sidebar.
The waiting count follows the live feed, not the submission receipt. When a live
waiting count reaches zero while the app is active, **All caught up** briefly
shows how many agents are still working. New attention clears that acknowledgement.
These updates never move keyboard focus or automatically navigate to another agent.

Palette entrances, hover feedback, and sidebar/inspector disclosure use short,
consistent transitions that respect Reduce Motion. Session state changes use one
brief highlight. Terminal input and session navigation do not wait for animation.

### Command palette and launch

**⌘⇧P** opens the command palette. Shortcuts align in a separate column, and the
footer shows navigation keys and the current mode. In **⌘P**, type `>` to search actions.
Abbreviations such as `swcc` match Split with Checkout Changes; aliases include
`diff`, `preferences`, and `terminal`. Exact names rank first, then available
actions relevant to the current pane. Matching title characters are highlighted,
and unavailable actions explain why.

The palette offers configured-project launches, recent-conversation resume/focus,
shells, History, checkout review, Runs, and workspace navigation. The Launch
Project menu names both the destination and its source. **Launch Settings…**
opens **Launch projects in**. Following CLI configuration distinguishes an explicit
`config.toml` choice from the tmux default. Preferences lists resolved
app, CLI, and plan destinations; **Follow CLI** aligns the app with CLI configuration.

An empty workspace offers **Launch Project…**, **Conversation History**, and
**Open Dashboard**, and names the effective launch destination. With no configured
projects it offers **Set Up First Project…**, running the existing `amx config
setup` guide in an embedded terminal. The guide asks about the profile, directory,
hooks, and backend. Its summary and any errors stay visible until you press Return;
then the native workspace refreshes projects and the effective launch destination.
Setup never launches an agent. Pending configuration reads, failed reads, and
daemon recovery remain distinct from an empty configuration.

### Waiting

Waiting follows the left sidebar's group filter by default. **Show all groups**
includes waiting sessions from every group; selecting one outside the current
group switches the left sidebar to **All** and reveals the session. Group filter
changes apply immediately, including while reading context.

Waiting in the inspector supports category filters, oldest-first sorting,
expandable question/output context, and **Next Waiting**. **All** groups decisions
before other attention and completed turns, with counts. While the pointer or an
expanded context holds the list, updates wait behind **Update List** so targets
stay put; changed prompts are disabled immediately. Answer opens the existing
prompt or supervisor panel and never submits an answer itself.

**Window › Waiting** (⌘⇧I) focuses the reader. Up/Down read items, Return opens
the answer panel, N advances, Space toggles context, and Page Up/Page Down scroll
context. Escape restores the previous workspace focus without attaching a session.
Closing an answer panel returns to the reader when the workspace still owns focus.
Context uses available inspector height, proportional text for supervisor
questions, and monospace text for terminal output.

Sidebar targets also keep their positions during pointer interaction, dragging,
or an open context menu. Attention badges still update; ended sessions held in
place cannot be acted on. Sidebar, Waiting, and menu-bar reasons share readable
labels; raw protocol reasons remain in the session inspector.

### Continue with another agent

**Session › Continue with Another Agent…**, a session's right-click menu, and
its command-palette entry open a native handoff preview. The action is also
available for the selected conversation in History and on Project Briefing's
conversation cards.

Choose any configured Claude, Codex, or Copilot profile, review and edit the
context, and optionally add a next instruction such as “Review this for concurrency
bugs.” The preview captures the source conversation's goal and recent messages,
checkout directory, branch, and Git status. Loading it makes no model request
and starts no agent. **Launch Agent** saves the reviewed text as a private
handoff pack and starts a fresh receiving agent in the displayed checkout. The
pack records a snapshot; the receiving agent is told to verify current state.
The original conversation stays available.

The receiving session opens automatically and is placed below the source when
that live session is available. The Session inspector and Conversation History
show **Continued from** and **Continued in** links, so you can follow a handoff
in either direction. Links are shared with CLI handoffs and survive app and
daemon restarts and closing both agents. Until the receiving agent reports its
first interactive conversation, the source shows that its continuation has
not reported a conversation yet. Older app-only handoffs retain their existing
**Read Source Conversation** link.
An incomplete or timed-out launch stays visible for inspection and cannot be
repeated from the same panel; check the session list before starting again.
Saved packs can be revealed from a failed launch panel.

### Conversation History

**View › Find or Resume a Conversation…** (⌘Y, also ⌘⇧H) searches full transcripts, scoped to all history,
the selected session's project, or its conversation. Resume, fork, or focus a live
session from the preview. Search opens around its first matching prose message,
even outside the latest 50 messages. Highlighted terms and previous/next match
buttons navigate the conversation. Earlier/later paging preserves the reading
anchor; the message navigation menu also reports the loaded message range.

Result snippets highlight the same terms and quoted phrases. Results load 50 at
a time; **Load more conversations** extends the list without changing the preview.
Empty results offer **Clear Search**, **Search All History**, and **Include
Automated Sessions** as applicable, with an actual hidden-match count. Rows show
relative activity times with exact tooltips; all-history rows include checkout
paths. The conversation heading's tooltip also includes its checkout.

**⌘R** and the preview refresh button read only the selected conversation. Search
or Return in the search field refreshes results. Updated messages keep their
reading position; a **new messages** button offers navigation to appended messages
without scrolling automatically. Superseded searches and transcript reads cancel
their running read subprocesses. A failed search does not disable transcript refresh.

History remembers query, scope, selected conversation, loaded results, and reading
position across restarts. Its draggable results divider remembers width without
losing the wider preference when the window shrinks. The sidebar button or **⌘⌥S**
hides results and restores focus to the selected result on reopening. Down from
search also reveals results. Selection and reading position remain intact.

**Edit › Search Conversation…** (⌘⇧S) opens the selected session's recorded conversation
in History once its identity is known. **Edit › Find in Terminal…** uses the
terminal find bar: **Find on visible screen** for a full-screen agent, **Find in
terminal history** otherwise. Utility terminals search terminal history.

### Checkout Changes

**View › Checkout Changes…** (⌘⇧C), or **Changes…** in the Session inspector,
shows checkout-wide files, line counts, and diffs against a chosen branch or commit.
Changes includes every session using that checkout. The app and `amx` must both
be rebuilt for checkout review.

Drag the file-list divider to choose its width; the preference and collapsed state
survive reopening and app restarts, including embedded reviews. The sidebar button
or **⌘⌥S** hides files to give the diff more room. Reopening restores keyboard focus
to the selected file; **⌘⌥F** reveals and focuses the list. Resizing clamps the
visible width without replacing the saved preference. The worktree toolbar occupies
space only when there is a review or error to display.

Diffs show old/new line numbers, addition/deletion shading, and previous/next hunk
controls with the visible hunk number. Previous/Next starts from the visible hunk,
including after manual scrolling, and wraps at the ends. Changed words receive stronger shading in
paired replacement lines; large or unpaired blocks retain whole-line shading.
Redundant file headers are hidden; **… → Show diff headers** restores them.
Mode changes and no-newline markers remain visible. The pencil button opens the
selected file in the configured editor.

The comparison base and selected file are remembered per checkout, with separate
scroll positions per checkout/base/file. Refresh follows matching nearby source
text where possible, falling back to a nearby source line. File labels use the
shortest unique trailing path; tooltips and accessibility retain the full path.
Switching files cancels obsolete diff reads; diff parsing runs off the UI thread.
Large diffs initially lay out just the visible region. Restoring a deep reading
position, changing wrapping, or resizing a restored large diff prepares its full
layout off the UI thread. **Restoring reading position…** labels that work; newer
selections discard obsolete layouts.

The **…** menu contains remembered **Wrap lines** and text-size preferences, plus
**Copy Unified Diff** and **Copy Changed Source Lines** (added and context lines
from displayed hunks). Copying selected text strips line numbers and diff markers;
**Copy Unified Diff** in the context menu keeps patch syntax. Unified-patch copying
always includes the original headers. Truncated diffs are labeled.

Managed worktrees offer source-branch comparison and confirmed fast-forward landing
with the CLI's clean/inactive checks. A details button expands warnings and
conflicts without pushing the diff or landing controls out of view. A retained
review cannot authorize landing while its replacement is being read.

### Project Briefing

**View › Project Briefing…** gathers one project's live sessions and ten most
recent conversations across agents and profiles, including automated and
unclassified sessions. Choose another project in the window's picker. It starts
on the workspace's selected project when possible, and is also available from
the command palette; assign a shortcut in Settings if desired.

**Needs you** links to sessions with questions, permission requests, or other
attention. **Live sessions** includes agents whose conversation identity is not
known yet. **Recent outcomes and unfinished work** shows each conversation's
saved goal, latest outcome, and outstanding work separately, with recap times,
profile/model, excerpt coverage, and supporting-message links into History.
Agent-reported results remain attributed to the agent.

Recorded handoff chains are grouped as conversation threads. Older indexed
members are included even when they fall outside the recent ten, and each
conversation keeps its own recap, live status, and Read/Focus/Resume actions.
**Continued from** and **Continued in** open the exact conversation in History.
An unavailable transcript does not remove the recorded relationship. Threads
reflect recorded handoffs; similar topics are not automatically merged.

Opening the briefing and **Refresh** (⌘R) read saved recaps without starting
model requests. **Generate Recap** or **Regenerate** on a conversation explicitly
uses the configured recap profile and its service/quota. Generation is disabled
while that conversation has a working agent, or live status is unavailable.
Missing, stale, and failed recaps remain visible as such. A failed refresh keeps
the last loaded conversations and recaps with a readable error.

**Focus Session** returns to an existing agent; **Resume** continues the exact
closed conversation through History's existing launch path. **Read Conversation**
and supporting-message links open History; closing it returns to the briefing
when that window is still open. Live state follows daemon updates; refresh
explicitly to reload recent history and saved recaps. Offline state labels the
last known live list and disables session actions. Older unfinished work can
fall outside the recent ten; this view does not combine or resolve claims across
conversations.

### Conversation recaps

The inspector's **Session** tab and Conversation History show an automatic recap
of the selected conversation: its goal, latest outcome, and explicitly unfinished
work. Saved recaps appear first. Generation runs in the background only for a
conversation you open and waits while its live agent is working. No typing is
required. **Supporting messages** expands the citations; selecting a message
opens the evidence in History. The refresh button regenerates or retries.
Timestamps, excerpt coverage, stale data, and failures remain visible.

Recaps use the first configured Claude profile, including for Codex and Copilot
conversations, and consume that profile's service/quota. Choose a different
profile/model or disable generation in `[defaults.recaps]`. See
[conversation recaps](../conversation-recaps.md) for configuration and limits.

### Since Last Looked

**View › Since Last Looked…** (⌘⇧U) shows recorded state transitions, process exits,
and ASL lifecycle events from the retained journal. Choose 1, 7, 30, or 90 days.
Select an event for details and the last timestamped assistant output at or before
it; **Load Earlier Messages** searches further back for supporting output.
Opening its conversation goes to that recorded location, briefly highlights it,
and returns focus to Activity when closed. Missing evidence is explained; a failed
read can retry the same event. You can also focus the matching live session.

**Mark Read** offers **Mark Selected Read** and **Mark All N Results Read**, including
loaded rows below the viewport but excluding records omitted by the query limit.
**Undo** restores the last successful acknowledgement and prior selection when
still in scope. Failed saves and Undo retain current markers and report the failure.
Opening or refreshing never marks anything read, and late arrivals stay unread.
Turn off **Unread only** to revisit read events. Retention limits, unavailable
transcripts, truncation, and unreadable journal records remain explicit.

### ASL Runs and Run a Plan

**View › ASL Runs…** (⌘⇧R) lists the full active/actionable run projection, including
pauses without an unanswered question. Select a run for progress, blockers, and
permitted controls above the scrolling plan sessions. **Run details** expands
paths, identifiers, and timestamps; **Recorded verification** expands check evidence.
The sidebar button or **⌘⌥S** hides the run list and restores focus to it on reopening;
**⌘⌥F** reveals and focuses it. Divider width and collapsed state are remembered.

**Answer…** is primary for an open question. For verification failures, the detail
heading names the failed check; **Inspect Failed Check** (⌘⇧I) expands recorded
verification and scrolls to the highlighted failure in the selected session's
latest completed attempt. This is recorded outcome evidence, not command output;
**Open Session** remains a separate action for investigation. Missing or unreadable
evidence disables inspection and explains why. A later successful attempt cannot
present an old failure as current. When only the attempt summary was retained,
the action says **Inspect Verification** and explains the missing individual checks.
An affected pending plan row says **Incomplete**.

**Resume** is primary for other resumable pauses. **Resume**, **Stop After Session…**,
and **Abort…** recheck ASL permission on submission; confirmation buttons name the
requested action. An uncertain response offers **Retry Receipt** for the same
operation ID. Polling and evidence inspection never repeat a control operation.

Runs polls summaries every five seconds while the app is active and the window
visible. Unchanged selected-plan details are reread every thirty seconds; changed
summaries trigger a read on the next poll. Explicit refresh and reactivation read
immediately. Hidden/inactive windows suspend polling. Evidence is run-scoped and
bounded; unavailable or truncated results are labeled.

**View › Run a Plan…** (⌘⇧N), also in Runs and the palette, opens the native launch form.
Choose a directory or configured plan name, optionally choose a supervising
project/profile and worker/review harnesses, then **Check Readiness**. Searchable
project and supervisor choices support arrows, Return, and Escape; unavailable
directories explain why. **Infer from plan workspace** and **Use project default**
keep inference explicit. Ambiguous inference requires a choice.

Readiness shows the actual command, resolved project/supervisor, and preflight
findings. Changed options require another check. **Start Supervised Run** uses
`amx plans run --backend tmux` and focuses the supervising session. All native plan
runs use the workspace. Submitted or uncertain launches are not repeated by the form.

### Reading, status, and feedback

History and Activity share a **Reading text size** menu, and a Waiting row's
context follows it. Prose size is remembered independently from code, diff, and
terminal-preview size. The same menu contains **Wrap code and diff lines**; open
readers update immediately. Transcripts
render headings, lists, nested indentation, tables, blockquotes, inline formatting,
and fenced code while preserving search highlights. Each code block keeps just its
language and **Copy code**, which copies original text without Markdown fences.
Diffs also expose shared monospace preferences in **…**. Successful **Copied**
feedback clears after two seconds without moving the reader; failures stay visible.

Read status uses a fixed toolbar slot. Brief reads do not flash a spinner;
slower reads say **Refreshing…**, and failures say **Refresh failed** even with
retained content. The status popover identifies each read, its last success, retained
results, and selectable recovery guidance. Independent ages cover search, transcript,
comparison, diff, run list, and plan details. Failed reads keep their old age. Click
an age for the exact timestamp and timezone; **Last activity** describes agent activity.
Failures also show their explanation and **Retry** beside the affected content.
Initial failures say which content could not be loaded; refresh failures label
the previously loaded content that remains visible. These read retries do not
repeat a worktree landing or an ASL operation.

History, Changes, Activity, and Runs restore their saved window position and size.
When a display is disconnected, the restored window is fitted onto an available
display; a window without saved placement opens centered.

The feedback-history button retains up to 30 native action outcomes, shared by
History, Changes, Runs, and the plan form. Unresolved errors and warnings survive
app restarts until dismissed or displaced by newer failures at the limit. Repeated
identical outcomes are grouped with their count and latest timestamp; routine
successes cannot displace unresolved failures. Notices keep their
original target, time, message, and recovery action and can be copied or dismissed.
Recovery opens the original conversation/checkout, checks original plan options, or
checks the exact pending receipt. It never repeats a resume, fork, landing, launch,
or run control, and an old receipt cannot retry a newer operation. After a restart,
receipt notices open fresh state for the original run instead of retrying its old
receipt. Only messages and read destinations are saved, never authorization tokens
or executable actions.

### Panes and restoration

**View › Split with Shell** keeps an agent beside a shell in its working directory;
**Split with Checkout Changes** opens native review alongside it. In **⌘⇧P**, choose
**Split with <session name>** to compare agents. Click a pane or its heading to focus
it; a filled dot marks the active pane. Sidebar selections and session actions apply
to that pane. **Focus Checkout Review** returns to an opened review after navigation.
**Focus Other Pane**, **Swap Panes**, and **Hide Other Pane** are in View and the palette.

Hiding preserves agents and shells; ending a session asks for confirmation. Closing
the workspace detaches both agents, preserves utility programs, and returns to one
pane when reopened. App-restart restoration remembers both panes, their arrangement,
divider, active side, and embedded review checkout. Attachments are checked; sessions
attached elsewhere stay detached. Restored utility programs require explicit Reopen.

Assign keys in **Settings › Workspace shortcuts** with **Record Shortcut**. Bindings
apply immediately and persist. **Clear** removes a binding; **Restore Default**
restores its original key: ⌘⇧P for Command Palette, ⌘⇧R for ASL Runs…, ⌘⇧N for
Run a Plan…, ⌘⇧U for Since Last Looked…, ⌘⇧H for Conversation History…, and ⌘⇧C
for Checkout Changes…; the pane commands have none. None of these holds Option, and
none is a key Ghostty binds with ⌘⇧.
The recorder checks menu/global conflicts; Escape cancels. These keys work while
the app is active. History and Changes also have separate resizable native windows.
