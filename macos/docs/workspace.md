# Workspace implementation

[Back to the macOS development guide](../README.md)

## Workspace

**Open Workspace** in the status menu shows the workspace window
(`UI/WorkspaceWindow.swift`): a native sidebar of the live sessions beside the
terminal of the selected one. Opening the app again while it runs (Finder,
Spotlight, `open`) shows it too, and Preferences › Open the workspace at
launch (`amxOpenWorkspaceAtLaunch`) opens it at launch. While the window is open the app is a regular app, with a
Dock icon and a main menu (`UI/WorkspaceMenu.swift`); closing it returns the
app to the menu bar accessory. The status item, the global shortcuts, and the
ASL answer panel work the same either way.

- **Which sessions.** The app subscribes with the `runtime_sessions` feature.
  The daemon sends v2 snapshots with each tmux session's runtime identity
  (`CompanionSession.runtime`). The runtime-session ID identifies a session in
  the sidebar and attention cycle.
- **Group scope.** The group pills at the top of the left sidebar offer
  **All**, then every group declared by configured projects, alphabetically,
  including groups with no live sessions. Pills wrap to fit the sidebar.
  Click a group to show only its projects' sessions and their attached
  terminals. Projects can belong to
  several groups and appear under each of their tags. Click the selected pill
  again, or **All**, to clear the filter. Counts and session shortcuts follow
  the filtered list. Each group remembers its active session or view, both
  split panes, their active side, and divider position. Switching groups
  restores that context, reusing hidden terminals. The first visit selects
  the first session in the group's sidebar order, or an empty pane.
  `UI/WorkspaceWindow+GroupContext.swift` coordinates the switch;
  `amxWorkspaceGroupContexts` stores layouts and active conversation identities
  by group, with a distinct key for All. Window reopening and app restart use
  the same restoration and ownership checks. Ended agents and shells are never
  restarted. The former global layout migrates into its selected group's context.
  Sessions launched with a different profile inherit their project's tags
  unless that project/profile pair has its own explicit config entry.
  Config edits refresh the choices automatically; removing the selected group
  returns to **All**. Revealing a session outside the scope (for example with
  ⌘J or the global attention shortcut) returns to **All** to show its row.
- **Sidebar.** `Model/WorkspaceSidebar.swift` builds it as data: one group per
  project, sorted by name, and under it one row per session labeled with its
  profile, sorted by profile. A row shows the state glyph, harness, branch,
  age, and the reason it waits. A project's line counts its sessions that wait on
  you. Sessions needing attention temporarily move into **Needs attention**,
  below the **Pinned** group and above every project, even when their project
  is collapsed.
  Like Overview, this includes permission requests, errors, questions, and
  unread completed turns. When the daemon marks a completion seen or resolves
  the waiting status, the session returns to its saved position. Its terminal,
  selection, pin, and dragged order are preserved. The temporary section
  always stays expanded, names each session's project, and cannot be dragged.
  A session's line flashes with its displayed section header's background color
  when its state changes. ⌘J uses the same section color to reveal a session;
  state changes use one 350 ms opacity highlight. Explicit reveal keeps its
  two orientation pulses (one with Reduce Motion).
  These flashes leave focus, scrolling, and collapsed groups alone; first
  sightings and updates to names or ages do not flash.
  Preferences › Appearance › Group sessions by selects **State → Project**
  (the default), **Project → State**, **Projects**, **Compact list**, or
  **Projects → Branches**. Pinned sessions lead every layout. Project-first
  keeps unpinned sessions, including those needing attention, inside their
  project, with collapsible Waiting, Done, Working, and Idle sections underneath. Changes
  apply immediately and persist across launches; each project's state sections
  collapse independently.
  **Projects** omits state headings and keeps sessions in their saved positions
  as states change. **Projects → Branches** adds collapsible branch headings
  only for projects with multiple branches; sessions without a reported branch
  appear under **No branch**. Both list pins above projects and keep unpinned
  waiting sessions in their project, counting them on project headings.
  **Compact list** uses one flat list with project names on every row and its
  own saved drag order across projects below the pinned sessions. The state
  chips above these lists still jump to sessions without moving them out of
  their project.
  **Sort sessions by** is independent of grouping: **Manual order** uses saved
  drag positions, while **Recently visited** sorts within each group by when
  you opened a session or one of its attached terminals. Agent output does not
  change visit order. Returning to Manual order restores the saved arrangement.
  **Waiting only** below the group pills filters the sidebar to sessions waiting
  on you, including completed turns awaiting review. It combines with group
  scope and every grouping/sort option, without closing hidden terminals or
  changing the active pane. Reveal (⌘J) clears the filter if needed to show the
  selected session. The filter is temporary and starts off in a new window.
  Preferences › Appearance › Show Overview in sidebar restores the optional
  Overview entry beside Dashboard. It is hidden by default, applies immediately,
  and is saved for the next launch. Overview remains available from the View menu
  and its shortcut.
  Preferences › Appearance › Compact sidebar places attached terminals as
  tabs beside their session, saving a row for each terminal. The setting is
  kept for the next launch; the regular layout remains the default. Each tab
  has a × that closes only its terminal.
- **Dragging.** Press a session's row and drag it to move it among its
  project's sessions; the terminals beside it go with it. The order is a
  `WorkspaceOrder`, kept in the `amxWorkspaceSessionOrder` preference, and a
  session launched later follows the dragged ones. Dragging other sessions
  preserves the saved places of those temporarily needing attention. A terminal
  beside a session drags among that session's terminals in the regular layout
  (`WorkspaceViews.moveTerminal`); its tab's right-click menu offers Move Left
  and Move Right in compact mode. Terminal order lasts for as long as the app
  runs. `dropSlots(forItemAt:)` is the whole rule:
  sessions stay within their current project, state, or branch group; Compact
  list permits ordering across projects. Filtered-out sessions keep their saved
  positions. Session dragging is available in Manual order; attached terminals
  can still be reordered in either sort. Headings never move.
- **Pinning.** **Pin to Top** in a session's right-click menu, or **Session ›
  Pin or Unpin Session** (⌘.) for the one the pane is on, moves the session out of
  its project into a **Pinned** group listed above every project in all layouts
  (Compact list keeps pins first without project headings)
  (`WorkspaceSidebar.groups(_:order:)`, `setPinned(_:_:)`); its line then
  names its project. A pinned session needing attention stays in **Pinned**,
  which already leads the list; in State → Project, while that group is collapsed,
  it appears in **Needs attention**, keeping its pin. Pinned sessions keep the order
  they were pinned in until they are dragged among themselves, and **Unpin**
  puts one back under its project. A pin is the session's own: the `★` in
  front of its title, written by `amx pin-session` and reported in the
  snapshot as `pinned`, so the dashboard lists the same pins above its Needs
  you. Only their order is the `WorkspaceOrder`'s, kept in the
  `amxWorkspacePinnedSessions` preference; the pins this app kept there before
  pins were shared are pinned once at the first live snapshot
  (`amxWorkspacePinsShared`). Everything that follows
  sidebar order (⌘1 to ⌘0, the next-attention shortcut, the inspector's
  waiting list) follows them. Any session can be pinned. The
  place of a session that ended is forgotten the next time a pin changes while
  the feed is live.
- **Selecting and focusing.** Selecting a row (arrow keys, **Session ›
  Next/Previous Session or Terminal**) shows its tmux terminal in the app.
  Focusing a row (a click, status-menu row, next-attention shortcut, or banner)
  moves the keyboard into that embedded terminal.
- **Terminals.** Each embedded session has at most one `Ghostty.SurfaceView`,
  running `amx attach <runtime-session-id>`, made the first time the session
  is selected and kept, hidden, while another is shown. A terminal is let go
  when its `amx attach` ends (the session was attached somewhere else, or
  detached with **F12**; the pane then offers **Reattach**, its default
  button, instead of taking the session back by itself; a click on the
  session's line, Return on it, or choosing it in the Go to… list reattaches
  too), when a live snapshot no longer lists its session, or when the window
  closes. None of those ends the agent. A hidden terminal hears of a new
  size, and of a new backing scale, only once it is shown, so tmux is never
  sized for a grid nobody sees.
- **Accessibility.** A terminal is a text area to VoiceOver
  (`SurfaceView+Accessibility.swift`, ported from upstream): its value is
  the screen's text, reread at most every half second, with the selection
  and ranges in UTF-16 units of that text (never Ghostty's grid-cell
  offsets, which `selectedRange()` keeps for the input method).
- **Keyboard with the sidebar hidden.** A hidden sidebar never takes the
  keyboard: when a terminal it would have gone to ends, the pane's terminal,
  the placeholder's button, or the window takes it instead, so the arrow keys
  never switch sessions in a list that cannot be seen.
- **Session colors.** `WorkspaceRowView` draws neutral rows with a small
  profile-color marker. Child terminals share that marker; selection always
  uses the system accent. Text follows the native appearance. The daemon's
  `tab_color` still supplies the profile or F6-picked color. Native completed
  turns use a checkmark; every session row also describes status in words.
  Duplicate display names gain a branch or short runtime suffix consistently
  across sidebar, switcher, Waiting, inspector, and dialogs.

- **Harness icons.** An embedded session's line starts with its harness
  icon, ahead of the profile: the `[harness_emojis]` emoji its launch put in
  its title, with nothing to set up, until F5 picks another or clears it.
  The daemon reads the title on every reconciliation pass and publishes the
  configured emoji it starts with as the session's `harness_icon`, so a pick
  shows as soon as a color does.
- **Closing from the sidebar.** A session row's ×, **End Session…** (⌘⇧W)
  in the Session menu, and the row context menu end its agent through the same
  confirmation policy. They run `amx close <runtime-session-id> --yes` after
  confirmation by default. ⌘W detaches its terminal, leaving the agent running
  and listed; click the row or choose Reattach to return.
  Its conversation stays on disk; terminals opened beside it close with it.
  ⌘W's menu item says **Detach Terminal** on
  a session and **Close View** on a utility view. Closing a utility view ends
  its program, asking first when Ghostty reports it still needs confirmation.
  Settings → Workspace offers Follow config.toml and Always/Never app
  overrides. Native actions read `amx config confirmation --json` when inheriting.
  Embedded management views read an app-owned policy file at action time through
  `AMX_WORKSPACE_CONFIRMATION_FILE`; shells and attachments receive no override.
  Thus open native and embedded views follow changes without restarting. Failed
  reads confirm conservatively. Existing explicit Boolean choices migrate;
  app quit confirmation remains enabled.
  Dashboard and Overview have no sidebar ×.

- **Losing the daemon.** `WorkspaceState` replaces its rows only from a live
  snapshot. While the feed is down the last rows stay listed, dimmed, with the
  daemon line under them; the selection and every terminal stay as they are,
  since tmux holds the sessions and `amx attach` does not need the daemon.
  The first snapshot after the reconnect then decides what is still there.
  The daemon line is `WorkspaceSidebar.feedNotice` (`Connecting to the
  daemon…`, `Daemon not running`, `Daemon too old — choose Restart Daemon`,
  `Reconnecting… (reason)`). Under it, whatever is listed, the sidebar offers
  **Start Daemon** when it is not running (`workspaceStartDaemon`, the menu's
  `amx daemon start`) and **Restart Daemon** when it is too old, or when it is
  reconnecting for a reason waiting will not cure: it stopped answering, or
  sends sessions the app cannot read (`WorkspaceSidebar.stuckReasons`; the
  menu bar offers the same) (`workspaceRestartDaemon`); a second start or restart is refused while one
  runs. With no rows to hold, the pane says the same instead of "No live
  sessions" (`feedExplanation`) and offers the same action. The inspector
  shows the daemon line above the lines it holds, and its Answer… and action
  buttons wait until the feed is live again.
  A wake from sleep keeps a healthy feed and redials one that was waiting to
  retry; the terminals are local processes and are not touched.
- **Coming back.** `WorkspaceRestoration` (`Model/WorkspaceRecovery.swift`)
  keeps two preferences: whether the window was open (`amxWorkspaceWindowOpen`)
  and where its pane was (`amxWorkspacePlace`: `session:<id>` or
  `view:dashboard|overview`). The controller
  reports every move (`workspacePlaceChanged`); quitting does not count as
  closing the window. At launch the app reopens the window if it was open and
  calls `restore(_:)`: a view is shown at once; a session is shown once a live
  snapshot lists it **and** `amx attach <id> --json` (which checks and never
  attaches) says no terminal is attached to it. A session that has one is
  restored **Detached**: coming back never takes a session from whoever
  attached meanwhile. The pane says `Restoring` until then.
- **Agents that stopped.** When a live snapshot no longer lists the session
  the pane was on, or the session being restored, the app asks amx what
  became of it (`WorkspaceSessionStatus`) and the pane says **Agent exited**
  (`runtime_session_exited`: its process ended, with the status) or **Agent
  lost** (`runtime_session_lost`: its pane or server is gone). A session whose
  conversation is known (`WorkspaceRow.conversation`, and for the session the
  pane was on at the last quit the `amxWorkspaceConversation` preference)
  offers **Resume from Past Sessions**, which opens a `history` view:
  `amx workspace-view history HARNESS:SESSION_ID`, the TUI started in Past
  Sessions with that conversation selected, where the operator presses `c`.
  Any other is offered the Dashboard. No code path in the workspace launches
  an agent: the view only lists, and the key is the operator's.
- **Main menu.** The terminals offer every key equivalent to the main menu
  before encoding it for their program, so the menu is the list of shortcuts
  the workspace keeps: ⌘C/⌘V/⌘A, ⌘+/⌘-/⌘⇧-, ⌘⇧A (next needing attention),
  ⌘] and ⌘[ (next and previous session or terminal under one), ⌘D (Dashboard), ⌘O
  (Overview), ⌘1 to ⌘0 (the first ten sessions in sidebar order,
  `WorkspaceMenu.sessionKeys`), ⌘⇧] and ⌘⇧[ (next and previous session, skipping terminals), ⌘L (back to the place shown before, `toggleRecentPlace`), ⌘P (the Go to… list, `toggleSwitcher`), ⌘B (hide or show the sidebar), ⌘⇧B (hide or show the inspector on the right, `toggleInspector`), ⌘J (scroll the sidebar to what the pane is on and flash its line, expanding a collapsed project and showing a hidden sidebar; the keyboard stays put; every key that moves the pane to another session or terminal reveals where it went the same way, `AppDelegate.followingFocus`), ⌘T (one more shell in the home folder each time, Shell 2 and on, `openNewShell`), ⌘↩ (a terminal below the session shown), ⌘⇧↩ (a fresh session with a harness/profile picker and optional directory, opened automatically), ⌘R (name the session or terminal shown, `nameShownSession`), ⌘W (close what the pane is on, `closeShown`: a view or an embedded session attachment; the agent keeps running; another window that has the keyboard closes instead), ⌘⇧W (end the shown session, with confirmation enabled by default), ⌘M,
  ⌘, and ⌘Q.
- **Two panes.** View and the command palette can split with a session shell or
  native checkout review; the palette can also select another agent. Clicking a
  pane promotes its selection and moves the existing surface between the active
  and inactive containers without reattaching. Header controls show focus; View
  also offers focus-other, swap, and hide-other. Detached and failed surfaces stay
  explicit recovery states in either pane. Hiding a pane preserves its program;
  closing the workspace detaches agents and collapses the split. Review close and
  replacement are blocked during landing. `WorkspacePanesTests` covers these
  transitions and confirms terminal geometry and attachment counts.
- **Go to.** ⌘P shows `WorkspaceSwitcherView` above the pane: a field over
  the places `Model/WorkspaceSwitcher.swift` lists, which are the sidebar's
  sessions in its order and then the views, overlays left out, except that
  the place the pane was on before comes first, so ⌘P then Return goes back
  as ⌘L does. A query keeps the entries that hold every word of it, case and
  accents aside, those whose title starts with it first. Return or a click calls `activate(_:)` for a session and
  `showView(_:focus:)` for a view, what a click on the sidebar line calls, so
  ordinary navigation never launches an agent. A leading `>` switches to actions
  (also opened directly with **⌘⇧P**, **Session › Command Palette…**): launch
  a configured project, focus or resume a recent conversation, open a shell,
  review changes, search history, or use the workspace commands. Unavailable
  actions show their reason and cannot be invoked. The field has the keyboard while the list is
  up; Escape and ⌘P give it back to what had it, and a click elsewhere leaves
  it where the click put it. A terminal under the list claims a click only
  when the window's own hit test lands on it, so a click on a row is the
  list's. A snapshot that changes the places re-filters
  an open list. A query that keeps nothing says **No matches**; the selected
  row is drawn as a focused list's (`WorkspaceSwitcherRowView`), since it is
  what Return goes to; and the list shrinks to stay 16 points above the
  pane's bottom. A terminal, pager, or picker is listed with the name of the
  session it is about (`WorkspaceUtilityView.detail(session:)`), the one it
  was last listed under once that session is gone, and is found by it; the
  sidebar line and its tooltip say the same.
- **Inspector.** ⌘⇧B shows `WorkspaceInspectorView` on the pane's other
  side, built as data by `Model/WorkspaceInspector.swift`. A Waiting row
  carries a `WorkspaceWaitingAnswer` when it can be answered without going
  to its session: `.asl` when `MenuBuilder.waitingBySession` binds a waiting
  run to it, on either backend, else `.prompt` when the workspace embeds it
  and its state is `key` or `question`, else none. The row's **Answer…**
  button reports it through `workspaceAnswer(_:)`, and the app opens the
  panel the menu and the banners open (`answer(_:)`, `answerPrompt(_:)`).
  The inspector decides nothing about sending: the prompt panel runs
  `amx send --look` before it enables a key, and `amx send` refuses a session
  with a supervisor question open. The button refuses first responder, so
  the terminal keeps the keyboard.
  Revealing a session with ⌘J or Next Waiting Session (⌘⇧A) also scrolls to
  and flashes its Waiting row when the inspector is open and the row is
  listed under its current tab and filters.
- **Session names.** ⌘R and the sidebar's right-click menu
  (`menuNeedsUpdate`, `contextMenuItems(forRow:)`) ask for a name in a sheet
  and run `amx name-session --runtime-session <id> --json -- <name>`, which writes
  the pane's `cc_name`; an empty name removes it. The daemon reads it on
  every reconciliation pass and publishes it as the v2 snapshot's `name`, so
  the line changes with the next snapshot and not before. A named row is
  taller by one line (`WorkspaceRowCellView.nameLabel`, bold, with a blue
  appropriate to the current appearance). A session is called by
  its name, else "project — profile" (`WorkspaceRow.displayName`), in the
  window title, the inspector, and the end, name, and failure sheets.
- **Terminal names.** ⌘R and **Name Terminal…** in the sidebar’s right-click
  menu name terminals opened below sessions and standalone shells. The name
  appears in the sidebar, window and pane titles, Go to… search, and close
  prompts. Clearing it restores the original label. Naming keeps the same
  running process; saved pane layouts retain the name when restored.
- **Right-click menu.** A session's line offers **Name Session…**,
  **Color…**, **Icon…**, **Mute**/**Unmute**, and **Pin to Top**/**Unpin**,
  acting on the line under the pointer rather than the session the pane is on.
  An embedded session also offers **New Terminal Below**, **New Session Below…**,
  **Copy Runtime Session ID**, and **End
  Session…** (the same sheet as its ×), preceded by **Reattach** while its
  terminal has ended and is not remade until asked for. An
  opened view, or the Dashboard or Overview while its program runs, offers
  **Close**; shells and terminals also offer **Name Terminal…**. A line with nothing to offer, a heading among them, shows no
  menu (`WorkspaceSidebarView.hasMenu`).
- **Function keys.** F1 to F11 are the menu's too, with no modifier
  (`WorkspaceMenu.functionKeys`, listed under **Session › Function Keys**), acting on the
  session the pane is on (`shownSessionRow`). A surface declines an unbound
  function key, so AppKit offers it to the menu. F1, F5, and F6 open overlay
  views (`isOverlay`): `amx workspace-view keybindings`, `icon-picker
  <runtime-session-id>`, and `color-picker <runtime-session-id>`, which go
  back to where the pane was when their program ends and are never one of
  ⌘L's two places. F8 and F11 run `amx hook-indicator user_clear|user_active`
  and F10 `amx mute-toggle`, naming the session by `--runtime-session`.
  F3 has no target: it is
  `SurfaceView.toggleMarkers`, the eighth history request, disabled where
  there is no server to ask. The Go and app key lists are checked together by
  `TestWorkspaceFunctionKeysMatchTheApp`.

### Decision inbox, conversation history, and changes

The inspector’s **Waiting** tab has category filters (permissions, questions,
errors, completed turns, other), optional oldest-first order, and **Next waiting**.
Filtering never changes the main sidebar order or the global attention cycle.
**Context** fetches the actual supervisor question or current screen output,
without attaching or sending keys. Context is memory-only and discarded when its
question changes, the feed disconnects, or the inspector closes. **Answer…** still
uses the existing independently authorized answer panels.

**View › Conversation History…** opens a native search and transcript view. Search
uses the same transcript backend as the CLI, with **All history**, **This project**,
and **This conversation** scopes captured from the workspace session when opened.
The automated-session toggle includes ASL and subagent conversations. Results show
matching excerpts; the preview initially loads 50 messages and **Load earlier
messages** adds 100. **Focus live session**, **Resume**, and **Fork** use exact
harness-qualified identities. Resumes and forks from this view open in the workspace.
An error remains visible, and an unknown launch outcome is never retried automatically.

**View › Checkout Changes…**, or **Changes…** in the Session inspector,
opens a checkout-wide review. An embedded session supplies its current directory;
otherwise a folder picker selects the checkout. The comparison base defaults to
HEAD and accepts another commit or reference. Files show additions/deletions,
binary status, and untracked status; selecting one loads its unified diff, bounded
to 2 MiB with an explicit truncation notice. **Open in editor** uses the configured
editor in a workspace utility view. Git external diff drivers and textconv do not
run. Changes belong to the checkout, which can be shared by multiple agents.

Managed checkouts additionally show their source branch, ahead/behind counts,
landing verdict, and warnings. **Compare with source** uses the recorded merge base.
**Land…** requires explicit confirmation and uses the existing CLI fast-forward
rules, which recheck clean/inactive status. It retains the worktree and branch.
The review requires the companion `amx session changes` command from this build.

### Native ASL Runs and plan launch

**View › ASL Runs…** uses the complete `asl_snapshot` projection rather than the
companion feed's waiting subset. Questionless pauses remain visible. The selected
run shows its phase, current session, timestamps, blockers, and ASL-provided action
availability. `WorkspaceRunsModel` fetches a fresh row before a control request and
binds confirmation to the original run identity. Unknown outcomes retain the exact
operation ID and parameters for **Retry Receipt**; polling never retries mutations.
Open questions continue to use `ASLQuestionFlow`.

`amx plans status PLAN --json` reads ASL's public status report for plan progress.
It projects recorded verification from the event path ASL reports, filtering to the
exact run and excluding arbitrary output and question prose. Reads are bounded to
the last 4 MiB and 200 verification records, with truncation and missing evidence
reported explicitly. The app rejects details belonging to a different run or plan.

**Run a Plan…** previews `amx plans run --check` with explicit optional project,
profile, worker, review, and commit choices. Only a successful matching readiness
check enables launch; the existing CLI rechecks before creating a tmux supervising
session. A pending, submitted, or uncertain launch cannot be repeated by the form.
The app must be rebuilt with its companion CLI for the progress endpoint.

### Dashboard and utility views

The sidebar's first section, **Workspace**, lists utility views
(`Model/WorkspaceViews.swift`): Dashboard, Overview when enabled in Preferences,
and a shell, editor, or pager while one is open. A utility view is a
`Ghostty.SurfaceView` running `amx workspace-view <kind> [target]`
(`cmd/dashboard/workspace_view.go`); the app passes a kind and a target and
never a command line, and amx decides the program and removes every session
  variables from its environment before running it.

- **Not sessions.** A `WorkspaceUtilityView` has no `AttentionCycler.Key` and
  nothing about it comes from a snapshot, so it cannot be counted as waiting,
  appear in the status menu, or be reached by the attention shortcut. The
  window keeps `viewSelection` beside `WorkspaceState.selection`; only one of
  the two is ever set.
- **Routing.** The app subscribes with the `workspace_views` feature. The
  embedded dashboard and overview send the daemon a `workspace_view` request
  when they launch or focus a session or want a utility view; the daemon
  validates it and forwards it as a `workspace_view` frame
  (`StreamFrame.workspaceView`), and `WorkspaceWindowController.open(_:)` acts
  on it: a session request focuses that session's own `amx attach` terminal,
  a just-launched one (`await`) is held until a snapshot lists it or
  `awaitLimit` passes, and a utility request opens its view. A request that
  arrives while the window is closed is dropped. If no workspace is connected
  the daemon refuses and the surface shows the view in its own terminal.
- **Lifetime.** A utility terminal is made when its view is first shown. A
  shell, editor, or pager that exits unlists its view and the pane goes back
  to where it was; Dashboard and Overview remain available after exiting and
  offer **Reopen**, with sidebar visibility controlled by Preferences. Closing the window hides utility terminals without ending
  them, unlike attachments.
- **Terminals beside a session.** ⌘↩ (`openTerminalBesideSession`) opens a
  `.terminal` view: `amx workspace-view session-shell <runtime-session-id>`,
  a login shell in the directory of that session's pane, which amx reads only
  while the private server holds the pane for that session. The sidebar lists
  it under the session's row, or beside it in compact mode (`reloadSidebar`),
  rather than in **Workspace**,
  and closes it when the session ends, including when a live snapshot stops
  listing the session. A disconnected feed keeps the terminals intact. It is a
  utility view in every other respect: `WorkspaceState.rows` never holds it,
  so the digit shortcuts cannot land on it; `selectNeighbor` walks the
  sidebar's sessions and the terminals under them, so ⌘] and ⌘[ do, and
  with `raising` (⌘⇧] and ⌘⇧[) it walks the sessions alone.
- **The dashboard shortcut.** It shows the workspace's Dashboard.
  **Launch Project** runs `amx project launch --backend tmux` and shows the
  session in the workspace.
