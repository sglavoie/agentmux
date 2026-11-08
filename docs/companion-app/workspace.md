# Workspace and terminals

[Back to the companion guide](../companion-app.md)

## Workspace

Lifecycle labels match the TUI: **Detach Terminal** leaves the agent running,
**End Session** stops it, and **Close View** ends a utility program. Preferences › Workspace offers **Follow config.toml**, **Always — app override**,
and **Never — app override** for confirmations. The policy applies to native
controls and the embedded Dashboard, Overview, and conversation actions, including
views already open. Follow config.toml rereads `defaults.confirm_destructive` before
a destructive action. Existing explicit app choices migrate to Always or Never;
new installations inherit config.toml. An unreadable policy asks for confirmation.
Terminals outside the app retain their own configuration.


**Open Workspace** in the menu shows a window with every live session in a
sidebar, grouped by state (Waiting, Done, Working, Idle) and then by project,
labeled by profile, with the same state glyphs the tab titles use and a count
of waiting sessions beside each project. Pinned sessions and those needing
attention lead the list.

- The group buttons above the sidebar filter sessions by the projects' configured
  `groups` tags. **All** shows every session; clicking the selected group also
  returns to All. The choice survives app restarts. A group removed from the
  configuration falls back to All after a successful project refresh; loading
  or failed reads keep the saved choice.
  Buttons show nonzero waiting counts, including sessions outside the selected
  group. A session in several groups counts once in each, and once in All.
  Counts disappear while the daemon feed is disconnected.
- Each group, including **All**, remembers its active session or view and split
  panes, their active side, and divider position. Switching groups returns to
  that context; it also survives closing the window or restarting the app.
  The first visit selects the group's first session in sidebar order, or an
  empty pane when none is running. Existing terminals keep running while hidden.
  Restoration never restarts an ended agent or shell, and a session attached
  elsewhere stays detached. A removed group returns to All after a successful
  project refresh. Switching waits for any checkout operation in the review
  pane to finish. Opening a session from a notification or an out-of-group
  launch still takes you to that session.
- **Launch Project** in the workspace and menu bar follows the selected group,
  including configured projects with no running sessions. Its **All projects**
  submenu offers the full catalog without changing the group just by opening it.
  Launching a project outside the group reveals the new session and switches
  the sidebar to All. The command palette remains a search across all projects.

- A line of chips above the sessions counts each section listed below it,
  Needs attention and Pinned included. A click on one goes to that section's
  first session and finds its line as **Reveal in Sidebar** (⌘J) does,
  expanding its section and project if they are collapsed.

- A click on a **tmux** session shows its terminal in the window and puts the
  keyboard in it. The terminal is a view of the agent, not the agent: closing
  the window, quitting the app, or attaching to the session somewhere else
  (`amx attach`, the dashboard) leaves the agent running. A view that lost
  its session to another terminal says **Detached** and offers **Reattach**.
  Each session has one owner at a time: the terminal that attached last has
  the keyboard and sets the size, and the agent keeps that size when nobody
  is attached. Previews and the Overview only read the screen.
- The window comes back where it was. If it was open when the app last ran
  (it quit, crashed, or was upgraded), it reopens on the same session or view.
  The agents were running in tmux all along and are simply shown again. A
  session that another terminal attached to meanwhile is shown **Detached**
  rather than taken back.
- An agent that is no longer running is never restarted automatically. The
  pane says what happened instead: **Agent exited** when the agent ended on
  its own (with its exit status), **Agent lost** when the terminal it ran in
  is gone, as after a reboot. When the agent's conversation is known the pane
  offers **Resume Conversation…**. A native panel reads that exact conversation
  and shows its profile, checkout, and Workspace (tmux) destination. **Resume
  Conversation** rechecks the source before opening it; a conversation already
  running is focused. Changed source details require review and refresh. Otherwise
  starting or resuming is done from the Dashboard.
- If the daemon stops, the sidebar keeps its sessions, dimmed, and says so at
  the bottom; the terminals keep working. Everything is current again when
  the daemon is back.

While the window is open the app has a Dock icon and a menu bar; closing the
window makes it a menu bar app again.

Session rows use neutral backgrounds and a small profile-color marker. The system
accent identifies selection; status appears in words as well as an icon. A completed
turn uses a checkmark in the native app. Sessions with duplicate display names gain
a distinguishing branch or short runtime identity, also used by the switcher,
Waiting, the inspector, and session dialogs. Long duplicate names truncate in the
middle to retain their distinguishing suffix.

Only section headers carry state colors: green for Done, blue for Working, gray
for Idle, amber for Waiting and Needs attention, and purple for Pinned. The rounded
header backgrounds adapt to light and dark mode. Project headers and session rows
stay neutral, with profile colors confined to a narrow marker.
Session flashes use their displayed section's same background color and opacity,
including when revealing a session with ⌘J. A session in Pinned or Needs attention
uses that section's color, even if its underlying state differs.

Each session pane has a compact context strip with its name, project/branch, and
current status. Its height stays fixed as status changes. **View question** opens
the existing answer panel for that session, including from the other split pane,
without changing the active terminal. Disconnected status is labeled **Last known**
and question actions are unavailable until the feed returns.

After you pause on a session, **Last outcome** previews its saved outcome in the
context strip. Rapid switching cancels pending reads. Hover for the saved time,
or click for the full recap and supporting messages. These reads use only saved
data; they never generate a recap. The popover shows the saved time, excerpt
coverage, stale results, and failures. Supporting-message links open
History. Switching to another session closes the popover so an old outcome cannot
be mistaken for the new session's. Generate recaps through the Session inspector
or the existing conversation readers.

### Dashboard and utility views

Above the projects the sidebar lists the workspace's own views. They are
terminals of their own beside the agents.

- **Dashboard** (⌘D) is the `amx` dashboard, embedded. The workspace opens on
  it. Everything the dashboard does in a terminal it does here: launching,
  Past Sessions, Plans, Worktrees, Runs, the action menu. What changes is
  where things show. Launching an agent, resuming or forking a conversation,
  restarting a session, or pressing Enter on a row takes you to that session's
  own view in the workspace, and the Dashboard stays where it was behind it.
  New agents launch on tmux.
- **Overview** (⌘O) is the live session overview. Enter on a tile goes to the
  session's view the same way.
- **Find or Resume a Conversation…** (⌘Y) opens native Conversation History,
  the main place to search, read, resume, or fork a conversation. The older
  configurable ⌘⇧H shortcut is an alias to the same browser. **Handoff / More…**
  opens the existing terminal conversation actions on the selected conversation
  and committed search. ⌘Y returns to History with its selection and reading
  position retained. Past Sessions remains available inside the Dashboard and
  through `amx past-sessions`.
- **Shell**, **editor**, and **scrollback** views open when the Dashboard asks
  for them (*Open shell*, *Open editor*, *Peek scrollback*, the plan and
  worktree editors) and from **View › New Shell** (⌘T). Each is listed while
  its program runs and goes away when it exits; **View › Close** (⌘W)
  ends the one you are on, and so does the × in the top right corner of its row.
- The × on a session row and **Session › End Session…** (⌘⇧W), also in the
  row's context menu, end the agent through the same confirmation policy. Its
  conversation remains on disk. ⌘W **detaches its terminal**, leaving the agent
  running and listed; click its row or choose **Reattach** to return.
- **Session › New Terminal Below** (⌘↩) opens a shell where the session you
  are on is working and lists it right under that session; pressed again, it
  adds another. Click one to go back to it, or step to it: ⌘] and ⌘[
  (**Session › Next Session or Terminal** and **Previous Session or
  Terminal**, also ⌘⇧→ and ⌘⇧←) stop at every session and every terminal
  under one, in sidebar order; F9 and F7 (**Next Session Only** and
  **Previous Session Only**) go from session to session, passing over the
  terminals. The digits (⌘1 to ⌘0) count sessions only. With no session shown
  the item is greyed out. If the session ends first, the
  shell keeps running and moves up to the **Workspace** section.

- **Session › New Session Below…** (⌘⇧↩), also in a session's right-click
  menu and the command palette, opens a launch dialog. Choose a harness/profile
  and optionally type or choose a directory. An empty directory uses the source
  session's current working directory; relative paths start there. **Launch
  Session** starts a fresh agent in the workspace and opens it automatically,
  saving its place below the source in the project's session order. The sidebar
  still groups sessions by state. These choices apply only to this launch.

A view is not a session. It is never counted as waiting, never shown in the
status menu, and skipped by the next-attention shortcut. Its program runs
without any session's identity, so a harness you start by hand in a shell
view is not mistaken for a managed agent. Closing the window detaches the
session terminals but leaves a running shell, editor, or the Dashboard alone;
they end when they exit, when their view is closed, or when the app quits.

The global dashboard shortcut shows the workspace's Dashboard. **Launch Project**
in the menu launches on tmux and shows the new session in the workspace.

### Keys, history, and the mouse in a terminal

Whatever has the keyboard decides where a key goes.

- In a **session's terminal** every key without ⌘ is the agent's: Return,
  Shift+Return, Escape, Tab, the arrows, Page Up, the function keys, and
  every Control and Option chord. The one exception is **F12**, which
  detaches the view (the agent keeps running and never sees that key).
- In the **Dashboard** or any other utility view the same keys go to the
  program running there.
- In the **session list** the arrow keys move the selection and Return goes
  into the selected row. Nothing typed there reaches a program.
  **Window › Session List** (⌘⇧L) puts the keyboard there.
  **View › Reveal in Sidebar** (⌘J) finds what the pane is on in the
  list instead, leaving the keyboard where it is: it shows a hidden
  sidebar, expands a collapsed project holding the line, scrolls the line
  into view, and flashes it. Every key that moves the pane to another
  session or terminal (⌘] and ⌘[, ⌘⇧← and ⌘⇧→, ⌘← and ⌘→, ⌘1 to ⌘0, ⌘L,
  F7 and F9, and their Shift forms) does the same once it has moved.
- The button in the title bar, or **View › Toggle Sidebar** (⌘B), hides the
  sidebar so the terminal has the whole window, and brings it back; ⌘⇧L
  shows it too. The choice is kept across launches, and so is the width
  the sidebar and the inspector were dragged to: hiding one and showing it
  again keeps its width.
- The button at the other end of the title bar, or **View › Toggle
  Inspector** (⌘⇧B), shows a second sidebar on the right, hidden until it is
  asked for. Its **Waiting** tab lists sessions waiting on you within the
  selected group, with **Show all groups** available to include every group, in the
  order the session list has them (the order **F4** steps through), with why
  and for how long; a click goes to the session as a click on its line in
  the session list does. A row that can be answered from where you are has
  an **Answer…** button, which opens the panel a banner's Answer… opens and
  leaves the pane on the session it shows: the supervisor question of an
  agent-session-loop run, or the prompt a session in the workspace is
  showing. A stopped agent and an error have no button. Its **Session** tab says what the daemon knows
  about the session the pane is on (state, reason, times, flags, profile,
  conversation and runtime-session IDs, branch), copies an identifier when
  its line is clicked, and has buttons for what ⌘R and F5, F6, F8, F10, and
  F11 do. Pointer actions preserve terminal focus; Window › Waiting (⌘⇧I)
  explicitly focuses its keyboard reader. Whether it is shown and which tab it
  is on are kept across launches.
- **⌘1** to **⌘0** go to the first ten sessions in the order the sidebar
  lists them, top to bottom: ⌘1 is the first session and ⌘0 the tenth.
  Their items in the Session menu name the session each goes to, and are
  greyed out past the last one.
  Visible sessions take the digits first, including while rows are held in
  place during hover, a menu, or a drag. Sessions in collapsed groups follow
  the visible ones; choosing one expands its group.
  Shells and other opened views are not counted. **⌘]** and **⌘[** step to
  the next and the previous session or terminal under one, wrapping. **⌘⇧→**
  and **⌘⇧←** do the same; **⌘⇧]**
  and **⌘⇧[** step to the next and the previous session, skipping the
  terminals. From a terminal, the previous session is the one it is under.
- These keys stay in the workspace. macOS takes ⌘⇧3, ⌘⇧4, and ⌘⇧5 for
  screenshots before any app sees them, unless disabled in System Settings.
- **⌘←** and **⌘→** cycle through the current project's sessions and the
  terminals beneath them, wrapping at either end in sidebar order. Pinned
  sessions still belong to their original project, and collapsed groups
  expand when reached. These shortcuts are available while a session or
  one of its terminals is shown; global views such as Dashboard have no project.
- **⌘L** goes back to the session or view shown before this one, and again
  returns: the two most recently shown.
- **⌘P** (**Session › Go to…**) lists every session and view over the pane.
  Type any words of a name, project, profile, or harness to narrow it, move
  with the arrow keys, and press Return to go there as a click on its line
  would; Escape, ⌘P again, or a click elsewhere puts the list away. It only
  goes to what is already running.
- **⌘R** names the session shown (**Session › Name Session…**), and so does
  **Name Session…** in the menu a right-click on a session's line opens. A
  named session has its name on a bold first line above its profile. An empty name
  removes it. The name is kept with the session (`amx name-session`), so it
  is still there after the app was quit, and shows about two seconds after
  it was given. Only a session embedded here can be named.
- **Pin to Top**, in the same right-click menu, moves a session out of its
  project into a **Pinned** group at the top of the list, above every
  project; its line then names its project. **Session › Pin Session**
  (or **Unpin Session**, when it is pinned) does it for the session shown. Pinned sessions are listed in the
  order they were pinned and can be dragged among themselves; **Unpin** puts
  one back under its project. The digits, the next-attention shortcut, and
  the inspector follow the list, so the first visible pinned session is ⌘1. Pins are
  kept across launches, and any session can be pinned.
  The order sessions were dragged into within a project is kept too; a
  session that ended, or a project with none left, is dropped from it once
  the daemon lists the sessions again.
- A right-click on a project's header offers **Collapse Project** (or
  **Expand Project**; **Collapse Pinned** on the Pinned group). A collapsed
  project lists its header only, marked ▸, which still counts its sessions
  waiting on you; the digits and the next-attention shortcut still reach its
  sessions. Collapsed projects are kept across launches. A detached session's
  menu has **Reattach**, which does what a click on the line does.
- A right-click in a terminal offers **Copy**, **Paste**, and **Select All**,
  and **Open Link** and **Copy Link** while the pointer is on a link (hold ⌘
  to find one). A program that tracks the mouse is sent the click instead,
  tmux in every session view among them, so there is no menu there.
- Every shortcut of the menu bar holds ⌘, so none is taken from a program,
  with two exceptions: Shift+Page Up and Shift+Page Down, and the function
  keys below. The global shortcuts work whatever has the keyboard.

**Function keys.** F1 to F11 act on the workspace's sessions
(**Session › Function Keys**). The ones
about a session act on the one shown, or on the one a terminal beside it, a
pager, or a picker is about. F12 stays the key that detaches.

| Key | What it does |
| --- | --- |
| F1 | Shows this list in the pane; `q` or Escape goes back |
| F2 | The Overview |
| F3 | Marks errors, failures, warnings, and deprecations in a session's history; again to return (see History) |
| F4 | The next session needing attention |
| F5 / F6 | Picks the session's harness icon, or its color; the pane goes back when the pick is made |
| F7 / F9 | The previous or next session, passing over terminals and staying in the app |
| F8 | Clears the session's markers and state icon (**Clear Session Markers**) |
| F10 | Mutes or unmutes the session's notifications |
| F11 | Marks the session active (⚡) until its next real event |

With no session shown
the keys about one are greyed out in **Session › Function Keys**.

A key or item that cannot act says why at the top of the pane for a few
seconds, as well as on the menu bar's line. One that fails, such as F8 when
the daemon does not take it, or naming or ending a session, opens an alert
with amx's own error, **Copy Details**, and **Start Daemon** while the daemon
is down or **Open Settings** (Advanced, at the amx binary) when amx was not
found. amx still being found at launch is said as such: waiting fixes it.

**History** belongs to the session, not to the window showing it, so it is
all still there after the window was closed, the app was quit, or another
terminal attached. Nothing here needs a tmux key or prefix.

| Menu | Key | In a session's terminal |
| --- | --- | --- |
| View › Page Up / Page Down | ⇧Page Up / ⇧Page Down | One page through the history |
| View › Scroll to Top | ⌘Home | The oldest line tmux holds |
| View › Scroll to Bottom | ⌘End | Back to the live screen |
| Edit › Find in Terminal… | ⌘F | Asks for text at the bottom of the terminal and jumps to the nearest match above |
| Edit › Find Next / Find Previous | ⌘G / ⇧⌘G | The match before, or back toward the newest |
| Session › Function Keys › Toggle Error and Warning Markers | F3 | Highlights every `error`, `failed`, `warning`, and `deprecated`, in any case, from the last one up; ⌘G steps to the one before, and F3 again returns to the live screen |

Escape, `q`, or scrolling back down to the bottom also returns to the live
screen. The wheel scrolls the same history.

An agent that draws its own full screen, as Claude Code, Codex, and Copilot
do, keeps its transcript itself. There, paging and the wheel are handed to
the agent, which scrolls its own transcript, and **Find** covers only what is
on screen: use the agent's own search for the rest (in Claude Code, Control+O
and then `/`; in Codex, F3). F3 is handed to such an agent as its own key
rather than marking anything.

In a utility view the paging items scroll that terminal's own buffer, a
scrollbar at its right edge shows where in it the view is (drag it to move;
`scrollbar = never` in the Ghostty config hides it), and F3 is the program's.
**Find** (⌘F) opens a find bar at the top of the view that searches the
buffer as you type: Return and ⌘G go to the next match, Shift-Return and ⇧⌘G
to the previous one, and Escape closes it.

A utility view whose program reports progress (OSC 9;4, as some build tools
do) shows it on its sidebar line, as a percentage or as paused or failed,
until the program clears it or 15 seconds pass without another report. It is
never shown as needing attention.

**Selecting and copying.** Hold Shift and drag to select text in any
terminal, then **Edit › Copy** (⌘C). In a session whose program does not use
the mouse, a plain drag selects on release. A selection goes to Agent Mux's
own selection clipboard rather than the system one, as in Ghostty: a middle
click pastes it, and ⌘C copies it to the system clipboard. An agent that uses the
mouse receives plain clicks and drags itself. **Edit › Paste** (⌘V) pastes as one block: a pasted line break is not
a Return. A right click never opens a tmux menu.

**Links.** ⌘-click opens a link where the program is not using the mouse,
such as a shell view. Where a program does use the mouse it receives plain
and ⌘ clicks itself, and a link opens with ⌘⇧-click: that is every session's
terminal, and any utility view whose program tracks the mouse.

**Input methods** compose at the cursor; the agent receives the text when it
is committed.
