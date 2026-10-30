# Menu bar companion

`Agent Mux.app` is an optional native macOS menu bar app for Agent Mux. It
shows the live managed sessions with attention counts in the menu bar, shows
a session from a menu row or a global shortcut, shows the daemon's
notifications as native banners, and answers ASL supervisor questions in a
native panel. Sessions run on the tmux backend and open in the app's own
[workspace window](#workspace), which is also where Launch Project starts a
new one. The app needs no Kitty; a session that runs as a Kitty tab is focused
in Kitty ([Kitty](#kitty-optional)). The
daemon stays the source of truth: the app subscribes to the daemon's companion
stream and runs the `amx` CLI for actions. Nothing in Agent Mux depends on it,
and quitting it returns every behavior to what it was without it; agents on
tmux keep running and `amx attach` reaches them from any terminal.

This page is the operator's guide. How the app is built, signed, and laid out,
and how each feature works inside, is in [`macos/README.md`](../macos/README.md).

## Install

Requirements: macOS 14 or later, Xcode command line tools, and the
repository's usual toolchain (Go, `just`). The first build also builds the
embedded terminal engine from source, which needs `git`, network access,
several minutes, and about 5 GB under `macos/.cache/`; later builds reuse it.
See [GhosttyKit](../macos/README.md#ghosttykit).

```bash
just install        # amx and the daemon first: the app needs a daemon that streams
just mac-install    # Release build, then install or update /Applications/Agent Mux.app and relaunch it
```

Install `amx` first. The app talks to whichever daemon is running, and a
daemon older than the companion protocol leaves the app in degraded mode (see
[Troubleshooting](#troubleshooting)). After an upgrade of the pair, reinstall
the app too, so its bundled `amx` and the installed one stay close.

The app must live in `/Applications` for the login item to work, which is
where `just mac-install` puts it (`AMX_APP_INSTALL_DIR` overrides it, at the
cost of the login item).

**Nothing else needs installing.** The bundle carries its own `amx` and its
own tmux: a pinned release, built from checksummed sources and linked
statically, so the app runs on a Mac with no Homebrew and no tmux. Before
`amx` runs that tmux it checks every file of it against the checksums sealed
into the bundle and copies it to `~/.local/share/agent-mux/runtime/`. The copy
is what runs, so the tmux a session server was started from is never inside
the app.

### Updating

`just mac-install` again. The installer checks the new bundle (signature,
`amx`, tmux against its checksums), quits the app, stages the new copy beside
the old one, and renames it into place.

**Agents keep running through an update.** They live in the session server,
not in the app, and the installer never stops, restarts, or replaces that
server: it reads the server's identity before and after and fails if it
changed. When the new app opens, the same agents are in its sidebar. If the
update ships a newer tmux, the running server keeps the tmux it was started
with until its last agent has ended; the next server starts with the new one.
`amx backend status` shows which is in use.

### Removing

```bash
just mac-uninstall                                # the app only
just mac-uninstall --end-sessions                 # and end every running agent
just mac-uninstall --end-sessions --remove-app-data --yes
```

Removing the app removes the app. What else happens is yours to choose:

- **Running agents keep running** unless you pass `--end-sessions`. Without
  it the uninstaller says how many it left, and prints the one command that
  ends them later without `amx`.
- **Your data stays.** `config.toml`, conversations and their indexes,
  handoffs, worktrees, and the session registry are never touched.
  `--remove-app-data` removes only what is the app's alone: its preferences
  (the `io.github.sglavoie.agentmux` defaults domain), its saved window
  state, and the tmux runtime it installed. It is refused while agents run on
  that runtime unless you end them too.

Both choices ask before acting; `--yes` answers for a script.

## Permissions

The app needs no Accessibility, Automation, or Screen Recording permission.
Two grants matter:

- **Notifications.** The app asks the first time the daemon sends it a
  notification, not at launch. While it has never been asked, or while it is
  allowed, it claims the daemon's notification backend role; when it is denied
  or turned off in Preferences, the daemon uses Alerter as before. Change it
  later in System Settings › Notifications › Agent Mux. The app rereads the
  permission whenever it comes to the front and whenever its menu opens.
- **Login item.** Settings › Open at Login registers the app with
  `SMAppService`. It shows in System Settings › General › Login Items, and
  removing it there is respected: the app does not add it back.

macOS ties both grants to the app's code signature. A build signed with an
Apple Development or Developer ID certificate keeps them across rebuilds. An
ad-hoc build (no certificate in the keychain, or `AMX_CODESIGN_IDENTITY=-`)
looks like a new app every time, so both grants are asked for again after each
install. Every copy shares the bundle identifier, so the build scripts sign
the `DerivedData` copies with the same certificate: an ad-hoc copy answering a
prompt would otherwise take the grant from the installed app. See
[Signing](../macos/README.md#signing).

## Kitty (optional)

Nothing in the app needs Kitty. **Launch projects in** (Preferences ›
Workspace) defaults to **Workspace (tmux)**. **Follow CLI configuration**
reads the effective `config.toml` destination again before launching a project
or resuming a closed conversation from Recent. **Kitty** explicitly launches into Kitty;
if it cannot be reached the app reports the failure. The retired **Automatic**
preference migrates to Workspace, preserving its destination. **Show in Kitty**
and the Kitty socket preference are unchanged.

For sessions that do run as Kitty tabs, focus, Recent, Mute, a dashboard shown
in Kitty, and Launch Project sent to Kitty all reach it through its
remote-control socket, so `kitty.conf` needs:

```conf
allow_remote_control yes
listen_on unix:/tmp/kitty
```

Kitty appends its PID, so each instance listens on `unix:/tmp/kitty-<pid>`;
that is how the app tells two Kitty instances apart and brings the right one
forward. When the app cannot find any socket to try, its menu says so in place
of acting: "Kitty has no remote-control socket: add `allow_remote_control yes`
and `listen_on unix:/tmp/kitty` to kitty.conf, then restart Kitty." A changed
`listen_on` needs a Kitty restart, not a reload. If you use another socket
path, set it in Preferences › Kitty socket.

The daemon accepts a `focus_window` only for a socket it learned itself, so a
Kitty instance that has never run a managed session is not focusable until it
has.

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
- A click on a **Kitty** session brings Kitty forward on its window, as the
  menu does. Its row is marked `Kitty`. Moving through the sidebar with the
  arrow keys only selects; it never switches apps.
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

### Dashboard and utility views

Above the projects the sidebar lists the workspace's own views. They are
terminals of their own beside the agents, and none of them needs Kitty.

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
- The × on a session row and ⌘W **detach its terminal**. The agent keeps
  running and stays in the sidebar; click its row or choose **Reattach** to
  return. **Session › End Session…** (⌘⇧W), also in the row's context menu, ends
  the agent after confirmation. Its conversation remains on disk. A Kitty
  session has no ×; manage its terminal in Kitty.
- **Session › New Terminal Below** (⌘↩) opens a shell where the session you
  are on is working and lists it right under that session; pressed again, it
  adds another. Click one to go back to it, or step to it: ⌘] and ⌘[
  (**Session › Next Session or Terminal** and **Previous Session or
  Terminal**, also ⌘⇧→ and ⌘⇧←) stop at every session and every terminal
  under one, in sidebar order; F9 and F7 (**Next Session Only** and
  **Previous Session Only**) go from session to session, passing over the
  terminals, and ⌘⇧] and ⌘⇧[ do the same but bring a Kitty session forward
  in Kitty (**Next Session, Raising Kitty**, shown while a Kitty session is
  listed). The digits
  (⌘1 to ⌘0) count sessions only. It applies to sessions whose terminal
  is in the workspace, not to Kitty sessions; with no such session shown
  the item is greyed out. If the session ends first, the
  shell keeps running and moves up to the **Workspace** section.

A view is not a session. It is never counted as waiting, never shown in the
status menu, and skipped by the next-attention shortcut. Its program runs
without any session's identity, so a harness you start by hand in a shell
view is not mistaken for a managed agent. Closing the window detaches the
session terminals but leaves a running shell, editor, or the Dashboard alone;
they end when they exit, when their view is closed, or when the app quits.

The global dashboard shortcut shows the workspace's Dashboard. Preferences ›
Show the dashboard in can send it to Kitty instead: it then focuses the
dashboard there, or opens one, and shows the workspace's only when there is no
Kitty to ask. **Launch Project** in the menu launches on tmux and
shows the new session in the workspace.

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
  asked for. Its **Waiting** tab lists every session waiting on you, in the
  order the session list has them (the order **F4** steps through), with why
  and for how long; a click goes to the session as a click on its line in
  the session list does. A row that can be answered from where you are has
  an **Answer…** button, which opens the panel a banner's Answer… opens and
  leaves the pane on the session it shows: the supervisor question of an
  agent-session-loop run, or the prompt a session in the workspace is
  showing. A stopped agent, an error, and a prompt in a Kitty session have
  no button; go there. Its **Session** tab says what the daemon knows
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
- Without Shift those keys never leave the app: a session that lives in
  Kitty is selected and its pane says where it is. Add Shift (**⌘⇧1** to **⌘⇧0**, **⌘⇧]**,
  **⌘⇧[**) and such a session comes forward in Kitty instead, as a click on
  its row does. A session embedded here is focused here either way. macOS
  takes ⌘⇧3, ⌘⇧4, and ⌘⇧5 for screenshots before any app sees them, unless
  those are turned off in System Settings › Keyboard › Keyboard Shortcuts.
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
  kept across launches, and any session can be pinned, a Kitty one too.
  The order sessions were dragged into within a project is kept too; a
  session that ended, or a project with none left, is dropped from it once
  the daemon lists the sessions again.
- A right-click on a project's header offers **Collapse Project** (or
  **Expand Project**; **Collapse Pinned** on the Pinned group). A collapsed
  project lists its header only, marked ▸, which still counts its sessions
  waiting on you; the digits and the next-attention shortcut still reach its
  sessions. Collapsed projects are kept across launches. A Kitty session's
  menu has **Show in Kitty**, and a detached session's **Reattach**, which do
  what a click on the line does.
- A right-click in a terminal offers **Copy**, **Paste**, and **Select All**,
  and **Open Link** and **Copy Link** while the pointer is on a link (hold ⌘
  to find one). A program that tracks the mouse is sent the click instead,
  tmux in every session view among them, so there is no menu there.
- Every shortcut of the menu bar holds ⌘, so none is taken from a program,
  with two exceptions: Shift+Page Up and Shift+Page Down, and the function
  keys below. The global shortcuts work whatever has the keyboard.

**Function keys.** F1 to F11 do here what they do in Kitty, on the
workspace's sessions instead of tabs (**Session › Function Keys**). The ones
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

F5 and F6 are for sessions embedded here; a session that lives in Kitty has
its pickers in Kitty. F8, F10, and F11 work for both. With no session shown
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

## Native workflow views

### Command palette and launch

**⌘⇧P** opens the command palette. In **⌘P**, type `>` to search actions.
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
to the selected file; **⌘⇧F** reveals and focuses the list. Resizing clamps the
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
**⌘⇧F** reveals and focuses it. Divider width and collapsed state are remembered.

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

## Review-window shortcuts

| View | Keys |
| --- | --- |
| History | ⌘F search; Down from search reveals results; ⌘G / ⌘⇧G next/previous match; ⌘R refresh transcript; ⌘Return resume/focus; ⌘⇧Return fork; ⌘⌥S toggle results |
| Changes | ⌘G / ⌘⇧G next/previous hunk; ⌘R refresh comparison; ⌘E open file; ⌘⇧F reveal/focus files; ⌘⇧E edit base; ⌘⌥S toggle files |
| Runs | ⌘R refresh; ⌘⇧F reveal/focus runs; ⌘Return open session; ⌘⇧I inspect failed check; ⌘⌥S toggle runs |
| Activity | ⌘R refresh; ⌘⇧F reveal/focus events; ⌘Return focus live session; ⌘⇧Return open recorded conversation; ⌘⌥S toggle events |

**⌘W** closes a standalone review window and returns to the workspace when open.
The four review windows share one minimum size and remember their placement. Each
reads again when it is shown; Checkout Changes also compares again when you come
back to the app, since an agent may have edited files meanwhile, and Runs polls only
while its window can be seen.
In a split workspace, review shortcuts belong to the focused review pane.
Refreshes retain selection and focus; toggling a sidebar retains reading position.

## Global shortcuts

| Default | Does |
| --- | --- |
| ⌃⌥⌘A | Shows the workspace's Dashboard. Sent to Kitty in Preferences, it brings the dashboard running there forward, or opens one (`amx open-dashboard`). |
| ⌃⌥⌘N | Focuses the next session needing attention, in the order the workspace lists sessions (projects by name, then each project's sessions as arranged there), across Kitty instances and the workspace: a tmux session opens in the workspace and is revealed in its sidebar as ⌘J does, a Kitty session in Kitty. Beeps when nothing waits. |

Change or clear them in Preferences. They are Carbon hotkeys and need no
Accessibility permission. Preferences warns when a shortcut is taken by the
other one, by another app, or by macOS itself in System Settings › Keyboard ›
Keyboard Shortcuts, which Carbon cannot detect. A shortcut that cannot act
beeps and leaves the reason in the menu.

## Notifications

With the app running and allowed, the daemon still applies every gate (quiet
hours, mute, snooze, cooldown) and then hands each notification to the app,
which shows it through Notification Center. Clicking it focuses a Kitty pane
the way an Alerter click does, and shows a tmux session in the workspace,
opening the window if it is closed; the banner's actions mute the session, snooze for an
hour, or open the ASL answer panel. A permission prompt's banner offers no
approval. For a session in the workspace it offers **Answer…**, which opens a
small panel showing the session's screen: you read the prompt there and press
the key that answers it (`1`–`9`, `y`, `n`, arrows, Return, or the `esc`
button), without bringing the workspace forward. If the app does not confirm delivery
within 3 seconds, or reports a failure, the daemon withdraws it and Alerter
shows it instead. Quitting the app falls back to Alerter at once.

```bash
amx notifications test   # "confirmed via app" when the app showed it
```

The first notification after a fresh install usually goes to Alerter: the
permission prompt it raises outlasts the 3-second limit.

## Checking the pairing

```bash
amx daemon status        # the companion line names each subscriber and its version
amx doctor --strict      # companion_app (info) per connected app
amx debug subscribe --frames 5   # the same stream the app reads
```

`amx doctor` raises `companion_build_mismatch` (warning) when the app reported
an `amx` build that is not the daemon's. The app reports the `amx` it resolved
in `subscribe.amx_build`, with the full sha256 of the executable rather than
the prefix `amx --version` prints, and subscribes again whenever a new
resolution selects a different binary; until one is resolved it reports none
and nothing is compared. Preferences shows which `amx` binary the app resolved
and why.

## Troubleshooting

**"daemon too old — run `amx daemon restart`".** The running daemon predates
the companion stream (it answered `subscribe` as an unknown request). The app
falls back to polling `health` every 10 seconds and retries `subscribe`; menu
items that need the daemon are greyed out with the same advice. Restart the
daemon after installing a current `amx`.

**`not running` in the menu.** No daemon owns the socket. The app never starts
one on its own; choose **Start daemon** or run `amx daemon start`.

**`reconnecting` or `unresponsive`.** The stream ended or went quiet. The app
redials with backoff, reconnects within 0.5s after a `bye` (as after
`amx daemon restart`), and treats 30 seconds without a frame as a half-open
socket. A wake from sleep reconnects at once.

**Build mismatch.** `companion_build_mismatch` means the app runs an `amx`
that is not the daemon's build, usually after `just install` without
`just mac-install`, or the reverse. Reinstall the side that is behind. The app
prefers `~/.local/bin/amx` over its bundled copy when the local one matches
the daemon, is newer, or is the same clean build; to pin one, set
Preferences › amx binary.

**"no supported tmux found".** A workspace launch needs tmux, and none could
be used. The app's own tmux is used whenever the bundle has one, so this
means a build without it (a hand-made Debug build), a bundle whose tmux no
longer matches its checksums (reinstall the app), or a
`[defaults.tmux] path` in `config.toml` that names something that is not a
tmux 3.5 or newer: an explicit path is the only one tried. Preferences ›
Workspace shows which tmux is in use and, when none is, each place that was
tried and why it was refused; `amx backend status` prints the same.

**Grayed-out menu items.** The resolved `amx` lacks the command
(`project`, `open-dashboard`, `mute-toggle --json`); the tooltip says which.
Install a current `amx`.

**Kitty does not come forward, or shows the wrong Space.** The app is an
accessory app and activates Kitty cooperatively: it yields activation to the
Kitty process that owns the socket and asks it to activate. macOS 14 and later
may decline a cooperative activation when the app itself was not active, and
when only one Kitty runs the app also opens it through Launch Services, which
macOS does honor. With several Kitty instances that fallback cannot name one,
so it is skipped. Kitty focuses the tab and window either way; whether macOS
switches to the Space holding that OS window is governed by System Settings ›
Desktop & Dock › "When switching to an application, switch to a Space with
open windows for the application". The app deliberately does not use
AppleScript or Accessibility to force a Space switch.

**Notifications never arrive in the app.** Check System Settings ›
Notifications › Agent Mux and Preferences › Use app notifications. After an
ad-hoc install the permission resets; allow it again. `amx debug tail` names
the backend each dispatch used (`app` or `alerter`).

**Logs.**

```bash
log show --last 5m --predicate 'subsystem == "io.github.sglavoie.agentmux"'
```

## Known limits

- **Find in a full-screen agent** reaches only the visible screen; the rest
  of the conversation is in the agent's own transcript and search.
- **Mute lag.** A mute made from the app (or `amx mute-toggle`) is written to
  Kitty at once, but the daemon learns it on its next Kitty listing, at most
  one render cadence (2 seconds) later. A notification raised inside that
  window is not yet suppressed. The menu row shows a mute made from the app
  at once and keeps showing it until a snapshot agrees, for up to 5 seconds,
  so reopening the menu right away does not offer the toggle again. See
  [Daemon › Mute lag](daemon.md#mute-lag) for why there is no `mute_set`
  request.
- **Paused ASL runs** appear in Waiting for answering only when their pause left
  an open supervisor question. **View › ASL Runs…** also lists pauses without one.
- **One Kitty per window ID.** Window IDs mean something only inside the Kitty
  that issued them, so Mute never tries another instance's socket.
- **Other Kitty instances.** The daemon lists the instance whose hook it heard
  last and, on the same cadence, each other instance that has a session, so
  those sessions are named from their own tabs. Each is kept until its own
  instance stops reporting the window or that Kitty exits, never because a
  listing of another instance could not see it. The daemon keys state by the
  instance socket and window number, so reused numbers retain independent
  attention and completion state.
- **Codex completion visibility.** Managed interactive launches select `--no-daemon`
  when the launched CLI advertises support, preserving the terminal's hook environment
  across resume and `/new`. Explicit remote transport choices and older CLIs retain
  their behavior. Shared-server hooks with ambiguous or unbound owners are rejected;
  they are not borrowed from another terminal. The mitigation requires an updated
  installation and a newly launched managed client. Existing clients are not rebound.
  The operator confirmed the installed-build resume, `/new`, app-reopen, and physical
  sleep/wake checks on 2026-10-18; see the [F-001 observation record](acceptance/workspace-consistency/observations.json).

### Waiting controls and update times

Waiting context distinguishes loading, failed reads, and unavailable questions.
**Refresh** keeps the current content and scroll position; **Retry** retries only
the read. Failed refreshes retain the previous content and its original read
time. ASL questions use the same Markdown reader as History, including code-copy
controls; terminal captures remain monospace. A replaced question clears its
context immediately and cannot be answered through the old card.

The Session inspector describes prompt reasons in ordinary language and shows
the recorded evidence supporting the displayed state: screen or transcript
inference, harness/process lifecycle, subagent, or ASL. **Observed** is that
fact's timestamp, independent of the latest event from another source. Missing
or older-daemon evidence stays explicitly unavailable; it is not proof of idle.

While using Find in an embedded agent, **Search Full Conversation…** opens
History on that exact conversation and carries the active terminal query.
Terminal Find still labels whether it covers the visible screen or terminal
history. History searches saved prose using words and quoted phrases, not
terminal regular expressions. An unavailable query read is explained. Closing
History returns focus to the original visible terminal. Utility views keep
their own terminal Find; they have no conversation to search.

Expanded Waiting cards put the question directly below the session title and
Answer controls, with a compact refresh icon beside Hide context. Read time and
retained-read failures appear below the question; failures keep an explicit Retry
button. The collapsed card keeps its reason text, and accessible labels retain
session context. Lighter card backgrounds keep the prose prominent.

Waiting keeps category and Oldest first on one row. Next Waiting stays available;
Update List appears only when changes are held while you read. The keyboard hint
appears while the Waiting reader has focus and disappears when focus leaves.
Changed questions immediately lose their answer actions, even before you apply
an updated list.

Review toolbars show **Updated 12s ago** (then minutes, hours, or days), using
the oldest successful read of the displayed content. Refresh failures and
warnings take priority; slower reads show Refreshing. Click the status for each
read's exact timestamp, missing reads, and retry actions. Updating a run list
alone cannot make older plan details appear new. **Show in Finder** reveals the
selected plan session file; **Stop After Session…** requests a stop after the
current session finishes.

### Resolved action feedback

Retained warnings and errors show **Needs attention** until matching evidence
resolves them. A successful readiness check resolves only the same plan and options;
a successful resume resolves only that conversation and operation; a new
worktree review resolves a changed-review warning; a successful landing resolves
that worktree's landing failure. A matching final receipt resolves an unknown
receipt outcome, including when the receipt reports rejection (the rejection remains
a separate warning). Unrelated successes and ordinary refreshes do not clear action
failures. A later fork can create a different conversation, so it does not resolve
an earlier fork's failure or unknown outcome. Resolved notices retain their original message, resolution evidence, and
time across restarts, subject to the existing 30-entry bound. Repeated failures
reopen the notice. Older notices without an exact issue identity remain dismissible.
